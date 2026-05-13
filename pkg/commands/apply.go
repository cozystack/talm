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
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/cozystack/talm/pkg/engine"
	"github.com/cozystack/talm/pkg/modeline"
	"github.com/spf13/cobra"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/siderolabs/talos/cmd/talosctl/pkg/talos/helpers"
	machineapi "github.com/siderolabs/talos/pkg/machinery/api/machine"
	"github.com/siderolabs/talos/pkg/machinery/client"
	"github.com/siderolabs/talos/pkg/machinery/constants"
)

// parentDir is the path element that escapes the current directory
// when it appears at the start of a cleaned relative path. Hoisted to
// a const so the goconst gate sees a single canonical reference.
const parentDir = ".."

// applyCommandName labels this subcommand inside engine.Options for
// FailIfMultiNodes error wording. Centralised so the template
// rendering options block and any test asserting against the field
// share a single canonical value.
const applyCommandName = "talm apply"

// roleAnchor and roleSidePatch label the role of a config file in
// the apply chain for operator-facing error messages
// (rejectMultiNodeOverlayFiles). Hoisted to constants so goconst
// stays clean across the applyCmd.Long block, which mentions the
// "anchor" concept several times in operator-facing prose.
const (
	roleAnchor    = "anchor"
	roleSidePatch = "side-patch"
)

//nolint:gochecknoglobals // cobra command flag struct, idiomatic for cobra-based CLIs
var applyCmdFlags struct {
	helpers.Mode

	certFingerprints       []string
	insecure               bool
	configFiles            []string // -f/--files
	talosVersion           string
	withSecrets            string
	debug                  bool
	kubernetesVersion      string
	dryRun                 bool
	preserve               bool
	stage                  bool
	force                  bool
	configTryTimeout       time.Duration
	nodesFromArgs          bool
	endpointsFromArgs      bool
	skipResourceValidation bool
	skipDriftPreview       bool
	skipPostApplyVerify    bool
	showSecretsInDrift     bool
}

//nolint:gochecknoglobals // cobra command, idiomatic for cobra-based CLIs
var applyCmd = &cobra.Command{
	Use:   "apply",
	Short: "Apply config to a Talos node",
	Long: `Apply rendered configuration to a Talos node.

Multi-file invocation (anchor + side-patches, per #184): the FIRST -f file
is the anchor — it must carry a "# talm: nodes=[…], templates=[…]" modeline
and lives under a project root (Chart.yaml + secrets.yaml). Subsequent -f
files are side-patches stacked on top of the anchor's rendered config in
the order they appear; each is merged via the same overlay mechanism the
anchor's node body uses. A single ApplyConfiguration is issued per node
with the composed result.

Examples:

  # Single node file (anchor only):
  talm apply -f nodes/cp01.yaml

  # Stacked side-patches (e.g. one-shot debug overlay):
  talm apply -f nodes/cp01.yaml -f /tmp/debug-kubelet.yaml

  # Side-patches do NOT need to live under the project root; the
  # first file anchors detection. Reversing the order is an error
  # (the orphan path has no project to anchor on).`,
	Args: cobra.NoArgs,
	PreRunE: func(cmd *cobra.Command, _ []string) error {
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
			GlobalArgs.Endpoints = append(GlobalArgs.Endpoints, defaultLocalEndpoint)
		}

		return nil
	},
	RunE: func(_ *cobra.Command, _ []string) error {
		return apply()
	},
}

func apply() error {
	// Expand directories to YAML files
	expandedFiles, err := ExpandFilePaths(applyCmdFlags.configFiles)
	if err != nil {
		return err
	}

	// Detect root from files if specified, otherwise fallback to cwd
	err = DetectAndSetRootFromFiles(expandedFiles)
	if err != nil {
		return err
	}

	// Pre-#184: each -f file ran through applyOneFile in turn, with
	// each iteration producing its own ApplyConfiguration. For a
	// chain like `-f node.yaml -f side-patch.yaml` that meant the
	// second apply OVERWROTE the first — Talos replaces the whole
	// MachineConfig per call. Per #184 the first file anchors the
	// project root and any subsequent files are side-patches stacked
	// on top of the first file's rendered config; the result is a
	// single ApplyConfiguration per node carrying the composed
	// bundle. Multiple modelined node files (the historical "apply
	// to N nodes in one command" idiom) is still supported via the
	// first file's modeline carrying `nodes=[…]`.
	if len(expandedFiles) == 0 {
		return nil
	}

	return applyOneFile(expandedFiles[0], expandedFiles[1:])
}

