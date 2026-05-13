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
	"strings"
	"testing"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/cozystack/talm/pkg/applycheck"
	machineapi "github.com/siderolabs/talos/pkg/machinery/api/machine"
)

const renderedV1_11 = `version: v1alpha1
machine:
  type: controlplane
  install:
    disk: /dev/sda
  network:
    interfaces:
      - interface: eth9999
`

const renderedV1_12Multidoc = `version: v1alpha1
machine:
  type: controlplane
  install:
    diskSelector:
      model: "Samsung*"
---
apiVersion: v1alpha1
kind: LinkConfig
name: eth0
`

func stubLinksDisksReader(snapshot applycheck.HostSnapshot, ok bool) linksDisksReader {
	return func(context.Context) (applycheck.HostSnapshot, bool, error) {
		return snapshot, ok, nil
	}
}

// stubLinksDisksReaderErr returns a reader that simulates a transient
// COSI read failure. Used to pin the "blocker wraps the underlying
// cause" contract, distinguishing transient failures from auth-disallowed.
func stubLinksDisksReaderErr(err error) linksDisksReader {
	return func(context.Context) (applycheck.HostSnapshot, bool, error) {
		return applycheck.HostSnapshot{}, false, err
	}
}

func stubMachineConfigReader(configBytes []byte, ok bool, err error) machineConfigReader {
	return func(context.Context) ([]byte, bool, error) {
		return configBytes, ok, err
	}
}

func TestPreflightValidateResources_LinkMissing_BlocksWithHint(t *testing.T) {
	t.Parallel()

	snapshot := applycheck.HostSnapshot{
		Links: []string{"eth0", "eth1"},
		Disks: []applycheck.DiskInfo{{DevPath: "/dev/sda", Model: "Samsung"}},
	}

	buf := &bytes.Buffer{}
	err := preflightValidateResources(
		context.Background(),
		stubLinksDisksReader(snapshot, true),
		[]byte(renderedV1_11),
		buf,
	)
	if err == nil {
		t.Fatal("expected blocker error for missing eth9999, got nil")
	}

	out := buf.String()
	if !strings.Contains(out, "eth9999") {
		t.Errorf("output should cite the offending name, got %q", out)
	}

	if !strings.Contains(out, "eth0") || !strings.Contains(out, "eth1") {
		t.Errorf("output should list available links as a hint, got %q", out)
	}
}

func TestPreflightValidateResources_AllRefsResolvable_Passes(t *testing.T) {
	t.Parallel()

	snapshot := applycheck.HostSnapshot{
		Links: []string{"eth0"},
		Disks: []applycheck.DiskInfo{{DevPath: "/dev/sda", Model: "Samsung 980 Pro"}},
	}

	err := preflightValidateResources(
		context.Background(),
		stubLinksDisksReader(snapshot, true),
		[]byte(renderedV1_12Multidoc),
		&bytes.Buffer{},
	)
	if err != nil {
		t.Errorf("expected nil error when every ref resolves, got %v", err)
	}
}

func TestPreflightValidateResources_ReaderFails_SurfacesAndBlocks(t *testing.T) {
	t.Parallel()

	err := preflightValidateResources(
		context.Background(),
		stubLinksDisksReader(applycheck.HostSnapshot{}, false),
		[]byte(renderedV1_11),
		&bytes.Buffer{},
	)
	if err == nil {
		t.Fatal("expected error when reader returns ok=false, got nil")
	}

	if !strings.Contains(err.Error(), "unavailable") {
		t.Errorf("expected 'unavailable' in error for ok=false path, got err=%v", err)
	}
}

// TestPreflightValidateResources_TransientErr_WrapsUnderlying pins the
// three-valued reader contract: a transient (err != nil) read failure
// must surface the underlying cause to the operator, NOT the
// "config is wrong" blocker class used for ok=false. Without the
// distinction a 2s COSI timeout looked like a config defect.
func TestPreflightValidateResources_TransientErr_WrapsUnderlying(t *testing.T) {
	t.Parallel()

	transient := errors.New("apid: connection reset")
	err := preflightValidateResources(
		context.Background(),
		stubLinksDisksReaderErr(transient),
		[]byte(renderedV1_11),
		&bytes.Buffer{},
	)
	if err == nil {
		t.Fatal("expected error when reader returns transient err, got nil")
	}

	if !strings.Contains(err.Error(), "connection reset") {
		t.Errorf("expected underlying transient error in chain, got %v", err)
	}

	if !strings.Contains(err.Error(), "reading host links/disks snapshot") {
		t.Errorf("expected 'reading host links/disks snapshot' context wrap, got %v", err)
	}
}

