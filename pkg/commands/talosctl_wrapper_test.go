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

	"github.com/cockroachdb/errors"
	taloscommands "github.com/siderolabs/talos/cmd/talosctl/cmd/talos"
	"github.com/spf13/cobra"
)

// TestPropagatePersistentFlags_SkipsRootShadowedNames asserts that
// every name in rootShadowedPersistentFlags is dropped from the
// propagation pass. talm's root already registers these flags
// bound to commands.GlobalArgs.<X>; letting upstream's PersistentFlag
// registration propagate would bind the subcommand-level parse to
// taloscommands.GlobalArgs.<X> and then the wrapper PreRunE's
// `taloscommands.GlobalArgs = commands.GlobalArgs` sync would wipe
// it. Table-driven so adding a name to the shadow map automatically
// extends coverage.
func TestPropagatePersistentFlags_SkipsRootShadowedNames(t *testing.T) {
	for name := range rootShadowedPersistentFlags {
		t.Run(name, func(t *testing.T) {
			upstream := &cobra.Command{Use: "upstream"}
			upstream.PersistentFlags().String(name, "default", "shadowed persistent flag")

			wrapped := &cobra.Command{Use: "wrapped"}

			propagatePersistentFlags(upstream, wrapped)

			if got := wrapped.PersistentFlags().Lookup(name); got != nil {
				t.Errorf("shadowed name %q must not propagate to wrappedCmd.PersistentFlags(); got %+v", name, got)
			}
		})
	}
}

// TestPropagatePersistentFlags_NonShadowedPropagates is the
// companion check: a name NOT in the shadow map flows through the
// propagation pass normally. Builds a synthetic flag whose name is
// distinct from anything talm root reserves (e.g. "namespace" is
// the real upstream case but feels load-bearing in production; use
// a synthetic name so the test can't false-positive on a future
// schema collision).
func TestPropagatePersistentFlags_NonShadowedPropagates(t *testing.T) {
	upstream := &cobra.Command{Use: "upstream"}
	upstream.PersistentFlags().String("definitely-not-shadowed", "v", "regular upstream persistent flag")

	wrapped := &cobra.Command{Use: "wrapped"}

	propagatePersistentFlags(upstream, wrapped)

	if got := wrapped.PersistentFlags().Lookup("definitely-not-shadowed"); got == nil {
		t.Fatal("non-shadowed persistent flag must propagate to wrappedCmd.PersistentFlags(); got nil")
	}
}

// TestPropagatePersistentFlags_RenamesShorthandF pins the defensive
// `-f` → `-F` rename branch in propagatePersistentFlags. Today no
// upstream persistent flag carries shorthand `f`, so this branch
// is dead code on production input — exactly the regression-trap
// shape: defensive code without coverage is the kind that gets
// "cleaned up" by a future refactor and silently re-introduces
// the collision.
//
// Build a synthetic upstream parent with --foo / -f as a persistent
// flag; propagate; assert the wrapped command has --foo with
// shorthand F (not f, which would collide with talm's own
// --file / -f flag added by the local-flag loop in wrapTalosCommand).
func TestPropagatePersistentFlags_RenamesShorthandF(t *testing.T) {
	upstream := &cobra.Command{Use: "upstream"}
	upstream.PersistentFlags().StringP("foo", "f", "default", "shadow shorthand f")

	wrapped := &cobra.Command{Use: "wrapped"}

	propagatePersistentFlags(upstream, wrapped)

	got := wrapped.PersistentFlags().Lookup("foo")
	if got == nil {
		t.Fatal("propagation must register --foo on wrappedCmd.PersistentFlags(); got nil")
	}

	if got.Shorthand != "F" {
		t.Errorf("propagation must rename shorthand from 'f' to 'F' to avoid collision with talm's --file / -f flag; got %q", got.Shorthand)
	}
}

