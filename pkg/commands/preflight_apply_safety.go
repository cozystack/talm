// Copyright Cozystack Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package commands

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/cosi-project/runtime/pkg/resource"
	"github.com/cosi-project/runtime/pkg/safe"
	"github.com/cozystack/talm/pkg/applycheck"
	"github.com/siderolabs/talos/pkg/machinery/client"
	"github.com/siderolabs/talos/pkg/machinery/resources/block"
	"github.com/siderolabs/talos/pkg/machinery/resources/config"
	"github.com/siderolabs/talos/pkg/machinery/resources/network"
	yaml "gopkg.in/yaml.v3"
)

// linksDisksReader returns the host's link and disk inventory used by
// Phase 1 validation. The reader is a function type so tests can supply
// fakes without standing up a Talos client.
//
// Three-valued return mirrors machineConfigReader so the validator can
// distinguish:
//
//   - (snapshot, true, nil) — success.
//   - (zero, false, nil) — reader signalled "no result, no error".
//     The link / disk COSI resources are NonSensitive, so this shape
//     should not happen in production via the COSI reader; reserved
//     for future custom readers that genuinely want to surrender
//     without an err. The validator treats it the same as the (zero,
//     false, err) branch — surfaces as a hint-bearing blocker —
//     because Phase 1 cannot make a useful declared-resource decision
//     without a snapshot in hand.
//   - (zero, false, err) — transient read failure (apid timeout,
//     network blip). The validator wraps and surfaces it so the
//     operator sees the actual cause, not a misleading "config is
//     wrong" blocker.
//
// The three-valued signature mirrors machineConfigReader to keep the
// test-fake surface unified, even though only the (zero, false, err)
// branch is exercised in production.
type linksDisksReader func(ctx context.Context) (snapshot applycheck.HostSnapshot, ok bool, err error)

// machineConfigReader returns the current on-node MachineConfig bytes
// used by Phase 2 (drift preview + post-apply verify). The MachineConfig
// COSI resource is declared meta.Sensitive in Talos, so the auth path
// can read it but the insecure / maintenance connection cannot —
// ok=false on that path is the documented graceful-degrade signal,
// distinct from a transient read failure (err non-nil).
type machineConfigReader func(ctx context.Context) (configBytes []byte, ok bool, err error)

// maintenanceConnectionMessage is the single line printed when the
// drift gates run against a connection that cannot read the Sensitive
// MachineConfig resource (insecure / maintenance mode). Both Phase 2
// hooks surface this and proceed without blocking.
const maintenanceConnectionMessage = "drift verification unavailable on maintenance connection"

// preflightValidateResources runs Phase 1: extract host-resource references
// from the rendered MachineConfig and verify each against the node's
// snapshot. Returns nil when there are no blockers (warnings are printed
// but do not block). Returns an error when there is at least one
// blocker; the error message names every blocker so a single rerun
// surfaces every problem.
func preflightValidateResources(
	ctx context.Context,
	read linksDisksReader,
	rendered []byte,
	w io.Writer,
) error {
	snapshot, ok, err := read(ctx)
	if err != nil {
		// Transient COSI read failure (apid timeout, network blip).
		// Surface the underlying cause so the operator doesn't get
		// a misleading "your config is wrong, pass --skip-...".
		//nolint:wrapcheck // cockroachdb/errors.WithHint at boundary.
		return errors.WithHint(
			errors.Wrap(err, "pre-flight: reading host links/disks snapshot from the node"),
			"this is a node-side connection / COSI error, not a config defect. Retry, fix connectivity, or pass --skip-resource-validation to bypass the gate.",
		)
	}

	if !ok {
		// Single message via the error + hint chain. An earlier
		// version printed a duplicate warning line to w, so the
		// operator saw --skip-resource-validation mentioned twice.
		//nolint:wrapcheck // cockroachdb/errors.WithHint at boundary.
		return errors.WithHint(
			errors.New("pre-flight: host links/disks snapshot unavailable"),
			"the node didn't surface its links/disks resources — typically the auth path is wrong. Pass --skip-resource-validation to bypass.",
		)
	}

	refs, err := applycheck.WalkRefs(rendered)
	if err != nil {
		return errors.Wrap(err, "pre-flight: walking rendered MachineConfig")
	}

	findings := applycheck.ValidateRefs(refs, snapshot)

	netAddrFindings, err := applycheck.WalkNetAddrFindings(rendered)
	if err != nil {
		return errors.Wrap(err, "pre-flight: walking rendered MachineConfig for net-addr fields")
	}

	findings = append(findings, netAddrFindings...)

	if len(findings) == 0 {
		return nil
	}

	blockers := 0

	for i := range findings {
		f := &findings[i]
		printFinding(w, f)

		if f.IsBlocker() {
			blockers++
		}
	}

	if blockers == 0 {
		return nil
	}

	//nolint:wrapcheck // cockroachdb/errors.WithHint at boundary.
	return errors.WithHint(
		errors.Newf("pre-flight: %d declared host resource(s) do not resolve on the target node", blockers),
		"correct the values referenced above, or pass --skip-resource-validation to bypass.",
	)
}

