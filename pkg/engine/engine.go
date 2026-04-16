package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"unsafe"

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

	// Print patches
	for _, patch := range patches {
		if string(patch[0]) == "@" {
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

// Function to initialize configuration settings
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

// Function for serializing the configuration
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
		return nil, fmt.Errorf("reading patch %s: %w", patchFile, err)
	}
	if isEffectivelyEmptyYAML(patchBytes) {
		return rendered, nil
	}
	patches, err := configpatcher.LoadPatches([]string{"@" + patchFile})
	if err != nil {
		return nil, fmt.Errorf("loading patch from %s: %w", patchFile, err)
	}
	out, err := configpatcher.Apply(configpatcher.WithBytes(rendered), patches)
	if err != nil {
		return nil, fmt.Errorf("applying patch from %s: %w", patchFile, err)
	}
	merged, err := out.Bytes()
	if err != nil {
		return nil, fmt.Errorf("encoding merged config from %s: %w", patchFile, err)
	}
	return merged, nil
}

// isEffectivelyEmptyYAML reports whether the input contains nothing but
// YAML comments, document separators, and whitespace. Used by
// MergeFileAsPatch to detect modeline-only node files that the Talos
// config-patcher misclassifies as empty JSON6902 patches.
func isEffectivelyEmptyYAML(data []byte) bool {
	for _, line := range bytes.Split(data, []byte("\n")) {
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 {
			continue
		}
		if trimmed[0] == '#' {
			continue
		}
		if string(trimmed) == "---" || string(trimmed) == "..." {
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
		return nil, fmt.Errorf("templates are not set for the command: please use `--file` or `--template` flag")
	}

	configPatches := []string{}
	for _, templateFile := range opts.TemplateFiles {
		// Use path.Join (not filepath.Join) because helm engine keys always use forward slashes
		requestedTemplate := path.Join(chrt.Name(), NormalizeTemplatePath(templateFile))
		configPatch, ok := out[requestedTemplate]
		if !ok {
			return nil, fmt.Errorf("template %s not found", templateFile)
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

// builds resource with metadata, spec and stringSpec fields
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
			var unmarshalledData any
			if err := yaml.Unmarshal([]byte(yamlValue.(string)), &unmarshalledData); err != nil {
				return res, fmt.Errorf("error unmarshaling yaml: %w", err)
			}
			res["spec"] = unmarshalledData
		} else {
			return res, fmt.Errorf("field 'yaml' not found")
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