func TestPreviewDrift_RemovedLinkSurfacesInOutput(t *testing.T) {
	t.Parallel()

	current := []byte(`version: v1alpha1
machine:
  type: controlplane
---
apiVersion: v1alpha1
kind: LinkConfig
name: eth1
up: true
`)
	desired := []byte(`version: v1alpha1
machine:
  type: controlplane
---
apiVersion: v1alpha1
kind: LinkConfig
name: eth0
up: true
`)

	buf := &bytes.Buffer{}
	err := previewDrift(
		context.Background(),
		stubMachineConfigReader(current, true, nil),
		desired,
		"",
		buf,
		false,
	)
	if err != nil {
		t.Fatalf("previewDrift error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "- LinkConfig") || !strings.Contains(out, "eth1") {
		t.Errorf("output should surface '- LinkConfig{name: eth1}', got %q", out)
	}

	if !strings.Contains(out, "+ LinkConfig") || !strings.Contains(out, "eth0") {
		t.Errorf("output should surface '+ LinkConfig{name: eth0}', got %q", out)
	}
}

func TestPreviewDrift_InsecurePath_DegradesGracefully(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	err := previewDrift(
		context.Background(),
		stubMachineConfigReader(nil, false, nil),
		[]byte(renderedV1_12Multidoc),
		"",
		buf,
		false,
	)
	if err != nil {
		t.Errorf("previewDrift on insecure path should not block, got err=%v", err)
	}

	if !strings.Contains(buf.String(), "maintenance connection") {
		t.Errorf("expected 'maintenance connection' explanatory line, got %q", buf.String())
	}
}

func TestVerifyAppliedState_Match_NoError(t *testing.T) {
	t.Parallel()

	sent := []byte(renderedV1_12Multidoc)
	err := verifyAppliedState(
		context.Background(),
		stubMachineConfigReader(sent, true, nil),
		sent,
		"",
		&bytes.Buffer{},
		false,
	)
	if err != nil {
		t.Errorf("verifyAppliedState should accept matching configs, got err=%v", err)
	}
}

func TestVerifyAppliedState_Divergence_BlocksWithDetails(t *testing.T) {
	t.Parallel()

	sent := []byte(`version: v1alpha1
machine:
  type: controlplane
---
apiVersion: v1alpha1
kind: LinkConfig
name: eth0
up: true
`)
	onNode := []byte(`version: v1alpha1
machine:
  type: controlplane
---
apiVersion: v1alpha1
kind: LinkConfig
name: eth0
up: false
`)

	buf := &bytes.Buffer{}
	err := verifyAppliedState(
		context.Background(),
		stubMachineConfigReader(onNode, true, nil),
		sent,
		"",
		buf,
		false,
	)
	if err == nil {
		t.Fatal("verifyAppliedState should block on divergence, got nil error")
	}

	out := buf.String()
	if !strings.Contains(out, "LinkConfig") || !strings.Contains(out, "eth0") {
		t.Errorf("output should name the diverging doc, got %q", out)
	}
}

// TestVerifyAppliedState_ReaderError_Blocks pins the contract: a
// non-nil err from the reader on the auth path is a hard blocker.
// The verify gate exists specifically to catch silent rollbacks, so
// swallowing a transient read error would defeat the purpose.
func TestVerifyAppliedState_ReaderError_Blocks(t *testing.T) {
	t.Parallel()

	transient := errors.New("apid: connection reset")
	err := verifyAppliedState(
		context.Background(),
		stubMachineConfigReader(nil, false, transient),
		[]byte(renderedV1_12Multidoc),
		"",
		&bytes.Buffer{},
		false,
	)
	if err == nil {
		t.Fatal("expected error on reader failure, got nil")
	}

	if !strings.Contains(err.Error(), "connection reset") {
		t.Errorf("expected underlying error in chain, got %v", err)
	}
}

func TestVerifyAppliedState_InsecurePath_NoBlock(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	err := verifyAppliedState(
		context.Background(),
		stubMachineConfigReader(nil, false, nil),
		[]byte(renderedV1_12Multidoc),
		"",
		buf,
		false,
	)
	if err != nil {
		t.Errorf("verifyAppliedState on insecure path should not block, got err=%v", err)
	}

	if !strings.Contains(buf.String(), "maintenance connection") {
		t.Errorf("expected 'maintenance connection' explanatory line, got %q", buf.String())
	}
}

// TestShouldRunDriftPreview_RespectsOnlySkipFlag pins the Phase 2A
// scheduling contract: dry-run does NOT skip the drift preview (the
// gate is read-only and dry-run wants the diff); only the explicit
// --skip-drift-preview flag suppresses it. This pins the
// fix(commands): run Phase 2A drift preview on --dry-run commit
// against future flips that might re-introduce the dry-run skip.
func TestShouldRunDriftPreview_RespectsOnlySkipFlag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		skip bool
		want bool
	}{
		{"default: runs", false, true},
		{"--skip-drift-preview: skipped", true, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := shouldRunDriftPreview(tc.skip)
			if got != tc.want {
				t.Errorf("shouldRunDriftPreview(skip=%v) = %v, want %v", tc.skip, got, tc.want)
			}
		})
	}
}

