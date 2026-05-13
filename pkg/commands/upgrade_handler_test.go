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
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/spf13/cobra"
)

// TestResolveUpgradeTargetNodes_CLINodesWin pins the resolution
// contract: --nodes overrides the talosconfig context's pre-
// configured node list when non-empty. A non-empty CLI list MUST
// shadow the context entirely (not merge), so an operator can
// scope a one-off upgrade without editing talosconfig.
func TestResolveUpgradeTargetNodes_CLINodesWin(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cli  []string
		ctx  []string
		want []string
	}{
		{"both empty", nil, nil, nil},
		{"cli wins outright", []string{"192.0.2.10"}, []string{"192.0.2.20"}, []string{"192.0.2.10"}},
		{"only cli set", []string{"192.0.2.10"}, nil, []string{"192.0.2.10"}},
		{"only ctx set falls through", nil, []string{"192.0.2.20"}, []string{"192.0.2.20"}},
		{"both empty slices (distinct from nil)", []string{}, []string{}, []string{}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := resolveUpgradeTargetNodes(tc.cli, tc.ctx)

			// Empty-vs-nil semantics: callers iterate via range,
			// which handles both identically. The test treats
			// (len == 0 && want len == 0) as a match regardless
			// of nil-ness — the contract is "no nodes", not "nil".
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}

			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("resolveUpgradeTargetNodes(%v, %v) = %v, want %v", tc.cli, tc.ctx, got, tc.want)
			}
		})
	}
}

// TestResolveUpgradeTargetNodes_DoesNotMutateInputs pins the
// no-aliasing contract: the resolver returns a freshly-allocated
// slice; callers can append to the result without poisoning
// GlobalArgs.Nodes or the talosconfig context's Nodes slice.
func TestResolveUpgradeTargetNodes_DoesNotMutateInputs(t *testing.T) {
	t.Parallel()

	cli := []string{"192.0.2.10"}
	ctx := []string{"192.0.2.20"}

	got := resolveUpgradeTargetNodes(cli, ctx)
	if len(got) > 0 {
		// Mutating the clone must NOT leak into either input slice.
		got[0] = "mutated-by-test"
	}

	if cli[0] != "192.0.2.10" || len(cli) != 1 {
		t.Errorf("cli mutated by resolver: %v", cli)
	}

	if ctx[0] != "192.0.2.20" || len(ctx) != 1 {
		t.Errorf("ctx mutated by resolver: %v", ctx)
	}
}

// TestRunPostUpgradeVersionVerifyInner_EmptyNodes_SkipsImmediately
// is the wall-clock regression pin: when there are no nodes to
// verify against, the early-return must fire BEFORE the reconcile-
// window wait. Previously the function logged "waiting 90s..." and
// only THEN discovered the empty list, wasting 90s of the operator's
// life. The assertion uses a 1-second budget against a 5-minute
// reconcile window — any sane implementation that places the wait
// after the empty-check sails through this; one that places it
// before fails by 4+ orders of magnitude.
func TestRunPostUpgradeVersionVerifyInner_EmptyNodes_SkipsImmediately(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}

	start := time.Now()
	err := runPostUpgradeVersionVerifyInner(
		context.Background(),
		context.Background(),
		nil,
		"ghcr.io/siderolabs/installer:v1.13.0",
		stubReader("v1.13.0", true),
		// Deliberately huge — if the implementation drifts back
		// to waiting before resolving nodes, this test stalls for
		// 5 minutes and the failure message names the regression.
		5*time.Minute,
		buf,
	)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("empty-nodes path should not error: got %v", err)
	}

	if elapsed > time.Second {
		t.Errorf("empty-nodes path stalled for %v — the reconcile-window wait must come AFTER the node-resolution check; if you see this, the wait was placed before the empty-check again", elapsed)
	}

	if !strings.Contains(buf.String(), "skipped, no target nodes resolved") {
		t.Errorf("empty-nodes path should emit the 'skipped, no target nodes' line, got %q", buf.String())
	}

	if strings.Contains(buf.String(), "waiting") {
		t.Errorf("empty-nodes path must NOT print the 'waiting ... for the node to finish booting' line, got %q", buf.String())
	}
}

