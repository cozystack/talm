package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"unsafe"

	"github.com/cockroachdb/errors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gopkg.in/yaml.v3"

	"github.com/cosi-project/runtime/pkg/resource"
	"github.com/cosi-project/runtime/pkg/resource/meta"
	helmEngine "github.com/cozystack/talm/pkg/engine/helm"
	"github.com/cozystack/talm/pkg/yamltools"
	"github.com/hashicorp/go-multierror"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/strvals"

	"github.com/siderolabs/talos/cmd/talosctl/pkg/talos/helpers"

	"github.com/siderolabs/talos/pkg/machinery/client"
	"github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/bundle"
	"github.com/siderolabs/talos/pkg/machinery/config/configpatcher"
	"github.com/siderolabs/talos/pkg/machinery/config/encoder"
	"github.com/siderolabs/talos/pkg/machinery/config/generate"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	"github.com/siderolabs/talos/pkg/machinery/config/machine"
)

// Options encapsulates all parameters necessary for rendering.
type Options struct {
	ValueFiles        []string
	StringValues      []string
	Values            []string
	FileValues        []string
	JsonValues        []string
	LiteralValues     []string
	TalosVersion      string
	WithSecrets       string
	Full              bool
	Debug             bool
	Root              string
	Offline           bool
	KubernetesVersion string
	TemplateFiles     []string
	ClusterName       string
	Endpoint          string
	// CommandName names the caller subcommand for error messages such as
	// the one produced by FailIfMultiNodes. Empty value falls back to "talm".
	CommandName string
}

// NormalizeTemplatePath converts OS-specific path separators to forward slash.
// Helm engine's Render() returns map keys with forward slashes regardless of OS,
// so input paths must be normalized to match.
func NormalizeTemplatePath(p string) string {
	return filepath.ToSlash(p)
}

// debugPhase is a unified debug function that prints debug information based on the given stage and context,
// then exits the program.
func debugPhase(opts Options, patches []string, clusterName string, clusterEndpoint string, mType machine.Type) {
	phase := 2

	if clusterName == "" {
		clusterName = "dummy"
		phase = 1
	}

	if clusterEndpoint == "" {
		clusterEndpoint = "clusterEndpoint"
		phase = 1
	}

	fmt.Printf(
		"# DEBUG(phase %d): talosctl gen config %s %s -t %s --with-secrets=%s --talos-version=%s --kubernetes-version=%s -o -",
		phase, clusterName, clusterEndpoint, mType,
		opts.WithSecrets, opts.TalosVersion, opts.KubernetesVersion,
	)

	patchOption := "--config-patch-control-plane"
	if mType == machine.TypeWorker {
		patchOption = "--config-patch-worker"
	}

	// Print patches. Skip empty entries — a template that conditionally
	// emits nothing legitimately produces "" in the slice, and indexing
	// patch[0] on an empty string would panic right at the moment the
	// operator is using --debug to investigate something.
	for _, patch := range patches {
		if patch == "" {
			continue
		}

		if patch[0] == '@' {
			// Apply patch is always one
			fmt.Printf(" %s=%s\n", patchOption, patch)
		} else {
			fmt.Printf("\n---")
			fmt.Printf("\n# DEBUG(phase %d): %s=\n%s", phase, patchOption, patch)
		}
	}

	os.Exit(0)
}

// FullConfigProcess handles the full process of creating and updating the Bundle.
func FullConfigProcess(ctx context.Context, opts Options, patches []string) (*bundle.Bundle, machine.Type, error) {
	configBundle, err := InitializeConfigBundle(opts)
	if err != nil {
		return nil, machine.TypeUnknown, fmt.Errorf("initial config bundle error: %w", err)
	}

	loadedPatches, err := configpatcher.LoadPatches(patches)
	if err != nil {
		if opts.Debug {
			debugPhase(opts, patches, "", "", machine.TypeUnknown)
		}

		return nil, machine.TypeUnknown, err
	}

	err = configBundle.ApplyPatches(loadedPatches, true, false)
	if err != nil {
		if opts.Debug {
			debugPhase(opts, patches, "", "", machine.TypeUnknown)
		}

		return nil, machine.TypeUnknown, fmt.Errorf("apply initial patches error: %w", err)
	}

	// Updating parameters after applying patches
	machineType := configBundle.ControlPlaneCfg.Machine().Type()
	clusterName := configBundle.ControlPlaneCfg.Cluster().Name()
	clusterEndpoint := configBundle.ControlPlaneCfg.Cluster().Endpoint()

	if machineType == machine.TypeUnknown {
		machineType = machine.TypeWorker
	}

	if opts.Debug {
		debugPhase(opts, patches, clusterName, clusterEndpoint.String(), machineType)
	}

	// Reinitializing the configuration bundle with updated parameters
	updatedOpts := Options{
		TalosVersion:      opts.TalosVersion,
		WithSecrets:       opts.WithSecrets,
		KubernetesVersion: opts.KubernetesVersion,
		ClusterName:       clusterName,
		Endpoint:          clusterEndpoint.String(),
	}

	configBundle, err = InitializeConfigBundle(updatedOpts)
	if err != nil {
		return nil, machineType, fmt.Errorf("reinit config bundle error: %w", err)
	}

	// Applying updated patches
	err = configBundle.ApplyPatches(loadedPatches, (machineType == machine.TypeControlPlane), (machineType == machine.TypeWorker))
	if err != nil {
		return nil, machineType, fmt.Errorf("apply updated patches error: %w", err)
	}

	return configBundle, machineType, nil
}

// InitializeConfigBundle initializes a Talos configuration bundle from opts.
func InitializeConfigBundle(opts Options) (*bundle.Bundle, error) {
	genOptions := []generate.Option{}

	if opts.TalosVersion != "" {
		versionContract, err := config.ParseContractFromVersion(opts.TalosVersion)
		if err != nil {
			return nil, fmt.Errorf("invalid talos-version: %w", err)
		}

		genOptions = append(genOptions, generate.WithVersionContract(versionContract))
	}

	if opts.WithSecrets != "" {
		secretsBundle, err := secrets.LoadBundle(opts.WithSecrets)
		if err != nil {
			return nil, fmt.Errorf("failed to load secrets bundle: %w", err)
		}

		genOptions = append(genOptions, generate.WithSecretsBundle(secretsBundle))
	}

	configBundleOpts := []bundle.Option{
		bundle.WithInputOptions(
			&bundle.InputOptions{
				ClusterName: opts.ClusterName,
				Endpoint:    opts.Endpoint,
				KubeVersion: strings.TrimPrefix(opts.KubernetesVersion, "v"),
				GenOptions:  genOptions,
			},
		),
		bundle.WithVerbose(false),
	}

	return bundle.NewBundle(configBundleOpts...)
}

// SerializeConfiguration serializes the configuration bundle for machineType.
func SerializeConfiguration(configBundle *bundle.Bundle, machineType machine.Type) ([]byte, error) {
	return configBundle.Serialize(encoder.CommentsDisabled, machineType)
}