// TestWrapTalosCommand_InheritsParentPersistentFlags pins the
// structural contract behind #194: when an upstream parent registers
// a persistent flag, every wrapped child must surface that flag in
// its effective flag set. cobra's mergePersistentFlags walks the
// wrapped parent's PersistentFlags() at parse time; if the wrapper
// only copies LOCAL flags, persistent ones from the upstream parent
// are silently dropped from the wrapped tree.
//
// Synthetic tree (no taloscommands dependency) so the test stays
// hermetic: build a parent with a persistent --foo, a child with a
// local --bar; wrap; assert the wrapped child sees both after
// ParseFlags.
func TestWrapTalosCommand_InheritsParentPersistentFlags(t *testing.T) {
	parent := &cobra.Command{Use: "parent"}
	parent.PersistentFlags().String("foo", "default", "persistent on parent")

	child := &cobra.Command{Use: "child", Run: func(_ *cobra.Command, _ []string) {}}
	child.Flags().String("bar", "default", "local on child")
	parent.AddCommand(child)

	wrappedParent := wrapTalosCommand(parent, "parent")
	wrappedChild, _, err := wrappedParent.Find([]string{"child"})
	if err != nil {
		t.Fatalf("Find child on wrapped parent: %v", err)
	}

	if err := wrappedChild.ParseFlags([]string{"--foo=fromparent", "--bar=fromchild"}); err != nil {
		t.Fatalf("ParseFlags on wrapped child: %v — persistent --foo from parent was dropped", err)
	}

	fooFlag := wrappedChild.Flags().Lookup("foo")
	if fooFlag == nil {
		t.Fatal("wrapped child must see parent's persistent --foo after merge; got nil")
	}

	if fooFlag.Value.String() != "fromparent" {
		t.Errorf("--foo value: got %q, want %q", fooFlag.Value.String(), "fromparent")
	}

	barFlag := wrappedChild.Flags().Lookup("bar")
	if barFlag == nil || barFlag.Value.String() != "fromchild" {
		t.Errorf("wrapped child must still see its own --bar; got %v", barFlag)
	}
}

// TestWrapTalosCommand_RealImageListPropagatesNamespace is the
// regression pin against the actual upstream surface. The upstream
// imageCmd.PersistentFlags() registers --namespace; image list is
// its subcommand. Through the talm wrapper, --namespace must remain
// resolvable on `image list`. Empirically broken before the fix
// (talm image list --help showed no --namespace).
func TestWrapTalosCommand_RealImageListPropagatesNamespace(t *testing.T) {
	var imageCmd *cobra.Command

	for _, cmd := range taloscommands.Commands {
		if cmd.Name() == "image" {
			imageCmd = cmd

			break
		}
	}

	if imageCmd == nil {
		t.Skip("upstream taloscommands.Commands has no 'image' command — schema changed")
	}

	wrapped := wrapTalosCommand(imageCmd, "image")

	listCmd, _, err := wrapped.Find([]string{"list"})
	if err != nil {
		t.Fatalf("Find list under wrapped image: %v", err)
	}

	if err := listCmd.ParseFlags([]string{"--namespace", "system"}); err != nil {
		t.Fatalf("ParseFlags on wrapped image list: %v — --namespace from imageCmd.PersistentFlags() was dropped", err)
	}

	ns := listCmd.Flags().Lookup("namespace")
	if ns == nil {
		t.Fatal("wrapped image list must see --namespace from parent's PersistentFlags()")
	}

	if ns.Value.String() != "system" {
		t.Errorf("--namespace value: got %q, want %q", ns.Value.String(), "system")
	}
}

// TestWrapCrashdumpCommand_PrepopulatesGlobalArgsNodes pins the
// contract for #180: when crashdump's per-class node flags
// (--init-node, --control-plane-nodes, --worker-nodes) are set
// and GlobalArgs.Nodes is otherwise empty, the wrapper's PreRunE
// populates GlobalArgs.Nodes from their union so the upstream
// WithClient guard at cmd/talosctl/pkg/talos/global/client.go
// is satisfied. Without this, operators following the documented
// `talm crashdump --control-plane-nodes <ip>` shape hit
// "nodes are not set for the command" before crashdump's own
// deprecation message can surface.
func TestWrapCrashdumpCommand_PrepopulatesGlobalArgsNodes(t *testing.T) {
	savedNodes := GlobalArgs.Nodes

	t.Cleanup(func() { GlobalArgs.Nodes = savedNodes })

	GlobalArgs.Nodes = nil

	cmd := &cobra.Command{Use: crashdumpCmdName}
	cmd.Flags().String("init-node", "", "")
	cmd.Flags().StringSlice("control-plane-nodes", nil, "")
	cmd.Flags().StringSlice("worker-nodes", nil, "")

	wrapCrashdumpCommand(cmd)

	if err := cmd.Flags().Set("control-plane-nodes", "192.0.2.10"); err != nil {
		t.Fatalf("set --control-plane-nodes: %v", err)
	}

	if err := cmd.Flags().Set("worker-nodes", "192.0.2.11"); err != nil {
		t.Fatalf("set --worker-nodes: %v", err)
	}

	if err := cmd.PreRunE(cmd, nil); err != nil {
		t.Fatalf("PreRunE returned: %v", err)
	}

	if len(GlobalArgs.Nodes) != 2 {
		t.Fatalf("expected GlobalArgs.Nodes populated from per-class flags; got %v", GlobalArgs.Nodes)
	}

	if GlobalArgs.Nodes[0] != "192.0.2.10" || GlobalArgs.Nodes[1] != "192.0.2.11" {
		t.Errorf("expected [192.0.2.10, 192.0.2.11], got %v", GlobalArgs.Nodes)
	}
}