// printFinding writes one finding line plus its hint to w. Output shape:
//
//	[blocker] declared link "eth9999" not found on target node
//	  hint: available links: eth0, eth1
func printFinding(w io.Writer, finding *applycheck.Finding) {
	tag := "blocker"
	if !finding.IsBlocker() {
		tag = "warning"
	}

	_, _ = fmt.Fprintf(w, "[%s] %s (source: %s)\n", tag, finding.Reason, finding.Ref.Source)

	if finding.Hint != "" {
		_, _ = fmt.Fprintf(w, "  hint: %s\n", finding.Hint)
	}
}

// previewDrift runs Phase 2A: read the node's current MachineConfig and
// diff against the rendered config we're about to apply, then print a
// +/-/~/= preview to w. Returns nil unconditionally — pre-apply preview
// is informational. On the insecure / maintenance path the MachineConfig
// resource is Sensitive and unreachable, so the reader returns
// ok=false; the function prints one explanatory line and returns nil.
//
// nodeID is the per-node identifier (typically an IP) used to prefix
// gate output lines on a multi-node apply so the operator can tell
// whose diff is whose. Pass "" when the call site has no per-node
// scope (template-rendering path with a single implicit node).
func previewDrift(
	ctx context.Context,
	read machineConfigReader,
	rendered []byte,
	nodeID string,
	w io.Writer,
	showSecrets bool,
) error {
	current, ok, err := read(ctx)
	if err != nil {
		_, _ = fmt.Fprintf(w, "%swarning: drift preview skipped, could not read on-node MachineConfig: %v\n", nodePrefix(nodeID), err)

		return nil
	}

	if !ok {
		_, _ = fmt.Fprintf(w, "%stalm: %s\n", nodePrefix(nodeID), maintenanceConnectionMessage)

		return nil
	}

	changes, err := applycheck.Diff(current, rendered)
	if err != nil {
		_, _ = fmt.Fprintf(w, "%swarning: drift preview skipped, diff failed: %v\n", nodePrefix(nodeID), err)

		return nil
	}

	printDriftPreview(w, headerWithNode("talm: drift preview", nodeID), changes, showSecrets)

	return nil
}

// nodePrefix returns "node X: " for non-empty nodeID, empty otherwise.
// Used at start of stderr lines to disambiguate multi-node output.
func nodePrefix(nodeID string) string {
	if nodeID == "" {
		return ""
	}

	return "node " + nodeID + ": "
}

// headerWithNode appends "(node X)" to a base header when nodeID is
// non-empty, leaving the bare header unchanged otherwise. Used by
// printDriftPreview so multi-node Phase 2A/2B output is per-node-
// distinguishable on stderr.
func headerWithNode(base, nodeID string) string {
	if nodeID == "" {
		return base
	}

	return base + " (node " + nodeID + ")"
}