// resetGlobalArgsBetweenFiles wipes the per-file GlobalArgs.Nodes /
// GlobalArgs.Endpoints state between iterations of a multi-file apply
// or template command. Each iteration's modeline rewrites these
// values in-place via processModelineAndUpdateGlobals; without a
// reset between files, the previous file's modeline-supplied values
// would leak into a subsequent file whose modeline omits them.
//
// The empty-slice reset on Endpoints is deliberate. The talos client
// (cmd/talosctl/pkg/talos/global/client.go) registers
// client.WithConfig(cfg) first and then layers client.WithEndpoints
// on top only when len(c.Endpoints) > 0. An empty GlobalArgs.Endpoints
// therefore falls back to the talosconfig context endpoints — the
// behavior an operator expects when their `talosconfig` already names
// the cluster. Re-seeding defaultLocalEndpoint here would override
// the talosconfig context with loopback on every file past the first
// whose modeline omits endpoints, silently mis-routing applies to
// 127.0.0.1.
//
// PreRunE seeds defaultLocalEndpoint once before the loop runs as a
// belt-and-braces guard against zero-endpoint client construction on
// the very first file. From the second file onward the talosconfig
// fallback is the intended behavior.
func resetGlobalArgsBetweenFiles(nodesFromArgs, endpointsFromArgs bool) {
	if !nodesFromArgs {
		GlobalArgs.Nodes = []string{}
	}

	if !endpointsFromArgs {
		GlobalArgs.Endpoints = []string{}
	}
}

// applyOneFile dispatches the apply pipeline for the anchor
// configFile (file[0] of the -f chain) and an optional ordered list
// of sidePatches (file[1:]). The anchor MUST be modelined when
// sidePatches are present — side-patches are stacked on top of the
// rendered config, which requires the chart templates from the
// anchor's modeline. Single-file invocations (no side-patches)
// retain the pre-#184 dispatch: modelined → template path,
// non-modelined → direct-patch path.
//
// Stack semantics (#184): the anchor's chart + body + each
// sidePatch in order produces ONE composed config per node; one
// ApplyConfiguration call is issued. Pre-#184 looped each file
// independently, so the second apply overwrote the first.
func applyOneFile(configFile string, sidePatches []string) error {
	// Reject modelined files in the side-patch slots before any
	// dispatch decisions. The apply chain treats file[0] as the
	// anchor and file[1:] as bytes-level patches stacked onto the
	// anchor's render — if a side-patch carries its own modeline,
	// its nodes/templates/endpoints are silently ignored because
	// MergeFileAsPatch treats `# talm: …` as a YAML comment.
	// Operators expecting per-file independent applies (the
	// kubectl-style multi-node shape) would silently target only
	// the first file's nodes; gate that here so the misuse is
	// loud, with a hint pointing at the correct per-file loop.
	if err := rejectModelinedSidePatches(sidePatches); err != nil {
		return err
	}

	hasModeline, modelineErr := fileHasTalmModeline(configFile)
	// Malformed modeline (typo in keys, bad JSON value, non-comment
	// line before the modeline) MUST surface to the operator —
	// silently routing onto the direct-patch path here would hide
	// the real cause and produce a misleading "no nodes" hint later.
	// Only the "no `# talm:` line in file" case (ErrModelineNotFound)
	// is allowed to fall through as a side-patch-shaped input.
	if modelineErr != nil {
		return errors.Wrapf(modelineErr, "parsing modeline in %s", configFile)
	}

	// Resolve secrets.yaml path relative to project root if not absolute
	withSecretsPath := ResolveSecretsPath(applyCmdFlags.withSecrets)

	if !hasModeline {
		if len(sidePatches) > 0 {
			//nolint:wrapcheck // cockroachdb/errors.WithHint at boundary.
			return errors.WithHint(
				errors.Newf("first -f file %s lacks a modeline; side-patches require a modelined anchor", configFile),
				"the first -f file in a chain must carry a `# talm: nodes=[…], templates=[…]` modeline so talm knows what to render before stacking side-patches on top",
			)
		}

		// Targets may come from --nodes OR from the talosconfig
		// context (resolved inside applyOneFileDirectPatchMode via
		// client.GetConfigContext().Nodes). Defer the "no nodes
		// anywhere" check to the apply path itself so the
		// talosconfig-default flow stays reachable.
		return applyOneFileDirectPatchMode(configFile, withSecretsPath)
	}

	// Modelined anchor: parse modeline, populate GlobalArgs, then
	// dispatch into template-mode (chart render + body + side-patches)
	// or direct-patch-mode (no templates declared).
	resetGlobalArgsBetweenFiles(applyCmdFlags.nodesFromArgs, applyCmdFlags.endpointsFromArgs)

	modelineTemplates, err := processModelineAndUpdateGlobals(configFile, applyCmdFlags.nodesFromArgs, applyCmdFlags.endpointsFromArgs, true)
	if err != nil {
		return err
	}

	if len(modelineTemplates) > 0 {
		return applyOneFileTemplateMode(configFile, sidePatches, modelineTemplates, withSecretsPath)
	}

	if len(sidePatches) > 0 {
		//nolint:wrapcheck // cockroachdb/errors.WithHint at boundary.
		return errors.WithHint(
			errors.Newf("anchor %s declares no templates; side-patches require chart rendering", configFile),
			"add `templates=[\"templates/<name>.yaml\"]` to the first file's modeline so the chain has a base config to stack onto",
		)
	}

	return applyOneFileDirectPatchMode(configFile, withSecretsPath)
}