// TestWrapCrashdumpCommand_DoesNotShadowExistingNodes pins the
// no-overwrite contract: when GlobalArgs.Nodes is already set (via
// --nodes flag or modeline), the per-class flag values are NOT
// merged in. This keeps the explicit --nodes assignment
// authoritative — same precedence as the rest of talm's wrapper
// (modeline pre-population is also no-overwrite when --nodes is
// explicit).
func TestWrapCrashdumpCommand_DoesNotShadowExistingNodes(t *testing.T) {
	savedNodes := GlobalArgs.Nodes

	t.Cleanup(func() { GlobalArgs.Nodes = savedNodes })

	GlobalArgs.Nodes = []string{"192.0.2.99"}

	cmd := &cobra.Command{Use: crashdumpCmdName}
	cmd.Flags().String("init-node", "", "")
	cmd.Flags().StringSlice("control-plane-nodes", nil, "")
	cmd.Flags().StringSlice("worker-nodes", nil, "")

	wrapCrashdumpCommand(cmd)

	if err := cmd.Flags().Set("control-plane-nodes", "192.0.2.10"); err != nil {
		t.Fatalf("set --control-plane-nodes: %v", err)
	}

	if err := cmd.PreRunE(cmd, nil); err != nil {
		t.Fatalf("PreRunE: %v", err)
	}

	if len(GlobalArgs.Nodes) != 1 || GlobalArgs.Nodes[0] != "192.0.2.99" {
		t.Errorf("explicit --nodes must take precedence; got %v", GlobalArgs.Nodes)
	}
}

// TestWrapKubeconfigCommand_PositionalPathErrorMessageMatchesContract
// pins the rewritten error message for #193. The previous wording
// claimed `use --login flag to pass arguments`, which conflated two
// distinct things: --login switches the kubeconfig target between
// local and system, it does not pass a positional path. The new
// message describes what the wrapper actually does (default writes
// to project root; --login redirects to system) and lists actual
// alternatives.
func TestWrapKubeconfigCommand_PositionalPathErrorMessageMatchesContract(t *testing.T) {
	var kubeconfigCmd *cobra.Command

	for _, cmd := range taloscommands.Commands {
		if cmd.Name() == defaultKubeconfigName {
			kubeconfigCmd = cmd

			break
		}
	}

	if kubeconfigCmd == nil {
		t.Skipf("upstream taloscommands.Commands has no %q command", defaultKubeconfigName)
	}

	wrapped := wrapTalosCommand(kubeconfigCmd, defaultKubeconfigName)

	err := wrapped.Args(wrapped, []string{"/some/positional/path"})
	if err == nil {
		t.Fatal("kubeconfig with positional path must error when --login is unset")
	}

	body := err.Error()
	if strings.Contains(body, "use --login flag to pass arguments") {
		t.Errorf("error body still carries the misleading '--login to pass arguments' wording; got: %q", body)
	}

	// Body must mention project root (default behaviour) so the
	// operator understands what default mode actually does.
	if !strings.Contains(strings.ToLower(body), "project root") {
		t.Errorf("error body must describe the default destination (project root); got: %q", body)
	}

	// cockroachdb/errors.WithHint stores the hint chain separately
	// from the body; err.Error() returns only the body. Walk the
	// hint chain via GetAllHints to inspect the operator-facing
	// guidance.
	hints := strings.Join(errors.GetAllHints(err), "\n")

	// Hint must NOT suggest stdout redirection. The kubeconfig is
	// written to the filesystem path (not stdout), so
	// `talm kubeconfig > /path` would leave the operator with an
	// empty /path and an unexpected ./kubeconfig in the project
	// root. Pin against the previous misleading advice.
	if strings.Contains(strings.ToLower(hints), "redirect stdout") || strings.Contains(hints, "kubeconfig > /") {
		t.Errorf("hint must not suggest stdout redirection; kubeconfig is written to filesystem path. got hints: %q", hints)
	}

	// Hint must describe the actual --login workflow: positional
	// path is honoured under --login, and --login with no
	// positional defaults to ~/.kube/config (the system
	// kubeconfig). Pinning either "--login /" (suggesting a path
	// after --login) or "~/.kube/config" verifies the workflow
	// reaches the operator accurately.
	if !strings.Contains(hints, "--login /") && !strings.Contains(hints, "~/.kube/config") {
		t.Errorf("hint must describe the --login workflow (pass --login /path or rely on the ~/.kube/config default); got hints: %q", hints)
	}
}