// MergeFileAsPatch overlays the YAML body of patchFile onto rendered using
// Talos's strategic-merge config patcher.
//
// patchFile is a node file: its first line is the talm modeline (a YAML
// comment) followed by an arbitrary Talos config patch (typically machine.*
// fields the user wants pinned per node). When the file contains only the
// modeline (or is otherwise empty after stripping comments and whitespace)
// the function returns rendered unchanged — short-circuiting Talos's
// configpatcher which would otherwise route the empty patch through
// JSON6902 and reject any multi-document rendered config (the v1.12+ output
// format) outright.
//
// Note that for non-empty patches the patcher round-trips rendered through
// its config loader, normalising YAML formatting and dropping comments.
// This is acceptable for the apply path (the result goes straight to
// ApplyConfiguration) but unsuitable for human-facing output such as
// `talm template` — which is why the template subcommand does not call
// this helper.
func MergeFileAsPatch(rendered []byte, patchFile string) ([]byte, error) {
	patchBytes, err := os.ReadFile(patchFile)
	if err != nil {
		return nil, errors.WithHint(
			errors.Wrapf(err, "reading patch %q", patchFile),
			"verify the path is correct and the file is readable by the user running talm",
		)
	}

	if isEffectivelyEmptyYAML(patchBytes) {
		return rendered, nil
	}

	cleanedRendered, renderedDirectivePaths, err := stripAllPatchDeleteDirectives(rendered)
	if err != nil {
		return nil, errors.WithHint(
			errors.Wrap(err, "stripping $patch:delete directives from rendered"),
			"the rendered template did not parse as YAML; this points at a chart-helper bug, not a user input issue",
		)
	}

	cleanedPatch, err := stripPatchDeleteDirectivesAtPaths(patchBytes, renderedDirectivePaths)
	if err != nil {
		return nil, errors.WithHintf(
			errors.Wrapf(err, "stripping redundant $patch:delete directives from %q", patchFile),
			"the node body did not parse as YAML; verify %q is well-formed",
			patchFile,
		)
	}

	cleanedPatch, err = stripPatchDeleteDirectivesAbsentInTarget(cleanedPatch, cleanedRendered)
	if err != nil {
		return nil, errors.WithHintf(
			errors.Wrapf(err, "stripping no-op $patch:delete directives from %q", patchFile),
			"the node body did not parse as YAML; verify %q is well-formed",
			patchFile,
		)
	}

	prunedBytes, allPruned, err := pruneBodyIdentitiesAgainstRendered(cleanedPatch, cleanedRendered)
	if err != nil {
		return nil, errors.WithHintf(
			errors.Wrapf(err, "pruning identity overlap in %q", patchFile),
			"the prune walk failed; the input is likely malformed YAML or has an unexpected document shape; inspect %q",
			patchFile,
		)
	}

	if allPruned {
		return cleanedRendered, nil
	}

	patch, err := configpatcher.LoadPatch(prunedBytes)
	if err != nil {
		return nil, errors.WithHint(
			errors.Wrapf(err, "loading patch from %q", patchFile),
			"the node body must be a Talos config (full or partial), a JSON Patch list, or a YAML patch list — see https://www.talos.dev/latest/talos-guides/configuration/patching/",
		)
	}

	out, err := configpatcher.Apply(configpatcher.WithBytes(cleanedRendered), []configpatcher.Patch{patch})
	if err != nil {
		return nil, errors.WithHintf(
			errors.Wrapf(err, "applying patch from %q", patchFile),
			"the patch references a path the rendered template does not contain; check the output of: talm template -f %q",
			patchFile,
		)
	}

	merged, err := out.Bytes()
	if err != nil {
		return nil, errors.WithHintf(
			errors.Wrapf(err, "encoding merged config from %q", patchFile),
			"configpatcher.Apply succeeded but the result could not be serialised back to YAML; this is internal — file an issue if reproducible",
		)
	}

	return merged, nil
}

// stripAllPatchDeleteDirectives walks every YAML document in `data` and
// removes every `<key>: {$patch: delete}` pair from mapping nodes,
// returning the cleaned bytes and the identity-prefixed paths of every
// removed pair.
//
// configpatcher.Apply loads the merge target via configloader.NewFromBytes
// WITHOUT WithAllowPatchDelete (apply.go: configOrBytes.Config), so the
// directive-aware decoding pass that would normally extract these pairs
// (configloader/internal/decoder/delete.go AppendDeletesTo) is never
// invoked for the target tree. A directive nested in the target therefore
// reaches the strict v1alpha1.Config decoder unprocessed: when the parent
// field's declared type is a scalar map (e.g. `machine.nodeLabels` is
// map[string]string), the directive's `{$patch: delete}` map-shaped value
// trips the decoder with `cannot construct !!map into string`. Talos's
// ApplyConfiguration RPC has the same constraint on the receiving side,
// so we cannot just forward the directive untouched either.
//
// Stripping the (key, directive) pair from the target preserves its
// observable effect — the named key is absent from the merged config that
// talm sends to Talos — without inventing new merge semantics.
//
// The function returns the cleaned bytes and the identity-prefixed
// paths of every removed pair. The caller uses those paths via
// stripPatchDeleteDirectivesAtPaths to scrub matching entries from the
// patch body, leaving any user-intent directive at a path the chart did
// not own.
//
// Multi-document inputs are handled per-document; the document identity
// tuple (apiVersion+kind+name, or the legacy-root sentinel) is embedded
// in each path so a body that re-orders typed documents relative to
// rendered still pairs the directives by content rather than by
// positional accident.
func stripAllPatchDeleteDirectives(data []byte) ([]byte, []string, error) {
	docs, err := decodeAllYAMLDocuments(data)
	if err != nil {
		return nil, nil, err
	}

	if len(docs) == 0 {
		return data, nil, nil
	}

	var stripped []string
	for _, doc := range docs {
		stripped = append(stripped, removePatchDeleteFromNode(doc, "/"+documentIdentityFromNode(doc), nil)...)
	}

	if len(stripped) == 0 {
		return data, nil, nil
	}

	out, err := encodeAllYAMLDocuments(docs)
	if err != nil {
		return nil, nil, err
	}

	return out, stripped, nil
}

// stripPatchDeleteDirectivesAtPaths walks every YAML document in `data`
// and removes only those `<key>: {$patch: delete}` pairs whose
// identity-prefixed path is present in `paths`. Directives at any other
// path are left intact so configpatcher.LoadPatch can honour them as
// user-intent (load.go: NewFromBytes with allowPatchDelete=true →
// AppendDeletesTo extracts them and applies the deletion as a Selector
// during the merge).
//
// `paths` is the slice returned by stripAllPatchDeleteDirectives on the
// rendered side — i.e. the addresses of every chart-emitted directive,
// each prefixed by its document's identity tuple. Pairing by identity
// rather than by document index lets a body that re-orders typed
// documents relative to rendered still strip the chart-side directives
// from the matching body documents (and leave user-intent directives at
// the same nominal path on a different doc untouched).
//
// When `paths` is empty (rendered carried no directives), nothing is
// stripped from the patch body.
func stripPatchDeleteDirectivesAtPaths(data []byte, paths []string) ([]byte, error) {
	if len(paths) == 0 {
		return data, nil
	}

	docs, err := decodeAllYAMLDocuments(data)
	if err != nil {
		return nil, err
	}

	if len(docs) == 0 {
		return data, nil
	}

	pruneSet := make(map[string]struct{}, len(paths))
	for _, p := range paths {
		pruneSet[p] = struct{}{}
	}

	stripped := 0
	for _, doc := range docs {
		stripped += len(removePatchDeleteFromNode(doc, "/"+documentIdentityFromNode(doc), pruneSet))
	}

	if stripped == 0 {
		return data, nil
	}

	return encodeAllYAMLDocuments(docs)
}

// stripPatchDeleteDirectivesAbsentInTarget walks every YAML document
// in `data` and removes $patch:delete directives whose path does not
// resolve to a key in the matching `target` document. configpatcher.Apply
// otherwise errors with `failed to delete path '...': lookup failed`
// — its Selector-based deleteForPath walks the parsed v1alpha1.Config
// struct and rejects any path segment that does not resolve. Kubernetes
// strategic merge patch treats delete-of-absent as a no-op, so this
// helper restores that semantic before the patch reaches the apply RPC,
// which keeps the chart's own pattern (a body that re-states a chart-
// emitted directive after `talm template -I`) usable on a fresh apply
// where the targeted key has not yet been populated on the node.
//
// `target` is the rendered template AFTER stripAllPatchDeleteDirectives
// has removed every chart-side directive — i.e. the structural shape
// configpatcher.Apply will see as the merge target. A directive whose
// path isn't reachable in that shape is a no-op by definition.
//
// Pairs body and target documents by identity tuple (apiVersion+kind+name,
// or the legacy-root sentinel) so a body re-ordering its typed documents
// relative to rendered still resolves directive paths against the right
// target document. A body document with no matching target document
// (no rendered counterpart at all) gets every directive stripped,
// matching the upstream contract: there is nothing to delete.
func stripPatchDeleteDirectivesAbsentInTarget(data, target []byte) ([]byte, error) {
	bodyDocs, err := decodeAllYAMLDocuments(data)
	if err != nil {
		return nil, err
	}

	if len(bodyDocs) == 0 {
		return data, nil
	}

	targetDocs, err := decodeAllYAMLDocuments(target)
	if err != nil {
		return nil, err
	}

	targetByID := make(map[string]*yaml.Node, len(targetDocs))
	for _, doc := range targetDocs {
		targetByID[documentIdentityFromNode(doc)] = doc
	}

	pruneSet := make(map[string]struct{})

	for _, bdoc := range bodyDocs {
		id := documentIdentityFromNode(bdoc)

		targetDoc := targetByID[id]
		for _, rel := range collectDeleteDirectivePaths(bdoc, "") {
			if !pathExistsInDoc(targetDoc, rel) {
				pruneSet[joinYAMLPath("/"+id, rel)] = struct{}{}
			}
		}
	}

	if len(pruneSet) == 0 {
		return data, nil
	}

	stripped := 0
	for _, doc := range bodyDocs {
		stripped += len(removePatchDeleteFromNode(doc, "/"+documentIdentityFromNode(doc), pruneSet))
	}

	if stripped == 0 {
		return data, nil
	}

	return encodeAllYAMLDocuments(bodyDocs)
}