// fileHasTalmModeline classifies configFile into one of three
// shapes by attempting to parse its modeline:
//
//   - (true, nil)  — file has a well-formed `# talm: …` modeline
//   - (false, nil) — file has no `# talm:` line at all (the
//     side-patch input shape — caller routes to direct-patch)
//   - (false, err) — file has a modeline candidate but parsing it
//     failed (typo in keys, bad JSON value, non-comment YAML
//     before the modeline). The caller surfaces err to the
//     operator so the real cause is visible.
//
// Pre-fix this returned a bare bool and swallowed all errors,
// silently routing typoed modelines onto the direct-patch path
// where the operator saw a misleading "no nodes" hint instead of
// "parsing modeline: invalid JSON".
func fileHasTalmModeline(configFile string) (bool, error) {
	_, _, err := modeline.FindAndParseModeline(configFile)
	if err == nil {
		return true, nil
	}

	if errors.Is(err, modeline.ErrModelineNotFound) {
		return false, nil
	}

	// The caller (applyOneFile) wraps this with the configFile
	// path; FindAndParseModeline already attaches WithHint at its
	// boundary, so wrapping again here would double-message.
	return false, err //nolint:wrapcheck // forwarded verbatim; caller wraps with configFile context
}

// rejectMultiNodeOverlayFiles scans the anchor and every side-patch
// for a per-node body overlay (hostname, address, VIP, machine
// identifier) when the apply targets more than one node. The
// anchor's overlay check is the original guard against an
// operator pointing one node file at N nodes; the side-patch
// check is the same guard extended to the chain. A side-patch is
// stamped identically onto every node in the chain, so a per-node
// field inside it produces the same N-machines-same-hostname
// footgun the anchor check was added to prevent.
func rejectMultiNodeOverlayFiles(configFile string, sidePatches, nodes []string) error {
	candidates := append([]string{configFile}, sidePatches...)
	for _, path := range candidates {
		hasOverlay, err := engine.NodeFileHasOverlay(path)
		if err != nil {
			return errors.Wrapf(err, "checking %q for per-node body overlay", path)
		}

		if !hasOverlay {
			continue
		}

		role := roleAnchor
		if path != configFile {
			role = roleSidePatch
		}
		//nolint:wrapcheck // sentinel constructed in-place; WithHintf attaches operator guidance
		return errors.WithHintf(
			errors.Newf("%s %q would be stamped onto %d nodes (%v) but carries a non-empty per-node body", role, path, len(nodes), nodes),
			"split %q into one file per node, or remove the per-node fields if you want the file applied uniformly across each",
			path,
		)
	}

	return nil
}

// rejectModelinedSidePatches scans the side-patch slots of an
// apply chain (file[1:] of `talm apply -f anchor -f sideA -f sideB`)
// and rejects the chain when any side-patch carries its own
// modeline. Operators familiar with `kubectl apply -f *.yaml`
// often pass multiple modelined node files expecting independent
// applies — but the new chain semantics treat file[1:] as
// bytes-level patches stacked onto the anchor's render. A side-
// patch's `# talm: …` line is just a YAML comment to
// MergeFileAsPatch, so its nodes/templates/endpoints would be
// silently dropped while its body got merged into the anchor's
// targets. This guard surfaces that misuse loudly with a hint
// pointing at the per-file shell loop, which is the correct shape
// for the multi-node-apply pattern.
//
// A malformed modeline in a side-patch slot also surfaces — the
// operator should fix the typo regardless of whether they intended
// the file as anchor or side-patch.
func rejectModelinedSidePatches(sidePatches []string) error {
	for _, path := range sidePatches {
		hasModeline, modelineErr := fileHasTalmModeline(path)
		if modelineErr != nil {
			return errors.Wrapf(modelineErr, "parsing modeline in side-patch %s", path)
		}

		if hasModeline {
			//nolint:wrapcheck // cockroachdb/errors.WithHint at boundary.
			return errors.WithHint(
				errors.Newf("side-patch %s carries its own modeline; the apply chain treats only the first -f file as the anchor and stacks subsequent files as side-patches", path),
				"to apply multiple modelined node files independently, run one apply per file (e.g. `for f in nodes/*.yaml; do talm apply -f $f; done`); to use this file as a side-patch on the current anchor, strip its `# talm: …` line",
			)
		}
	}

	return nil
}