// TestWrapDmesgCommand_TailEqualsNumeric_RewritesError pins the
// FlagErrorFunc cushion for #195. Upstream talosctl registers
// --tail as a BoolVarP (toggling tail-mode for --follow), but
// operators' first instinct is tail(1)-style line count. `--tail=3`
// trips pflag's ParseBool and surfaces a cryptic
// 'strconv.ParseBool: parsing "3": invalid syntax' error.
//
// The wrapper installs a FlagErrorFunc that detects this specific
// pattern and rewrites it with a hint pointing at the actual
// upstream contract + a pipe-to-tail(1) suggestion for the
// line-count case operators usually want.
func TestWrapDmesgCommand_TailEqualsNumeric_RewritesError(t *testing.T) {
	var dmesgCmd *cobra.Command

	for _, cmd := range taloscommands.Commands {
		if cmd.Name() == "dmesg" {
			dmesgCmd = cmd

			break
		}
	}

	if dmesgCmd == nil {
		t.Skip("upstream taloscommands.Commands has no 'dmesg' command — schema changed")
	}

	wrapped := wrapTalosCommand(dmesgCmd, "dmesg")

	// FlagErrorFunc is invoked by cobra's Execute() lifecycle on a
	// ParseFlags failure. Reproduce that path by capturing the
	// ParseFlags error and feeding it through the registered hook —
	// this is exactly what cobra does internally in command.execute.
	rawErr := wrapped.ParseFlags([]string{"--tail=3"})
	if rawErr == nil {
		t.Fatal("--tail=3 must error from pflag (upstream --tail is bool); got nil")
	}

	hook := wrapped.FlagErrorFunc()
	if hook == nil {
		t.Fatal("wrapped dmesg must register a FlagErrorFunc to rewrite ParseBool errors")
	}

	err := hook(wrapped, rawErr)

	msg := err.Error()

	// Top-level message must describe the actual contract
	// (boolean toggle for --follow) so the operator sees it first
	// before any inherited ParseBool noise.
	if !strings.Contains(strings.ToLower(msg), "boolean") && !strings.Contains(strings.ToLower(msg), "follow") {
		t.Errorf("rewritten error must describe --tail's actual contract (boolean toggle for --follow); got: %q", msg)
	}

	// Chain must preserve the original ParseBool error via
	// errors.Wrap so the underlying pflag failure remains
	// reachable for verbose debugging (errors.Unwrap,
	// fmt.Sprintf("%+v", err), etc.). Without wrap-not-replace,
	// downstream tooling that walks the chain or matches against
	// the upstream error type loses information.
	if !strings.Contains(msg, "ParseBool") {
		t.Errorf("rewritten error must wrap (not replace) the original ParseBool error so the chain stays inspectable; got: %q", msg)
	}

	if unwrapped := errors.Unwrap(err); unwrapped == nil {
		t.Errorf("rewritten error must be unwrappable to the original; errors.Unwrap returned nil for %q", msg)
	}
}

