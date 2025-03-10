package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"unsafe"

	"gopkg.in/yaml.v3"

	helmEngine "github.com/cozystack/talm/pkg/engine/helm"
	"github.com/cozystack/talm/pkg/yamltools"
	"github.com/cosi-project/runtime/pkg/resource"
	"github.com/cosi-project/runtime/pkg/resource/meta"
	"github.com/hashicorp/go-multierror"
	"helm.sh/helm/v3/pkg/chart"
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
	Insecure          bool
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
func FullConfigProcess(ctx context.Context, opts Options, patches []string) (*bundle.Bundle, error) {
	configBundle, err := InitializeConfigBundle(opts)
	if err != nil {
		return nil, fmt.Errorf("initial config bundle error: %w", err)
	}

	loadedPatches, err := configpatcher.LoadPatches(patches)
	if err != nil {
		if opts.Debug {
			debugPhase(opts, patches, "", "", machine.TypeUnknown)
		}
		return nil, err
	}

	err = configBundle.ApplyPatches(loadedPatches, true, false)
	if err != nil {
		if opts.Debug {
			debugPhase(opts, patches, "", "", machine.TypeUnknown)
		}
		return nil, fmt.Errorf("apply initial patches error: %w", err)
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
		return nil, fmt.Errorf("reinit config bundle error: %w", err)
	}

	// Applying updated patches
	err = configBundle.ApplyPatches(loadedPatches, (machineType == machine.TypeControlPlane), (machineType == machine.TypeWorker))
	if err != nil {
		return nil, fmt.Errorf("apply updated patches error: %w", err)
	}

	return configBundle, nil
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

// Render executes the rendering of templates based on the provided options.
func Render(ctx context.Context, c *client.Client, opts Options) ([]byte, error) {

	// Gather facts and enable lookup options
	if !opts.Offline {
		if err := helpers.FailIfMultiNodes(ctx, "talm template"); err != nil {
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

	rootValues := map[string]interface{}{
		"Values": mergeMaps(chrt.Values, values),
	}

	eng := helmEngine.Engine{}
	out, err := eng.Render(chrt, rootValues)
	if err != nil {
		return nil, err
	}

	configPatches := []string{}
	for _, templateFile := range opts.TemplateFiles {
		requestedTemplate := filepath.Join(chrt.Name(), templateFile)
		configPatch, ok := out[requestedTemplate]
		if !ok {
			return nil, fmt.Errorf("template %s not found", templateFile)
		}
		configPatches = append(configPatches, configPatch)
	}

	finalConfig, err := applyPatchesAndRenderConfig(ctx, opts, configPatches, chrt)
	if err != nil {
		// TODO
		return nil, err
	}

	return finalConfig, nil
}

// Imported from Helm
// https://github.com/helm/helm/blob/c6beb169d26751efd8131a5d65abe75c81a334fb/pkg/cli/values/options.go#L44
func loadValues(opts Options) (map[string]interface{}, error) {
	// Base map to hold the merged values
	base := make(map[string]interface{})

	// Load values from files specified with -f or --values
	for _, filePath := range opts.ValueFiles {
		currentMap := make(map[string]interface{})
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
		currentMap := make(map[string]interface{})
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
func mergeMaps(a, b map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(a))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		if vm, ok := v.(map[string]interface{}); ok {
			if bv, ok := out[k]; ok {
				if bvm, ok := bv.(map[string]interface{}); ok {
					out[k] = mergeMaps(bvm, vm)
					continue
				}
			}
		}
		out[k] = v
	}
	return out
}

func applyPatchesAndRenderConfig(ctx context.Context, opts Options, configPatches []string, chrt *chart.Chart) ([]byte, error) {
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

	patches, err := configpatcher.LoadPatches(configPatches)
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
		var config map[string]interface{}
		if err := yaml.Unmarshal(configOrigin, &config); err != nil {
			return nil, err
		}
		if machine, ok := config["machine"].(map[string]interface{}); ok {
			machine["type"] = "unknown"
		}
		if cluster, ok := config["cluster"].(map[string]interface{}); ok {
			cluster["clusterName"] = ""
			controlPlane, ok := cluster["controlPlane"].(map[string]interface{})
			if !ok {
				controlPlane = map[string]interface{}{}
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
	for _, configPatch := range configPatches {
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
	encoder.Close()

	return buf.Bytes(), nil
}

func readUnexportedField(field reflect.Value) any {
	return reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().Interface()
}

// builds resource with metadata, spec and stringSpec fields
func extractResourceData(r resource.Resource) (map[string]interface{}, error) {
	// extract metadata
	o, _ := resource.MarshalYAML(r)
	m, _ := yaml.Marshal(o)
	var res map[string]interface{}

	yaml.Unmarshal(m, &res)

	// extract spec
	val := reflect.ValueOf(r.Spec())
	if val.Kind() == reflect.Ptr {
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

func newLookupFunction(ctx context.Context, c *client.Client) func(resource string, namespace string, id string) (map[string]interface{}, error) {
	return func(kind string, namespace string, id string) (map[string]interface{}, error) {
		var multiErr *multierror.Error

		var resources []map[string]interface{}

		callbackResource := func(parentCtx context.Context, hostname string, r resource.Resource, callError error) error {
			if callError != nil {
				multiErr = multierror.Append(multiErr, callError)
				return nil
			}

			res, err := extractResourceData(r)
			if err != nil {
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
			return map[string]interface{}{}, helperErr
		}
		if len(resources) == 0 {
			return map[string]interface{}{}, nil
		}
		if id != "" && len(resources) == 1 {
			return resources[0], nil
		}
		items := map[string]interface{}{}
		for i, res := range resources {
			items["_"+strconv.Itoa(i)] = res
		}
		return map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "List",
			"items":      items,
		}, nil
	}
}
