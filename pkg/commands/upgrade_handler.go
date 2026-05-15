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
	"time"

	"github.com/cockroachdb/errors"
	"github.com/siderolabs/talos/pkg/machinery/client"
	"github.com/spf13/cobra"
)

const (
	// upgradeCmdName is the upstream cobra command name for the
	// upgrade subcommand. Used by both the dispatch site and the
	// per-command wrapper tests.
	upgradeCmdName = "upgrade"

	// defaultPostUpgradeReconcileWindow is how long we wait after
	// talosctl upgrade returns before re-reading the running
	// version. Talos reboots and reaches "running" stage in well
	// under a minute on healthy hardware; auto-rollback adds ~30s
	// on top of that. 90s covers both paths with margin. Operators
	// with slow hardware widen via --post-upgrade-reconcile-window.
	defaultPostUpgradeReconcileWindow = 90 * time.Second
)

// upgradeCmdFlags carries the talm-side flags layered on top of the
// talosctl-derived upgrade command (set up in wrapUpgradeCommand).
//
//nolint:gochecknoglobals // command-scoped flag struct, mirrors applyCmdFlags pattern.
var upgradeCmdFlags struct {
	skipPostUpgradeVerify      bool
	postUpgradeReconcileWindow time.Duration
}

// validatePostUpgradeReconcileWindow rejects non-positive durations.
// A zero or negative window would have the version-read loop run
// while the node is still rebooting and surface a false "rollback"
// verdict every time — far worse failure mode than a small range
// check up front.
//
// Hint mentions "positive duration" verbatim so the boundary test
// can pin the contract against future copy drift.
func validatePostUpgradeReconcileWindow(window time.Duration) error {
	if window <= 0 {
		//nolint:wrapcheck // cockroachdb/errors.WithHint at boundary.
		return errors.WithHint(
			errors.Newf("--post-upgrade-reconcile-window must be a positive duration; got %s", window),
			"pass a positive duration like 90s or 2m — the default is 90s",
		)
	}

	return nil
}

