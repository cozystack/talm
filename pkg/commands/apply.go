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
	"path/filepath"
	"strings"
	"time"

	"github.com/cozystack/talm/pkg/engine"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/siderolabs/talos/cmd/talosctl/pkg/talos/helpers"
	machineapi "github.com/siderolabs/talos/pkg/machinery/api/machine"
	"github.com/siderolabs/talos/pkg/machinery/client"
	"github.com/siderolabs/talos/pkg/machinery/constants"
)

var applyCmdFlags struct {
	helpers.Mode
	certFingerprints  []string
	insecure          bool
	configFiles       []string // -f/--files
	talosVersion      string
	withSecrets       string
	debug             bool
	kubernetesVersion string
	dryRun            bool
	preserve          bool
	stage             bool
	force             bool
	configTryTimeout  time.Duration
	nodesFromArgs     bool
	endpointsFromArgs bool
}

var applyCmd = &cobra.Command{
	Use:   "apply",
	Short: "Apply config to a Talos node",
	Long:  ``,
	Args:  cobra.NoArgs,
	PreRunE: func(cmd *cobra.Command, args []string) error {
		if !cmd.Flags().Changed("talos-version") {
			applyCmdFlags.talosVersion = Config.TemplateOptions.TalosVersion
		}
		if !cmd.Flags().Changed("with-secrets") {
			applyCmdFlags.withSecrets = Config.TemplateOptions.WithSecrets
		}
		if !cmd.Flags().Changed("kubernetes-version") {
			applyCmdFlags.kubernetesVersion = Config.TemplateOptions.KubernetesVersion
		}
		if !cmd.Flags().Changed("debug") {
			applyCmdFlags.debug = Config.TemplateOptions.Debug
		}
		if !cmd.Flags().Changed("preserve") {
			applyCmdFlags.preserve = Config.UpgradeOptions.Preserve
		}
		if !cmd.Flags().Changed("stage") {
			applyCmdFlags.stage = Config.UpgradeOptions.Stage
		}
		if !cmd.Flags().Changed("force") {
			applyCmdFlags.force = Config.UpgradeOptions.Force
		}
		applyCmdFlags.nodesFromArgs = len(GlobalArgs.Nodes) > 0
		applyCmdFlags.endpointsFromArgs = len(GlobalArgs.Endpoints) > 0
		// Set dummy endpoint to avoid errors on building client
		if len(GlobalArgs.Endpoints) == 0 {
			GlobalArgs.Endpoints = append(GlobalArgs.Endpoints, "127.0.0.1")
		}

		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		return apply(args)
	},
}

func apply(args []string) error {
	ctx := context.Background()

	// Expand directories to YAML files
	expandedFiles, err := ExpandFilePaths(applyCmdFlags.configFiles)
	if err != nil {
		return err
	}

	// Detect root from files if specified, otherwise fallback to cwd
	if err := DetectAndSetRootFromFiles(expandedFiles); err != nil {
		return err
	}

	for _, configFile := range expandedFiles {
		modelineTemplates, err := processModelineAndUpdateGlobals(configFile, applyCmdFlags.nodesFromArgs, applyCmdFlags.endpointsFromArgs, true)
		if err != nil {
			return err
		}
		// Resolve secrets.yaml path relative to project root if not absolute
		withSecretsPath := ResolveSecretsPath(applyCmdFlags.withSecrets)

		if len(modelineTemplates) > 0 {
			// Template rendering path: connect to the node first, render
			// templates online per node (so lookup() functions resolve each
			// node's own discovery data), merge the node file as a patch,
			// then apply. The bare client wrapper is used so the per-node
			// loop can attach a single-node gRPC context per iteration —
			// engine.Render's FailIfMultiNodes guard rejects a batched
			// multi-node context.
			opts := buildApplyRenderOptions(modelineTemplates, withSecretsPath)
			nodes := append([]string(nil), GlobalArgs.Nodes...)

			if err := withApplyClientBare(func(ctx context.Context, c *client.Client) error {
				fmt.Printf("- talm: file=%s, nodes=%s, endpoints=%s\n", configFile, GlobalArgs.Nodes, GlobalArgs.Endpoints)
				return applyTemplatesPerNode(ctx, c, opts, configFile, nodes,
					engine.Render,
					func(ctx context.Context, c *client.Client, data []byte) error {
						resp, err := c.ApplyConfiguration(ctx, &machineapi.ApplyConfigurationRequest{
							Data:           data,
							Mode:           applyCmdFlags.Mode.Mode,
							DryRun:         applyCmdFlags.dryRun,
							TryModeTimeout: durationpb.New(applyCmdFlags.configTryTimeout),
						})
						if err != nil {
							return fmt.Errorf("error applying new configuration: %w", err)
						}
						helpers.PrintApplyResults(resp)
						return nil
					},
				)
			}); err != nil {
				return err
			}
		} else {
			// Direct patch path: apply config file as patch against empty bundle
			opts := buildApplyPatchOptions(withSecretsPath)
			patches := []string{"@" + configFile}
			configBundle, machineType, err := engine.FullConfigProcess(ctx, opts, patches)
			if err != nil {
				return fmt.Errorf("full config processing error: %w", err)
			}

			result, err := engine.SerializeConfiguration(configBundle, machineType)
			if err != nil {
				return fmt.Errorf("error serializing configuration: %w", err)
			}

			if err := withApplyClient(func(ctx context.Context, c *client.Client) error {
				fmt.Printf("- talm: file=%s, nodes=%s, endpoints=%s\n", configFile, GlobalArgs.Nodes, GlobalArgs.Endpoints)

				resp, err := c.ApplyConfiguration(ctx, &machineapi.ApplyConfigurationRequest{
					Data:           result,
					Mode:           applyCmdFlags.Mode.Mode,
					DryRun:         applyCmdFlags.dryRun,
					TryModeTimeout: durationpb.New(applyCmdFlags.configTryTimeout),
				})
				if err != nil {
					return fmt.Errorf("error applying new configuration: %w", err)
				}

				helpers.PrintApplyResults(resp)

				return nil
			}); err != nil {
				return err
			}
		}

		// Reset args
		if !applyCmdFlags.nodesFromArgs {
			GlobalArgs.Nodes = []string{}
		}
		if !applyCmdFlags.endpointsFromArgs {
			GlobalArgs.Endpoints = []string{}
		}
	}
	return nil
}