// collectDeleteDirectivePaths walks `node` and returns the
// JSON-pointer-escaped paths (relative to the document root, no
// identity prefix) of every $patch:delete directive it contains.
// Used by stripPatchDeleteDirectivesAbsentInTarget to enumerate body's
// directives so each can be checked against the target document.
func collectDeleteDirectivePaths(node *yaml.Node, parentRel string) []string {
	if node == nil {
		return nil
	}

	var found []string

	switch node.Kind {
	case yaml.DocumentNode:
		for _, child := range node.Content {
			found = append(found, collectDeleteDirectivePaths(child, parentRel)...)
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			keyNode := node.Content[i]
			valueNode := node.Content[i+1]

			if keyNode.Kind != yaml.ScalarNode {
				continue
			}

			childRel := joinYAMLPath(parentRel, jsonPointerEscape(keyNode.Value))
			if isPatchDeleteDirective(valueNode) {
				found = append(found, childRel)

				continue
			}

			if valueNode.Kind == yaml.MappingNode {
				found = append(found, collectDeleteDirectivePaths(valueNode, childRel)...)
			}
		}
	case yaml.SequenceNode, yaml.ScalarNode, yaml.AliasNode:
		// Directives live only inside mappings (as values of named keys).
		// Sequences carry no key, scalars/aliases carry no children to walk.
	}

	return found
}

// pathExistsInDoc resolves `path` (a slash-separated sequence of
// JSON-pointer-escaped segments, no leading slash, no document
// identity prefix) against the YAML document `doc` and returns true
// when every segment names an existing key in the corresponding
// mapping. An empty path resolves to the document root (true unless
// doc is nil or non-mapping at the root).
//
// The walk is deliberately mapping-only: configpatcher.Apply's
// Selector-based deleteForPath addresses scalar map fields by name
// (machine.nodeLabels.<label>) and bails on the first non-matching
// segment regardless of the target's kind below it. This helper
// reproduces the same predicate so a path declared no-op here is
// guaranteed to be the same path the apply RPC would have erred on.
func pathExistsInDoc(doc *yaml.Node, path string) bool {
	if doc == nil {
		return false
	}

	cur := doc
	if cur.Kind == yaml.DocumentNode && len(cur.Content) > 0 {
		cur = cur.Content[0]
	}

	if cur == nil || cur.Kind != yaml.MappingNode {
		return false
	}

	if path == "" {
		return true
	}

	for escaped := range strings.SplitSeq(path, "/") {
		seg := jsonPointerUnescape(escaped)

		if cur.Kind != yaml.MappingNode {
			return false
		}

		found := false

		for i := 0; i+1 < len(cur.Content); i += 2 {
			if cur.Content[i].Value == seg {
				cur = cur.Content[i+1]
				found = true

				break
			}
		}

		if !found {
			return false
		}
	}

	return true
}

// jsonPointerUnescape reverses jsonPointerEscape per RFC 6901
// (~1 → /, ~0 → ~). Order matters: ~0 must be processed last so a
// literal "~0" written into a YAML key survives the round-trip.
func jsonPointerUnescape(s string) string {
	s = strings.ReplaceAll(s, "~1", "/")
	s = strings.ReplaceAll(s, "~0", "~")

	return s
}

func decodeAllYAMLDocuments(data []byte) ([]*yaml.Node, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))

	var docs []*yaml.Node

	for {
		var doc yaml.Node

		err := dec.Decode(&doc)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			return nil, errors.WithHint(
				errors.Wrap(err, "decoding YAML before stripping $patch:delete directives"),
				"the input is malformed YAML; check for unbalanced quotes or stray indentation in the rendered template or node body",
			)
		}

		docs = append(docs, &doc)
	}

	return docs, nil
}

func encodeAllYAMLDocuments(docs []*yaml.Node) ([]byte, error) {
	var buf bytes.Buffer

	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)

	for _, doc := range docs {
		if err := enc.Encode(doc); err != nil {
			return nil, errors.WithHint(
				errors.Wrap(err, "re-encoding YAML after stripping $patch:delete directives"),
				"the YAML.v3 encoder rejected the post-strip tree; file an issue with the rendered+body that triggered it",
			)
		}
	}

	if err := enc.Close(); err != nil {
		return nil, errors.WithHint(
			errors.Wrap(err, "closing YAML encoder after stripping $patch:delete directives"),
			"the YAML.v3 encoder failed to flush; file an issue with the rendered+body that triggered it",
		)
	}

	return buf.Bytes(), nil
}

// removePatchDeleteFromNode recursively walks `node` and removes every
// (key, value) pair where value is the directive `{$patch: delete}`.
// When `prunePaths` is nil every directive is removed (rendered-side
// pass). When non-nil only directives at JSON-Pointer paths in the set
// are removed; others survive for downstream configpatcher.LoadPatch.
//
// Returns the JSON-Pointer paths of every removed pair.
func removePatchDeleteFromNode(node *yaml.Node, parentPath string, prunePaths map[string]struct{}) []string {
	if node == nil {
		return nil
	}

	var removed []string

	switch node.Kind {
	case yaml.DocumentNode:
		for _, child := range node.Content {
			removed = append(removed, removePatchDeleteFromNode(child, parentPath, prunePaths)...)
		}
	case yaml.MappingNode:
		kept := make([]*yaml.Node, 0, len(node.Content))
		for i := 0; i+1 < len(node.Content); i += 2 {
			keyNode := node.Content[i]
			valueNode := node.Content[i+1]
			childPath := parentPath + "/" + jsonPointerEscape(keyNode.Value)

			if isPatchDeleteDirective(valueNode) {
				if prunePaths == nil {
					removed = append(removed, childPath)

					continue
				}

				if _, prune := prunePaths[childPath]; prune {
					removed = append(removed, childPath)

					continue
				}
			}

			removed = append(removed, removePatchDeleteFromNode(valueNode, childPath, prunePaths)...)
			kept = append(kept, keyNode, valueNode)
		}

		node.Content = kept
	case yaml.SequenceNode:
		for i, child := range node.Content {
			removed = append(removed, removePatchDeleteFromNode(child, fmt.Sprintf("%s/%d", parentPath, i), prunePaths)...)
		}
	case yaml.ScalarNode, yaml.AliasNode:
		// Scalars and aliases have no children, so they cannot host a
		// $patch:delete directive — nothing to remove.
	}

	return removed
}

// jsonPointerEscape encodes a YAML mapping key as a JSON Pointer segment
// per RFC 6901 (~ → ~0, / → ~1). The encoded form is what JSON Patch
// implementations expect, but here we use it only to give every directive
// a unique, comparable identity across the rendered- and body-side
// strips.
func jsonPointerEscape(s string) string {
	s = strings.ReplaceAll(s, "~", "~0")
	s = strings.ReplaceAll(s, "/", "~1")

	return s
}

// isPatchDeleteDirective reports whether `n` is exactly the YAML mapping
// `{$patch: delete}` — a single key/value pair with scalar key "$patch"
// and scalar value "delete".
func isPatchDeleteDirective(n *yaml.Node) bool {
	if n == nil || n.Kind != yaml.MappingNode {
		return false
	}

	if len(n.Content) != 2 {
		return false
	}

	k, v := n.Content[0], n.Content[1]

	return k.Kind == yaml.ScalarNode && k.Value == "$patch" &&
		v.Kind == yaml.ScalarNode && v.Value == "delete"
}

