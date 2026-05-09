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
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/cozystack/talm/pkg/engine"
	"github.com/spf13/cobra"
	"google.golang.org/grpc/metadata"
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
			// Template rendering path: render templates online per node and
			// apply the rendered config plus the node file overlay. See
			// applyTemplatesPerNode for why the loop is mandatory.
			opts := buildApplyRenderOptions(modelineTemplates, withSecretsPath)

			nodes := append([]string(nil), GlobalArgs.Nodes...)
			fmt.Printf("- talm: file=%s, nodes=%s, endpoints=%s\n", configFile, nodes, GlobalArgs.Endpoints)

			applyClosure := func(ctx context.Context, c *client.Client, data []byte) error {
				// ctx is shaped for ApplyConfiguration on every apply path:
				// the auth branch sets `nodes` (plural, one element) via
				// openClientPerNodeAuth so apid resolves a single backend
				// and helpers.ForEachResource can read the plural key from
				// inside template lookups; the insecure branch carries no
				// node metadata at all and the maintenance client dials a
				// single endpoint per call.
				//
				// The COSI preflight needs a different context shape:
				// Talos's apid director rejects every COSI method whose
				// ctx carries the plural "nodes" key, regardless of slice
				// length (its COSI guard is unconditional). cosiVersionReader
				// swallows errors and returns ok=false on rejection, so the
				// preflight would silently no-op on the auth path — defeating
				// the whole point of the version-mismatch warning that
				// preflightCheckTalosVersion exists to surface.
				// cosiPreflightContext rebuilds ctx with the singular "node"
				// key so the COSI router accepts the call; ApplyConfiguration
				// keeps the original ctx unchanged.
				cosiCtx, err := cosiPreflightContext(ctx)
				if err != nil {
					return err
				}

				preflightCheckTalosVersion(cosiCtx, cosiVersionReader(c), applyCmdFlags.talosVersion, os.Stderr)

				resp, err := c.ApplyConfiguration(ctx, &machineapi.ApplyConfigurationRequest{
					Data:           data,
					Mode:           applyCmdFlags.Mode.Mode,
					DryRun:         applyCmdFlags.dryRun,
					TryModeTimeout: durationpb.New(applyCmdFlags.configTryTimeout),
				})
				if err != nil {
					return errors.Wrap(annotateApplyConfigError(err), "applying new configuration")
				}

				helpers.PrintApplyResults(resp)

				return nil
			}

			if applyCmdFlags.insecure {
				openClient := openClientPerNodeMaintenance(applyCmdFlags.certFingerprints, WithClientMaintenance)
				if err := applyTemplatesPerNode(opts, configFile, nodes, openClient, engine.Render, applyClosure); err != nil {
					return err
				}
			} else {
				if err := withApplyClientBare(func(parentCtx context.Context, c *client.Client) error {
					resolved := resolveAuthTemplateNodes(nodes, c)
					openClient := openClientPerNodeAuth(parentCtx, c)

					return applyTemplatesPerNode(opts, configFile, resolved, openClient, engine.Render, applyClosure)
				}); err != nil {
					return err
				}
			}
		} else {
			// Direct patch path: apply config file as patch against empty bundle
			opts := buildApplyPatchOptions(withSecretsPath)
			patches := []string{"@" + configFile}

			configBundle, machineType, err := engine.FullConfigProcess(ctx, opts, patches)
			if err != nil {
				return errors.WithHint(
					errors.Wrap(err, "full config processing"),
					"the chart did not render or could not be combined with the supplied patches; check that the chart in scope and the patches reference fields that exist",
				)
			}

			result, err := engine.SerializeConfiguration(configBundle, machineType)
			if err != nil {
				return errors.WithHint(
					errors.Wrap(err, "serializing configuration"),
					"the merged config bundle could not be encoded back to YAML; this is internal — file an issue if reproducible",
				)
			}

			if err := withApplyClient(func(ctx context.Context, c *client.Client) error {
				// wrapWithNodeContext fills ctx via client.WithNodes from
				// talosconfig when --nodes is omitted, but does not mutate
				// GlobalArgs.Nodes. Mirror its resolution here so the log line
				// and the per-node preflight loop see the actual targets.
				targetNodes := append([]string(nil), GlobalArgs.Nodes...)
				if len(targetNodes) == 0 {
					if cfg := c.GetConfigContext(); cfg != nil {
						targetNodes = append(targetNodes, cfg.Nodes...)
					}
				}

				fmt.Printf("- talm: file=%s, nodes=%s, endpoints=%s\n", configFile, targetNodes, GlobalArgs.Endpoints)

				// COSI does not support multi-node proxying — apid's
				// director rejects every /cosi.* method whose ctx
				// carries the plural "nodes" key, regardless of slice
				// length. The rule lives in
				// internal/app/apid/pkg/director/director.go (search
				// for the "one-2-many proxying is not supported"
				// guard). Run preflight per node with a single-target
				// context.
				//
				// client.WithNode (singular) here is intentional and unrelated
				// to the auth template-rendering apply path's switch from
				// WithNode to WithNodes (openClientPerNodeAuth) — preflight
				// performs a direct COSI Get against one resource, not a
				// helpers.ForEachResource walk that reads the plural "nodes"
				// metadata key. apid's COSI router accepts the singular
				// "node" key for single-target addressing (and rejects the
				// plural "nodes" key for any COSI method, regardless of
				// slice length — see cosiPreflightContext for the auth
				// path's workaround that has to scope ctx back to "node"
				// before calling the same COSI preflight).
				read := cosiVersionReader(c)
				for _, node := range targetNodes {
					preflightCheckTalosVersion(client.WithNode(ctx, node), read, applyCmdFlags.talosVersion, os.Stderr)
				}

				resp, err := c.ApplyConfiguration(ctx, &machineapi.ApplyConfigurationRequest{
					Data:           result,
					Mode:           applyCmdFlags.Mode.Mode,
					DryRun:         applyCmdFlags.dryRun,
					TryModeTimeout: durationpb.New(applyCmdFlags.configTryTimeout),
				})
				if err != nil {
					return errors.Wrap(annotateApplyConfigError(err), "applying new configuration")
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

// withApplyClient creates a Talos client appropriate for the current apply
// mode and invokes the given action with it. The action receives a context
// in which gRPC node metadata is set to the resolved node list — either
// GlobalArgs.Nodes (when set) or the talosconfig context's Nodes (when not).
// Used by the direct-patch branch where multi-node fan-out happens at the
// gRPC layer inside ApplyConfiguration.
func withApplyClient(f func(ctx context.Context, c *client.Client) error) error {
	return withApplyClientBare(wrapWithNodeContext(f))
}

// withApplyClientBare connects to Talos for the current apply mode but does
// NOT inject node metadata into the context — leaving that decision to the
// caller. Used by the template-rendering path (see applyTemplatesPerNode for
// the rationale).
func withApplyClientBare(f func(ctx context.Context, c *client.Client) error) error {
	if applyCmdFlags.insecure {
		// Maintenance mode reads its endpoints directly from
		// GlobalArgs.Nodes — gRPC node metadata is not consulted.
		return WithClientMaintenance(applyCmdFlags.certFingerprints, f)
	}

	if GlobalArgs.SkipVerify {
		return WithClientSkipVerify(f)
	}

	return WithClientNoNodes(f)
}

// renderFunc, applyFunc and openClientFunc are injection points for
// applyTemplatesPerNode so unit tests can drive the loop with fakes instead
// of a real Talos client.
type (
	renderFunc func(ctx context.Context, c *client.Client, opts engine.Options) ([]byte, error)
	applyFunc  func(ctx context.Context, c *client.Client, data []byte) error
)

// openClientFunc opens a Talos client suitable for a single node and runs
// action with it. Authenticated mode reuses one parent client and rotates
// the node via single-element-slice gRPC metadata (client.WithNodes with
// one entry — the plural key is what helpers.ForEachResource and apid both
// read, while FailIfMultiNodes still treats len("nodes") == 1 as
// single-target). Insecure (maintenance) mode opens a fresh
// single-endpoint client per node because Talos's maintenance client
// ignores node metadata in the context and round-robins between its
// configured endpoints.
type openClientFunc func(node string, action func(ctx context.Context, c *client.Client) error) error

// applyTemplatesPerNode runs render → MergeFileAsPatch → apply once per
// node. Two reasons it has to iterate:
//
//   - engine.Render's FailIfMultiNodes guard rejects a context that carries
//     more than one node, so the auth-mode caller has to attach a single
//     node per iteration — and discovery via lookup() should resolve each
//     node's own topology in any case.
//   - In insecure (maintenance) mode the client connects directly to a
//     Talos node and ignores nodes-in-context entirely, so each node needs
//     its own client; otherwise gRPC round-robins ApplyConfiguration
//     across the endpoint list and most nodes never see the config.
//
// Both modes share this loop via openClient.
func applyTemplatesPerNode(
	opts engine.Options,
	configFile string,
	nodes []string,
	openClient openClientFunc,
	render renderFunc,
	apply applyFunc,
) error {
	if len(nodes) == 0 {
		return errors.WithHint(
			errors.New("nodes are not set for the command"),
			"set the targets via --nodes, a `# talm: nodes=[...]` modeline at the top of the node file, or the talosconfig context",
		)
	}
	// A node-file body (hostname, address, VIP, etc.) is a per-node
	// pin. Replaying it across multiple targets would stamp the same
	// value onto every machine, so reject this combination early and
	// ask the user to split the file. Modeline-only files (no body)
	// are fine — they just carry the target list.
	if len(nodes) > 1 {
		hasOverlay, err := engine.NodeFileHasOverlay(configFile)
		if err != nil {
			return err
		}

		if hasOverlay {
			return errors.WithHintf(
				errors.Newf("node file %q targets %d nodes (%v) but carries a non-empty per-node body", configFile, len(nodes), nodes),
				"split %q into one file per node, or remove the per-node fields if you want the rendered template alone applied to each",
				configFile,
			)
		}
	}

	for _, node := range nodes {
		if err := openClient(node, func(ctx context.Context, c *client.Client) error {
			return renderMergeAndApply(ctx, c, opts, configFile, render, apply)
		}); err != nil {
			return errors.Wrapf(err, "node %s", node)
		}
	}

	return nil
}

// maintenanceClientFunc is the contract WithClientMaintenance satisfies in
// production and a fake satisfies in tests. Injection lets the unit tests
// run the real openClientPerNodeMaintenance body without dialing a Talos
// node.
type maintenanceClientFunc func(fingerprints []string, action func(ctx context.Context, c *client.Client) error) error

// openClientPerNodeMaintenance returns an openClientFunc that opens a
// fresh single-endpoint maintenance client per node. Multi-node insecure
// apply (first bootstrap of a multi-node cluster) needs this because
// WithClientMaintenance creates a client with all endpoints and gRPC then
// round-robins ApplyConfiguration across them — most nodes never see the
// config. Narrowing GlobalArgs.Nodes to the current iteration's node and
// restoring it via defer keeps the wrapper's signature unchanged.
//
// Correctness depends on mkClient reading GlobalArgs.Nodes synchronously
// — i.e. before action returns. WithClientMaintenance does this today
// (it builds the client with the endpoints and calls action inside the
// same goroutine). A future Talos refactor that defers endpoint
// resolution onto a goroutine, or that captures the slice for later
// use, would silently break this contract; an upstream overload that
// takes endpoints as a parameter would be the durable fix.
//
// mkClient is normally WithClientMaintenance; tests pass a fake that
// captures the GlobalArgs.Nodes value at the moment WithClientMaintenance
// would have read it.
func openClientPerNodeMaintenance(fingerprints []string, mkClient maintenanceClientFunc) openClientFunc {
	return func(node string, action func(ctx context.Context, c *client.Client) error) error {
		savedNodes := append([]string(nil), GlobalArgs.Nodes...)

		GlobalArgs.Nodes = []string{node}
		defer func() { GlobalArgs.Nodes = savedNodes }()

		return mkClient(fingerprints, action)
	}
}

// openClientPerNodeAuth returns an openClientFunc that reuses one
// authenticated client (the one withApplyClientBare opened above this
// callback) and rotates the addressed node via client.WithNodes on the
// per-iteration context, passing a single-element slice. The plural key
// is what Talos's helpers.ForEachResource reads inside template lookups
// (cmd/talosctl/pkg/talos/helpers/resources.go) — a singular "node" key
// is invisible to it and the helper falls back to []string{""}, which
// surfaces as `rpc error: code = Internal desc = invalid target ""`
// from inside template `lookup` calls. helpers.FailIfMultiNodes accepts
// len("nodes") <= 1, so a single-element slice still satisfies the
// multi-node guard while making lookups work.
func openClientPerNodeAuth(parentCtx context.Context, c *client.Client) openClientFunc {
	return func(node string, action func(ctx context.Context, c *client.Client) error) error {
		return action(client.WithNodes(parentCtx, node), c)
	}
}

// cosiPreflightContext returns a context suitable for a COSI call
// against the same single target the caller's ctx addresses on the
// machine API. Talos's apid director rejects every COSI method whose
// outgoing context carries the plural "nodes" metadata key, regardless
// of how many entries the slice has — the COSI router insists on the
// singular "node" key. The director rule lives in
// internal/app/apid/pkg/director/director.go (search for the
// "/cosi." method-prefix branch); a future maintainer who suspects
// the rule has changed should re-check that file before relaxing this
// helper.
//
// The auth template-rendering apply path uses client.WithNodes
// (plural, single-element slice) so that helpers.ForEachResource and
// the apid backend resolver can both read the plural key from template
// lookups; that ctx is therefore unsuitable for COSI reads as is.
//
// client.WithNode (machinery client/context.go) copies the existing
// outgoing metadata, deletes "nodes", and sets "node" — so calling it
// with the single target is enough; we do not have to mutate metadata
// ourselves. ctx is unchanged for the insecure (maintenance) path
// that carries no node metadata at all.
//
// A multi-element plural slice is a programmer error at this layer:
// applyTemplatesPerNode iterates one node at a time, so an outgoing
// "nodes" of length > 1 means a future caller broke the per-node
// invariant. Surface it as an error instead of silently passing the
// ctx through to a COSI call that apid will reject — the latter is
// the exact silent no-op this helper exists to prevent.
func cosiPreflightContext(ctx context.Context) (context.Context, error) {
	md, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		return ctx, nil
	}

	nodes := md.Get("nodes")
	switch len(nodes) {
	case 0:
		return ctx, nil
	case 1:
		return client.WithNode(ctx, nodes[0]), nil
	default:
		return nil, errors.WithHint(
			errors.Newf("cosiPreflightContext: refusing to scope ctx with %d nodes; expected exactly one", len(nodes)),
			"applyTemplatesPerNode iterates one node at a time, so a multi-element plural slice at this point indicates a broken caller",
		)
	}
}

// resolveAuthTemplateNodes returns the node list the authenticated
// template-rendering path should iterate over. cliNodes (from --nodes
// or the modeline) takes precedence; when empty, the talosconfig
// context's Nodes are used so a user who already ran `talosctl config
// node <ip>` does not have to repeat the node list on every `talm
// apply`. Mirrors wrapWithNodeContext's fallback for the direct-patch
// branch. Insecure mode does not call this helper — maintenance
// clients talk to node IPs directly and have no talosconfig context.
func resolveAuthTemplateNodes(cliNodes []string, c *client.Client) []string {
	if len(cliNodes) > 0 {
		return cliNodes
	}

	if c == nil {
		return nil
	}

	cfg := c.GetConfigContext()
	if cfg == nil {
		return nil
	}

	return append([]string(nil), cfg.Nodes...)
}

// renderMergeAndApply is the per-node body shared by every apply mode.
func renderMergeAndApply(ctx context.Context, c *client.Client, opts engine.Options, configFile string, render renderFunc, apply applyFunc) error {
	rendered, err := render(ctx, c, opts)
	if err != nil {
		return errors.WithHint(
			errors.Wrap(err, "template rendering"),
			"the chart did not render against the current node's discovery state; verify the templates referenced in the modeline exist and the node is reachable",
		)
	}

	merged, err := engine.MergeFileAsPatch(rendered, configFile)
	if err != nil {
		return errors.Wrapf(err, "merging node file %q as patch", configFile)
	}

	return apply(ctx, c, merged)
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
				return errors.WithHint(
					errors.New("resolving config context: no client available"),
					"this code path requires a Talos client; if you reached it from a flow that did not open one, check the call site",
				)
			}

			configContext := c.GetConfigContext()
			if configContext == nil {
				return errors.WithHint(
					errors.New("resolving config context"),
					"the talosconfig has no active context; pick one with `talosctl config context <name>` or pass --talosconfig",
				)
			}

			nodes = configContext.Nodes
		}

		ctx = client.WithNodes(ctx, nodes...)

		return f(ctx, c)
	}
}

// isOutsideRoot reports whether a cleaned relative path escapes the
// project root. A HasPrefix(".." ) test would misclassify sibling
// directories whose first path element merely starts with "..", such
// as "..templates/controlplane.yaml"; we match a full path element
// instead.
func isOutsideRoot(relPath string) bool {
	return relPath == ".." || strings.HasPrefix(relPath, ".."+string(filepath.Separator))
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
		if isOutsideRoot(relPath) {
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