// wrapUpgradeCommand adds special handling for upgrade command: extract image from config and set --image flag
//
//nolint:gocognit,gocyclo,cyclop,funlen // cobra wrapper branching over (image extraction, file paths, modeline) for the upgrade flow; each branch is short.
func wrapUpgradeCommand(wrappedCmd *cobra.Command, originalRunE func(*cobra.Command, []string) error) {
	// Extend the upstream Long with talm-specific behaviour so
	// `talm upgrade --help` describes the actual image-resolution
	// chain (values.yaml, --image override) instead of just the
	// generic upstream upgrade flow.
	wrappedCmd.Long = `Upgrade Talos on the target node(s).

Image resolution (when -f is provided):
  - --image <ref>         takes precedence and is used as-is.
  - otherwise, talm reads ` + "`image:`" + ` from values.yaml at the
    project root and passes it as the upgrade target. Bumping
    values.yaml::image is the canonical "raise the cluster's
    Talos version" workflow — re-running ` + "`talm template`" + ` to
    refresh node files first is NOT required.

The first -f file anchors the project root (Chart.yaml +
secrets.yaml); its modeline supplies the nodes / endpoints. The
node body's machine.install.image is no longer consulted by the
upgrade flow.

Post-upgrade sync (when the upgrade succeeds):
  - talm point-patches machine.install.image in every -f node body
    to the image that was applied. Keeps the body consistent with
    the running node — without this a follow-up ` + "`talm apply`" + ` would
    merge the stale install.image over the chart-rendered new value
    and silently pin the cluster back to the pre-upgrade image on
    the next A/B boot. Comments, modeline, and unrelated keys are
    preserved (yaml.v3 node round-trip); files without an
    install.image key (orphans / side-patches) are silently skipped.
  - The patch fires after post-upgrade verify confirms the running
    version matches the target. A failed verify (auto-rollback)
    intentionally leaves the body untouched, so it still reflects
    what the node actually runs.`

	wrappedCmd.Flags().BoolVar(&upgradeCmdFlags.skipPostUpgradeVerify, "skip-post-upgrade-verify", false,
		"skip the post-upgrade check that compares running Talos version against the target image's tag (detects silent A/B rollback after the RPC acks success)")

	wrappedCmd.Flags().DurationVar(&upgradeCmdFlags.postUpgradeReconcileWindow, "post-upgrade-reconcile-window", defaultPostUpgradeReconcileWindow,
		"how long to wait after upgrade returns before re-reading the running version; widen for slow hardware / large image pulls")

	// Shell completion for `talm upgrade --file`: returns modelined
	// yaml files under <root>/nodes/. ValidArgsFunction is NOT
	// wired because upstream's upgrade command declares no
	// positional args; cobra's __complete path suppresses
	// ValidArgsFunction when the arg-constraint is NoArgs.
	_ = wrappedCmd.RegisterFlagCompletionFunc("file", completeNodeFiles)

	wrappedCmd.RunE = func(cmd *cobra.Command, args []string) error {
		// Fail-fast on a bad --post-upgrade-reconcile-window BEFORE
		// any talosctl upgrade RPC fires. A zero / negative value
		// reaching the Phase 2C wait would fall through to the
		// version-read loop while the node is still rebooting and
		// always report 'rollback'. Worse — the upgrade itself has
		// already executed by then; the operator's mistake gets
		// validated after the partial state change. Validate first.
		if err := validatePostUpgradeReconcileWindow(upgradeCmdFlags.postUpgradeReconcileWindow); err != nil {
			return err
		}

		// Get config files from --file flag
		var filesToProcess []string

		if fileFlag := cmd.Flags().Lookup("file"); fileFlag != nil {
			if fileFlagValue, err := cmd.Flags().GetStringSlice("file"); err == nil {
				filesToProcess = fileFlagValue
			}
		}

		// Expand directories to YAML files
		expandedFiles, err := ExpandFilePaths(filesToProcess)
		if err != nil {
			return err
		}

		filesToProcess = expandedFiles

		// Detect root from files if specified, otherwise fallback to cwd
		if err := DetectAndSetRootFromFiles(filesToProcess); err != nil {
			return err
		}

		// If config files are provided and --image flag is not set,
		// resolve the upgrade target image from values.yaml.
		// values.yaml is the source of truth for cluster-wide knobs;
		// the upgrade target reads from there directly rather than
		// re-extracting from a rendered node body (which would pin
		// the image to whatever was templated last, ignoring later
		// values.yaml bumps). Per-node image override remains
		// possible by passing --image explicitly.
		if len(filesToProcess) > 0 && !cmd.Flags().Changed("image") {
			// Process modeline so GlobalArgs.Nodes / .Endpoints are
			// populated for the downstream talosctl invocation; we
			// no longer use the modeline templates list, but the
			// nodes/endpoints carry on.
			configFile := filesToProcess[0]

			nodesFromArgs := len(GlobalArgs.Nodes) > 0

			endpointsFromArgs := len(GlobalArgs.Endpoints) > 0
			if _, err := processModelineAndUpdateGlobals(configFile, nodesFromArgs, endpointsFromArgs, true); err != nil {
				return errors.Wrap(err, "failed to process modeline")
			}

			image, err := resolveUpgradeImageFromValues(Config.RootDir)
			if err != nil {
				return err
			}

			if err := cmd.Flags().Set("image", image); err != nil {
				// Flag might not exist (extremely unlikely given
				// upgradeCmd registers it); fall through with a
				// warning rather than aborting.
				fmt.Fprintf(os.Stderr, "Warning: failed to set --image flag: %v\n", err)
			} else {
				fmt.Fprintf(os.Stderr, "Using image from values.yaml: %s\n", image)
			}
		}

		// Capture the upgrade target image + path-shaping flags
		// BEFORE original RunE runs. talosctl's own upgrade handler
		// can overwrite the --image flag with the node's
		// currently-running install.image (the no-op-upgrade path),
		// which would mask the version mismatch Phase 2C exists to
		// catch. --insecure and --stage are captured here too so
		// the post-upgrade gate's mode predicate sees what the
		// operator actually asked for, not whatever state talosctl
		// left in the flags afterwards.
		targetImage, _ := cmd.Flags().GetString("image")
		insecure, _ := cmd.Flags().GetBool("insecure")
		staged, _ := cmd.Flags().GetBool("stage")

		// Execute original command
		var execErr error

		switch {
		case originalRunE != nil:
			execErr = originalRunE(cmd, args)
		case wrappedCmd.Run != nil:
			wrappedCmd.Run(cmd, args)
		}

		if execErr != nil {
			return execErr
		}

		// Phase 2C: post-upgrade version verify. Detects the silent
		// auto-rollback case: talosctl upgrade acks the RPC, Talos
		// pulls + writes the new install, A/B boot fails its
		// readiness check, Talos rolls back to the prior partition,
		// and the operator's "successful" upgrade silently no-ops.
		// Skip predicate documents the cases where this gate cannot
		// produce a meaningful result.
		if !shouldRunPostUpgradeVerify(insecure, staged, upgradeCmdFlags.skipPostUpgradeVerify) {
			// Verify skipped (operator opt-out / insecure / staged).
			// Still sync node bodies: the RPC was acked, and skipping
			// the verify is an explicit operator choice — the body
			// must track what talosctl was asked to install so the
			// next `talm apply` does not silently revert install.image
			// over the chart-rendered new value.
			return writeBackInstallImageToFiles(filesToProcess, targetImage)
		}

		if targetImage == "" {
			fmt.Fprintln(os.Stderr, "post-upgrade verify: skipped, no target image to compare against")

			return nil
		}

		if err := runPostUpgradeVersionVerify(cmd.Context(), targetImage); err != nil {
			return err
		}

		// Verify did not block — patch the node body. The verify
		// helper returns nil on three shapes: (a) running version
		// matches the target (the common case), (b) zero nodes were
		// resolved (rare — the talosctl upgrade RPC above would have
		// also no-op'd, so the patch is vacuously consistent with
		// what hit the cluster), (c) the version reader surrendered
		// silently (reserved future contract). A failed verify
		// (auto-rollback detected) returns non-nil and was already
		// returned above, intentionally leaving the body untouched:
		// the body still points at the pre-upgrade image, which
		// matches what the node ended up running. An operator who
		// fixes the rollback cause and re-runs upgrade will sync the
		// body on the next pass that clears verify.
		return writeBackInstallImageToFiles(filesToProcess, targetImage)
	}
}