// applyOneFileTemplateMode runs the template-rendering apply path for
// a single rooted configFile and an optional chain of sidePatches.
// Per node: renders templates, overlays the node body, then stacks
// every sidePatch in order via engine.MergeFileAsPatch — a single
// ApplyConfiguration is issued per node with the final composed
// config. Empty sidePatches reproduces the single-file shape. See
// applyTemplatesPerNode for the per-node loop.
func applyOneFileTemplateMode(configFile string, sidePatches, modelineTemplates []string, withSecretsPath string) error {
	opts := buildApplyRenderOptions(modelineTemplates, withSecretsPath)

	nodes := append([]string(nil), GlobalArgs.Nodes...)
	// Elide the `side-patches=` segment in the single-file case so
	// the conventional path's progress line stays uncluttered. The
	// dominant invocation shape is `talm apply -f nodes/<name>.yaml`
	// without side-patches; reporting `side-patches=[]` on every line
	// was visible noise without operator value.
	if len(sidePatches) == 0 {
		//nolint:forbidigo // CLI progress line surfaces the file-to-target mapping for the operator
		fmt.Printf("- talm: file=%s, nodes=[%s], endpoints=[%s]\n", configFile, strings.Join(nodes, ","), strings.Join(GlobalArgs.Endpoints, ","))
	} else {
		//nolint:forbidigo // CLI progress line surfaces the file-to-target mapping for the operator
		fmt.Printf("- talm: file=%s, side-patches=[%s], nodes=[%s], endpoints=[%s]\n", configFile, strings.Join(sidePatches, ","), strings.Join(nodes, ","), strings.Join(GlobalArgs.Endpoints, ","))
	}

	applyClosure := buildApplyClosure()

	if applyCmdFlags.insecure {
		openClient := openClientPerNodeMaintenance(applyCmdFlags.certFingerprints, WithClientMaintenance)

		return applyTemplatesPerNode(opts, configFile, sidePatches, nodes, openClient, engine.Render, applyClosure)
	}

	return withApplyClientBare(func(parentCtx context.Context, c *client.Client) error {
		resolved := resolveAuthTemplateNodes(nodes, c)
		openClient := openClientPerNodeAuth(parentCtx, c)

		return applyTemplatesPerNode(opts, configFile, sidePatches, resolved, openClient, engine.Render, applyClosure)
	})
}

// buildApplyClosure builds the per-node apply step used by every
// template-rendering mode. ctx is shaped for ApplyConfiguration on
// every apply path: the auth branch sets `nodes` (plural, one
// element) via openClientPerNodeAuth so apid resolves a single
// backend and helpers.ForEachResource can read the plural key from
// inside template lookups; the insecure branch carries no node
// metadata at all and the maintenance client dials a single endpoint
// per call.
//
// The COSI preflight needs a different context shape: Talos's apid
// director rejects every COSI method whose ctx carries the plural
// "nodes" key, regardless of slice length (its COSI guard is
// unconditional). cosiVersionReader swallows errors and returns
// ok=false on rejection, so the preflight would silently no-op on
// the auth path — defeating the whole point of the version-mismatch
// warning that preflightCheckTalosVersion exists to surface.
// cosiPreflightContext rebuilds ctx with the singular "node" key so
// the COSI router accepts the call; ApplyConfiguration keeps the
// original ctx unchanged.
func buildApplyClosure() applyFunc {
	return func(ctx context.Context, c *client.Client, data []byte) error {
		cosiCtx, nodeID, err := cosiPreflightContext(ctx)
		if err != nil {
			return err
		}

		preflightCheckTalosVersion(cosiCtx, cosiVersionReader(c), applyCmdFlags.talosVersion, os.Stderr)

		if err := runPreApplyGates(cosiCtx, c, data, nodeID, os.Stderr); err != nil {
			return err
		}

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

		if err := runPostApplyGate(cosiCtx, c, data, nodeID, os.Stderr); err != nil {
			return err
		}

		return nil
	}
}