// TestRunPostUpgradeVersionVerifyInner_MultiNode_AllErrorsReported
// pins the collect-then-block semantics for multi-node upgrades.
// talosctl upgrade has already executed on every node by the time
// this loop runs, so short-circuiting on the first failing node
// would hide partial rollbacks on the rest — operator fixes node 1,
// re-runs, discovers node 3 separately, wasting an upgrade cycle.
// The error must name every failing node so a single re-run cycle
// surfaces every problem.
func TestRunPostUpgradeVersionVerifyInner_MultiNode_AllErrorsReported(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}

	// Reader signals a rollback on every node — running version is
	// pinned at v1.12.6 while the upgrade asked for v1.13.0.
	read := func(_ context.Context) (string, bool, error) {
		return "v1.12.6", true, nil
	}

	err := runPostUpgradeVersionVerifyInner(
		context.Background(),
		context.Background(),
		[]string{"192.0.2.10", "192.0.2.11", "192.0.2.12"},
		"ghcr.io/siderolabs/installer:v1.13.0",
		read,
		time.Millisecond,
		buf,
	)
	if err == nil {
		t.Fatal("three rollbacked nodes must surface as an error, got nil")
	}

	msg := err.Error()
	for _, node := range []string{"192.0.2.10", "192.0.2.11", "192.0.2.12"} {
		if !strings.Contains(msg, node) {
			t.Errorf("joined error must cite node %s, got:\n%s", node, msg)
		}
	}
}

// TestRunPostUpgradeVersionVerifyInner_MultiNode_FirstFailureDoesNotBlockRest
// pins that one node's failure doesn't short-circuit the loop. The
// stub here returns alternating verdicts via a call counter — call
// 1 (node 192.0.2.10) rolls back, call 2 (node 192.0.2.11) matches.
// Without collect-then-block the loop would exit after node 1 and
// node 2's call wouldn't happen; with collect-then-block both are
// invoked and the joined error names the failing one.
func TestRunPostUpgradeVersionVerifyInner_MultiNode_FirstFailureDoesNotBlockRest(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}

	// Alternate per call: first call → mismatch, second call → match.
	calls := 0
	read := func(_ context.Context) (string, bool, error) {
		calls++

		if calls == 1 {
			return "v1.12.6", true, nil // version mismatch
		}

		return "v1.13.0", true, nil // matches target
	}

	err := runPostUpgradeVersionVerifyInner(
		context.Background(),
		context.Background(),
		[]string{"192.0.2.10", "192.0.2.11"},
		"ghcr.io/siderolabs/installer:v1.13.0",
		read,
		time.Millisecond,
		buf,
	)
	if err == nil {
		t.Fatal("first-node mismatch must surface as an error, got nil")
	}

	if calls != 2 {
		t.Errorf("expected both nodes' readers to be invoked (collect-then-block); got %d call(s)", calls)
	}

	if !strings.Contains(err.Error(), "192.0.2.10") {
		t.Errorf("error must cite the failing node 192.0.2.10, got: %v", err)
	}
}

// TestRunPostUpgradeVersionVerifyInner_NonEmptyNodes_WaitsAndVerifies
// is the companion check: with a non-empty node list the function
// DOES print the wait line and DOES invoke the reader. Uses a 1ms
// reconcile window so the test runs quickly while still going
// through the time.After branch.
func TestRunPostUpgradeVersionVerifyInner_NonEmptyNodes_WaitsAndVerifies(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}

	called := 0
	read := func(_ context.Context) (string, bool, error) {
		called++

		return "v1.13.0", true, nil
	}

	err := runPostUpgradeVersionVerifyInner(
		context.Background(),
		context.Background(),
		[]string{"192.0.2.10"},
		"ghcr.io/siderolabs/installer:v1.13.0",
		read,
		time.Millisecond,
		buf,
	)
	if err != nil {
		t.Fatalf("matching version path should not error: got %v", err)
	}

	if called != 1 {
		t.Errorf("expected reader invoked once per node, got %d", called)
	}

	if !strings.Contains(buf.String(), "waiting") {
		t.Errorf("non-empty path should print the 'waiting' line, got %q", buf.String())
	}
}

// TestDefaultPostUpgradeReconcileWindow_Is90s pins back-compat for
// #190: the upgrade flow defaulted to a hard-coded 90s wait before
// the flag was introduced; the new --post-upgrade-reconcile-window
// must register the same value as its default so operators who
// never pass the flag observe byte-identical timing.
func TestDefaultPostUpgradeReconcileWindow_Is90s(t *testing.T) {
	t.Parallel()

	if defaultPostUpgradeReconcileWindow != 90*time.Second {
		t.Errorf("default reconcile window changed: got %s, want 90s — back-compat regression (#190)", defaultPostUpgradeReconcileWindow)
	}
}