// TestMultisetDiff_StringKeyedSlices_StableDedup pins the contract
// for the schema shape MachineConfig actually surfaces today: a
// slice of strings (certSANs, peers, routes, link names). The %v
// key function is stable for strings, so duplicate-dedup works.
func TestMultisetDiff_StringKeyedSlices_StableDedup(t *testing.T) {
	t.Parallel()

	oldSlice := []any{"127.0.0.1", "127.0.0.1", "192.0.2.5"}
	newSlice := []any{"127.0.0.1", "192.0.2.5"}

	removed, added := multisetDiff(oldSlice, newSlice)
	if len(removed) != 1 || removed[0] != "127.0.0.1" {
		t.Errorf("removed: want [127.0.0.1], got %v", removed)
	}

	if len(added) != 0 {
		t.Errorf("added: want [], got %v", added)
	}
}

// TestMultisetDiff_MapElementsStableAcrossRuns pins the dedup
// behaviour on slice-of-map inputs — the shape Phase 2A produces
// when diffing v1.11 nested form (e.g. machine.network.interfaces[])
// or any other array-of-object configuration. The dedup relies on
// fmt's %v producing the same string for two equal-by-content maps
// across runs; `fmt` sorts map keys for stable output, so this
// holds for map[string]any / map[int]any / map[any]any equally.
//
// The test runs 100 times to surface any iteration-order
// non-determinism: if a future Go stdlib change drops the key-sort
// guarantee, this test starts flaking and multisetDiff's key
// function needs a structural canonicaliser instead.
func TestMultisetDiff_MapElementsStableAcrossRuns(t *testing.T) {
	t.Parallel()

	mapElem := map[string]any{"interface": "eth0", "mtu": 1500, "dhcp": true}

	for run := range 100 {
		oldSlice := []any{mapElem, mapElem}
		newSlice := []any{mapElem}

		removed, added := multisetDiff(oldSlice, newSlice)
		if len(removed) != 1 || len(added) != 0 {
			t.Fatalf("run %d: map-element dedup unstable — removed=%v, added=%v; if you see this, fmt's stable-map-print guarantee may have changed", run, removed, added)
		}
	}
}

// TestReadWithFreshTimeout_DoesNotLeakBudgetAcrossCalls pins the
// per-call-budget property used by cosiLinksDisksReader for its
// two COSI ListAll reads. Sharing a single context.WithTimeout
// across both reads would leak the first read's elapsed time
// into the second's budget: on a slow node a 1.5s links read
// would leave the disks read with sub-second budget and produce
// a false transient-timeout blocker.
//
// The test runs the helper twice — the first call burns most of
// the timeout, the second call inspects its own context's
// deadline and asserts the deadline is approximately the full
// budget from "now", not the residue after the first call.
func TestReadWithFreshTimeout_DoesNotLeakBudgetAcrossCalls(t *testing.T) {
	t.Parallel()

	parent := context.Background()
	budget := 200 * time.Millisecond

	// First call: burn ~3/4 of the budget. Sleeps inside the op.
	_, _ = readWithFreshTimeout(parent, budget, func(_ context.Context) (struct{}, error) {
		time.Sleep(150 * time.Millisecond)

		return struct{}{}, nil
	})

	// Second call: inspect the fresh context's deadline. It should be
	// approximately parent's "now" + full budget — proof that the
	// first call's elapsed time did NOT leak into this budget.
	checkpoint := time.Now()

	_, _ = readWithFreshTimeout(parent, budget, func(ctx context.Context) (struct{}, error) {
		deadline, hasDeadline := ctx.Deadline()
		if !hasDeadline {
			t.Fatal("second call should have a deadline; got none")
		}

		remaining := time.Until(deadline)
		// Tolerate 25ms of scheduling jitter. A leaked budget would
		// leave the second call with budget - 150ms = 50ms, well
		// below the budget - 25ms = 175ms floor below.
		if remaining < budget-25*time.Millisecond {
			t.Errorf("budget leaked: second-call deadline at %v from now, expected ~%v (budget=%v, checkpoint=%v)",
				remaining, budget, budget, checkpoint)
		}

		return struct{}{}, nil
	})
}

// TestPrintDriftPreview_SliceSetDiff_RemovesDuplicate pins the
// set-diff shape: when both sides are slices, the preview shows
// `removed [...]` / `added [...]` buckets instead of dumping the
// full slice twice. The certSANs duplicate-cleanup case shrinks
// from a 50+ char two-slice dump to a focused
// `removed [127.0.0.1]` line. Multiset semantics — one extra copy
// of 127.0.0.1 in Old surfaces as exactly one removal.
func TestPrintDriftPreview_SliceSetDiff_RemovesDuplicate(t *testing.T) {
	t.Parallel()

	changes := []applycheck.Change{{
		ID: applycheck.DocID{Kind: "MachineConfig"},
		Op: applycheck.OpUpdate,
		Fields: []applycheck.FieldChange{{
			Path:   "cluster.apiServer.certSANs",
			Old:    []any{"127.0.0.1", "127.0.0.1", "192.0.2.5"},
			New:    []any{"127.0.0.1", "192.0.2.5"},
			HasOld: true,
			HasNew: true,
		}},
	}}

	buf := &bytes.Buffer{}
	printDriftPreview(buf, "drift:", changes, false)

	out := buf.String()
	if !strings.Contains(out, "removed [127.0.0.1]") {
		t.Errorf("slice set-diff should surface the removed duplicate, got:\n%s", out)
	}

	if strings.Contains(out, "added [") {
		t.Errorf("no elements were added; the bucket should be omitted, got:\n%s", out)
	}

	// The full-slice fallback must not fire.
	if strings.Contains(out, "[127.0.0.1, 127.0.0.1, 192.0.2.5]") {
		t.Errorf("full-slice render should be replaced by set-diff, got:\n%s", out)
	}
}