// TestWrapTUICommand_NonTTY_RefusesWithHint pins the cushion for
// #183 across BOTH wrapped interactive-only commands (dashboard,
// edit). Each has a different upstream failure mechanism:
// dashboard panics in tcell teardown, edit hangs in the kubectl
// external-editor helper. The refusal here is the same shape for
// both — clear cobra-surfaced error with operator-facing hint —
// and the cmdLabel substitution lets the message correlate to
// the command the operator typed.
//
// Table-driven so the dispatch in wrapTalosCommand (which routes
// both dashboardCmdName and editCmdName through wrapTUICommand)
// is exercised symmetrically. A future refactor that hardcodes
// one branch or flips the OR silently breaks one side without
// failing the matrix.
func TestWrapTUICommand_NonTTY_RefusesWithHint(t *testing.T) {
	savedStdinIsTTY := stdinIsTTY

	t.Cleanup(func() { stdinIsTTY = savedStdinIsTTY })

	stdinIsTTY = func() bool { return false }

	tests := []string{dashboardCmdName, editCmdName}
	for _, label := range tests {
		t.Run(label, func(t *testing.T) {
			cmd := &cobra.Command{Use: label}
			wrapTUICommand(cmd, label)

			err := cmd.PreRunE(cmd, nil)
			if err == nil {
				t.Fatalf("non-tty stdin must refuse %s up front; got nil", label)
			}

			msg := err.Error()
			if !strings.Contains(strings.ToLower(msg), "tty") && !strings.Contains(strings.ToLower(msg), "terminal") {
				t.Errorf("refusal must mention tty/terminal so the operator can correlate; got: %q", msg)
			}

			// cmdLabel must appear in the message so an operator
			// running CI logs can grep for the command name and
			// land on the refusal directly. Pins the label-
			// substitution contract against a future refactor
			// that hardcodes the message.
			if !strings.Contains(msg, label) {
				t.Errorf("refusal must include the command label %q so the operator can correlate; got: %q", label, msg)
			}
		})
	}
}

// TestWrapRotateCACommand_LongDoesNotReferenceDroppedNShorthand
// pins the rotate-ca help text against the -n shorthand drop. The
// Long previously said "specify exactly ONE control-plane node via
// --endpoints/-e or --nodes/-n" — after the shorthand drop the
// reference to `-n` is stale and would teach operators a flag
// shape that errors out at parse time. Catches the same class of
// drift on the next change.
func TestWrapRotateCACommand_LongDoesNotReferenceDroppedNShorthand(t *testing.T) {
	cmd := &cobra.Command{Use: rotateCACmdName}
	cmd.Flags().Bool("with-docs", true, "")
	cmd.Flags().Bool("with-examples", true, "")
	cmd.PreRunE = func(_ *cobra.Command, _ []string) error { return nil }

	wrapRotateCACommand(cmd, nil)

	for _, banned := range []string{"--nodes/-n", " -n,", " -n "} {
		if strings.Contains(cmd.Long, banned) {
			t.Errorf("rotate-ca Long must not reference the dropped -n shorthand; found %q in:\n%s", banned, cmd.Long)
		}
	}
}

// TestWrapRotateCACommand_PerClassFlagsPopulateNodes pins the
// rotate-ca extension to the per-class populate logic. Like
// crashdump, rotate-ca's upstream registers --init-node /
// --control-plane-nodes / --worker-nodes (its API surface for
// CA rotation against a heterogeneous cluster), and the upstream
// WithClient guard pre-validates GlobalArgs.Nodes regardless of
// whether the operator used the global --nodes or a per-class
// flag. Without populating, operators following the documented
// `rotate-ca --control-plane-nodes X` shape hit the nodes-not-set
// guard before rotate-ca's RunE runs.
//
// Unlike crashdump (which collects all per-class flags as a
// diagnostic union), rotate-ca's contract is "exactly one CP
// node". The populate puts whatever the operator passed into
// GlobalArgs.Nodes; the existing multi-node guard then catches
// the case where multiple were passed and returns the same hint
// it always has.
func TestWrapRotateCACommand_PerClassFlagsPopulateNodes(t *testing.T) {
	savedNodes := GlobalArgs.Nodes

	t.Cleanup(func() { GlobalArgs.Nodes = savedNodes })

	GlobalArgs.Nodes = nil

	cmd := &cobra.Command{Use: rotateCACmdName}
	cmd.Flags().String("init-node", "", "")
	cmd.Flags().StringSlice("control-plane-nodes", nil, "")
	cmd.Flags().StringSlice("worker-nodes", nil, "")
	cmd.PreRunE = func(_ *cobra.Command, _ []string) error { return nil }

	wrapRotateCACommand(cmd, nil)

	if err := cmd.Flags().Set("control-plane-nodes", "192.0.2.10"); err != nil {
		t.Fatalf("set --control-plane-nodes: %v", err)
	}

	if err := cmd.PreRunE(cmd, nil); err != nil {
		t.Fatalf("PreRunE returned: %v", err)
	}

	if len(GlobalArgs.Nodes) != 1 || GlobalArgs.Nodes[0] != "192.0.2.10" {
		t.Errorf("expected GlobalArgs.Nodes populated from --control-plane-nodes; got %v", GlobalArgs.Nodes)
	}
}

