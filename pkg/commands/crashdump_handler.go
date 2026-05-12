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

import "github.com/spf13/cobra"

// wrapCrashdumpCommand pre-populates GlobalArgs.Nodes from upstream
// crashdump's per-class node flags (--init-node, --control-plane-nodes,
// --worker-nodes) so the upstream WithClient guard in
// `cmd/talosctl/pkg/talos/global/client.go` (which checks
// `len(GlobalArgs.Nodes) > 0`) is satisfied before crashdump's own
// RunE runs.
//
// Without this pre-population an operator who follows talosctl
// crashdump's documented help text — passing only --control-plane-nodes
// and --endpoints — hits a "nodes are not set for the command" error
// from the global client wrapper instead of crashdump's own
// "deprecated, please use `talosctl support` instead" message
// (crashdump is hidden + deprecated upstream, but the wrapper still
// imports it through `taloscommands.Commands`).
//
// Wrapper populates GlobalArgs.Nodes from the per-class flags
// BEFORE chaining to the original PreRunE. wrapTalosCommand's
// PreRunE syncs `taloscommands.GlobalArgs = commands.GlobalArgs`
// near its end — populating after the chain would update only
// talm's side and leave upstream still seeing an empty list.
//
// Resulting precedence:
//
//   - Explicit `--nodes` on the CLI wins (it lands in
//     GlobalArgs.Nodes during cobra's flag parse, before any
//     PreRunE runs; the populate gate sees it non-empty and
//     skips).
//   - Per-class flags win over modeline. The chain order
//     forced by the upstream-sync constraint runs the populate
//     before the wrapTalosCommand PreRunE's modeline merge,
//     and that merge captures `nodesFromArgs := len(GlobalArgs.
//     Nodes) > 0` from the now-populated talm side, telling it
//     NOT to overwrite. This is a deliberate trade-off: the
//     upstream guard surface-fires unless GlobalArgs.Nodes is
//     populated before sync, and the per-class flags carry
//     operator intent more specifically than the modeline
//     default for a deprecated command.
//   - Modeline wins only when GlobalArgs.Nodes is empty AND no
//     per-class flag was passed.
//
// Contract with the caller: wrapCrashdumpCommand MUST be installed
// AFTER wrapTalosCommand's PreRunE assignment in
// talosctl_wrapper.go, so its `originalPreRunE` capture points at
// the wrapTalosCommand closure rather than a nil. If a future
// refactor reorders the dispatch in talosctl_wrapper.go to call
// wrapCrashdumpCommand earlier, the chain order inverts and the
// upstream guard fires again.
func wrapCrashdumpCommand(wrappedCmd *cobra.Command) {
	originalPreRunE := wrappedCmd.PreRunE

	wrappedCmd.PreRunE = func(cmd *cobra.Command, args []string) error {
		populateNodesFromPerClassFlags(cmd)

		if originalPreRunE != nil {
			return originalPreRunE(cmd, args)
		}

		return nil
	}
}

// populateNodesFromPerClassFlags fills GlobalArgs.Nodes from the
// union of --init-node, --control-plane-nodes, and --worker-nodes
// when GlobalArgs.Nodes is otherwise empty. Used by the wrappers
// for upstream commands that consume these per-class lists rather
// than the global --nodes (crashdump, rotate-ca).
//
// MUST be called BEFORE wrapTalosCommand's PreRunE assignment in
// the chain — that closure syncs
// `taloscommands.GlobalArgs = commands.GlobalArgs` near its end,
// and any population after the sync only updates talm's side,
// leaving the upstream WithClient guard still seeing an empty
// list. Per-command wrappers (wrapCrashdumpCommand,
// wrapRotateCACommand) capture the original PreRunE first, then
// install a closure that calls this helper BEFORE chaining.
//
// Walk order is stable (init -> control-plane -> worker) so the
// resolved list is deterministic when multiple classes are set.
// Explicit --nodes (CLI or modeline) takes precedence: the gate
// is len(GlobalArgs.Nodes) == 0.
//
// Per-class flag lookups are tolerant: missing flag definitions
// return errors that are silently swallowed, so the helper is
// safe to call on any command that may or may not have all three
// registered.
func populateNodesFromPerClassFlags(cmd *cobra.Command) {
	if len(GlobalArgs.Nodes) > 0 {
		return
	}

	populated := []string{}

	if initNode, err := cmd.Flags().GetString("init-node"); err == nil && initNode != "" {
		populated = append(populated, initNode)
	}

	if cps, err := cmd.Flags().GetStringSlice("control-plane-nodes"); err == nil {
		populated = append(populated, cps...)
	}

	if workers, err := cmd.Flags().GetStringSlice("worker-nodes"); err == nil {
		populated = append(populated, workers...)
	}

	if len(populated) > 0 {
		GlobalArgs.Nodes = populated
	}
}