// TestPrintDriftPreview_SliceSetDiff_AddOnly pins the "added only"
// case: a new SAN appended to certSANs surfaces as
// `added [new.example]` with no `removed` bucket.
func TestPrintDriftPreview_SliceSetDiff_AddOnly(t *testing.T) {
	t.Parallel()

	changes := []applycheck.Change{{
		ID: applycheck.DocID{Kind: "MachineConfig"},
		Op: applycheck.OpUpdate,
		Fields: []applycheck.FieldChange{{
			Path:   "cluster.apiServer.certSANs",
			Old:    []any{"127.0.0.1"},
			New:    []any{"127.0.0.1", "192.0.2.5"},
			HasOld: true,
			HasNew: true,
		}},
	}}

	buf := &bytes.Buffer{}
	printDriftPreview(buf, "drift:", changes, false)

	out := buf.String()
	if !strings.Contains(out, "added [192.0.2.5]") {
		t.Errorf("slice set-diff should surface the added entry, got:\n%s", out)
	}

	if strings.Contains(out, "removed [") {
		t.Errorf("no elements were removed; the bucket should be omitted, got:\n%s", out)
	}
}

// TestPrintDriftPreview_SliceSetDiff_ReorderOnly pins the equal-
// multiset reorder case: when both slices contain the same
// elements with the same multiplicities but in a different order,
// reflect.DeepEqual flags an OpUpdate but the set diff is empty.
// Surface a `(reordered, same elements)` note so the operator
// isn't left wondering what changed.
func TestPrintDriftPreview_SliceSetDiff_ReorderOnly(t *testing.T) {
	t.Parallel()

	changes := []applycheck.Change{{
		ID: applycheck.DocID{Kind: "MachineConfig"},
		Op: applycheck.OpUpdate,
		Fields: []applycheck.FieldChange{{
			Path:   "cluster.apiServer.certSANs",
			Old:    []any{"127.0.0.1", "192.0.2.5"},
			New:    []any{"192.0.2.5", "127.0.0.1"},
			HasOld: true,
			HasNew: true,
		}},
	}}

	buf := &bytes.Buffer{}
	printDriftPreview(buf, "drift:", changes, false)

	out := buf.String()
	if !strings.Contains(out, "reordered") {
		t.Errorf("equal-multiset reorder should surface as 'reordered', got:\n%s", out)
	}
}

// TestPreviewDrift_MultiNode_HeaderCarriesNodeID pins the per-node
// header on Phase 2A output. Multi-node apply calls previewDrift /
// verifyAppliedState in a loop, sharing one stderr — without a node
// identifier on the header, an operator scanning the output cannot
// tell which node owns which diff. The header for a non-empty
// nodeID must include the node literal so multi-node output is
// disambiguated.
func TestPreviewDrift_MultiNode_HeaderCarriesNodeID(t *testing.T) {
	t.Parallel()

	current := []byte(`version: v1alpha1
machine:
  type: controlplane
  install:
    disk: /dev/sda
`)
	desired := []byte(`version: v1alpha1
machine:
  type: controlplane
  install:
    disk: /dev/sdb
`)

	buf := &bytes.Buffer{}
	err := previewDrift(
		context.Background(),
		stubMachineConfigReader(current, true, nil),
		desired,
		"192.0.2.10",
		buf,
		false,
	)
	if err != nil {
		t.Fatalf("previewDrift error: %v", err)
	}

	if !strings.Contains(buf.String(), "(node 192.0.2.10)") {
		t.Errorf("non-empty nodeID must appear in the drift-preview header, got:\n%s", buf.String())
	}
}