// pruneBodyIdentitiesAgainstRendered removes from body every key whose value
// is deep-equal to the same key in rendered. Talos's strategic-merge appends
// to primitive arrays rather than treating them as a set, so a body that
// re-states an unchanged primitive list (the dominant case after
// `talm template -I` writes the rendered template back into the node file as
// the body) would otherwise duplicate every entry on each apply round-trip:
// every certSAN, every nameserver, every podSubnet doubles per round-trip.
//
// Returns (prunedBytes, allPruned, err). When allPruned is true the body
// carried no semantic change beyond the rendered template and the caller
// should short-circuit to rendered.
//
// Multi-document inputs (Talos v1.12+ output format) are pruned per-document:
// each body document is matched against a rendered document by its identity
// tuple (apiVersion + kind + name for typed documents; the empty tuple for
// the legacy v1alpha1 root config), then pruneIdenticalKeys runs on the
// pair. Body documents with no matching rendered document survive untouched
// — they are user additions that the merge needs to see.
//
// Re-encoding goes through a fresh yaml.Encoder per kept document. That
// loses the original key order and any comments (including the
// modeline) — configpatcher.LoadPatch reads structure, not comments, so
// this is fine for the apply path; do not feed the output back into a
// human-facing rendering surface.
func pruneBodyIdentitiesAgainstRendered(body, rendered []byte) ([]byte, bool, error) {
	bodyDocs, bodyAllMaps, err := decodeAsMaps(body)
	if err != nil {
		return nil, false, errors.WithHint(
			errors.Wrap(err, "parsing body"),
			"the node body did not parse as YAML; check the file referenced by the modeline for unbalanced quotes or stray indentation",
		)
	}

	if !bodyAllMaps {
		// JSON Patch / YAML patch-list bodies: top-level is a sequence,
		// not a mapping, so the identity-prune step has no map keys to
		// compare. Pass through untouched and let configpatcher.LoadPatch
		// route it through the JSON Patch path (load.go: jsonpatch.DecodePatch).
		return body, false, nil
	}

	renderedDocs, _, err := decodeAsMaps(rendered)
	if err != nil {
		// Rendered should always parse — engine.Render produced it from
		// chart templates this binary owns. Surface the parse error
		// directly: continuing on to LoadPatch with the original body
		// would mask the real failure as a downstream configpatcher
		// error against malformed bytes.
		return nil, false, errors.WithHint(
			errors.Wrap(err, "parsing rendered template for identity prune"),
			"the rendered template did not parse as YAML; this points at a chart-helper bug, not a user input issue",
		)
	}

	if len(bodyDocs) == 0 {
		return nil, true, nil
	}

	renderedByID := make(map[string]map[string]any, len(renderedDocs))
	for _, doc := range renderedDocs {
		renderedByID[documentIdentity(doc)] = doc
	}

	keptDocs := make([]map[string]any, 0, len(bodyDocs))
	for _, bdoc := range bodyDocs {
		id := documentIdentity(bdoc)
		if rdoc, ok := renderedByID[id]; ok {
			pruneIdenticalKeys(bdoc, rdoc)
			// Typed multi-doc bodies use apiVersion/kind/name as the
			// identity tuple configpatcher.LoadPatch routes on. Those
			// keys are byte-equal between body and rendered when the
			// user does a partial edit, so the prune deletes them and
			// the surviving body looks like a bare {field: value} map
			// that LoadPatch rejects with "missing kind". Re-attach
			// the identity tuple from rendered when the body kept any
			// override fields. The legacy v1alpha1 root carries no
			// apiVersion/kind/name (its top-level identity is the
			// version field, which is at the same nesting level as the
			// machine/cluster blocks), so this only fires for the typed
			// multi-doc shape.
			if id != legacyRootIdentity && len(bdoc) > 0 {
				reattachIdentityKeys(bdoc, rdoc)
			}
		}

		if len(bdoc) > 0 {
			keptDocs = append(keptDocs, bdoc)
		}
	}

	if len(keptDocs) == 0 {
		return nil, true, nil
	}

	var buf bytes.Buffer

	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)

	for _, doc := range keptDocs {
		if err := enc.Encode(doc); err != nil {
			return nil, false, errors.WithHint(
				errors.Wrap(err, "re-encoding pruned body"),
				"the YAML.v3 encoder rejected the post-prune body; file an issue with the rendered+body that triggered it",
			)
		}
	}

	if err := enc.Close(); err != nil {
		return nil, false, errors.WithHint(
			errors.Wrap(err, "closing encoder for pruned body"),
			"the YAML.v3 encoder failed to flush; file an issue with the rendered+body that triggered it",
		)
	}

	return buf.Bytes(), false, nil
}

// replaceSemanticPaths lists YAML paths where Talos's upstream merge
// layer is annotated with `merge:"replace"`: at those paths, the
// patcher overwrites rendered's value with body's verbatim — unless
// body is the zero value, in which case rendered survives. Each entry
// mirrors a struct field tagged `merge:"replace"` in the upstream
// machinery types (collected from
// pkg/machinery/config/types/v1alpha1/v1alpha1_types.go and
// pkg/machinery/config/types/network/rule_config.go):
//
//   - cluster/network/podSubnets — v1alpha1 `PodSubnet []string ... merge:"replace"`
//   - cluster/network/serviceSubnets — v1alpha1 `ServiceSubnet []string ... merge:"replace"`
//   - cluster/apiServer/auditPolicy — v1alpha1 `AuditPolicyConfig Unstructured ... merge:"replace"`
//   - ingress — typed NetworkRuleConfig `Ingress IngressConfig ... merge:"replace"`
//   - portSelector/ports — typed NetworkRuleConfig `Ports PortRanges ... merge:"replace"`
//
// At these paths the prune must NOT subtract rendered-side entries from
// body's primitive list, recurse into body's map, or descend into body's
// object array: any of those reduce body to "just the user's deltas",
// the upstream replace then writes those deltas verbatim, and rendered's
// other entries / map keys silently vanish from the merged config — a
// partial-edit on podSubnets that adds a CIDR ends up losing the
// original; a partial-edit on auditPolicy that adds a rule loses the
// rendered apiVersion/kind/other rules.
//
// Paths are walked relative to the document root that owns the field,
// so the typed NetworkRuleConfig entries appear without an apiVersion/
// kind prefix — pruneBodyIdentitiesAgainstRendered pairs body and
// rendered docs by identity tuple before calling pruneIdenticalKeys, so
// each walk sees only its own document's keys. The bare paths above do
// not collide with anything in the v1alpha1 root (no `ingress` or
// `portSelector` keys exist there), so a flat lookup is sufficient.
//
// The deep-equal short-circuit at the top of pruneIdenticalKeysAt is
// still safe for replace paths: when body byte-equals rendered, deleting
// the body key reduces body to the zero value at that path, and the
// upstream replace then leaves rendered untouched. Skipping kicks in
// only on the partial-edit branches below the deep-equal check.
var replaceSemanticPaths = map[string]struct{}{
	"cluster/network/podSubnets":     {},
	"cluster/network/serviceSubnets": {},
	"cluster/apiServer/auditPolicy":  {},
	"ingress":                        {},
	"portSelector/ports":             {},
}

// objectArrayMergeKeys lists the identity fields Talos's upstream
// strategic-merge layer uses to match elements of an object array at
// the given YAML path. Each entry mirrors a custom Merge method on the
// corresponding v1alpha1 List type: when upstream merges-by-identity at
// a path, this prune must too, or partial edits would re-attach
// identity-bearing fields onto a body element that the patcher would
// then RE-merge in place — appending the inner primitive arrays
// (addresses, nested vlan addresses, exemption namespaces) and
// duplicating every rendered entry on every apply round-trip.
//
// Paths are slash-joined relative to the document root, with no leading
// slash and no array-index suffix — array elements share the parent's
// path. The list per path is checked in order against BODY only: the
// first key body sets to a non-empty value (in declaration order,
// mirroring upstream's switch enumeration) is used as the SOLE match
// key against rendered. matchObjectArrayItem does the body-driven
// selection — see its doc comment for why an "any-key-both-have"
// intersection would silently drop user-adds.
//
// Only the three paths below have a confirmed custom upstream Merge:
//
//   - machine.network.interfaces — NetworkDeviceList.Merge matches by
//     DeviceInterface (`interface`) or DeviceSelector (`deviceSelector`).
//   - machine.network.interfaces[].vlans — VlanList.Merge matches by
//     VlanID (`vlanId`).
//   - cluster.apiServer.admissionControl — AdmissionPluginConfigList.Merge
//     matches by PluginName (`name`).
//
// Other object arrays in the v1alpha1 schema (extraVolumes,
// seccompProfiles, inlineManifests, kernel.modules, wireguard.peers,
// authorizationConfig, ...) have no custom upstream Merge — the patcher
// simply appends body's elements to rendered's. Re-attaching an identity
// key on those would not avoid the upstream append; it would just leave
// behind a body element that lands as a duplicate next to rendered's.
// For those paths the deep-equal fallback in matchObjectArrayItem still
// drops body items that byte-equal a rendered item (the dominant case
// after `talm template -I` writes the rendered template back as the body),
// preserving idempotence on full restates without inventing identity
// where the upstream layer recognises none.
//
// One upstream type with a custom Merge is intentionally omitted:
// ConfigFileList (typed ExtensionServiceConfig.configFiles, matched by
// mountPath). Its element ConfigFile carries only string fields, so the
// upstream mergeConfigFile + merge.Merge field-by-field already produces
// the right result on partial edits — the deep-equal fallback in
// matchObjectArrayItem handles full-restate idempotence, and adding a
// path-keyed identity match would not change correctness, only firing
// path. Listing it would be misleading: the table is meant to call out
// fields where the inner-primitive append regression is reachable.
//
// Routes (machine.network.interfaces[].routes) sit in this same fallback
// bucket: the schema declares no single primary key for a route, so the
// only "same item" semantic available is byte-equality across all fields.
var objectArrayMergeKeys = map[string][]string{
	"machine/network/interfaces":         {"interface", "deviceSelector"},
	"machine/network/interfaces/vlans":   {"vlanId"},
	"cluster/apiServer/admissionControl": {"name"},
}