// verifyAppliedState runs Phase 2B: after ApplyConfiguration returns
// success, re-read the node's MachineConfig and structurally compare
// against the bytes we sent. Returns:
//
//   - nil if everything matches.
//   - nil with an informational line when ok=false (insecure /
//     maintenance path; MachineConfig is Sensitive there).
//   - a hint-bearing error on actual divergence.
//   - a hint-bearing error when err != nil from the reader: the verify
//     gate exists specifically to catch silent rollbacks, so a read
//     failure has to surface, not be swallowed.
func verifyAppliedState(
	ctx context.Context,
	read machineConfigReader,
	sent []byte,
	nodeID string,
	w io.Writer,
	showSecrets bool,
) error {
	onNode, ok, err := read(ctx)
	if err != nil {
		//nolint:wrapcheck // cockroachdb/errors.WithHint at boundary.
		return errors.WithHint(
			errors.Wrap(err, "post-apply: re-reading on-node MachineConfig"),
			"the apply succeeded but verification couldn't read the node back — pass --skip-post-apply-verify to bypass, or investigate the underlying COSI read error.",
		)
	}

	if !ok {
		_, _ = fmt.Fprintf(w, "%stalm: %s\n", nodePrefix(nodeID), maintenanceConnectionMessage)

		return nil
	}

	changes, err := applycheck.Diff(onNode, sent)
	if err != nil {
		return errors.Wrap(err, "post-apply: structural diff of on-node vs sent config")
	}

	changed := applycheck.FilterChanged(changes)
	if len(changed) == 0 {
		return nil
	}

	printDriftPreview(w, headerWithNode("talm: post-apply divergence", nodeID), changes, showSecrets)

	//nolint:wrapcheck // cockroachdb/errors.WithHint at boundary.
	return errors.WithHint(
		errors.Newf("post-apply: %d document(s) on the node diverge from the configuration sent to the node", len(changed)),
		"check that no controller / extension is reverting state, and that the talosVersion contract matches the running Talos.",
	)
}

// printDriftPreview renders a Change list with a header label. OpEqual
// entries are dropped from the per-line listing but counted in the
// trailing summary so the reader can confirm the diff against expected
// scope.
func printDriftPreview(w io.Writer, header string, changes []applycheck.Change, showSecrets bool) {
	_, _ = fmt.Fprintln(w, header)

	var adds, removes, updates, equals int

	for i := range changes {
		change := &changes[i]
		switch change.Op {
		case applycheck.OpEqual:
			equals++

			continue
		case applycheck.OpAdd:
			adds++
		case applycheck.OpRemove:
			removes++
		case applycheck.OpUpdate:
			updates++
		}

		_, _ = fmt.Fprintln(w, applycheck.FormatChange(change))

		for j := range change.Fields {
			f := &change.Fields[j]
			_, _ = fmt.Fprintf(w, "      %s\n", formatFieldChangeLine(f, showSecrets))
		}
	}

	_, _ = fmt.Fprintf(w, "talm: %d addition, %d removal, %d update, %d unchanged.\n", adds, removes, updates, equals)
}

// absentFieldValue is the rendering for a leaf field that does not
// exist on one side of a FieldChange (HasOld=false or HasNew=false).
// Hoisted so formatFieldValue and formatSecretFieldValue stay
// byte-identical on the absent path — a future drift in either would
// obscure add/remove vs rotate semantics in the drift preview.
const absentFieldValue = "(absent)"

// formatFieldChangeLine renders one FieldChange entry for the drift
// preview. The default form is "path: old -> new"; the slice-vs-slice
// case takes a set-diff fast path so a 50-element certSANs update
// surfaces as "path: removed [127.0.0.1]" instead of dumping both
// slices in full. The set-diff is multiset-aware (duplicate-cleanup
// surfaces correctly) and handles the equal-multiset reorder case
// with an explicit "(reordered, N element(s))" line so the operator
// isn't left wondering why an OpUpdate fired with no apparent change.
func formatFieldChangeLine(change *applycheck.FieldChange, showSecrets bool) string {
	// Secret check runs BEFORE bothSlices so a secret-bearing path
	// that happens to render as a slice (e.g. a future allowlist
	// entry naming the array itself rather than a leaf element)
	// still gets redacted instead of leaking the full element
	// values through formatSliceSetDiff.
	if !showSecrets && isSecretPath(change.Path) {
		return fmt.Sprintf("%s: %s -> %s", change.Path, formatSecretFieldValue(change.HasOld, change.Old), formatSecretFieldValue(change.HasNew, change.New))
	}

	if oldSlice, newSlice, ok := bothSlices(change); ok {
		return formatSliceSetDiff(change.Path, oldSlice, newSlice)
	}

	return fmt.Sprintf("%s: %s -> %s", change.Path, formatFieldValue(change.HasOld, change.Old), formatFieldValue(change.HasNew, change.New))
}