// TestUpgradeFlag_PostUpgradeReconcileWindow_Registered pins the
// flag registration: wrapUpgradeCommand must register a
// --post-upgrade-reconcile-window DurationVar with the 90s default,
// so `talm upgrade --help` discoverably surfaces the tunable.
func TestUpgradeFlag_PostUpgradeReconcileWindow_Registered(t *testing.T) {
	t.Parallel()

	cmd := &cobra.Command{Use: upgradeCmdName}
	wrapUpgradeCommand(cmd, nil)

	flag := cmd.Flag("post-upgrade-reconcile-window")
	if flag == nil {
		t.Fatal("--post-upgrade-reconcile-window must be registered by wrapUpgradeCommand")
	}

	if flag.DefValue != "1m30s" {
		t.Errorf("flag default rendered as %q, want %q (DurationVar formats 90s as 1m30s)", flag.DefValue, "1m30s")
	}

	// Cobra auto-appends "(default <Value>)" to the rendered Usage
	// line for any flag with a non-zero default. If the inline
	// Usage string also carries "(default ...)", `--help` renders
	// two confusing clauses. Pin that there is exactly one literal
	// "(default" substring in the rendered line.
	rendered := cmd.UsageString()

	if count := strings.Count(rendered, "(default"); count != 1 {
		t.Errorf("--post-upgrade-reconcile-window: rendered Usage has %d '(default' clauses; want exactly 1 (cobra auto-appends, inline Usage must not duplicate)", count)
	}
}

// TestValidatePostUpgradeReconcileWindow_ZeroRejected pins the
// non-positive-rejection contract. Passing 0s would emit
// "waiting 0s for the node to finish booting..." and immediately
// fall through to the version-read loop while Talos is still
// rebooting — the test would always report "rollback" because
// the old version is still running. Reject explicitly with a
// hint pointing at sensible values.
func TestValidatePostUpgradeReconcileWindow_ZeroRejected(t *testing.T) {
	t.Parallel()

	err := validatePostUpgradeReconcileWindow(0)
	if err == nil {
		t.Fatal("zero must be rejected — fall-through to version read while node is rebooting would always report rollback")
	}

	hints := errors.GetAllHints(err)

	found := false

	for _, h := range hints {
		if strings.Contains(h, "positive duration") {
			found = true

			break
		}
	}

	if !found {
		t.Errorf("hint must mention 'positive duration' so operators see a recovery path; got hints: %v", hints)
	}
}

// TestValidatePostUpgradeReconcileWindow_NegativeRejected is the
// boundary partner of the zero case. A negative DurationVar value
// (e.g. --post-upgrade-reconcile-window=-30s) parses fine via
// pflag but is operationally nonsensical; the validation must
// reject it with the same hint shape.
func TestValidatePostUpgradeReconcileWindow_NegativeRejected(t *testing.T) {
	t.Parallel()

	err := validatePostUpgradeReconcileWindow(-30 * time.Second)
	if err == nil {
		t.Fatal("negative duration must be rejected")
	}
}

// TestValidatePostUpgradeReconcileWindow_PositiveAccepted pins the
// happy path: any positive duration (1 ms, 90 s, 10 m) is accepted.
// Without this case the validator could regress to always-reject
// and the failing-zero / failing-negative tests would still pass.
func TestValidatePostUpgradeReconcileWindow_PositiveAccepted(t *testing.T) {
	t.Parallel()

	for _, d := range []time.Duration{time.Millisecond, 90 * time.Second, 10 * time.Minute} {
		if err := validatePostUpgradeReconcileWindow(d); err != nil {
			t.Errorf("positive duration %s must be accepted, got: %v", d, err)
		}
	}
}

// TestWrapUpgradeCommand_LongDocumentsImageResolution pins the
// operator-facing description of where the upgrade target image
// comes from. `talm upgrade --help` must explain the
// values.yaml → --image override chain so operators don't have to
// read the source. Without this pin a doc-removal refactor would
// silently revert the upgrade flow to "you have to guess where
// --image came from".
func TestWrapUpgradeCommand_LongDocumentsImageResolution(t *testing.T) {
	cmd := &cobra.Command{Use: upgradeCmdName}
	wrapUpgradeCommand(cmd, func(_ *cobra.Command, _ []string) error { return nil })

	for _, want := range []string{"values.yaml", "--image"} {
		if !strings.Contains(cmd.Long, want) {
			t.Errorf("upgrade Long must name %q in the image-resolution explanation; got:\n%s", want, cmd.Long)
		}
	}
}