// pruneIdenticalKeys recursively deletes every body[k] that deep-equals
// rendered[k] (mutating `body` in place — the caller still holds the
// reference). When a body sub-map becomes empty after pruning, the whole
// entry is removed so the encoded output stays minimal. For primitive
// arrays the function additionally subtracts every element already
// present in rendered's array, replacing body's slice with the user-add
// difference (when the diff is empty the entry is deleted) — this
// neutralises Talos's strategic-merge primitive-array append behaviour
// for both byte-identical and partial-edit cases — without it, every
// `talm template -I` round-trip would double every certSAN, nameserver,
// and podSubnet entry on the next apply.
//
// For object arrays the function descends into elements matched by
// their identity field (the per-path table above, with deep-equal as
// fallback), recurses, drops items that fully reduce to nothing, and
// re-attaches the identity-bearing keys on items that retained payload
// so the upstream merge can still match the element. Without this
// descent, configpatcher.Apply matches elements by identity field
// upstream and then appends the rendered-side primitive arrays nested
// inside them — duplicating every interface address, route, vlan
// address, and admission-control exemption namespace per apply
// round-trip.
//
// pruneIdenticalKeys is the document-root entry point; pruneIdenticalKeysAt
// threads the YAML path so the object-array descent can look up the
// identity key for the current location.
func pruneIdenticalKeys(body, rendered map[string]any) {
	pruneIdenticalKeysAt(body, rendered, "")
}

// pruneIdenticalKeysAt is pruneIdenticalKeys's recursive workhorse.
// yamlPath is a slash-joined YAML path from the document root (e.g.
// "machine/network/interfaces"), used to look up the configured
// identity field for an object-array branch. The empty path is the
// document root.
//
// The parameter is named yamlPath rather than path to avoid shadowing
// the stdlib path package imported elsewhere in this file.
func pruneIdenticalKeysAt(body, rendered map[string]any, yamlPath string) {
	for k, bodyV := range body {
		renderedV, exists := rendered[k]
		if !exists {
			continue
		}

		if reflect.DeepEqual(bodyV, renderedV) {
			delete(body, k)

			continue
		}

		childPath := joinYAMLPath(yamlPath, k)
		if _, replace := replaceSemanticPaths[childPath]; replace {
			// Upstream `merge:"replace"` overwrites rendered with body
			// verbatim. Any prune at this path leaks rendered-side
			// entries out of the final config — see replaceSemanticPaths.
			continue
		}

		if bodySub, ok := bodyV.(map[string]any); ok {
			if renderedSub, ok2 := renderedV.(map[string]any); ok2 {
				// Only delete when the recursive prune actually
				// removed every child entry. If bodySub was already
				// empty before the recursion, leave it: a user-intent
				// empty map (e.g. `key: {}` to clear a section) must
				// reach the merge as-is, not get silently dropped so
				// rendered's populated value wins.
				before := len(bodySub)
				pruneIdenticalKeysAt(bodySub, renderedSub, childPath)

				if before > 0 && len(bodySub) == 0 {
					delete(body, k)
				}

				continue
			}
		}

		if bodySlice, ok := bodyV.([]any); ok {
			if renderedSlice, ok2 := renderedV.([]any); ok2 {
				if isPrimitiveSlice(bodySlice) && isPrimitiveSlice(renderedSlice) {
					diff := primitiveSliceDifference(bodySlice, renderedSlice)
					if len(diff) == 0 {
						delete(body, k)
					} else {
						body[k] = diff
					}

					continue
				}

				pruned := pruneObjectArrayItems(bodySlice, renderedSlice, childPath)
				if len(pruned) == 0 {
					delete(body, k)
				} else {
					body[k] = pruned
				}
			}
		}
	}
}

// pruneObjectArrayItems iterates body's object-array elements, matches
// each to a rendered element by the registered identity key (or
// deep-equal fallback), recurses into matched pairs, and drops items
// whose payload reduced to nothing after recursion. Re-attaches the
// identity keys from rendered when the body item retained payload but
// the inner deep-equal pass stripped its identity-bearing fields, so
// the upstream strategic-merge can still match the element it belongs
// to.
//
// Body items that the recursion fully consumed are dropped — leaving
// behind an item that only carries its identity key would force
// configpatcher.Apply into a no-op match round and (when the only
// rendered-side payload was a primitive list) re-trigger the
// strategic-merge append we are trying to neutralise. Items with no
// rendered counterpart are user-adds and are kept verbatim.
func pruneObjectArrayItems(body, rendered []any, yamlPath string) []any {
	keys := objectArrayMergeKeys[yamlPath]

	out := make([]any, 0, len(body))
	for _, bElem := range body {
		bMap, ok := bElem.(map[string]any)
		if !ok {
			out = append(out, bElem)

			continue
		}

		rMap := matchObjectArrayItem(bMap, rendered, keys)
		if rMap == nil {
			out = append(out, bElem)

			continue
		}

		before := len(bMap)
		pruneIdenticalKeysAt(bMap, rMap, yamlPath)

		if before > 0 && len(bMap) == 0 {
			continue
		}

		for _, idKey := range keys {
			if _, hasInBody := bMap[idKey]; hasInBody {
				continue
			}

			if v, ok := rMap[idKey]; ok {
				bMap[idKey] = v
			}
		}

		out = append(out, bMap)
	}

	return out
}

// matchObjectArrayItem returns the rendered map sharing an identity
// field value with body. keys lists the allowed identity fields for
// the current YAML path. When keys is non-empty the helper mirrors
// upstream's body-driven selection: it picks the first identity key
// the body sets non-empty (in the table's declaration order, matching
// upstream's switch/case enumeration) and then matches ONLY on that
// key. Falling back to a different key when the chosen one does not
// match would group items the upstream patcher considers distinct —
// e.g. body's interface=eth0 vs rendered's interface=eth1 both with
// the same deviceSelector: upstream's NetworkDeviceList.mergeDevice
// picks body.DeviceInterface (non-empty) and finds no match, so it
// appends body verbatim; if the prune fell back to deviceSelector it
// would consume body's element and silently drop the user's eth0.
//
// When keys is empty (no entry in objectArrayMergeKeys for the path)
// the helper falls back to deep-equal: that catches schema fields with
// no single primary key — most notably machine.network.interfaces[]
// .routes — where two items are the "same" only if every field
// matches, which is the right semantic for dedup at this layer. A
// no-match returns nil so the caller can treat unknown items as
// user-adds.
func matchObjectArrayItem(body map[string]any, rendered []any, keys []string) map[string]any {
	if len(keys) > 0 {
		var (
			chosenKey string
			chosenVal any
		)

		for _, k := range keys {
			if v, ok := body[k]; ok && hasIdentityValue(v) {
				chosenKey = k
				chosenVal = v

				break
			}
		}

		if chosenKey == "" {
			return nil
		}

		for _, rElem := range rendered {
			rMap, ok := rElem.(map[string]any)
			if !ok {
				continue
			}

			if rv, hasR := rMap[chosenKey]; hasR && reflect.DeepEqual(rv, chosenVal) {
				return rMap
			}
		}

		return nil
	}

	for _, rElem := range rendered {
		if reflect.DeepEqual(rElem, body) {
			rMap, _ := rElem.(map[string]any)

			return rMap
		}
	}

	return nil
}