// formatSecretFieldValue is the redaction-aware counterpart of
// formatFieldValue. Absent (HasX=false) reads as `(absent)`
// unchanged so the operator can still distinguish add/remove from
// rotation. A present non-string value renders via fmt.Sprintf with
// the length tell so a number/bool rotation still surfaces as
// "different" — preserving the rotation-detection promise that
// motivated redactValue carrying len=N.
//
// Caveat: Go's fmt.Sprintf("%v", m) on a map[string]any iterates
// keys in randomised order (deliberately, since Go 1.0). For
// map-shaped secret values, two semantically-equal maps may render
// as different-length strings (false positive: looks like a
// rotation when nothing changed), and two unequal maps may
// coincidentally collide on length (false negative: rotation
// missed). The allowlist today contains no map-shaped entries; if
// a future addition does, either canonicalise the map (sorted keys
// before %v) or disclaim the rotation signal for that entry.
func formatSecretFieldValue(has bool, value any) string {
	if !has {
		return absentFieldValue
	}

	if s, ok := value.(string); ok {
		return redactValue(s)
	}

	// Non-string values: render the %v form's length so the
	// operator still sees a rotation signal. Same shape as
	// redactValue but without committing to "this was a string".
	return redactValue(fmt.Sprintf("%v", value))
}

// bothSlices returns the two sides as []any when the FieldChange
// represents a slice-to-slice update. Either side being absent or a
// non-slice type returns ok=false; callers fall back to the inline
// flow-style renderer.
func bothSlices(change *applycheck.FieldChange) ([]any, []any, bool) {
	if !change.HasOld || !change.HasNew {
		return nil, nil, false
	}

	oldSlice, okOld := change.Old.([]any)
	newSlice, okNew := change.New.([]any)

	if !okOld || !okNew {
		return nil, nil, false
	}

	return oldSlice, newSlice, true
}

// formatSliceSetDiff renders the multiset difference between two
// slices in the form "path: removed [...], added [...]". Buckets
// that are empty are omitted; an empty diff with non-empty input
// surfaces as "path: reordered (N element(s))" — telling the
// operator the slice's element identity is unchanged.
func formatSliceSetDiff(path string, oldSlice, newSlice []any) string {
	removed, added := multisetDiff(oldSlice, newSlice)

	switch {
	case len(removed) == 0 && len(added) == 0:
		return fmt.Sprintf("%s: reordered (%d element(s))", path, len(oldSlice))
	case len(removed) == 0:
		return fmt.Sprintf("%s: added %s", path, mustRenderFlow(added))
	case len(added) == 0:
		return fmt.Sprintf("%s: removed %s", path, mustRenderFlow(removed))
	default:
		return fmt.Sprintf("%s: removed %s, added %s", path, mustRenderFlow(removed), mustRenderFlow(added))
	}
}

// multisetDiff returns the elements present in oldSlice but not in
// newSlice (removed) and present in newSlice but not in oldSlice
// (added) using multiset semantics. Duplicates count individually:
// removing one of two copies of "127.0.0.1" surfaces as a single
// removal, not as the whole slice changing.
//
// Equality is keyed via fmt's %v so any hashable-or-not element type
// has a stable canonical form for the count map. The `fmt` package
// sorts map keys when printing — so map[string]any, map[int]any, and
// map[any]any all produce deterministic output across runs, and a
// slice of map elements (e.g., `machine.network.interfaces[]` in
// v1.11 nested form) dedups correctly. The behaviour is documented
// in the `fmt` package, not the language spec, so a future stdlib
// change could break it — but that would break far more things than
// this differ, and the change would surface as a wider failure.
func multisetDiff(oldSlice, newSlice []any) ([]any, []any) {
	counts := make(map[string]int, len(oldSlice))
	for _, item := range oldSlice {
		counts[canonicalKey(item)]++
	}

	added := make([]any, 0)

	for _, item := range newSlice {
		key := canonicalKey(item)
		if counts[key] > 0 {
			counts[key]--

			continue
		}

		added = append(added, item)
	}

	removed := make([]any, 0)

	for _, item := range oldSlice {
		key := canonicalKey(item)
		if counts[key] > 0 {
			counts[key]--

			removed = append(removed, item)
		}
	}

	return removed, added
}

