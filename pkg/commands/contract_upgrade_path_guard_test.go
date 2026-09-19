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
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cockroachdb/errors"
	"github.com/siderolabs/talos/pkg/machinery/api/machine"
	"github.com/siderolabs/talos/pkg/machinery/compatibility"
	"github.com/spf13/cobra"
)

// talosRef builds the installer reference shape the guard actually receives;
// the guard lifts the version out of the tag, it is not handed one.
func talosRef(tag string) string {
	return "ghcr.io/cozystack/cozystack/talos:" + tag
}

// Talos ships the authoritative upgrade matrix in pkg/machinery/compatibility
// and its own installer runs it as a pre-flight. Hand-rolling the comparison
// gets it wrong in both directions: one minor back is supported and would be
// refused, while a jump too far forward is not and would be waved through to
// fail inside the installer after the image is already pulled.
func TestContract_UnsupportedUpgradePathIsRefusedBeforeTheRPC(t *testing.T) {
	for _, tc := range []struct {
		name      string
		running   string
		target    string
		wantBlock bool
	}{
		{name: "one minor forward", running: "v1.12.6", target: talosRef("v1.13.0"), wantBlock: false},
		{name: "same minor, newer patch", running: "v1.12.5", target: talosRef("v1.12.6"), wantBlock: false},
		{name: "same version", running: "v1.12.6", target: talosRef("v1.12.6"), wantBlock: false},
		// One minor back is a supported rollback, and the most common reason to
		// move a node backwards at all.
		{name: "one minor back", running: "v1.14.0", target: talosRef("v1.13.7"), wantBlock: false},
		{name: "two minors back", running: "v1.14.0", target: talosRef("v1.12.6"), wantBlock: true},
		// Too far forward fails inside the installer, after the image is pulled.
		{name: "three minors forward", running: "v1.11.0", target: talosRef("v1.14.0"), wantBlock: true},
		{name: "running unreadable", running: "", target: talosRef("v1.12.6"), wantBlock: false},
		{name: "target unparseable", running: "v1.13.0", target: talosRef("latest"), wantBlock: false},
		{name: "digest-pinned target", running: "v1.13.0", target: "ghcr.io/cozystack/cozystack/talos@sha256:abc123", wantBlock: false},
		// A target newer than anything this talm build knows means the matrix
		// cannot judge it, not that the path is wrong.
		{name: "target beyond this build's matrix", running: "v1.14.0", target: talosRef("v1.15.0"), wantBlock: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkUpgradePathSupported(tc.running, tc.target)

			if tc.wantBlock && err == nil {
				t.Errorf("running=%s target=%s: expected a block, got none", tc.running, tc.target)
			}

			if !tc.wantBlock && err != nil {
				t.Errorf("running=%s target=%s: unexpected block: %v", tc.running, tc.target, err)
			}

			// Talos normalises the version it prints, dropping the leading "v".
			if tc.wantBlock && err != nil && !strings.Contains(err.Error(), strings.TrimPrefix(tc.running, "v")) {
				t.Errorf("block does not name the running version: %v", err)
			}
		})
	}
}

// A node that cannot be read must not block the upgrade: an unreachable node
// is a worse reason to refuse than the downgrade the guard is there to stop.
func TestContract_UpgradePathCheckSkipsUnreadableNodes(t *testing.T) {
	var buf bytes.Buffer

	unreadable := func(context.Context) (string, bool, error) {
		return "", false, errors.New("connection refused")
	}

	if err := checkNodesUpgradePath(context.Background(), []string{"node0"}, talosRef("v1.12.6"), unreadable, &buf); err != nil {
		t.Errorf("an unreadable node blocked the upgrade: %v", err)
	}

	if !strings.Contains(buf.String(), "node0") {
		t.Errorf("the skip was not reported:\n%s", buf.String())
	}
}

// Every node is consulted, so a set upgrade surfaces all refusals at once.
func TestContract_UpgradePathCheckReportsEveryNode(t *testing.T) {
	var buf bytes.Buffer

	running := func(context.Context) (string, bool, error) {
		return "v1.14.0", true, nil
	}

	err := checkNodesUpgradePath(context.Background(), []string{"node0", "node1"}, talosRef("v1.12.6"), running, &buf)
	if err == nil {
		t.Fatal("expected the upgrade to be refused")
	}

	for _, node := range []string{"node0", "node1"} {
		if !strings.Contains(err.Error(), node) {
			t.Errorf("refusal does not name %s: %v", node, err)
		}
	}

	// The refusal is the only thing standing between the operator and a
	// deliberate downgrade, so it has to carry the way out. errors.Join drops
	// hints from the errors it joins, including a single one, so the hint has
	// to sit on the joined error rather than on each refusal.
	hints := strings.Join(errors.GetAllHints(err), "\n")
	if !strings.Contains(hints, "--skip-upgrade-path-check") {
		t.Errorf("refusal does not tell the operator how to proceed anyway:\n%s", hints)
	}
}

