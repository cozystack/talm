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
	"strings"
	"testing"

	taloscommands "github.com/siderolabs/talos/cmd/talosctl/cmd/talos"
	"github.com/spf13/cobra"
)

// registerResetFlagsForTest stands up the two reset flags talm cares
// about in a shape that matches upstream's registrations in
// `cmd/talosctl/cmd/talos/reset.go`:
//
//   - `--wipe-mode` accepts a free-form string here (upstream uses a
//     custom pflag.Value with an enum, but the wrapper only consults
//     Changed() and the string value, so a plain StringVar with the
//     upstream default `"all"` is enough to exercise the flip gate).
//   - `--system-labels-to-wipe` is a StringSlice (repeatable + comma-
//     split) to match upstream's `StringSliceVar`.
func registerResetFlagsForTest(cmd *cobra.Command, wipeModeStore *string, labelsStore *[]string) {
	cmd.Flags().StringVar(wipeModeStore, "wipe-mode", "all", "disk reset mode")
	cmd.Flags().StringSliceVar(labelsStore, "system-labels-to-wipe", nil, "system disk partitions to wipe by label")
}

// TestWrapResetCommand_NoFlags_AppliesSafeDefault pins the safe
// default for `talm reset`: when an operator runs the command
// without choosing a wipe scope, the wrapper's PreRunE
// pre-populates `--system-labels-to-wipe` with STATE,EPHEMERAL so
// the META partition is preserved and the node self-recovers on
// reboot.
//
// `--wipe-mode` is intentionally left unchanged (still upstream's
// default). The server-side reset codepath, when SystemPartitionsToWipe
// is non-empty, takes the label-driven path regardless of Mode — see
// the upstream `--system-labels-to-wipe` flag doc in
// `cmd/talosctl/cmd/talos/reset.go`: "if set, just wipe selected
// system disk partitions by label but keep other partitions intact".
// The issue's manual reproduction confirms this empirically.
func TestWrapResetCommand_NoFlags_AppliesSafeDefault(t *testing.T) {
	cmd := &cobra.Command{Use: resetCmdName}

	var (
		wipeMode           string
		systemLabelsToWipe []string
	)

	registerResetFlagsForTest(cmd, &wipeMode, &systemLabelsToWipe)

	wrapResetCommand(cmd)

	if err := cmd.PreRunE(cmd, nil); err != nil {
		t.Fatalf("PreRunE returned: %v", err)
	}

	got, err := cmd.Flags().GetStringSlice("system-labels-to-wipe")
	if err != nil {
		t.Fatalf("get --system-labels-to-wipe: %v", err)
	}

	want := strings.Split(resetSafeDefaultLabels, ",")
	if len(got) != len(want) {
		t.Fatalf("safe default must populate --system-labels-to-wipe=%s; got %v", resetSafeDefaultLabels, got)
	}

	for i := range want {
		if got[i] != want[i] {
			t.Errorf("safe default labels[%d]: got %q, want %q", i, got[i], want[i])
		}
	}

	if cmd.Flags().Changed("wipe-mode") {
		t.Errorf("safe default must not touch --wipe-mode (operator opt-in via --wipe-mode=all stays the only destructive path); got Changed()=true")
	}
}

// TestWrapResetCommand_ExplicitWipeModeAll_SkipsDefault pins the
// opt-out contract: an operator who explicitly passes
// `--wipe-mode=all` is asking for upstream's destructive behaviour
// (wipe META along with everything else). The wrapper MUST NOT
// silently add `--system-labels-to-wipe=STATE,EPHEMERAL` in that
// case — doing so would override the operator's stated intent and
// quietly turn a destructive reset into a selective one.
func TestWrapResetCommand_ExplicitWipeModeAll_SkipsDefault(t *testing.T) {
	cmd := &cobra.Command{Use: resetCmdName}

	var (
		wipeMode           string
		systemLabelsToWipe []string
	)

	registerResetFlagsForTest(cmd, &wipeMode, &systemLabelsToWipe)

	wrapResetCommand(cmd)

	if err := cmd.Flags().Set("wipe-mode", "all"); err != nil {
		t.Fatalf("set --wipe-mode=all: %v", err)
	}

	if err := cmd.PreRunE(cmd, nil); err != nil {
		t.Fatalf("PreRunE returned: %v", err)
	}

	got, err := cmd.Flags().GetStringSlice("system-labels-to-wipe")
	if err != nil {
		t.Fatalf("get --system-labels-to-wipe: %v", err)
	}

	if len(got) != 0 {
		t.Errorf("explicit --wipe-mode=all must skip the safety override (operator opted into destructive); got --system-labels-to-wipe=%v", got)
	}
}