// TestVerifyAppliedState_MultiNode_HeaderCarriesNodeID is the
// Phase 2B counterpart of TestPreviewDrift_MultiNode_HeaderCarriesNodeID:
// the post-apply divergence header must also disambiguate per node
// so an operator scanning stderr from a multi-node apply that
// diverged on two nodes can act on each one independently.
func TestVerifyAppliedState_MultiNode_HeaderCarriesNodeID(t *testing.T) {
	t.Parallel()

	sent := []byte(`version: v1alpha1
machine:
  type: controlplane
---
apiVersion: v1alpha1
kind: LinkConfig
name: eth0
up: true
`)
	onNode := []byte(`version: v1alpha1
machine:
  type: controlplane
---
apiVersion: v1alpha1
kind: LinkConfig
name: eth0
up: false
`)

	buf := &bytes.Buffer{}
	err := verifyAppliedState(
		context.Background(),
		stubMachineConfigReader(onNode, true, nil),
		sent,
		"192.0.2.11",
		buf,
		false,
	)
	if err == nil {
		t.Fatal("expected divergence to surface as an error")
	}

	if !strings.Contains(buf.String(), "(node 192.0.2.11)") {
		t.Errorf("non-empty nodeID must appear in the post-apply divergence header, got:\n%s", buf.String())
	}
}

// TestPreviewDrift_EmptyNodeID_HeaderUnchanged pins the single-node
// case: when nodeID is "" (the implicit-single-node template path),
// the header stays bare so existing output formats are preserved.
func TestPreviewDrift_EmptyNodeID_HeaderUnchanged(t *testing.T) {
	t.Parallel()

	desired := []byte(`version: v1alpha1
machine:
  type: controlplane
  install:
    disk: /dev/sdb
`)
	current := []byte(`version: v1alpha1
machine:
  type: controlplane
  install:
    disk: /dev/sda
`)

	buf := &bytes.Buffer{}
	err := previewDrift(
		context.Background(),
		stubMachineConfigReader(current, true, nil),
		desired,
		"",
		buf,
		false,
	)
	if err != nil {
		t.Fatalf("previewDrift error: %v", err)
	}

	if strings.Contains(buf.String(), "(node ") {
		t.Errorf("empty nodeID should leave the header bare, got:\n%s", buf.String())
	}
}

// TestPrintDriftPreview_SliceFlowStyle_AbsentOnOneSide pins the
// flow-style fallback path that the set-diff layer does NOT cover:
// when a slice appears (or disappears) entirely, we render it as a
// YAML flow list `[a, b, c]` instead of Go's `[a b c]`. The set-diff
// path covers slice-vs-slice diffs; this test covers the slice
// vs. (absent) case where the operator still needs to see the
// list contents.
func TestPrintDriftPreview_SliceFlowStyle_AbsentOnOneSide(t *testing.T) {
	t.Parallel()

	changes := []applycheck.Change{{
		ID: applycheck.DocID{Kind: "MachineConfig"},
		Op: applycheck.OpUpdate,
		Fields: []applycheck.FieldChange{{
			Path:   "cluster.apiServer.certSANs",
			Old:    nil,
			New:    []any{"127.0.0.1", "192.0.2.5"},
			HasOld: false,
			HasNew: true,
		}},
	}}

	buf := &bytes.Buffer{}
	printDriftPreview(buf, "drift:", changes, false)

	out := buf.String()
	if strings.Contains(out, "[127.0.0.1 192.0.2.5]") {
		t.Errorf("slice rendered in Go fmt %%v style, want YAML flow:\n%s", out)
	}

	if !strings.Contains(out, "[127.0.0.1, 192.0.2.5]") {
		t.Errorf("slice should render as YAML flow list, got:\n%s", out)
	}

	if !strings.Contains(out, "(absent) ->") {
		t.Errorf("absent-old should render as '(absent) ->', got:\n%s", out)
	}
}

// TestPrintDriftPreview_MapFieldChange_RendersFlowStyle pins the
// map case: Go's %v produces "map[a:1 b:2]" which is awkward and
// can mislead the reader into thinking "a:1" is a single token.
// YAML flow mapping "{a: 1, b: 2}" mirrors how the same value
// would be typed in a Helm values file.
func TestPrintDriftPreview_MapFieldChange_RendersFlowStyle(t *testing.T) {
	t.Parallel()

	changes := []applycheck.Change{{
		ID: applycheck.DocID{Kind: "MachineConfig"},
		Op: applycheck.OpUpdate,
		Fields: []applycheck.FieldChange{{
			Path:   "machine.nodeLabels",
			Old:    map[string]any{"role": "control-plane"},
			New:    map[string]any{"role": "control-plane", "tier": "primary"},
			HasOld: true,
			HasNew: true,
		}},
	}}

	buf := &bytes.Buffer{}
	printDriftPreview(buf, "drift:", changes, false)

	out := buf.String()
	if strings.Contains(out, "map[role:control-plane]") {
		t.Errorf("map rendered in Go fmt %%v style, want YAML flow:\n%s", out)
	}

	if !strings.Contains(out, "{role: control-plane}") {
		t.Errorf("map should render as YAML flow mapping, got:\n%s", out)
	}
}