// hasIdentityValue reports whether v is a non-empty identity value —
// the analogue of upstream's `DeviceInterface != ""` and
// `DeviceSelector != nil` predicates against a decoded map. A zero
// string or empty map at an identity slot signals "the user did not
// pick this identity"; the upstream switch falls through to the next
// case in that situation, and matchObjectArrayItem must do the same
// or it will collapse a user-add onto the wrong rendered element.
func hasIdentityValue(v any) bool {
	if v == nil {
		return false
	}

	switch x := v.(type) {
	case string:
		return x != ""
	case map[string]any:
		return len(x) > 0
	case []any:
		return len(x) > 0
	default:
		return true
	}
}

// joinYAMLPath returns parent + "/" + key, dropping the separator when
// parent is the document root (the empty string). Used by the
// object-array descent to look up the configured identity field for
// the current location in objectArrayMergeKeys.
func joinYAMLPath(parent, key string) string {
	if parent == "" {
		return key
	}

	return parent + "/" + key
}

// isPrimitiveSlice reports whether every element of `s` is a YAML scalar
// (string, number, bool, nil) — i.e. a value Talos's strategic-merge
// would append rather than merge by key. Object arrays return false and
// are left to the configpatcher's patchMergeKey handling. Narrow integer
// widths are listed defensively: yaml.v3 returns `int` and `float64` for
// numbers in practice, but if a future caller hands us a body decoded
// by a different unmarshaller, an `[]int8` (or similar) would otherwise
// fall through to the default branch and skip the dedup pass.
func isPrimitiveSlice(s []any) bool {
	for _, e := range s {
		switch e.(type) {
		case nil, string, bool,
			int, int8, int16, int32, int64,
			uint, uint8, uint16, uint32, uint64,
			float32, float64:
			continue
		default:
			return false
		}
	}

	return true
}

// primitiveSliceDifference returns body \ rendered — every element of
// body whose deep-equal counterpart is not in rendered. Order from
// body is preserved on the elements that survive. Used to strip out
// rendered-side prefix entries from a partial-edit body so the
// strategic-merge append step does not duplicate them.
//
// Trade-off: this loses any user-side reordering of primitive arrays.
// If body is `[b, a]` and rendered is `[a, b]`, both elements match
// and the difference is `[]`, so the caller deletes the body's value
// and rendered's `[a, b]` order survives untouched. Strategic-merge's
// own primitive-array semantics already cannot replace, only append,
// so a body cannot impose a new order on a rendered list anyway —
// even without this prune, the merge result would have been the
// concatenation `[a, b]` ++ `[b, a]` (rendered prepended, body appended
// in body order).
// The dedup makes the silent-undo more visible because it now reaches
// the partial-edit case, but the underlying constraint is upstream.
// Callers that need ordered overrides have to model the field as a
// non-primitive merge target (e.g. patchMergeKey on an object array)
// or reach for a JSON Patch body, which the engine forwards through
// LoadPatch unchanged.
func primitiveSliceDifference(body, rendered []any) []any {
	out := make([]any, 0, len(body))
	for _, b := range body {
		found := false

		for _, r := range rendered {
			if reflect.DeepEqual(b, r) {
				found = true

				break
			}
		}

		if !found {
			out = append(out, b)
		}
	}

	return out
}

// decodeAsMaps parses every YAML document in `data` into a generic map.
// Returns the decoded documents, a flag indicating whether every
// document unmarshalled into map[string]any, and any error.
//
// allMaps == false signals that at least one document had a non-mapping
// top level — typically a JSON Patch list or YAML patch-list body, both
// of which are sequence-shaped at the root. The caller must NOT consume
// `docs` in that case; the right thing to do is bypass identity-keyed
// pruning entirely and forward the original bytes to configpatcher.LoadPatch.
//
// Returns (nil, true, nil) for empty input — vacuously "all maps".
func decodeAsMaps(data []byte) ([]map[string]any, bool, error) {
	if len(data) == 0 {
		return nil, true, nil
	}

	dec := yaml.NewDecoder(bytes.NewReader(data))

	var docs []map[string]any

	allMaps := true

	for {
		var doc any
		if err := dec.Decode(&doc); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			return nil, false, err
		}

		if doc == nil {
			continue
		}

		asMap, ok := doc.(map[string]any)
		if !ok {
			allMaps = false

			continue
		}

		docs = append(docs, asMap)
	}

	return docs, allMaps, nil
}

// legacyRootIdentity is the sentinel documentIdentity returns for the
// legacy v1alpha1 root config (no apiVersion/kind/name fields). The
// per-document identity prune skips identity-key reattachment for this
// shape because the legacy root carries no identity tuple to begin
// with — its only top-level identifier is `version`, which is at the
// same nesting level as the machine/cluster blocks rather than peer
// to a routable apiVersion/kind/name.
const legacyRootIdentity = "__legacy_root__"

// documentIdentity returns a stable string identifying a Talos config
// document. The legacy v1alpha1 root config (a single document with
// `version: v1alpha1` at the top and no apiVersion/kind/name fields)
// collapses to a fixed sentinel so that legacy bodies match legacy
// renders. Typed documents (HostnameConfig, LinkConfig, RegistryMirrorConfig,
// Layer2VIPConfig, …) identify by `apiVersion/kind` plus `/name` when a
// name is present.
//
// The shape mirrors configpatcher's StrategicMerge documentID
// (machinery/config/configpatcher/strategic.go: documentID), with one
// deliberate difference: upstream omits the trailing `/name` segment
// when the document does not implement NamedDocument; this function
// follows the same rule via the empty-name shortcut so a typed doc
// without a `name` field collides with itself across body and rendered
// streams instead of with every other unnamed doc of the same kind.
func documentIdentity(doc map[string]any) string {
	apiVersion, _ := doc["apiVersion"].(string)

	kind, _ := doc["kind"].(string)
	if apiVersion == "" && kind == "" {
		return legacyRootIdentity
	}

	id := apiVersion + "/" + kind
	if name, _ := doc["name"].(string); name != "" {
		id += "/" + name
	}

	return id
}

// documentIdentityFromNode returns the same identity tuple as
// documentIdentity, but operates on a *yaml.Node instead of a decoded
// map[string]any. The strip/prune-by-path passes work on yaml.Node
// trees (so they can preserve comments and key order on round-trip),
// so they need an identity helper that does not require a parallel
// map decode. The output is byte-for-byte equal to documentIdentity's
// output for the same logical document.
func documentIdentityFromNode(doc *yaml.Node) string {
	root := doc
	if root != nil && root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		root = root.Content[0]
	}

	if root == nil || root.Kind != yaml.MappingNode {
		return legacyRootIdentity
	}

	var apiVersion, kind, name string

	for i := 0; i+1 < len(root.Content); i += 2 {
		k := root.Content[i]

		v := root.Content[i+1]
		if k.Kind != yaml.ScalarNode || v.Kind != yaml.ScalarNode {
			continue
		}

		switch k.Value {
		case "apiVersion":
			apiVersion = v.Value
		case "kind":
			kind = v.Value
		case "name":
			name = v.Value
		}
	}

	if apiVersion == "" && kind == "" {
		return legacyRootIdentity
	}

	id := apiVersion + "/" + kind
	if name != "" {
		id += "/" + name
	}

	return id
}

// reattachIdentityKeys copies apiVersion / kind / name from rendered
// onto body when body's prune dropped them. Only intended for typed
// multi-doc bodies — the legacy v1alpha1 root carries no identity
// tuple to reattach. Each key is reattached only when missing, so a
// body that explicitly overrides one (rare, but possible — e.g. an
// operator pinning a different name on a Layer2VIPConfig) keeps its
// override.
func reattachIdentityKeys(body, rendered map[string]any) {
	for _, k := range []string{"apiVersion", "kind", "name"} {
		if _, has := body[k]; has {
			continue
		}

		if v, ok := rendered[k]; ok {
			body[k] = v
		}
	}
}