// withApplyClient creates a Talos client appropriate for the current apply mode
// and invokes the given action with it. The action receives a context with
// every node from GlobalArgs.Nodes batched into the gRPC metadata, matching
// the legacy direct-patch fan-out behaviour.
func withApplyClient(f func(ctx context.Context, c *client.Client) error) error {
	return withApplyClientBare(wrapWithNodeContext(f))
}

// withApplyClientBare connects to Talos for the current apply mode but does
// NOT inject GlobalArgs.Nodes into the context. The template-rendering path
// uses this so its per-node loop can attach a single-node context per
// iteration instead — engine.Render's FailIfMultiNodes guard rejects a
// batched multi-node context, and discovery via lookup() is per-node anyway.
func withApplyClientBare(f func(ctx context.Context, c *client.Client) error) error {
	if applyCmdFlags.insecure {
		// Maintenance mode connects directly to the node IP without talosconfig;
		// node context injection is not needed — the maintenance client handles
		// node targeting internally via GlobalArgs.Nodes.
		return WithClientMaintenance(applyCmdFlags.certFingerprints, f)
	}

	if GlobalArgs.SkipVerify {
		return WithClientSkipVerify(f)
	}

	return WithClientNoNodes(f)
}

// renderFunc and applyFunc are injection points for applyTemplatesPerNode so
// unit tests can drive the loop with fakes instead of a real Talos client.
type renderFunc func(ctx context.Context, c *client.Client, opts engine.Options) ([]byte, error)
type applyFunc func(ctx context.Context, c *client.Client, data []byte) error

// applyTemplatesPerNode runs render → MergeFileAsPatch → apply once per node,
// each iteration carrying a single-node gRPC context. Enables multi-node
// modelines and `--nodes A,B,C` against the template-rendering branch:
// engine.Render's FailIfMultiNodes guard requires a single-node context, and
// each node should resolve its own discovery via lookup() in any case.
func applyTemplatesPerNode(
	parentCtx context.Context,
	c *client.Client,
	opts engine.Options,
	configFile string,
	nodes []string,
	render renderFunc,
	apply applyFunc,
) error {
	if len(nodes) == 0 {
		return fmt.Errorf("no nodes specified for template-rendering apply")
	}
	for _, node := range nodes {
		perCtx := client.WithNodes(parentCtx, node)
		rendered, err := render(perCtx, c, opts)
		if err != nil {
			return fmt.Errorf("node %s: template rendering: %w", node, err)
		}
		merged, err := engine.MergeFileAsPatch(rendered, configFile)
		if err != nil {
			return fmt.Errorf("node %s: merging node file as patch: %w", node, err)
		}
		if err := apply(perCtx, c, merged); err != nil {
			return fmt.Errorf("node %s: %w", node, err)
		}
	}
	return nil
}

// buildApplyRenderOptions constructs engine.Options for the template rendering path.
// Offline is false because templates need a live Talos client for lookup() functions
// (e.g., discovering interface names, addresses, routes). The caller creates the
// client and passes it to engine.Render together with these options.
func buildApplyRenderOptions(modelineTemplates []string, withSecretsPath string) engine.Options {
	resolvedTemplates := resolveTemplatePaths(modelineTemplates, Config.RootDir)
	return engine.Options{
		TalosVersion:      applyCmdFlags.talosVersion,
		WithSecrets:       withSecretsPath,
		KubernetesVersion: applyCmdFlags.kubernetesVersion,
		Debug:             applyCmdFlags.debug,
		Full:              true,
		Root:              Config.RootDir,
		TemplateFiles:     resolvedTemplates,
		CommandName:       "talm apply",
	}
}