// shouldRunPostUpgradeVerify is the pure predicate for Phase 2C
// scheduling. The gate cannot produce a meaningful result when:
//
//   - --skip-post-upgrade-verify is set (operator opt-out).
//   - --insecure was passed to upgrade: the maintenance / pre-auth
//     connection cannot reach the auth-only COSI ctx WithClient
//     builds. Pre-fix, the gate fell through to WithClient and
//     either silently surrendered on "version unreadable" or
//     connected to an unrelated node from talosconfig context.
//     Mirrors cosiMachineConfigReader's insecure-path branch in
//     pkg/commands/preflight_apply_safety.go.
//   - --stage was passed to upgrade: talosctl --stage writes the
//     new image to the inactive partition without activating it;
//     activation happens on the next reboot. runtime.Version still
//     reports the OLD version because the new partition isn't
//     booted — a guaranteed false-positive blocker without this
//     skip. Mirrors shouldRunPostApplyVerify's STAGED case in
//     pkg/commands/apply.go.
func shouldRunPostUpgradeVerify(insecure, staged, skip bool) bool {
	if skip {
		return false
	}

	if insecure {
		return false
	}

	if staged {
		return false
	}

	return true
}

// runPostUpgradeVersionVerify waits a reconcile window, then for
// each --nodes target reads the running Talos version and compares
// against the version parsed from the target image tag. Surfaces
// the first divergence as a blocker.
//
// parentCtx is used only for the reconcile-window wait; the actual
// COSI reads run under the ctx WithClient constructs from
// talosconfig, which is the right scope for per-node addressing.
//
//nolint:contextcheck // intentional ctx boundary at WithClient.
func runPostUpgradeVersionVerify(parentCtx context.Context, image string) error {
	if parentCtx == nil {
		parentCtx = context.Background()
	}

	if err := validatePostUpgradeReconcileWindow(upgradeCmdFlags.postUpgradeReconcileWindow); err != nil {
		return err
	}

	return WithClient(func(ctx context.Context, c *client.Client) error {
		ctxNodes := []string(nil)
		if cfg := c.GetConfigContext(); cfg != nil {
			ctxNodes = cfg.Nodes
		}

		nodes := resolveUpgradeTargetNodes(GlobalArgs.Nodes, ctxNodes)

		return runPostUpgradeVersionVerifyInner(parentCtx, ctx, nodes, image, cosiVersionReader(c), upgradeCmdFlags.postUpgradeReconcileWindow, os.Stderr)
	})
}

