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
	"os"
	"path/filepath"
	"strings"
	"testing"

	cerrors "github.com/cockroachdb/errors"
	"github.com/cozystack/talm/pkg/engine"
	"github.com/siderolabs/talos/pkg/machinery/client"
)

// TestApplyTemplatesPerNode_StacksSidePatches_SingleApplyPerNode pins
// the #184 stack-semantics contract: when the operator passes
// `talm apply -f anchor.yaml -f side1.yaml -f side2.yaml`, talm
// must call ApplyConfiguration exactly ONCE per node with the
// composed bundle, not three times where each later call
// overwrites the earlier one. Pre-#184 the loop iterated each -f
// file as an independent apply — Talos replaces the whole
// MachineConfig per call, so the second/third apply silently
// destroyed everything from the first.
//
// The test runs applyTemplatesPerNode with two side-patches and
// captures every (render, apply) invocation. Expectations:
//   - render called once per node (anchor's chart render)
//   - apply called once per node with the final merged bytes
//   - the final bytes contain content from BOTH side-patches in
//     order — proves the patches stacked instead of overwriting.
func TestApplyTemplatesPerNode_StacksSidePatches_SingleApplyPerNode(t *testing.T) {
	dir := t.TempDir()

	// Anchor: modeline-only file so MergeFileAsPatch on it is a
	// no-op and the test focuses on the side-patch chain.
	anchor := filepath.Join(dir, "anchor.yaml")
	if err := os.WriteFile(anchor, []byte(
		"# talm: nodes=[\"1.2.3.4\"], templates=[\"templates/cp.yaml\"]\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}

	// Two side-patches with distinctive content so the final
	// merged bytes prove both were applied.
	side1 := filepath.Join(dir, "side1.yaml")
	if err := os.WriteFile(side1, []byte(
		"machine:\n  certSANs:\n    - canary.side1.example.com\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}

	side2 := filepath.Join(dir, "side2.yaml")
	if err := os.WriteFile(side2, []byte(
		"machine:\n  network:\n    hostname: canary-side2\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}

	nodes := []string{testNodeAddrA}
	var renderCalls int
	var applyBodies [][]byte

	render := func(_ context.Context, _ *client.Client, _ engine.Options) ([]byte, error) {
		renderCalls++

		// Minimal valid machine config so MergeFileAsPatch can
		// parse and merge the side-patches.
		return []byte("version: v1alpha1\nmachine:\n  type: controlplane\n"), nil
	}
	apply := func(_ context.Context, _ *client.Client, data []byte) error {
		applyBodies = append(applyBodies, append([]byte(nil), data...))

		return nil
	}

	err := applyTemplatesPerNode(engine.Options{}, anchor, []string{side1, side2}, nodes, fakeAuthOpenClient(context.Background()), render, apply)
	if err != nil {
		t.Fatalf("applyTemplatesPerNode: %v", err)
	}

	if renderCalls != 1 {
		t.Errorf("render must be called once per node (1); got %d", renderCalls)
	}

	if len(applyBodies) != 1 {
		t.Fatalf("apply must be called exactly once per node (the pre-#184 bug was N calls overwriting each other); got %d", len(applyBodies))
	}

	final := string(applyBodies[0])
	if !strings.Contains(final, "canary.side1.example.com") {
		t.Errorf("final merged bytes must include side1's certSAN; got:\n%s", final)
	}
	if !strings.Contains(final, "canary-side2") {
		t.Errorf("final merged bytes must include side2's hostname; got:\n%s", final)
	}
}

// TestApplyTemplatesPerNode_StacksSidePatches_LastWriterWins pins
// the ordering invariant: when two side-patches set the SAME field
// to different values, the LATER one wins. The sibling stack test
// only proves both patches land; this test proves the merge is
// ordered, which is the contract engine.MergeFileAsPatch upholds
// when called in a loop ("each sidePatch in order"). Without this
// pin, a future refactor that swaps the merge loop for an order-
// blind walk (e.g. map iteration) would pass the both-present
// assertion while silently producing non-deterministic results on
// collision.
func TestApplyTemplatesPerNode_StacksSidePatches_LastWriterWins(t *testing.T) {
	dir := t.TempDir()

	anchor := filepath.Join(dir, "anchor.yaml")
	if err := os.WriteFile(anchor, []byte(
		"# talm: nodes=[\"1.2.3.4\"], templates=[\"templates/cp.yaml\"]\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}

	// Both side-patches set machine.network.hostname to different
	// values. Per last-writer-wins, side2's value must end up in
	// the merged config; side1's must NOT leak through.
	side1 := filepath.Join(dir, "side1.yaml")
	if err := os.WriteFile(side1, []byte(
		"machine:\n  network:\n    hostname: from-side1\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}

	side2 := filepath.Join(dir, "side2.yaml")
	if err := os.WriteFile(side2, []byte(
		"machine:\n  network:\n    hostname: from-side2\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}

	var applyBody []byte

	render := func(_ context.Context, _ *client.Client, _ engine.Options) ([]byte, error) {
		return []byte("version: v1alpha1\nmachine:\n  type: controlplane\n"), nil
	}
	apply := func(_ context.Context, _ *client.Client, data []byte) error {
		applyBody = append([]byte(nil), data...)

		return nil
	}

	if err := applyTemplatesPerNode(engine.Options{}, anchor, []string{side1, side2}, []string{testNodeAddrA}, fakeAuthOpenClient(context.Background()), render, apply); err != nil {
		t.Fatalf("applyTemplatesPerNode: %v", err)
	}

	final := string(applyBody)
	if !strings.Contains(final, "from-side2") {
		t.Errorf("last side-patch must win on collision; final config must include from-side2; got:\n%s", final)
	}
	if strings.Contains(final, "from-side1") {
		t.Errorf("earlier side-patch's value must be overwritten by the later patch's value; from-side1 leaked into final:\n%s", final)
	}
}

// TestApplyHelpText_DocumentsAnchorSidePatchContract pins that
// `talm apply --help` surfaces the #184 multi-file contract:
// first -f is the anchor (modelined, rooted), subsequent -f are
// side-patches stacked on top. Operators reading --help should
// not be surprised by the behaviour change from "batch independent
// applies" to "stacked composition".
func TestApplyHelpText_DocumentsAnchorSidePatchContract(t *testing.T) {
	if !strings.Contains(applyCmd.Long, "side-patch") {
		t.Errorf("apply Long must document side-patch semantics; got:\n%s", applyCmd.Long)
	}
	if !strings.Contains(applyCmd.Long, "anchor") {
		t.Errorf("apply Long must name the anchor concept; got:\n%s", applyCmd.Long)
	}

	fileFlag := applyCmd.Flags().Lookup("file")
	if fileFlag == nil {
		t.Fatal("apply --file flag missing")
	}
	if !strings.Contains(fileFlag.Usage, "anchor") || !strings.Contains(fileFlag.Usage, "side-patch") {
		t.Errorf("--file Usage must mention anchor + side-patch contract; got:\n%s", fileFlag.Usage)
	}
}

// TestApplyOneFile_MalformedModeline_SurfacesParseError pins the
// fix for the silent-routing bug: a file with a present-but-
// malformed modeline (e.g. typo `node` for `nodes`, broken JSON
// value, prose before the modeline) MUST surface the parse error
// to the operator rather than route into the direct-patch path
// where the real cause is hidden behind a misleading "no nodes"
// hint. fileHasTalmModeline now distinguishes
// modeline.ErrModelineNotFound (legitimate "no `# talm:` line")
// from any other parse failure.
func TestApplyOneFile_MalformedModeline_SurfacesParseError(t *testing.T) {
	dir := t.TempDir()

	file := filepath.Join(dir, "node.yaml")
	// `node=` (typo, missing `s`) — ParseModeline accepts the key
	// but stops short of mapping it to Config.Nodes. The real
	// cause we want to surface is malformed-JSON values; use a
	// broken JSON array which ParseModeline DOES reject.
	if err := os.WriteFile(file, []byte(
		"# talm: nodes=[broken json, endpoints=[\"1.2.3.4\"]\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}

	err := applyOneFile(file, nil)
	if err == nil {
		t.Fatal("expected parse error to surface; got nil")
	}

	msg := err.Error()
	if strings.Contains(msg, "no modeline in") || strings.Contains(msg, "no nodes available") {
		t.Errorf("malformed-modeline parse error must surface to the operator, not get rewritten as 'no nodes'; got:\n%s", msg)
	}
	if !strings.Contains(msg, "parsing modeline") {
		t.Errorf("error must name the parse step so the operator can locate the typo; got:\n%s", msg)
	}
}

// TestApplyOneFile_NonModelinedAnchorWithSidePatches_Rejected pins
// the failure mode for a chain whose first -f file lacks a
// modeline: rejected with a hint pointing at the missing modeline.
// Without this gate, talm would try to feed the non-modelined
// anchor through direct-patch mode while ignoring the side-patches
// entirely.
func TestApplyOneFile_NonModelinedAnchorWithSidePatches_Rejected(t *testing.T) {
	dir := t.TempDir()

	anchor := filepath.Join(dir, "anchor.yaml")
	if err := os.WriteFile(anchor, []byte(
		"machine:\n  type: controlplane\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}

	side := filepath.Join(dir, "side.yaml")
	if err := os.WriteFile(side, []byte("machine: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := applyOneFile(anchor, []string{side})
	if err == nil {
		t.Fatal("expected error: chain anchor without modeline must be rejected")
	}
	if !strings.Contains(err.Error(), "modeline") {
		t.Errorf("error must name the missing modeline; got: %v", err)
	}
}

// TestApplyOneFile_ModelinedSidePatches_RejectedWithLoopHint pins the
// guard against a silent breaking change. Before this gate, a chain
// of two modelined node files (`talm apply -f n1.yaml -f n2.yaml`,
// the kubectl-style multi-node-apply pattern) would silently treat
// n2 as a bytes-level patch stacked onto n1's render: n2's modeline
// became a YAML comment under MergeFileAsPatch, its body got merged
// into n1's targets, and n2's nodes/templates/endpoints were never
// reached. The gate rejects this shape loudly with a hint pointing
// at the per-file shell loop that produces independent applies.
func TestApplyOneFile_ModelinedSidePatches_RejectedWithLoopHint(t *testing.T) {
	dir := t.TempDir()

	anchor := filepath.Join(dir, "n1.yaml")
	if err := os.WriteFile(anchor, []byte(
		"# talm: nodes=[\"1.2.3.4\"], endpoints=[\"1.2.3.4\"], templates=[\"t.yaml\"]\n"+
			"machine:\n  type: controlplane\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}

	sideWithModeline := filepath.Join(dir, "n2.yaml")
	if err := os.WriteFile(sideWithModeline, []byte(
		"# talm: nodes=[\"5.6.7.8\"], endpoints=[\"5.6.7.8\"], templates=[\"t.yaml\"]\n"+
			"machine:\n  type: controlplane\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}

	err := applyOneFile(anchor, []string{sideWithModeline})
	if err == nil {
		t.Fatal("expected error: modelined side-patch must be rejected to prevent silent multi-node-apply breakage")
	}

	msg := err.Error()
	if !strings.Contains(msg, "side-patch") || !strings.Contains(msg, "modeline") {
		t.Errorf("error must name both the side-patch role and the modeline cause; got: %v", err)
	}

	hints := cerrors.GetAllHints(err)
	joinedHints := strings.Join(hints, "\n")
	if !strings.Contains(joinedHints, "for f in") && !strings.Contains(joinedHints, "loop") {
		t.Errorf("hint must point at the per-file shell loop pattern as the correct multi-node-apply shape; got hints:\n%s", joinedHints)
	}
}

// TestApplyOneFile_MalformedModelineInSidePatch_SurfacesParseError
// pins that a side-patch's malformed modeline surfaces as a parse
// error, not silently merged into the anchor's render. Operators
// should fix the typo regardless of which file slot it lives in.
func TestApplyOneFile_MalformedModelineInSidePatch_SurfacesParseError(t *testing.T) {
	dir := t.TempDir()

	anchor := filepath.Join(dir, "anchor.yaml")
	if err := os.WriteFile(anchor, []byte(
		"# talm: nodes=[\"1.2.3.4\"], endpoints=[\"1.2.3.4\"], templates=[\"t.yaml\"]\n"+
			"machine:\n  type: controlplane\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}

	badSide := filepath.Join(dir, "bad-side.yaml")
	if err := os.WriteFile(badSide, []byte(
		"# talm: nodes=[broken json, endpoints=[\"1.2.3.4\"]\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}

	err := applyOneFile(anchor, []string{badSide})
	if err == nil {
		t.Fatal("expected error: malformed modeline in side-patch must surface")
	}
	if !strings.Contains(err.Error(), "parsing modeline in side-patch") {
		t.Errorf("error must name the side-patch parse step; got: %v", err)
	}
}