// buildApplyPatchOptions constructs engine.Options for the direct patch path.
func buildApplyPatchOptions(withSecretsPath string) engine.Options {
	return engine.Options{
		TalosVersion:      applyCmdFlags.talosVersion,
		WithSecrets:       withSecretsPath,
		KubernetesVersion: applyCmdFlags.kubernetesVersion,
		Debug:             applyCmdFlags.debug,
	}
}

// wrapWithNodeContext wraps a client action function to resolve and inject node
// context. If GlobalArgs.Nodes is already set, uses those directly. Otherwise,
// attempts to resolve nodes from the client's config context.
// This function does not mutate GlobalArgs. It reads GlobalArgs.Nodes at
// invocation time (not at wrapper creation time) and makes a defensive copy.
func wrapWithNodeContext(f func(ctx context.Context, c *client.Client) error) func(ctx context.Context, c *client.Client) error {
	return func(ctx context.Context, c *client.Client) error {
		nodes := append([]string(nil), GlobalArgs.Nodes...)
		if len(nodes) < 1 {
			if c == nil {
				return fmt.Errorf("failed to resolve config context: no client available")
			}
			configContext := c.GetConfigContext()
			if configContext == nil {
				return fmt.Errorf("failed to resolve config context")
			}
			nodes = configContext.Nodes
		}

		ctx = client.WithNodes(ctx, nodes...)
		return f(ctx, c)
	}
}

// resolveTemplatePaths resolves template file paths relative to the project root,
// normalizing them for the Helm engine (forward slashes).
// Relative paths from the modeline are resolved against rootDir, not CWD.
//
// Note: template.go has similar path resolution in generateOutput() but resolves
// against CWD via filepath.Abs and has an additional fallback that tries
// templates/<basename> when a path resolves outside the root. This function
// intentionally resolves against rootDir (modeline paths are root-relative by
// convention) and does not perform the basename fallback to avoid silently
// substituting a different file.
func resolveTemplatePaths(templates []string, rootDir string) []string {
	resolved := make([]string, len(templates))
	if rootDir == "" {
		// No rootDir specified — normalize paths only, don't resolve against CWD
		for i, p := range templates {
			resolved[i] = engine.NormalizeTemplatePath(p)
		}
		return resolved
	}
	absRootDir, rootErr := filepath.Abs(rootDir)
	if rootErr != nil {
		for i, p := range templates {
			resolved[i] = engine.NormalizeTemplatePath(p)
		}
		return resolved
	}

	for i, templatePath := range templates {
		var absTemplatePath string
		if filepath.IsAbs(templatePath) {
			absTemplatePath = templatePath
		} else {
			// Resolve relative paths against rootDir, not CWD
			absTemplatePath = filepath.Join(absRootDir, templatePath)
		}
		relPath, relErr := filepath.Rel(absRootDir, absTemplatePath)
		if relErr != nil {
			resolved[i] = engine.NormalizeTemplatePath(templatePath)
			continue
		}
		relPath = filepath.Clean(relPath)
		if strings.HasPrefix(relPath, "..") {
			// Path goes outside project root — use original path as-is
			resolved[i] = engine.NormalizeTemplatePath(templatePath)
			continue
		}
		resolved[i] = engine.NormalizeTemplatePath(relPath)
	}
	return resolved
}

func init() {
	applyCmd.Flags().BoolVarP(&applyCmdFlags.insecure, "insecure", "i", false, "apply using the insecure (encrypted with no auth) maintenance service")
	applyCmd.Flags().StringSliceVarP(&applyCmdFlags.configFiles, "file", "f", nil, "specify config files or patches in a YAML file (can specify multiple)")
	applyCmd.Flags().StringVar(&applyCmdFlags.talosVersion, "talos-version", "", "the desired Talos version to generate config for (backwards compatibility, e.g. v0.8)")
	applyCmd.Flags().StringVar(&applyCmdFlags.withSecrets, "with-secrets", "", "use a secrets file generated using 'gen secrets'")
	applyCmd.Flags().StringVar(&applyCmdFlags.kubernetesVersion, "kubernetes-version", constants.DefaultKubernetesVersion, "desired kubernetes version to run")
	applyCmd.Flags().BoolVarP(&applyCmdFlags.debug, "debug", "", false, "show only rendered patches")
	applyCmd.Flags().BoolVar(&applyCmdFlags.dryRun, "dry-run", false, "check how the config change will be applied in dry-run mode")
	applyCmd.Flags().DurationVar(&applyCmdFlags.configTryTimeout, "timeout", constants.ConfigTryTimeout, "the config will be rolled back after specified timeout (if try mode is selected)")
	applyCmd.Flags().StringSliceVar(&applyCmdFlags.certFingerprints, "cert-fingerprint", nil, "list of server certificate fingeprints to accept (defaults to no check)")
	applyCmd.Flags().BoolVar(&applyCmdFlags.force, "force", false, "will overwrite existing files")
	helpers.AddModeFlags(&applyCmdFlags.Mode, applyCmd)

	addCommand(applyCmd)
}