// canonicalKey returns a stable string form for any value used as a
// multiset key. Today this is fmt's %v, which sorts map keys
// internally; a future Go stdlib change that drops that guarantee
// would silently break dedup stability across processes. Wrapping
// the call gives a single replacement site (sort map keys via
// json.Marshal with SortMapKeys, or migrate to a structural
// canonicaliser) when that day comes.
func canonicalKey(item any) string {
	return fmt.Sprintf("%v", item)
}

// mustRenderFlow is a render-or-fall-back wrapper around
// renderFlowYAML for the set-diff path: the caller cannot do anything
// useful with an error here (the bucket is non-empty by construction,
// so an encode failure would be deep), so degrade to Go's default %v
// rather than dropping the line entirely.
func mustRenderFlow(items []any) string {
	rendered, err := renderFlowYAML(items)
	if err != nil {
		return fmt.Sprintf("%v", items)
	}

	return rendered
}

// formatFieldValue renders one side of an OpUpdate FieldChange. A field
// that is absent on the side reads as `(absent)`; a present field whose
// value is literally nil/null reads as `<nil>` — distinct from absent
// and unambiguous in the output.
//
// Scalars (strings, ints, bools) render inline as before. Slices and
// maps go through a YAML flow-style encoder so the operator sees
// `[a, b, c]` / `{k: v, k2: v2}` instead of Go's `[a b c]` /
// `map[k:v k2:v2]` — the flow form mirrors how the same value would
// appear in a Helm values file and stays readable on long certSAN
// lists or nodeLabel maps.
func formatFieldValue(has bool, value any) string {
	if !has {
		return absentFieldValue
	}

	switch value.(type) {
	case nil, bool, string,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64:
		return fmt.Sprintf("%v", value)
	}

	rendered, err := renderFlowYAML(value)
	if err != nil {
		return fmt.Sprintf("%v", value)
	}

	return rendered
}

// renderFlowYAML returns a single-line YAML flow-style representation
// of v. Container nodes (slices, maps) recursively get yaml.FlowStyle
// so the output is e.g. `[127.0.0.1, 127.0.0.1, 192.0.2.5]` instead of
// the default block style (which would span multiple lines and break
// the `path: old -> new` diff layout).
func renderFlowYAML(value any) (string, error) {
	var node yaml.Node
	if err := node.Encode(value); err != nil {
		return "", errors.Wrap(err, "encoding FieldChange value to YAML node")
	}

	applyFlowStyle(&node)

	var buf strings.Builder

	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(0)

	if err := enc.Encode(&node); err != nil {
		return "", errors.Wrap(err, "marshaling FieldChange value in flow style")
	}

	_ = enc.Close()

	return strings.TrimRight(buf.String(), "\n"), nil
}

// applyFlowStyle walks a yaml.Node tree and forces every container
// node (sequence, mapping) into flow style. Scalar nodes inherit the
// natural representation Encode picked for them; setting FlowStyle on
// a scalar is a no-op.
func applyFlowStyle(n *yaml.Node) {
	n.Style = yaml.FlowStyle
	for _, child := range n.Content {
		applyFlowStyle(child)
	}
}