// TestWrapTUICommand_TTY_PassesThrough pins the no-op contract:
// when stdin IS a terminal the wrapper does NOT interfere with
// the command's normal flow.
func TestWrapTUICommand_TTY_PassesThrough(t *testing.T) {
	savedStdinIsTTY := stdinIsTTY

	t.Cleanup(func() { stdinIsTTY = savedStdinIsTTY })

	stdinIsTTY = func() bool { return true }

	cmd := &cobra.Command{Use: "dashboard"}
	wrapTUICommand(cmd, "dashboard")

	if err := cmd.PreRunE(cmd, nil); err != nil {
		t.Errorf("tty path must pass through; got %v", err)
	}
}

// TestWrapTalosCommand_RealCrashdumpPopulatesNodesFromControlPlane
// is the real-upstream-path companion to the synthetic crashdump
// populate test. Wraps the actual taloscommands.Commands entry
// for `crashdump` and confirms that --control-plane-nodes
// populates GlobalArgs.Nodes via the full wrapper chain.
//
// Matters because the dispatch ordering in wrapTalosCommand
// (wrapCrashdumpCommand installed AFTER wrapTalosCommand's
// PreRunE assignment so populate runs before the sync) is
// untested in isolation. A refactor that flips that order
// silently breaks the cushion — only the real-path test catches
// it.
func TestWrapTalosCommand_RealCrashdumpPopulatesNodesFromControlPlane(t *testing.T) {
	savedNodes := GlobalArgs.Nodes

	t.Cleanup(func() { GlobalArgs.Nodes = savedNodes })

	var crashdumpCmd *cobra.Command

	for _, cmd := range taloscommands.Commands {
		if cmd.Name() == crashdumpCmdName {
			crashdumpCmd = cmd

			break
		}
	}

	if crashdumpCmd == nil {
		t.Skipf("upstream taloscommands.Commands has no %q command", crashdumpCmdName)
	}

	GlobalArgs.Nodes = nil

	wrapped := wrapTalosCommand(crashdumpCmd, crashdumpCmdName)

	if err := wrapped.Flags().Set("control-plane-nodes", "192.0.2.10"); err != nil {
		t.Fatalf("set --control-plane-nodes: %v", err)
	}

	if err := wrapped.PreRunE(wrapped, nil); err != nil {
		t.Fatalf("PreRunE returned: %v", err)
	}

	if len(GlobalArgs.Nodes) == 0 {
		t.Fatal("real-upstream crashdump must populate GlobalArgs.Nodes from --control-plane-nodes; got empty")
	}

	if GlobalArgs.Nodes[0] != "192.0.2.10" {
		t.Errorf("populated node value: got %q, want 192.0.2.10", GlobalArgs.Nodes[0])
	}
}

// TestWrapTalosCommand_RealMetaWritePropagatesInsecure pins the
// persistent-shorthand path: metaCmd.PersistentFlags() registers
// -i / --insecure with shorthand "i". Through the wrapper, both
// the long and short forms must resolve on `meta write`. Pinning
// the shorthand catches a future regression where the wrapper
// might copy the long flag but lose the shorthand attribute.
func TestWrapTalosCommand_RealMetaWritePropagatesInsecure(t *testing.T) {
	var metaCmd *cobra.Command

	for _, cmd := range taloscommands.Commands {
		if cmd.Name() == "meta" {
			metaCmd = cmd

			break
		}
	}

	if metaCmd == nil {
		t.Skip("upstream taloscommands.Commands has no 'meta' command — schema changed")
	}

	wrapped := wrapTalosCommand(metaCmd, "meta")

	writeCmd, _, err := wrapped.Find([]string{"write"})
	if err != nil {
		t.Fatalf("Find write under wrapped meta: %v", err)
	}

	// Long form.
	if err := writeCmd.ParseFlags([]string{"--insecure"}); err != nil {
		t.Fatalf("ParseFlags --insecure on wrapped meta write: %v", err)
	}

	if writeCmd.Flags().Lookup("insecure") == nil {
		t.Fatal("wrapped meta write must see --insecure from metaCmd.PersistentFlags()")
	}

	// Short form. Re-parse to exercise the -i alias path.
	if err := writeCmd.ParseFlags([]string{"-i"}); err != nil {
		t.Errorf("ParseFlags -i on wrapped meta write: %v — shorthand attribute lost during copy?", err)
	}
}
