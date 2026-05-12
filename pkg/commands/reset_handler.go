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
	"github.com/cockroachdb/errors"
	"github.com/spf13/cobra"
)

const (
	resetCmdName = "reset"

	// resetSafeDefaultLabels are the system partition labels talm's
	// wrapper populates into `--system-labels-to-wipe` when an
	// operator runs `talm reset` without explicitly choosing a wipe
	// scope. Wiping STATE clears node-specific persistent state
	// (machine config, identity); wiping EPHEMERAL clears the
	// container/runtime layer. Leaving META untouched is the key
	// property: META carries the bootstrap config Talos uses to
	// rejoin the cluster on the next boot, so a reset with only
	// these two labels self-recovers without operator intervention.
	resetSafeDefaultLabels = "STATE,EPHEMERAL"
)

// wrapResetCommand flips talm's `talm reset` default away from
// upstream's destructive `--wipe-mode=all` toward the META-preserving
// selective-wipe recipe. The flip only fires when the operator passed
// neither `--wipe-mode` nor `--system-labels-to-wipe` on the CLI:
//
//   - No wipe flags: PreRunE pre-populates
//     `--system-labels-to-wipe=STATE,EPHEMERAL`. The server-side
//     reset codepath, when SystemPartitionsToWipe is non-empty,
//     takes the label-driven path and "keep[s] other partitions
//     intact" per upstream's `--system-labels-to-wipe` flag doc in
//     `cmd/talosctl/cmd/talos/reset.go`. META survives; on the next
//     boot Talos rejoins the cluster from META without operator
//     intervention.
//   - Operator passed `--wipe-mode=...`: the safety override is
//     skipped. `--wipe-mode=all` remains the explicit destructive
//     opt-in; `--wipe-mode=system-disk` / `--wipe-mode=user-disks`
//     also bypass the flip on the assumption that the operator
//     stated wipe-scope intent.
//   - Operator passed `--system-labels-to-wipe=...`: the operator's
//     list is honored byte-for-byte. The wrapper does not silently
//     expand a narrower selection (e.g. STATE alone) to the safe
//     default — operators choosing a narrower scope are doing so
//     deliberately.
//
// Help-text overrides on both flags spell out the divergence so
// `talm reset --help` carries the operator-facing story.
//
// Chain order: capture the wrapTalosCommand-installed PreRunE first,
// run the flip BEFORE chaining. Order is not load-bearing here
// (modeline does not touch wipe flags), but matching the shape of
// the crashdump / rotate-ca wrappers keeps the dispatch site
// readable.
func wrapResetCommand(wrappedCmd *cobra.Command) {
	if wipeFlag := wrappedCmd.Flag("wipe-mode"); wipeFlag != nil {
		wipeFlag.Usage = "disk reset mode (talm default: --system-labels-to-wipe=" + resetSafeDefaultLabels +
			" preserves META so the node self-recovers; pass --wipe-mode=all or --wipe-mode=system-disk explicitly for upstream's destructive behaviour — both destroy META)"
	}

	if labelsFlag := wrappedCmd.Flag("system-labels-to-wipe"); labelsFlag != nil {
		labelsFlag.Usage = "wipe selected system disk partitions by label, keeping others intact (talm default when no wipe flag is set: " +
			resetSafeDefaultLabels + ")"
	}

	originalPreRunE := wrappedCmd.PreRunE

	wrappedCmd.PreRunE = func(cmd *cobra.Command, args []string) error {
		if !cmd.Flags().Changed("wipe-mode") && !cmd.Flags().Changed("system-labels-to-wipe") {
			if err := cmd.Flags().Set("system-labels-to-wipe", resetSafeDefaultLabels); err != nil {
				return errors.WithHint(
					errors.Wrap(err, "applying talm safe-default wipe labels"),
					"this should not happen at runtime; if it does, fall back to passing --system-labels-to-wipe=STATE,EPHEMERAL explicitly",
				)
			}
		}

		if originalPreRunE != nil {
			return originalPreRunE(cmd, args)
		}

		return nil
	}
}