// TestWrapResetCommand_ExplicitLabels_PreservesOperatorChoice pins
// the operator-list-honored contract: when the operator already
// passed `--system-labels-to-wipe=...`, the wrapper MUST NOT silently
// expand the list (e.g. by appending EPHEMERAL). An operator who
// passes a narrower list than the safe default is doing so
// deliberately and the wrapper must respect it byte-for-byte.
func TestWrapResetCommand_ExplicitLabels_PreservesOperatorChoice(t *testing.T) {
	cmd := &cobra.Command{Use: resetCmdName}

	var (
		wipeMode           string
		systemLabelsToWipe []string
	)

	registerResetFlagsForTest(cmd, &wipeMode, &systemLabelsToWipe)

	wrapResetCommand(cmd)

	if err := cmd.Flags().Set("system-labels-to-wipe", "STATE"); err != nil {
		t.Fatalf("set --system-labels-to-wipe=STATE: %v", err)
	}

	if err := cmd.PreRunE(cmd, nil); err != nil {
		t.Fatalf("PreRunE returned: %v", err)
	}

	got, err := cmd.Flags().GetStringSlice("system-labels-to-wipe")
	if err != nil {
		t.Fatalf("get --system-labels-to-wipe: %v", err)
	}

	if len(got) != 1 || got[0] != "STATE" {
		t.Errorf("operator's explicit narrower list must be honored byte-for-byte; got %v, want [STATE]", got)
	}
}

// TestWrapResetCommand_ExplicitWipeModeSystemDisk_SkipsDefault pins
// the value-agnostic gate: the wrapper checks `Changed("wipe-mode")`
// only, not the value. Any --wipe-mode=... selection counts as
// "operator stated wipe-scope intent" and disables the safety
// override. Verified for --wipe-mode=system-disk (the other
// destructive lane besides --wipe-mode=all); --wipe-mode=user-disks
// would behave the same way at the gate level but doesn't touch
// system partitions server-side, so it's safe either way.
func TestWrapResetCommand_ExplicitWipeModeSystemDisk_SkipsDefault(t *testing.T) {
	cmd := &cobra.Command{Use: resetCmdName}

	var (
		wipeMode           string
		systemLabelsToWipe []string
	)

	registerResetFlagsForTest(cmd, &wipeMode, &systemLabelsToWipe)

	wrapResetCommand(cmd)

	if err := cmd.Flags().Set("wipe-mode", "system-disk"); err != nil {
		t.Fatalf("set --wipe-mode=system-disk: %v", err)
	}

	if err := cmd.PreRunE(cmd, nil); err != nil {
		t.Fatalf("PreRunE returned: %v", err)
	}

	got, err := cmd.Flags().GetStringSlice("system-labels-to-wipe")
	if err != nil {
		t.Fatalf("get --system-labels-to-wipe: %v", err)
	}

	if len(got) != 0 {
		t.Errorf("explicit --wipe-mode=system-disk must skip the safety override (operator stated wipe-scope intent); got --system-labels-to-wipe=%v", got)
	}
}

// TestWrapResetCommand_BothFlagsChanged_SkipsDefault pins the
// no-interference contract for the mixed case: when the operator
// passed BOTH `--wipe-mode=...` and `--system-labels-to-wipe=...`,
// the wrapper MUST NOT touch either flag. The operator has stated
// intent on both axes; the wrapper passes the request through to
// upstream as-is.
func TestWrapResetCommand_BothFlagsChanged_SkipsDefault(t *testing.T) {
	cmd := &cobra.Command{Use: resetCmdName}

	var (
		wipeMode           string
		systemLabelsToWipe []string
	)

	registerResetFlagsForTest(cmd, &wipeMode, &systemLabelsToWipe)

	wrapResetCommand(cmd)

	if err := cmd.Flags().Set("wipe-mode", "system-disk"); err != nil {
		t.Fatalf("set --wipe-mode=system-disk: %v", err)
	}

	if err := cmd.Flags().Set("system-labels-to-wipe", "STATE"); err != nil {
		t.Fatalf("set --system-labels-to-wipe=STATE: %v", err)
	}

	if err := cmd.PreRunE(cmd, nil); err != nil {
		t.Fatalf("PreRunE returned: %v", err)
	}

	got, err := cmd.Flags().GetStringSlice("system-labels-to-wipe")
	if err != nil {
		t.Fatalf("get --system-labels-to-wipe: %v", err)
	}

	if len(got) != 1 || got[0] != "STATE" {
		t.Errorf("both flags changed: wrapper must not interfere; got --system-labels-to-wipe=%v, want [STATE]", got)
	}
}