// resolveUpgradeTargetNodes picks the per-node target list for the
// post-upgrade verify. CLI `--nodes` wins outright when non-empty;
// otherwise the talosconfig context's pre-configured node list is
// used. Returns a freshly-allocated slice so callers can append
// without mutating either source.
func resolveUpgradeTargetNodes(cliNodes, ctxNodes []string) []string {
	if len(cliNodes) > 0 {
		return append([]string(nil), cliNodes...)
	}

	return append([]string(nil), ctxNodes...)
}

// runPostUpgradeVersionVerifyInner is the testable body of Phase 2C.
// It resolves the "no work to do" case BEFORE the reconcile-window
// wait so an operator with empty `--nodes` and no talosconfig nodes
// doesn't sit through 90s of a "waiting for the node to finish
// booting..." line just to be told there was no target node.
//
// reconcileWindow and stderr are dependency-injected so the test can
// run with a sub-millisecond window and capture output without
// touching the package globals.
func runPostUpgradeVersionVerifyInner(
	parentCtx, clientCtx context.Context,
	nodes []string,
	image string,
	read versionReader,
	reconcileWindow time.Duration,
	stderr io.Writer,
) error {
	if len(nodes) == 0 {
		_, _ = fmt.Fprintln(stderr, "post-upgrade verify: skipped, no target nodes resolved from --nodes or talosconfig context")

		return nil
	}

	_, _ = fmt.Fprintf(stderr, "post-upgrade verify: waiting %s for the node to finish booting...\n", reconcileWindow)

	select {
	case <-time.After(reconcileWindow):
	case <-parentCtx.Done():
		return errors.Wrap(parentCtx.Err(), "post-upgrade verify: context cancelled while waiting for reconcile window")
	}

	// Collect per-node verify errors and join at the end rather than
	// short-circuiting on the first failure. Mirrors runPostApplyGates
	// (apply.go) — talosctl upgrade has already executed on every
	// node before this loop starts, so stopping at the first failure
	// hides partial rollbacks on the remaining nodes. The operator
	// sees one blocker now, fixes it, re-runs, and discovers the
	// second blocker — wasting an upgrade cycle. errors.Join keeps
	// every node's verdict in the final error.
	var perNodeErrs []error

	for _, node := range nodes {
		nodeCtx := client.WithNode(clientCtx, node)
		if err := verifyPostUpgradeVersion(nodeCtx, read, image, reconcileWindow, stderr); err != nil {
			perNodeErrs = append(perNodeErrs, errors.Wrapf(err, "node %s", node))
		}
	}

	return errors.Join(perNodeErrs...)
}