// applyOneFileDirectPatchMode runs the direct-patch apply path for a
// single configFile: renders the chart against an empty bundle with
// the file as a patch, then ApplyConfigurations the merged result.
// COSI does not support multi-node proxying — apid's director
// rejects every /cosi.* method whose ctx carries the plural "nodes"
// key, regardless of slice length. The rule lives in
// internal/app/apid/pkg/director/director.go (search for the
// "one-2-many proxying is not supported" guard). Run preflight per
// node with a single-target context.
//
// client.WithNode (singular) here is intentional and unrelated to
// the auth template-rendering apply path's switch from WithNode to
// WithNodes (openClientPerNodeAuth) — preflight performs a direct
// COSI Get against one resource, not a helpers.ForEachResource walk
// that reads the plural "nodes" metadata key. apid's COSI router
// accepts the singular "node" key for single-target addressing (and
// rejects the plural "nodes" key for any COSI method, regardless of
// slice length — see cosiPreflightContext for the auth path's
// workaround that has to scope ctx back to "node" before calling the
// same COSI preflight).
// resolveDirectPatchTargetNodes resolves the per-node target list
// for the direct-patch apply path. Order of precedence: --nodes
// (GlobalArgs.Nodes) first, falling back to the active talosconfig
// context's Nodes slice. Returns an operator-facing error when
// both sources are empty — the direct-patch path has no modeline
// to read targets from, so a missing --nodes AND a context with
// no Nodes leaves nowhere to send the apply.
func resolveDirectPatchTargetNodes(c *client.Client, configFile string) ([]string, error) {
	targetNodes := append([]string(nil), GlobalArgs.Nodes...)
	if len(targetNodes) == 0 {
		if cfg := c.GetConfigContext(); cfg != nil {
			targetNodes = append(targetNodes, cfg.Nodes...)
		}
	}

	if len(targetNodes) == 0 {
		//nolint:wrapcheck // cockroachdb/errors.WithHint at boundary.
		return nil, errors.WithHint(
			errors.Newf("no nodes to target for %s", configFile),
			"the file lacks a `# talm: nodes=[…]` modeline, no --nodes flag was passed, and the active talosconfig context carries no nodes; pass --nodes explicitly or supply a modelined node file",
		)
	}

	return targetNodes, nil
}

func applyOneFileDirectPatchMode(configFile, withSecretsPath string) error {
	opts := buildApplyPatchOptions(withSecretsPath)
	patches := []string{"@" + configFile}

	configBundle, machineType, err := engine.FullConfigProcess(opts, patches)
	if err != nil {
		//nolint:wrapcheck // already wrapped via errors.Wrap, WithHint adds operator-facing guidance
		return errors.WithHint(
			errors.Wrap(err, "full config processing"),
			"the chart did not render or could not be combined with the supplied patches; check that the chart in scope and the patches reference fields that exist",
		)
	}

	result, err := engine.SerializeConfiguration(configBundle, machineType)
	if err != nil {
		//nolint:wrapcheck // already wrapped via errors.Wrap, WithHint adds operator-facing guidance
		return errors.WithHint(
			errors.Wrap(err, "serializing configuration"),
			"the merged config bundle could not be encoded back to YAML; this is internal — file an issue if reproducible",
		)
	}

	return withApplyClient(func(ctx context.Context, c *client.Client) error {
		targetNodes, err := resolveDirectPatchTargetNodes(c, configFile)
		if err != nil {
			return err
		}

		//nolint:forbidigo // CLI progress line surfaces the file-to-target mapping for the operator
		fmt.Printf("- talm: file=%s, nodes=[%s], endpoints=[%s]\n", configFile, strings.Join(targetNodes, ","), strings.Join(GlobalArgs.Endpoints, ","))

		read := cosiVersionReader(c)

		for _, node := range targetNodes {
			nodeCtx := client.WithNode(ctx, node)
			preflightCheckTalosVersion(nodeCtx, read, applyCmdFlags.talosVersion, os.Stderr)

			if err := runPreApplyGates(nodeCtx, c, result, node, os.Stderr); err != nil {
				return err
			}
		}

		resp, err := c.ApplyConfiguration(ctx, &machineapi.ApplyConfigurationRequest{
			Data:           result,
			Mode:           applyCmdFlags.Mode.Mode,
			DryRun:         applyCmdFlags.dryRun,
			TryModeTimeout: durationpb.New(applyCmdFlags.configTryTimeout),
		})
		if err != nil {
			// Post-apply verify intentionally not run on this path:
			// Talos's ApplyConfiguration is multi-node aware, so an
			// error here doesn't pinpoint which nodes succeeded and
			// which didn't. Surface the wrapped error so the operator
			// can re-run apply (which will re-trigger the pre-apply
			// gate too) — running verify on possibly-partially-applied
			// state would produce confusing per-node divergence noise
			// on top of the actual failure.
			return errors.Wrap(annotateApplyConfigError(err), "applying new configuration")
		}

		helpers.PrintApplyResults(resp)

		return runPostApplyGates(ctx, c, result, targetNodes)
	})
}