// TestPrintDriftPreview_ScalarFieldChange_StaysInline pins the
// scalar case: short string / int / bool values render inline
// without yaml-quoting noise. The operator sees
// "cluster.network.dnsDomain: cozy.local -> cozy.example"
// the same as before.
func TestPrintDriftPreview_ScalarFieldChange_StaysInline(t *testing.T) {
	t.Parallel()

	changes := []applycheck.Change{{
		ID: applycheck.DocID{Kind: "MachineConfig"},
		Op: applycheck.OpUpdate,
		Fields: []applycheck.FieldChange{{
			Path:   "cluster.network.dnsDomain",
			Old:    "cozy.local",
			New:    "cozy.example",
			HasOld: true,
			HasNew: true,
		}},
	}}

	buf := &bytes.Buffer{}
	printDriftPreview(buf, "drift:", changes, false)

	out := buf.String()
	if !strings.Contains(out, "cozy.local -> cozy.example") {
		t.Errorf("scalar field should render inline, got:\n%s", out)
	}
}

// TestShouldRunDriftPreview_ModeAgnostic pins the Phase 2A invariant
// that the preview runs for EVERY apply mode. The preview is read-only
// (a Diff against the on-node MachineConfig), so reboot/staged/try/auto/
// no-reboot all benefit from seeing it. The matrix below iterates every
// apply mode and asserts the predicate ignores it — if a future change
// adds mode-gating, this test forces the author to update the matrix
// (and reconsider why the read-only preview should ever be skipped).
//
// The matrix is paired with shouldRunPostApplyVerify_RespectsModeAndDryRun
// where mode-gating IS the correct contract (the verify can race
// against rollback or reach a stale snapshot). Keep them next to each
// other so the divergence is obvious.
func TestShouldRunDriftPreview_ModeAgnostic(t *testing.T) {
	t.Parallel()

	modes := []machineapi.ApplyConfigurationRequest_Mode{
		machineapi.ApplyConfigurationRequest_REBOOT,
		machineapi.ApplyConfigurationRequest_NO_REBOOT,
		machineapi.ApplyConfigurationRequest_AUTO,
		machineapi.ApplyConfigurationRequest_STAGED,
		machineapi.ApplyConfigurationRequest_TRY,
	}

	for _, mode := range modes {
		t.Run(mode.String(), func(t *testing.T) {
			t.Parallel()

			// Phase 2A is mode-agnostic by design: the predicate
			// signature `(skip bool) bool` deliberately excludes
			// mode. The mode loop here documents the invariant for
			// future readers — if somebody adds a `mode` parameter
			// to shouldRunDriftPreview, this test stops compiling
			// and forces a conscious decision.
			if !shouldRunDriftPreview(false) {
				t.Errorf("Phase 2A must run for mode=%v, got skip", mode)
			}

			if shouldRunDriftPreview(true) {
				t.Errorf("--skip-drift-preview must suppress Phase 2A for mode=%v, got run", mode)
			}
		})
	}
}

// TestShouldRunPostApplyVerify_RespectsModeAndDryRun pins the post-apply
// gate's skip predicate. staged/try modes have contracts that diverge
// from 'sent == on-node' (staged: not yet active until reboot; try:
// auto-rollback), reboot kills the COSI connection mid-verify, AUTO
// promotes to REBOOT internally when the change requires it, and
// dry-run obviously didn't apply anything. The gate must skip in
// those cases regardless of the skip flag — only --mode=no-reboot
// reliably leaves the node up with a stable ActiveID for the verify
// to read.
func TestShouldRunPostApplyVerify_RespectsModeAndDryRun(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		mode machineapi.ApplyConfigurationRequest_Mode
		dry  bool
		skip bool
		want bool
	}{
		// no-reboot leaves the node up after apply, so the verify
		// reaches a stable COSI snapshot. The only mode where verify
		// runs by default.
		{"no_reboot runs", machineapi.ApplyConfigurationRequest_NO_REBOOT, false, false, true},
		// Modes that reach the node only intermittently or never on
		// ActiveID: skipped.
		{"reboot skipped", machineapi.ApplyConfigurationRequest_REBOOT, false, false, false},
		{"staged skipped", machineapi.ApplyConfigurationRequest_STAGED, false, false, false},
		{"try skipped", machineapi.ApplyConfigurationRequest_TRY, false, false, false},
		// AUTO is skipped because Talos's apply-server promotes it to
		// REBOOT internally when the change requires one. The verify
		// would race the reboot; same shape as the explicit REBOOT skip.
		// Pinned to keep a future maintainer from "fixing" AUTO back to
		// runs and re-introducing the race.
		{"auto skipped (Talos promotes to REBOOT internally)", machineapi.ApplyConfigurationRequest_AUTO, false, false, false},
		{"dry-run skipped", machineapi.ApplyConfigurationRequest_NO_REBOOT, true, false, false},
		{"skip flag honoured", machineapi.ApplyConfigurationRequest_NO_REBOOT, false, true, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := shouldRunPostApplyVerify(tc.mode, tc.dry, tc.skip)
			if got != tc.want {
				t.Errorf("shouldRunPostApplyVerify(mode=%v, dry=%v, skip=%v) = %v, want %v", tc.mode, tc.dry, tc.skip, got, tc.want)
			}
		})
	}
}