// TestWrapResetCommand_HelpTextMentionsMetaPreservation pins the
// operator-facing help-text contract against drift. The default
// flip is invisible without a clear `talm reset --help` story:
// operators relying on the help text to learn what changed will
// otherwise be surprised when `talm reset` (no flags) suddenly
// preserves META instead of wiping it.
//
// Both flags get a wrapper-side Usage override:
//   - `--wipe-mode`: mentions the talm safe default + the explicit
//     opt-out (`--wipe-mode=all`) for the destructive case.
//   - `--system-labels-to-wipe`: spells out the safe default labels
//     so an operator inspecting either flag's help finds the same
//     story.
//
// The substrings asserted here are narrow ("preserves META" and the
// `resetSafeDefaultLabels` literal) — enough to pin intent without
// over-fitting to exact prose.
func TestWrapResetCommand_HelpTextMentionsMetaPreservation(t *testing.T) {
	cmd := &cobra.Command{Use: resetCmdName}

	var (
		wipeMode           string
		systemLabelsToWipe []string
	)

	registerResetFlagsForTest(cmd, &wipeMode, &systemLabelsToWipe)

	wrapResetCommand(cmd)

	wipeFlag := cmd.Flag("wipe-mode")
	if wipeFlag == nil {
		t.Fatal("--wipe-mode flag missing after wrap")
	}

	if !strings.Contains(wipeFlag.Usage, "preserves META") {
		t.Errorf("--wipe-mode help must mention that the talm default preserves META; got: %q", wipeFlag.Usage)
	}

	// Both --wipe-mode=all and --wipe-mode=system-disk land in the
	// destructive server-side branch when --system-labels-to-wipe is
	// empty. Operators picking system-disk to "be less destructive"
	// are walking into the same trap — pin both names in the help
	// text so the surface they read describes both opt-out lanes.
	if !strings.Contains(wipeFlag.Usage, "system-disk") {
		t.Errorf("--wipe-mode help must mention --wipe-mode=system-disk as an opt-out lane (also destroys META); got: %q", wipeFlag.Usage)
	}

	labelsFlag := cmd.Flag("system-labels-to-wipe")
	if labelsFlag == nil {
		t.Fatal("--system-labels-to-wipe flag missing after wrap")
	}

	if !strings.Contains(labelsFlag.Usage, resetSafeDefaultLabels) {
		t.Errorf("--system-labels-to-wipe help must mention the safe default labels (%s); got: %q", resetSafeDefaultLabels, labelsFlag.Usage)
	}
}

// TestWrapResetCommand_RealUpstreamResetDispatched is the end-to-end
// pin that talm's dispatch in wrapTalosCommand actually routes the
// real upstream `reset` command through wrapResetCommand. Without
// this test a refactor of the dispatch chain could silently drop
// the wrapper and the unit tests above (synthetic-cobra) would all
// keep passing while production stops applying the safe default.
//
// Pattern mirrors TestWrapCrashdumpCommand-style real-upstream tests:
// pull the real cobra.Command out of taloscommands.Commands, run it
// through talm's wrapTalosCommand, then invoke the wrapped PreRunE
// and assert the META-preserving default landed on the slice.
func TestWrapResetCommand_RealUpstreamResetDispatched(t *testing.T) {
	var resetCmd *cobra.Command

	for _, cmd := range taloscommands.Commands {
		if cmd.Name() == resetCmdName {
			resetCmd = cmd

			break
		}
	}

	if resetCmd == nil {
		t.Skipf("upstream taloscommands.Commands has no %q command — schema changed", resetCmdName)
	}

	wrapped := wrapTalosCommand(resetCmd, resetCmdName)

	if err := wrapped.PreRunE(wrapped, nil); err != nil {
		t.Fatalf("wrapped reset PreRunE returned: %v", err)
	}

	got, err := wrapped.Flags().GetStringSlice("system-labels-to-wipe")
	if err != nil {
		t.Fatalf("get --system-labels-to-wipe on wrapped reset: %v", err)
	}

	want := strings.Split(resetSafeDefaultLabels, ",")
	if len(got) != len(want) {
		t.Fatalf("dispatch must route real upstream reset through wrapResetCommand (expected --system-labels-to-wipe=%s after PreRunE); got %v", resetSafeDefaultLabels, got)
	}

	for i := range want {
		if got[i] != want[i] {
			t.Errorf("dispatched safe default labels[%d]: got %q, want %q", i, got[i], want[i])
		}
	}
}