// runPostApplyGates fans Phase 2B verification across every target
// node, collecting per-node findings before surfacing them. Apply
// already happened on every node — short-circuiting on the first
// divergence would hide later nodes' state. Mirrors ValidateRefs's
// collect-then-block pattern.
func runPostApplyGates(ctx context.Context, c *client.Client, result []byte, targetNodes []string) error {
	var perNodeErrs []error

	for _, node := range targetNodes {
		if err := runPostApplyGate(client.WithNode(ctx, node), c, result, node, os.Stderr); err != nil {
			perNodeErrs = append(perNodeErrs, errors.Wrapf(err, "node %s", node))
		}
	}

	return errors.Join(perNodeErrs...)
}

// runPreApplyGates wires the two pre-apply safety gates against the
// rendered MachineConfig. Phase 1 (resource existence) blocks on bad
// refs unless --skip-resource-validation is set. Phase 2A (drift
// preview) is informational and never blocks; --skip-drift-preview
// suppresses the read entirely.
//
// Phase 2A intentionally runs on --dry-run: the diff is read-only,
// and "show me what would change" is precisely what dry-run is for.
// Skipping it would leave operators with no way to preview drift
// short of a real apply.
func runPreApplyGates(ctx context.Context, c *client.Client, rendered []byte, nodeID string, w io.Writer) error {
	if !applyCmdFlags.skipResourceValidation {
		if err := preflightValidateResources(ctx, cosiLinksDisksReader(c), rendered, w); err != nil {
			return err
		}
	}

	if !shouldRunDriftPreview(applyCmdFlags.skipDriftPreview) {
		return nil
	}

	return previewDrift(ctx, cosiMachineConfigReader(c, applyCmdFlags.insecure), rendered, nodeID, w, applyCmdFlags.showSecretsInDrift)
}

// shouldRunDriftPreview is the testable predicate for Phase 2A
// scheduling. Pure function for pin-testing the contract: dry-run
// is intentionally NOT a skip reason here (the preview is read-only;
// dry-run wants the diff). Only the explicit skip flag suppresses
// the gate.
func shouldRunDriftPreview(skip bool) bool {
	return !skip
}

// runPostApplyGate wires Phase 2B (post-apply state verification).
// Skipped on dry-run (no real apply) and on the staged/try/reboot
// apply modes:
//
//   - --mode=staged stores the new config as staged; the active
//     MachineConfig resource is unchanged until reboot, so a verify
//     against ActiveID always reports divergence.
//   - --mode=try applies the config but auto-rolls back after the
//     configured timeout; verify would race against the rollback
//     timer and produce false positives.
//   - --mode=reboot reboots the node after ApplyConfiguration
//     returns success; the COSI connection dies mid-verify and the
//     reader returns a transient error, which the gate would
//     surface as a blocker — a false positive for a successful
//     reboot apply.
//
// All three modes have explicit contracts that diverge from "what
// was sent is what is on the node now after success was reported"
// (or guarantee the on-node state is inaccessible for verification);
// the gate respects them.
func runPostApplyGate(ctx context.Context, c *client.Client, sent []byte, nodeID string, w io.Writer) error {
	if !shouldRunPostApplyVerify(applyCmdFlags.Mode.Mode, applyCmdFlags.dryRun, applyCmdFlags.skipPostApplyVerify) {
		return nil
	}

	return verifyAppliedState(ctx, cosiMachineConfigReader(c, applyCmdFlags.insecure), sent, nodeID, w, applyCmdFlags.showSecretsInDrift)
}

// shouldRunPostApplyVerify is the testable predicate for runPostApplyGate.
// Returns false when the verify must be skipped for any reason listed in
// runPostApplyGate's doc.
func shouldRunPostApplyVerify(mode machineapi.ApplyConfigurationRequest_Mode, dryRun, skip bool) bool {
	if skip || dryRun {
		return false
	}

	switch mode {
	case machineapi.ApplyConfigurationRequest_STAGED,
		machineapi.ApplyConfigurationRequest_TRY,
		machineapi.ApplyConfigurationRequest_REBOOT,
		// AUTO is skipped because Talos's apply-server promotes AUTO
		// to REBOOT internally when the change requires it (the
		// CanApplyImmediate check inside v1alpha1_server.go's AUTO
		// branch). The verify call would then race the reboot the
		// node dispatches in a goroutine before returning the RPC,
		// producing a false-positive transient-error blocker —
		// the same shape the explicit REBOOT skip avoids.
		//
		// Cost: AUTO applies that DON'T require a reboot lose their
		// post-apply verify. Acceptable trade-off: the verify is
		// default-off until the Talos-mutated-field allowlist lands
		// (see #172), and an operator who needs verify-on-no-reboot
		// can pass --mode=no-reboot explicitly.
		machineapi.ApplyConfigurationRequest_AUTO:
		return false
	case machineapi.ApplyConfigurationRequest_NO_REBOOT:
		// fall through to default true.
	}

	return true
}