// TestPreviewDrift_MaintenanceMessage_CarriesNodePrefix pins the
// per-node prefix on the maintenance-connection warning emitted by
// previewDrift when the reader returns ok=false. The drift / divergence
// headers already disambiguate per node via headerWithNode; the
// maintenance line lagged behind, producing identical bare warnings on
// every node in a multi-node insecure apply with no way for the
// operator to tell which node each line came from. Mirrors
// TestPreviewDrift_MultiNode_HeaderCarriesNodeID — same expectation
// shape, different emission site.
func TestPreviewDrift_MaintenanceMessage_CarriesNodePrefix(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	err := previewDrift(
		context.Background(),
		stubMachineConfigReader(nil, false, nil),
		[]byte(renderedV1_12Multidoc),
		"192.0.2.10",
		buf,
		false,
	)
	if err != nil {
		t.Fatalf("previewDrift on insecure path should not block, got err=%v", err)
	}

	want := "node 192.0.2.10: talm: " + maintenanceConnectionMessage
	if !strings.Contains(buf.String(), want) {
		t.Errorf("non-empty nodeID must prefix the maintenance-connection line; want substring %q, got:\n%s", want, buf.String())
	}
}

// TestVerifyAppliedState_MaintenanceMessage_CarriesNodePrefix is the
// Phase 2B counterpart of TestPreviewDrift_MaintenanceMessage_CarriesNodePrefix.
// Multi-node apply hits both previewDrift (Phase 2A) and
// verifyAppliedState (Phase 2B) in the same loop, so both maintenance
// emissions need the same per-node disambiguation.
func TestVerifyAppliedState_MaintenanceMessage_CarriesNodePrefix(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	err := verifyAppliedState(
		context.Background(),
		stubMachineConfigReader(nil, false, nil),
		[]byte(renderedV1_12Multidoc),
		"192.0.2.11",
		buf,
		false,
	)
	if err != nil {
		t.Errorf("verifyAppliedState on insecure path should not block, got err=%v", err)
	}

	want := "node 192.0.2.11: talm: " + maintenanceConnectionMessage
	if !strings.Contains(buf.String(), want) {
		t.Errorf("non-empty nodeID must prefix the maintenance-connection line; want substring %q, got:\n%s", want, buf.String())
	}
}

// TestPreflightValidateResources_NetAddrFinding_Blocks pins the
// Phase 1 integration of WalkNetAddrFindings: a rendered config with
// a malformed WireguardConfig peer endpoint must block before the
// apply RPC. Without this pin, the walker could regress to "called
// but findings discarded" while the unit tests in pkg/applycheck/
// keep passing.
func TestPreflightValidateResources_NetAddrFinding_Blocks(t *testing.T) {
	t.Parallel()

	snapshot := applycheck.HostSnapshot{
		Links: []string{"eth0", "eth1"},
		Disks: []applycheck.DiskInfo{{DevPath: "/dev/sda"}},
	}

	rendered := []byte(`version: v1alpha1
machine:
  type: controlplane
  install:
    disk: /dev/sda
---
apiVersion: v1alpha1
kind: WireguardConfig
name: wg-broken
peers:
  - publicKey: ZZZ
    endpoint: notavalid:endpoint
`)

	buf := &bytes.Buffer{}
	err := preflightValidateResources(
		context.Background(),
		stubLinksDisksReader(snapshot, true),
		rendered,
		buf,
	)
	if err == nil {
		t.Fatal("expected malformed Wireguard peer endpoint to block Phase 1, got nil")
	}

	out := buf.String()
	if !strings.Contains(out, "WireguardConfig.peers[0].endpoint") {
		t.Errorf("preflight output should cite the offending field path, got %q", out)
	}
}

// TestPreflightValidateResources_NetAddrFinding_StaticHostConfig_Blocks
// pins the Phase 1 integration of WalkNetAddrFindings for the
// StaticHostConfig kind. Walker-level unit tests cover the handler
// in isolation; this test exercises the full pipeline
// preflightValidateResources -> applycheck.WalkNetAddrFindings ->
// finding -> printFinding output -> Phase 1 blocker error. Without
// this pin, a walker integration regression for StaticHostConfig
// could pass walker unit tests while production silently no-ops.
func TestPreflightValidateResources_NetAddrFinding_StaticHostConfig_Blocks(t *testing.T) {
	t.Parallel()

	snapshot := applycheck.HostSnapshot{
		Links: []string{"eth0"},
		Disks: []applycheck.DiskInfo{{DevPath: "/dev/sda"}},
	}

	rendered := []byte(`version: v1alpha1
machine:
  type: controlplane
  install:
    disk: /dev/sda
---
apiVersion: v1alpha1
kind: StaticHostConfig
name: 999.999.0.1
hostnames:
  - foo.example
`)

	buf := &bytes.Buffer{}
	err := preflightValidateResources(
		context.Background(),
		stubLinksDisksReader(snapshot, true),
		rendered,
		buf,
	)
	if err == nil {
		t.Fatal("expected malformed StaticHostConfig.name to block Phase 1, got nil")
	}

	out := buf.String()
	if !strings.Contains(out, "StaticHostConfig.name") {
		t.Errorf("preflight output should cite the offending field path; got %q", out)
	}

	if !strings.Contains(out, "999.999.0.1") {
		t.Errorf("preflight output should cite the offending value; got %q", out)
	}
}