// TestWrapUpgradeCommand_BadReconcileWindow_FailsFastBeforeOriginalRunE
// pins fail-fast semantics on the reconcile-window flag: a zero or
// negative value must reject BEFORE the talosctl upgrade RPC fires.
// Validating only inside Phase 2C (after the RPC) would mean an
// operator's typo lands a partial upgrade before the validation
// surfaces, then surfaces a 'rollback' hint that's actually 'you
// passed 0s'.
//
// The test installs wrapUpgradeCommand with a sentinel originalRunE
// that flips a boolean if invoked, sets the flag to 0s, runs RunE,
// and asserts (1) error returned with the hint, (2) originalRunE
// was NOT called.
func TestWrapUpgradeCommand_BadReconcileWindow_FailsFastBeforeOriginalRunE(t *testing.T) {
	saved := upgradeCmdFlags.postUpgradeReconcileWindow

	t.Cleanup(func() { upgradeCmdFlags.postUpgradeReconcileWindow = saved })

	originalRunECalled := false
	originalRunE := func(_ *cobra.Command, _ []string) error {
		originalRunECalled = true

		return nil
	}

	cmd := &cobra.Command{Use: upgradeCmdName}
	wrapUpgradeCommand(cmd, originalRunE)

	upgradeCmdFlags.postUpgradeReconcileWindow = 0

	err := cmd.RunE(cmd, nil)
	if err == nil {
		t.Fatal("expected fail-fast on 0s reconcile window, got nil")
	}

	hints := errors.GetAllHints(err)

	found := false

	for _, h := range hints {
		if strings.Contains(h, "positive duration") {
			found = true

			break
		}
	}

	if !found {
		t.Errorf("error must carry 'positive duration' hint; got hints: %v", hints)
	}

	if originalRunECalled {
		t.Error("originalRunE (talosctl upgrade RPC) must NOT be invoked when reconcile-window validation rejects the flag — operator's typo would land a partial upgrade")
	}
}

// TestReadmePostUpgradeVerify_NoHardcoded90s mirrors
// TestPostUpgradeVersionMismatchHint_NoHardcoded90s for the
// operator-facing README. The pre-#190 README claimed "waits 90s
// for the node to finish booting"; after #190 the window is
// operator-tunable via --post-upgrade-reconcile-window. The README
// bullet must reference "the configured reconcile window" rather
// than a literal 90s so an operator running with a custom window
// does not read contradictory documentation. Pin the absence of
// the literal so a future README edit re-introducing it fails this
// test.
func TestReadmePostUpgradeVerify_NoHardcoded90s(t *testing.T) {
	t.Parallel()

	readmePath := filepath.Join("..", "..", "README.md")

	body, err := os.ReadFile(readmePath)
	if err != nil {
		t.Skipf("README.md not present at %s (likely a vendored source release without the repo layout): %v", readmePath, err)
	}

	if strings.Contains(string(body), "waits 90s") {
		t.Errorf("README.md must not hardcode 'waits 90s' — the post-upgrade reconcile window is operator-tunable via --post-upgrade-reconcile-window; replace with 'the configured reconcile window (default 90s, tune via --post-upgrade-reconcile-window)'")
	}
}

// TestPostUpgradeVersionMismatchHint_NoHardcoded90s catches future
// drift in the hint text. The original const baked "90s reconcile
// window" verbatim; with the new flag, operators running
// --post-upgrade-reconcile-window=180s would see "the 90s reconcile
// window" in the version-mismatch hint, which contradicts what
// they typed on the command line. Pin the absence of the literal
// "90s" so a future "helpful clarification" reintroducing it
// fails this test.
func TestPostUpgradeVersionMismatchHint_NoHardcoded90s(t *testing.T) {
	t.Parallel()

	if strings.Contains(postUpgradeVersionMismatchHint, "90s") {
		t.Errorf("hint must not hardcode 90s — operators passing --post-upgrade-reconcile-window=<custom> see misleading copy; got: %q", postUpgradeVersionMismatchHint)
	}
}