// The guard only helps if it runs before the RPC, and --skip-upgrade-path-check
// only helps if it actually suppresses it. Neither is visible in the pure
// functions: moving the call below originalRunE, or dropping the flag check,
// leaves every other test in this file green.
//
// The skip half runs offline — with the flag set the guard never builds a
// client — so the sentinel reaching originalRunE is the whole assertion.
func TestContract_UpgradePathCheckIsSkippableAndRunsBeforeTheRPC(t *testing.T) {
	savedSkip := upgradeCmdFlags.skipUpgradePathCheck
	savedWindow := upgradeCmdFlags.postUpgradeReconcileWindow
	savedVerify := upgradeCmdFlags.skipPostUpgradeVerify

	t.Cleanup(func() {
		upgradeCmdFlags.skipUpgradePathCheck = savedSkip
		upgradeCmdFlags.postUpgradeReconcileWindow = savedWindow
		upgradeCmdFlags.skipPostUpgradeVerify = savedVerify
	})

	reached := false

	cmd := &cobra.Command{Use: upgradeCmdName}
	cmd.Flags().StringP("image", "i", "", "")
	cmd.Flags().Bool("stage", false, "")
	wrapUpgradeCommand(cmd, func(*cobra.Command, []string) error {
		reached = true

		return nil
	})

	upgradeCmdFlags.skipPostUpgradeVerify = true

	// Both halves need a target the guard would actually judge and a
	// talosconfig it cannot use. Hoisted above the subtests so the only thing
	// that differs between them is the flag: with the setup inside the second
	// subtest, the first one left the image empty and the guard returned before
	// ever reading the flag, which made it pass with the flag check deleted.
	if err := cmd.Flags().Set("image", talosRef("v1.12.6")); err != nil {
		t.Fatal(err)
	}

	savedTalosconfig := GlobalArgs.Talosconfig
	t.Cleanup(func() { GlobalArgs.Talosconfig = savedTalosconfig })

	GlobalArgs.Talosconfig = filepath.Join(t.TempDir(), "absent.yaml")

	t.Run("the flag suppresses the guard", func(t *testing.T) {
		reached = false
		upgradeCmdFlags.skipUpgradePathCheck = true

		if err := cmd.RunE(cmd, nil); err != nil {
			t.Fatalf("skipped guard still failed the upgrade: %v", err)
		}

		if !reached {
			t.Error("--skip-upgrade-path-check did not suppress the guard: the RPC was never reached")
		}
	})

	t.Run("a guard that cannot run blocks before the RPC", func(t *testing.T) {
		reached = false
		upgradeCmdFlags.skipUpgradePathCheck = false

		// No talosconfig means the guard cannot build a client and returns an
		// error. What that pins is the ordering: the sentinel must not have
		// fired, so the guard sits above the RPC rather than after it.
		if err := cmd.RunE(cmd, nil); err == nil {
			t.Fatal("expected the guard to fail without a talosconfig")
		}

		if reached {
			t.Error("the RPC fired before the guard: moving the guard below originalRunE would not be caught")
		}
	})
}

// An empty node list must say so rather than pass silently, matching the
// post-upgrade verify's contract for the same shape.
func TestContract_UpgradePathCheckAnnouncesNoTargetNodes(t *testing.T) {
	var buf bytes.Buffer

	never := func(context.Context) (string, bool, error) {
		t.Error("the reader was called with no target nodes")

		return "", false, nil
	}

	if err := checkNodesUpgradePath(context.Background(), nil, talosRef("v1.12.6"), never, &buf); err != nil {
		t.Fatalf("an empty node list failed the upgrade: %v", err)
	}

	if !strings.Contains(buf.String(), "no target nodes") {
		t.Errorf("the skip was not announced:\n%s", buf.String())
	}
}

// A reader may surrender without an error — the "not applicable on this path"
// shape the post-upgrade verify also allows for. Reporting that as
// "could not read: <nil>" would name a cause that does not exist.
func TestContract_UpgradePathCheckReportsASilentSurrender(t *testing.T) {
	var buf bytes.Buffer

	silent := func(context.Context) (string, bool, error) {
		return "", false, nil
	}

	if err := checkNodesUpgradePath(context.Background(), []string{"node0"}, talosRef("v1.12.6"), silent, &buf); err != nil {
		t.Fatalf("a silent surrender blocked the upgrade: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "unavailable") {
		t.Errorf("the surrender was not reported in its own shape:\n%s", out)
	}

	if strings.Contains(out, "<nil>") {
		t.Errorf("named a cause that does not exist:\n%s", out)
	}
}

// The guard tells "this minor is absent from the matrix" from a real verdict by
// probing the target against itself, which only holds while a same-version pair
// can fail for no other reason. That is a property of Talos's tables, not a
// contract they owe us: the day an upstream bump lists a version of the same
// minor in DeniedHostUpgradeVersions, the probe would read it as "unknown
// minor" and switch the whole guard off silently.
func TestContract_UpgradePathSelfProbeStillSeparatesUnknownMinors(t *testing.T) {
	for minor := 2; minor <= 14; minor++ {
		tag := fmt.Sprintf("v1.%d.0", minor)

		version, err := compatibility.ParseTalosVersion(&machine.VersionInfo{Tag: tag})
		if err != nil {
			t.Fatalf("%s: %v", tag, err)
		}

		if err := version.UpgradeableFrom(version); err != nil {
			t.Errorf("%s fails against itself, so the guard reads it as an unknown minor and turns itself off: %v", tag, err)
		}
	}
}