// NodeFileHasOverlay reports whether a node file carries a non-empty
// per-node body below its modeline. The apply path uses this to reject
// multi-node node files that would otherwise stamp the same pinned
// hostname/address/VIP onto every target.
func NodeFileHasOverlay(patchFile string) (bool, error) {
	data, err := os.ReadFile(patchFile)
	if err != nil {
		return false, fmt.Errorf("reading node file %s: %w", patchFile, err)
	}

	return !isEffectivelyEmptyYAML(data), nil
}

// isEffectivelyEmptyYAML reports whether the input contains nothing but
// YAML comments, document separators, and whitespace. Used by
// MergeFileAsPatch to detect modeline-only node files that the Talos
// config-patcher misclassifies as empty JSON6902 patches.
//
// Document separators must appear at column 0 per the YAML spec; an
// indented "  ---" is a scalar inside a parent mapping, not a
// separator, so the comparison is against the line minus only trailing
// whitespace rather than against the fully trimmed form.
func isEffectivelyEmptyYAML(data []byte) bool {
	for line := range bytes.SplitSeq(data, []byte("\n")) {
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 {
			continue
		}

		if trimmed[0] == '#' {
			continue
		}

		untrailed := string(bytes.TrimRight(line, " \t\r"))
		if untrailed == "---" || untrailed == "..." {
			continue
		}

		return false
	}

	return true
}

// Render executes the rendering of templates based on the provided options.
func Render(ctx context.Context, c *client.Client, opts Options) ([]byte, error) {
	// Validate TalosVersion early so malformed values surface a user-friendly
	// error instead of an opaque "semverCompare: invalid semantic version" from
	// inside template rendering.
	if opts.TalosVersion != "" {
		if _, err := config.ParseContractFromVersion(opts.TalosVersion); err != nil {
			return nil, fmt.Errorf("invalid talos-version: %w", err)
		}
	}

	// Gather facts and enable lookup options
	if !opts.Offline {
		cmdName := opts.CommandName
		if cmdName == "" {
			cmdName = "talm"
		}

		if err := helpers.FailIfMultiNodes(ctx, cmdName); err != nil {
			return nil, err
		}

		helmEngine.LookupFunc = newLookupFunction(ctx, c)
	}

	chartPath, err := os.Getwd()
	if err != nil {
		return nil, err
	}

	if opts.Root != "" {
		chartPath = opts.Root
	}

	chrt, err := loader.LoadDir(chartPath)
	if err != nil {
		return nil, err
	}

	values, err := loadValues(opts)
	if err != nil {
		return nil, err
	}

	rootValues := map[string]any{
		"Values":       mergeMaps(chrt.Values, values),
		"TalosVersion": opts.TalosVersion,
	}

	eng := helmEngine.Engine{}

	out, err := eng.Render(chrt, rootValues)
	if err != nil {
		return nil, err
	}

	if len(opts.TemplateFiles) == 0 {
		return nil, errors.New("templates are not set for the command: please use `--file` or `--template` flag")
	}

	configPatches := []string{}

	for _, templateFile := range opts.TemplateFiles {
		// Use path.Join (not filepath.Join) because helm engine keys always use forward slashes
		requestedTemplate := path.Join(chrt.Name(), NormalizeTemplatePath(templateFile))

		configPatch, ok := out[requestedTemplate]
		if !ok {
			return nil, errors.Newf("template %s not found", templateFile)
		}

		configPatches = append(configPatches, configPatch)
	}

	finalConfig, err := applyPatchesAndRenderConfig(opts, configPatches)
	if err != nil {
		// TODO
		return nil, err
	}

	return finalConfig, nil
}

// Imported from Helm
// https://github.com/helm/helm/blob/c6beb169d26751efd8131a5d65abe75c81a334fb/pkg/cli/values/options.go#L44
func loadValues(opts Options) (map[string]any, error) {
	// Base map to hold the merged values
	base := make(map[string]any)

	// Load values from files specified with -f or --values
	for _, filePath := range opts.ValueFiles {
		currentMap := make(map[string]any)

		bytes, err := os.ReadFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("failed to read values file %s: %w", filePath, err)
		}

		if err := yaml.Unmarshal(bytes, &currentMap); err != nil {
			return nil, fmt.Errorf("failed to unmarshal values from file %s: %w", filePath, err)
		}

		base = mergeMaps(base, currentMap)
	}

	// Parse and merge values from --set-json
	for _, value := range opts.JsonValues {
		currentMap := make(map[string]any)
		if err := json.Unmarshal([]byte(value), &currentMap); err != nil {
			return nil, fmt.Errorf("failed to unmarshal JSON value '%s': %w", value, err)
		}

		base = mergeMaps(base, currentMap)
	}

	// Parse and merge values from --set
	for _, value := range opts.Values {
		if err := strvals.ParseInto(value, base); err != nil {
			return nil, fmt.Errorf("failed to parse set value '%s': %w", value, err)
		}
	}

	// Parse and merge values from --set-string
	for _, value := range opts.StringValues {
		if err := strvals.ParseIntoString(value, base); err != nil {
			return nil, fmt.Errorf("failed to parse set-string value '%s': %w", value, err)
		}
	}

	// Parse and merge values from --set-file
	for _, value := range opts.FileValues {
		content, err := os.ReadFile(value)
		if err != nil {
			return nil, fmt.Errorf("failed to read file for set-file value '%s': %w", value, err)
		}

		if err := strvals.ParseInto(fmt.Sprintf("%s=%s", value, content), base); err != nil {
			return nil, fmt.Errorf("failed to parse set-file value '%s': %w", value, err)
		}
	}

	// Parse and merge values from --set-literal
	for _, value := range opts.LiteralValues {
		if err := strvals.ParseInto(value, base); err != nil {
			return nil, fmt.Errorf("failed to parse set-literal value '%s': %w", value, err)
		}
	}

	return base, nil
}

// Imported from Helm
// https://github.com/helm/helm/blob/c6beb169d26751efd8131a5d65abe75c81a334fb/pkg/cli/values/options.go#L108
func mergeMaps(a, b map[string]any) map[string]any {
	out := make(map[string]any, len(a))
	maps.Copy(out, a)

	for k, v := range b {
		if vm, ok := v.(map[string]any); ok {
			if bv, ok := out[k]; ok {
				if bvm, ok := bv.(map[string]any); ok {
					out[k] = mergeMaps(bvm, vm)

					continue
				}
			}
		}

		out[k] = v
	}

	return out
}

// isTalosConfigPatch checks if a YAML document is a Talos config patch.
// Returns (isTalosPatch, parseError) - parseError is non-nil if YAML is invalid.
func isTalosConfigPatch(doc string) (bool, error) {
	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(doc), &parsed); err != nil {
		return false, err
	}

	_, hasMachine := parsed["machine"]
	_, hasCluster := parsed["cluster"]

	return hasMachine || hasCluster, nil
}

// yamlDocSeparator matches YAML document separator at the start of a line.
// Handles variations like "---", "--- ", "---\n" regardless of preceding content.
var yamlDocSeparator = regexp.MustCompile(`(?m)^---[ \t]*$`)

// extractExtraDocuments separates Talos config patches from other YAML documents.
// Returns the Talos patches to be processed, extra documents to be appended to output, and any error.
func extractExtraDocuments(patches []string) (talosPatches []string, extraDocs []string, err error) {
	for _, patch := range patches {
		// Normalize CRLF to LF for consistent splitting
		patch = strings.ReplaceAll(patch, "\r\n", "\n")
		// Split by YAML document separator (--- at start of line)
		docs := yamlDocSeparator.Split(patch, -1)
		for _, doc := range docs {
			doc = strings.TrimSpace(doc)
			if doc == "" {
				continue
			}

			isTalos, parseErr := isTalosConfigPatch(doc)
			if parseErr != nil {
				return nil, nil, fmt.Errorf("invalid YAML in template output: %w\n\nTemplate output:\n%s", parseErr, doc)
			}

			if isTalos {
				talosPatches = append(talosPatches, doc)
			} else {
				extraDocs = append(extraDocs, doc)
			}
		}
	}

	return talosPatches, extraDocs, nil
}