// readWithFreshTimeout runs op with a context derived from parent
// that carries a fresh timeout budget. Used by cosiLinksDisksReader
// to give each COSI ListAll call its own deadline — sharing one
// context.WithTimeout across multiple reads leaks the time spent on
// the first into the second's budget, which on a slow node turns a
// legitimate-but-slow read into a false transient-timeout blocker.
//
// Generic over the return type so the helper composes with
// safe.StateListAll[T]'s generic signature without an adapter.
func readWithFreshTimeout[T any](parent context.Context, timeout time.Duration, op func(context.Context) (T, error)) (T, error) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	return op(ctx)
}

// cosiLinksDisksReader returns a linksDisksReader backed by the node's
// COSI state. Both LinkStatus (network namespace) and Disk (block
// namespace) are NonSensitive, so the maintenance / insecure path can
// read them — Phase 1 works on both paths.
//
// Each of the two list operations gets its OWN preflightCOSIReadTimeout
// budget via readWithFreshTimeout. See that helper's godoc for the
// shared-budget leak it prevents.
func cosiLinksDisksReader(c *client.Client) linksDisksReader {
	return func(ctx context.Context) (applycheck.HostSnapshot, bool, error) {
		links, err := readWithFreshTimeout(ctx, preflightCOSIReadTimeout, func(ctx context.Context) (safe.List[*network.LinkStatus], error) {
			return safe.StateListAll[*network.LinkStatus](ctx, c.COSI)
		})
		if err != nil {
			return applycheck.HostSnapshot{}, false, errors.Wrap(err, "listing LinkStatus resources")
		}

		snapshot := applycheck.HostSnapshot{
			Links: make([]string, 0, links.Len()),
		}

		for link := range links.All() {
			snapshot.Links = append(snapshot.Links, link.Metadata().ID())
		}

		disks, err := readWithFreshTimeout(ctx, preflightCOSIReadTimeout, func(ctx context.Context) (safe.List[*block.Disk], error) {
			return safe.StateListAll[*block.Disk](ctx, c.COSI)
		})
		if err != nil {
			return applycheck.HostSnapshot{}, false, errors.Wrap(err, "listing Disk resources")
		}

		snapshot.Disks = make([]applycheck.DiskInfo, 0, disks.Len())

		for disk := range disks.All() {
			spec := disk.TypedSpec()
			snapshot.Disks = append(snapshot.Disks, applycheck.DiskInfo{
				DevPath:    spec.DevPath,
				Model:      spec.Model,
				Serial:     spec.Serial,
				WWID:       spec.WWID,
				Modalias:   spec.Modalias,
				UUID:       spec.UUID,
				BusPath:    spec.BusPath,
				Transport:  spec.Transport,
				Size:       spec.Size,
				Rotational: spec.Rotational,
				Readonly:   spec.Readonly,
				CDROM:      spec.CDROM,
				Symlinks:   append([]string(nil), spec.Symlinks...),
			})
		}

		return snapshot, true, nil
	}
}

// cosiMachineConfigReader returns a machineConfigReader backed by the
// node's COSI state. The MachineConfig resource is meta.Sensitive: on
// the insecure / maintenance path the Reader role doesn't see it.
// Callers know which path they're on (it's the --insecure flag, set
// statically at command-line parse), so we branch deterministically
// on insecure rather than collapsing every COSI error into
// ok=false. Transient failures (network blip, apid restart) surface
// as err non-nil so verifyAppliedState can surface them rather than
// silently passing.
func cosiMachineConfigReader(c *client.Client, insecure bool) machineConfigReader {
	if insecure {
		return func(context.Context) ([]byte, bool, error) {
			return nil, false, nil
		}
	}

	return func(ctx context.Context) ([]byte, bool, error) {
		ctx, cancel := context.WithTimeout(ctx, preflightCOSIReadTimeout)
		defer cancel()

		res, err := safe.StateGet[*config.MachineConfig](
			ctx,
			c.COSI,
			resource.NewMetadata(config.NamespaceName, config.MachineConfigType, config.ActiveID, resource.VersionUndefined),
		)
		if err != nil {
			return nil, false, errors.Wrap(err, "reading on-node MachineConfig")
		}

		bytesRaw, err := res.Provider().Bytes()
		if err != nil {
			return nil, false, errors.Wrap(err, "marshaling on-node MachineConfig")
		}

		return bytesRaw, true, nil
	}
}