// TestPreflightValidateResources_NetAddrFinding_NetworkRuleConfig_Blocks
// pins the Phase 1 integration for the NetworkRuleConfig kind.
// Exercises both subnet and except validation paths through the
// full pipeline. Two malformed entries (one bad subnet + one bad
// except next to a valid subnet) must produce TWO blocker findings
// with distinct path indices, so an operator with multiple typos
// sees all of them in one Phase 1 pass.
func TestPreflightValidateResources_NetAddrFinding_NetworkRuleConfig_Blocks(t *testing.T) {
	t.Parallel()

	snapshot := applycheck.HostSnapshot{
		Links: []string{"eth0"},
		Disks: []applycheck.DiskInfo{{DevPath: "/dev/sda"}},
	}

	rendered := []byte(`version: v1alpha1
machine:
  type: controlplane
  install:
    disk: /dev/sda
---
apiVersion: v1alpha1
kind: NetworkRuleConfig
name: rule-broken
portSelector:
  ports: [22]
  protocol: tcp
ingress:
  - subnet: 192.0.2.0/24
  - subnet: notacidr
  - subnet: 10.0.0.0/24
    except: 999.999.0.1/30
`)

	buf := &bytes.Buffer{}
	err := preflightValidateResources(
		context.Background(),
		stubLinksDisksReader(snapshot, true),
		rendered,
		buf,
	)
	if err == nil {
		t.Fatal("expected malformed NetworkRuleConfig ingress fields to block Phase 1, got nil")
	}

	out := buf.String()
	if !strings.Contains(out, "ingress[1].subnet") {
		t.Errorf("preflight output should cite ingress[1].subnet (the malformed subnet); got %q", out)
	}

	if !strings.Contains(out, "ingress[2].except") {
		t.Errorf("preflight output should cite ingress[2].except (the malformed except); got %q", out)
	}

	if strings.Contains(out, "ingress[0]") {
		t.Errorf("ingress[0].subnet is valid (192.0.2.0/24) and must NOT be cited; got %q", out)
	}
}

// TestPreflightValidateResources_NetAddrFinding_ValidPasses pins the
// happy path of the new walker integration: a rendered config with
// valid host:port endpoints (IPv4 and IPv6) must NOT block. Catches
// the symmetric regression where the walker flags valid input as
// malformed.
func TestPreflightValidateResources_NetAddrFinding_ValidPasses(t *testing.T) {
	t.Parallel()

	snapshot := applycheck.HostSnapshot{
		Links: []string{"eth0", "eth1"},
		Disks: []applycheck.DiskInfo{{DevPath: "/dev/sda"}},
	}

	rendered := []byte(`version: v1alpha1
machine:
  type: controlplane
  install:
    disk: /dev/sda
---
apiVersion: v1alpha1
kind: WireguardConfig
name: wg-ok
peers:
  - publicKey: AAA
    endpoint: 192.0.2.10:51820
  - publicKey: BBB
    endpoint: "[2001:db8::1]:51820"
---
apiVersion: v1alpha1
kind: StaticHostConfig
name: 192.0.2.20
hostnames:
  - host1.example
`)

	buf := &bytes.Buffer{}
	err := preflightValidateResources(
		context.Background(),
		stubLinksDisksReader(snapshot, true),
		rendered,
		buf,
	)
	if err != nil {
		t.Errorf("valid net-addr fields should pass Phase 1, got err=%v, output=%q", err, buf.String())
	}
}

// TestPreviewDrift_MaintenanceMessage_EmptyNodeIDPreservesBareLine
// pins the single-node UX regression guard: when nodeID is empty (the
// implicit-single-node path), the maintenance line must stay bare —
// no leading "node : " prefix. Without this pin, a prefix-always-on
// implementation would produce "node : talm: ..." which is uglier
// than today's bare output for the common single-node case.
func TestPreviewDrift_MaintenanceMessage_EmptyNodeIDPreservesBareLine(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	err := previewDrift(
		context.Background(),
		stubMachineConfigReader(nil, false, nil),
		[]byte(renderedV1_12Multidoc),
		"",
		buf,
		false,
	)
	if err != nil {
		t.Fatalf("previewDrift on insecure path should not block, got err=%v", err)
	}

	out := buf.String()

	want := "talm: " + maintenanceConnectionMessage
	if !strings.Contains(out, want) {
		t.Errorf("empty nodeID: maintenance-connection line must remain present; want substring %q, got:\n%s", want, out)
	}

	if strings.Contains(out, "node : ") {
		t.Errorf("empty nodeID: must NOT produce 'node : ' prefix; got:\n%s", out)
	}
}