func applyPatchesAndRenderConfig(opts Options, configPatches []string) ([]byte, error) {
	// Separate Talos config patches from extra documents (like UserVolumeConfig)
	talosPatches, extraDocs, err := extractExtraDocuments(configPatches)
	if err != nil {
		return nil, err
	}

	// Generate options for the configuration based on the provided flags
	genOptions := []generate.Option{}

	if opts.TalosVersion != "" {
		versionContract, err := config.ParseContractFromVersion(opts.TalosVersion)
		if err != nil {
			return nil, fmt.Errorf("invalid talos-version: %w", err)
		}

		genOptions = append(genOptions, generate.WithVersionContract(versionContract))
	}

	if opts.WithSecrets != "" {
		secretsBundle, err := secrets.LoadBundle(opts.WithSecrets)
		if err != nil {
			return nil, fmt.Errorf("failed to load secrets bundle: %w", err)
		}

		genOptions = append(genOptions, generate.WithSecretsBundle(secretsBundle))
	}

	configBundleOpts := []bundle.Option{
		bundle.WithInputOptions(
			&bundle.InputOptions{
				KubeVersion: strings.TrimPrefix(opts.KubernetesVersion, "v"),
				GenOptions:  genOptions,
			},
		),
		bundle.WithVerbose(false),
	}

	// Load and apply patches to discover the machine type
	configBundle, err := bundle.NewBundle(configBundleOpts...)
	if err != nil {
		return nil, err
	}

	patches, err := configpatcher.LoadPatches(talosPatches)
	if err != nil {
		if opts.Debug {
			debugPhase(opts, configPatches, "", "", machine.TypeUnknown)
		}

		return nil, err
	}

	err = configBundle.ApplyPatches(patches, true, false)
	if err != nil {
		if opts.Debug {
			debugPhase(opts, configPatches, "", "", machine.TypeUnknown)
		}

		return nil, err
	}

	machineType := configBundle.ControlPlaneCfg.Machine().Type()
	clusterName := configBundle.ControlPlaneCfg.Cluster().Name()
	clusterEndpoint := configBundle.ControlPlaneCfg.Cluster().Endpoint()

	if machineType == machine.TypeUnknown {
		machineType = machine.TypeWorker
	}

	if opts.Debug {
		debugPhase(opts, configPatches, clusterName, clusterEndpoint.String(), machineType)
	}

	// Reload config with the correct machineType, clusterName and endpoint
	configBundleOpts = []bundle.Option{
		bundle.WithInputOptions(
			&bundle.InputOptions{
				ClusterName: clusterName,
				Endpoint:    clusterEndpoint.String(),
				KubeVersion: strings.TrimPrefix(opts.KubernetesVersion, "v"),
				GenOptions:  genOptions,
			},
		),
		bundle.WithVerbose(false),
	}

	configBundle, err = bundle.NewBundle(configBundleOpts...)
	if err != nil {
		return nil, err
	}

	var configOrigin, configFull []byte
	if !opts.Full {
		configOrigin, err = configBundle.Serialize(encoder.CommentsDisabled, machineType)
		if err != nil {
			return nil, err
		}

		// Overwrite some fields to preserve them for diff
		var config map[string]any
		if err := yaml.Unmarshal(configOrigin, &config); err != nil {
			return nil, err
		}

		if machine, ok := config["machine"].(map[string]any); ok {
			machine["type"] = "unknown"
		}

		if cluster, ok := config["cluster"].(map[string]any); ok {
			cluster["clusterName"] = ""

			controlPlane, ok := cluster["controlPlane"].(map[string]any)
			if !ok {
				controlPlane = map[string]any{}
				cluster["controlPlane"] = controlPlane
			}

			controlPlane["endpoint"] = ""
		}

		configOrigin, err = yaml.Marshal(&config)
		if err != nil {
			return nil, err
		}
	}

	err = configBundle.ApplyPatches(patches, (machineType == machine.TypeControlPlane), (machineType == machine.TypeWorker))
	if err != nil {
		return nil, err
	}

	configFull, err = configBundle.Serialize(encoder.CommentsDisabled, machineType)
	if err != nil {
		return nil, err
	}

	var target []byte
	if opts.Full {
		target = configFull
	} else {
		target, err = yamltools.DiffYAMLs(configOrigin, configFull)
		if err != nil {
			return nil, err
		}
	}

	var targetNode yaml.Node
	if err := yaml.Unmarshal(target, &targetNode); err != nil {
		return nil, err
	}

	// Copy comments from source configuration to the final output
	for _, configPatch := range talosPatches {
		var sourceNode yaml.Node
		if err := yaml.Unmarshal([]byte(configPatch), &sourceNode); err != nil {
			return nil, err
		}

		dstPaths := make(map[string]*yaml.Node)
		yamltools.CopyComments(&sourceNode, &targetNode, "", dstPaths)
		yamltools.ApplyComments(&targetNode, "", dstPaths)
	}

	buf := &bytes.Buffer{}
	encoder := yaml.NewEncoder(buf)
	encoder.SetIndent(2)

	if err := encoder.Encode(&targetNode); err != nil {
		return nil, err
	}

	_ = encoder.Close()

	// Append extra documents (like UserVolumeConfig) that are not part of Talos config
	for _, extraDoc := range extraDocs {
		buf.WriteString("---\n")
		buf.WriteString(extraDoc)
		buf.WriteString("\n")
	}

	return buf.Bytes(), nil
}

func readUnexportedField(field reflect.Value) any {
	return reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().Interface()
}

// extractResourceData builds resource with metadata, spec and stringSpec fields.
func extractResourceData(r resource.Resource) (map[string]any, error) {
	res := make(map[string]any)

	// Extract metadata directly from resource methods
	md := r.Metadata()
	metadata := map[string]any{
		"namespace": string(md.Namespace()),
		"type":      string(md.Type()),
		"id":        string(md.ID()),
		"version":   md.Version().String(),
		"phase":     md.Phase().String(),
		"owner":     string(md.Owner()),
	}

	res["metadata"] = metadata

	// extract spec
	val := reflect.ValueOf(r.Spec())
	if val.Kind() == reflect.Pointer {
		val = val.Elem()
	}

	if val.Kind() == reflect.Struct {
		if yamlField := val.FieldByName("yaml"); yamlField.IsValid() {
			yamlValue := readUnexportedField(yamlField)

			yamlString, ok := yamlValue.(string)
			if !ok {
				return res, errors.Newf("field 'yaml' is not a string (got %T)", yamlValue)
			}

			var unmarshalledData any
			if err := yaml.Unmarshal([]byte(yamlString), &unmarshalledData); err != nil {
				return res, errors.Wrap(err, "unmarshaling yaml")
			}

			res["spec"] = unmarshalledData
		} else {
			return res, errors.New("field 'yaml' not found")
		}
	}

	return res, nil
}

func newLookupFunction(ctx context.Context, c *client.Client) func(resource string, namespace string, id string) (map[string]any, error) {
	return func(kind string, namespace string, id string) (map[string]any, error) {
		var multiErr *multierror.Error

		var resources []map[string]any

		callbackResource := func(parentCtx context.Context, hostname string, r resource.Resource, callError error) error {
			if callError != nil {
				// Ignore NotFound and PermissionDenied errors - resource doesn't exist or is not accessible
				errCode := status.Code(callError)

				errStr := callError.Error()
				if errCode == codes.NotFound || errCode == codes.PermissionDenied ||
					strings.Contains(errStr, "code = NotFound") || strings.Contains(errStr, "code = PermissionDenied") {
					return nil
				}

				multiErr = multierror.Append(multiErr, callError)

				return nil
			}

			res, err := extractResourceData(r)
			if err != nil {
				multiErr = multierror.Append(multiErr, fmt.Errorf("resource %s/%s: %w", r.Metadata().Type(), r.Metadata().ID(), err))

				return nil
			}

			resources = append(resources, res)

			return nil
		}
		callbackRD := func(definition *meta.ResourceDefinition) error {
			return nil
		}

		helperErr := helpers.ForEachResource(ctx, c, callbackRD, callbackResource, namespace, kind, id)
		if helperErr != nil {
			return map[string]any{}, helperErr
		}

		if err := multiErr.ErrorOrNil(); err != nil {
			return map[string]any{}, err
		}

		if len(resources) == 0 {
			return map[string]any{}, nil
		}

		if id != "" && len(resources) == 1 {
			return resources[0], nil
		}
		// Return items as a slice for proper range iteration in templates
		items := make([]any, len(resources))
		for i, res := range resources {
			items[i] = res
		}

		return map[string]any{
			"apiVersion": "v1",
			"kind":       "List",
			"items":      items,
		}, nil
	}
}