// withApplyClient creates a Talos client appropriate for the current apply
// mode and invokes the given action with it. The action receives a context
// in which gRPC node metadata is set to the resolved node list — either
// GlobalArgs.Nodes (when set) or the talosconfig context's Nodes (when not).
// Used by the direct-patch branch where multi-node fan-out happens at the
// gRPC layer inside ApplyConfiguration.
func withApplyClient(action func(ctx context.Context, c *client.Client) error) error {
	return withApplyClientBare(wrapWithNodeContext(action))
}

// withApplyClientBare connects to Talos for the current apply mode but does
// NOT inject node metadata into the context — leaving that decision to the
// caller. Used by the template-rendering path (see applyTemplatesPerNode for
// the rationale).
func withApplyClientBare(action func(ctx context.Context, c *client.Client) error) error {
	if applyCmdFlags.insecure {
		// Maintenance mode reads its endpoints directly from
		// GlobalArgs.Nodes — gRPC node metadata is not consulted.
		return WithClientMaintenance(applyCmdFlags.certFingerprints, action)
	}

	if GlobalArgs.SkipVerify {
		return WithClientSkipVerify(action)
	}

	return WithClientNoNodes(action)
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
//
//nolint:gocritic // opts taken by value to keep the test-injection signature stable; engine.Options is treated as a value type elsewhere
func applyTemplatesPerNode(
	opts engine.Options,
	configFile string,
	sidePatches []string,
	nodes []string,
	openClient openClientFunc,
	render renderFunc,
	apply applyFunc,
) error {
	if len(nodes) == 0 {
		//nolint:wrapcheck // sentinel constructed in-place; WithHint attaches operator guidance
		return errors.WithHint(
			errors.New("nodes are not set for the command"),
			"set the targets via --nodes, a `# talm: nodes=[...]` modeline at the top of the node file, or the talosconfig context",
		)
	}
	// A node-file body (hostname, address, VIP, etc.) is a per-node
	// pin. Replaying it across multiple targets would stamp the same
	// value onto every machine, so reject this combination early and
	// ask the user to split the file. Modeline-only files (no body)
	// are fine — they just carry the target list. Side-patches are
	// subject to the same rule: they are stacked identically onto
	// every node, so a per-node field inside a side-patch is the
	// same multi-node-stamp footgun as a per-node field inside the
	// anchor.
	if len(nodes) > 1 {
		if err := rejectMultiNodeOverlayFiles(configFile, sidePatches, nodes); err != nil {
			return err
		}
	}

	for _, node := range nodes {
		err := openClient(node, func(ctx context.Context, c *client.Client) error {
			return renderMergeAndApply(ctx, c, opts, configFile, sidePatches, render, apply)
		})
		if err != nil {
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
func cosiPreflightContext(ctx context.Context) (context.Context, string, error) {
	//nolint:varnamelen // 'md' is the canonical short name for grpc metadata.MD across the codebase.
	md, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		// Maintenance / insecure path: openClientPerNodeMaintenance
		// pins the current node in GlobalArgs.Nodes but does not
		// attach outgoing-context metadata (the maintenance client
		// reads node directly from GlobalArgs). Fall back to the
		// single-element GlobalArgs.Nodes so per-node-prefixed
		// stderr lines (drift / divergence / maintenance warning)
		// still disambiguate.
		if len(GlobalArgs.Nodes) == 1 {
			return ctx, GlobalArgs.Nodes[0], nil
		}

		return ctx, "", nil
	}

	nodes := md.Get("nodes")
	switch len(nodes) {
	case 0:
		return ctx, "", nil
	case 1:
		return client.WithNode(ctx, nodes[0]), nodes[0], nil
	default:
		//nolint:wrapcheck // sentinel constructed in-place; WithHint attaches operator guidance
		return nil, "", errors.WithHint(
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
// sidePatches is the chain of additional -f files stacked on top of
// the rooted configFile (#184). Each is merged in order via
// engine.MergeFileAsPatch; the result is a single composed config
// applied with one ApplyConfiguration call per node. Empty slice
// (single -f file) reproduces the pre-#184 single-merge shape.
//
//nolint:gocritic // opts taken by value to mirror applyTemplatesPerNode's test-injection signature
func renderMergeAndApply(ctx context.Context, c *client.Client, opts engine.Options, configFile string, sidePatches []string, render renderFunc, apply applyFunc) error {
	rendered, err := render(ctx, c, opts)
	if err != nil {
		//nolint:wrapcheck // already wrapped via errors.Wrap, WithHint adds operator-facing guidance
		return errors.WithHint(
			errors.Wrap(err, "template rendering"),
			"the chart did not render against the current node's discovery state; verify the templates referenced in the modeline exist and the node is reachable",
		)
	}

	merged, err := engine.MergeFileAsPatch(rendered, configFile)
	if err != nil {
		return errors.Wrapf(err, "merging node file %q as patch", configFile)
	}

	for _, sidePatch := range sidePatches {
		merged, err = engine.MergeFileAsPatch(merged, sidePatch)
		if err != nil {
			return errors.Wrapf(err, "merging side-patch %q onto rendered config", sidePatch)
		}
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
		CommandName:       applyCommandName,
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
func wrapWithNodeContext(action func(ctx context.Context, c *client.Client) error) func(ctx context.Context, c *client.Client) error {
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

		return action(ctx, c)
	}
}

// isOutsideRoot reports whether a cleaned relative path escapes the
// project root. A HasPrefix(parentDir) test would misclassify sibling
// directories whose first path element merely starts with "..", such
// as "..templates/controlplane.yaml"; we match a full path element
// instead.
func isOutsideRoot(relPath string) bool {
	return relPath == parentDir || strings.HasPrefix(relPath, parentDir+string(filepath.Separator))
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
	applyCmd.Flags().StringSliceVarP(&applyCmdFlags.configFiles, "file", "f", nil, "node config files / patches (`.yaml` / `.yml`; shell completion narrows to these extensions). First -f is the modelined anchor (must live under a `talm init`'d project root); subsequent -f files are side-patches stacked onto the anchor's rendered config and may live anywhere (#184).")
	applyCmd.Flags().StringVar(&applyCmdFlags.talosVersion, "talos-version", "", "the desired Talos version to generate config for (backwards compatibility, e.g. v0.8)")
	applyCmd.Flags().StringVar(&applyCmdFlags.withSecrets, "with-secrets", "", "use a secrets file generated using 'gen secrets'")
	applyCmd.Flags().StringVar(&applyCmdFlags.kubernetesVersion, "kubernetes-version", constants.DefaultKubernetesVersion, "desired kubernetes version to run")
	applyCmd.Flags().BoolVarP(&applyCmdFlags.debug, "debug", "", false, "show only rendered patches")
	applyCmd.Flags().BoolVar(&applyCmdFlags.dryRun, "dry-run", false, "check how the config change will be applied in dry-run mode")
	applyCmd.Flags().DurationVar(&applyCmdFlags.configTryTimeout, "timeout", constants.ConfigTryTimeout, "the config will be rolled back after specified timeout (if try mode is selected)")
	applyCmd.Flags().StringSliceVar(&applyCmdFlags.certFingerprints, "cert-fingerprint", nil, "list of server certificate fingeprints to accept (defaults to no check)")
	applyCmd.Flags().BoolVar(&applyCmdFlags.force, "force", false, "will overwrite existing files")
	applyCmd.Flags().BoolVar(&applyCmdFlags.skipResourceValidation, "skip-resource-validation", false, "skip the pre-apply check that declared host resources (links, disks) exist on the target node")
	applyCmd.Flags().BoolVar(&applyCmdFlags.skipDriftPreview, "skip-drift-preview", false, "skip the pre-apply diff of on-node vs rendered MachineConfig")
	applyCmd.Flags().BoolVar(&applyCmdFlags.skipPostApplyVerify, "skip-post-apply-verify", true, "skip the post-apply structural verification of on-node vs sent MachineConfig (default skip until the Talos-mutated field allowlist lands; see #172)")
	applyCmd.Flags().BoolVar(&applyCmdFlags.showSecretsInDrift, "show-secrets-in-drift", false, "show secret-bearing field values verbatim in drift preview / post-apply verify output (default: redacted; cluster.token, cluster.ca.key, machine.token, Wireguard private keys, etc.)")
	helpers.AddModeFlags(&applyCmdFlags.Mode, applyCmd)

	addCommand(applyCmd)
}
