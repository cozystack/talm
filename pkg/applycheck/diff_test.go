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

package applycheck_test

import (
	"strings"
	"testing"

	"github.com/cozystack/talm/pkg/applycheck"
)

const driftCurrentMultidoc = `version: v1alpha1
machine:
  type: controlplane
  install:
    disk: /dev/sda
---
apiVersion: v1alpha1
kind: LinkConfig
name: eth0
up: true
---
apiVersion: v1alpha1
kind: LinkConfig
name: eth1
up: true
---
apiVersion: v1alpha1
kind: HostnameConfig
hostname: cp-old
`

const driftDesiredMultidoc = `version: v1alpha1
machine:
  type: controlplane
  install:
    disk: /dev/sda
---
apiVersion: v1alpha1
kind: LinkConfig
name: eth0
up: true
---
apiVersion: v1alpha1
kind: LinkConfig
name: eth2
up: true
---
apiVersion: v1alpha1
kind: HostnameConfig
hostname: cp-new
`

func findChange(changes []applycheck.Change, kind, name string) (applycheck.Change, bool) {
	for _, c := range changes {
		if c.ID.Kind == kind && c.ID.Name == name {
			return c, true
		}
	}

	return applycheck.Change{}, false
}

func TestDiff_DetectsAddRemoveUpdateOnMultidoc(t *testing.T) {
	t.Parallel()

	changes, err := applycheck.Diff([]byte(driftCurrentMultidoc), []byte(driftDesiredMultidoc))
	if err != nil {
		t.Fatalf("Diff error: %v", err)
	}

	// eth0 unchanged.
	if c, ok := findChange(changes, "LinkConfig", "eth0"); !ok || c.Op != applycheck.OpEqual {
		t.Errorf("eth0 expected Op=Equal, got %+v ok=%v", c, ok)
	}

	// eth1 removed.
	if c, ok := findChange(changes, "LinkConfig", "eth1"); !ok || c.Op != applycheck.OpRemove {
		t.Errorf("eth1 expected Op=Remove, got %+v ok=%v", c, ok)
	}

	// eth2 added.
	if c, ok := findChange(changes, "LinkConfig", "eth2"); !ok || c.Op != applycheck.OpAdd {
		t.Errorf("eth2 expected Op=Add, got %+v ok=%v", c, ok)
	}

	// HostnameConfig updated (cp-old -> cp-new).
	c, ok := findChange(changes, "HostnameConfig", "")
	if !ok || c.Op != applycheck.OpUpdate {
		t.Fatalf("HostnameConfig expected Op=Update, got %+v ok=%v", c, ok)
	}

	if len(c.Fields) == 0 {
		t.Errorf("HostnameConfig update should carry leaf-level field changes, got empty")
	}

	var sawHostname bool

	for _, f := range c.Fields {
		if f.Path == "hostname" {
			sawHostname = true

			if f.Old != "cp-old" || f.New != "cp-new" {
				t.Errorf("hostname field change Old=%v New=%v, want cp-old/cp-new", f.Old, f.New)
			}
		}
	}

	if !sawHostname {
		t.Errorf("expected a FieldChange at path 'hostname', got %+v", c.Fields)
	}
}

func TestDiff_IdenticalInputs_AllEqual(t *testing.T) {
	t.Parallel()

	changes, err := applycheck.Diff([]byte(driftCurrentMultidoc), []byte(driftCurrentMultidoc))
	if err != nil {
		t.Fatalf("Diff error: %v", err)
	}

	for _, c := range changes {
		if c.Op != applycheck.OpEqual {
			t.Errorf("identical inputs should produce only Equal ops, got %+v", c)
		}
	}

	// All four documents present.
	if len(changes) < 4 {
		t.Errorf("expected >= 4 Equal entries (root + 2 LinkConfig + Hostname), got %d", len(changes))
	}
}

func TestFilterChanged_DropsEqualOps(t *testing.T) {
	t.Parallel()

	in := []applycheck.Change{
		{Op: applycheck.OpAdd, ID: applycheck.DocID{Kind: "X", Name: "a"}},
		{Op: applycheck.OpEqual, ID: applycheck.DocID{Kind: "X", Name: "b"}},
		{Op: applycheck.OpRemove, ID: applycheck.DocID{Kind: "X", Name: "c"}},
		{Op: applycheck.OpEqual, ID: applycheck.DocID{Kind: "X", Name: "d"}},
	}

	out := applycheck.FilterChanged(in)
	if len(out) != 2 {
		t.Errorf("FilterChanged returned %d, want 2: %+v", len(out), out)
	}
}

func TestDiff_EmptyInputs_NoChanges(t *testing.T) {
	t.Parallel()

	changes, err := applycheck.Diff(nil, nil)
	if err != nil {
		t.Fatalf("Diff(nil, nil) error: %v", err)
	}

	if len(changes) != 0 {
		t.Errorf("Diff(nil, nil) returned %d changes, want 0", len(changes))
	}
}

// TestDiff_KeyOrderIrrelevant pins that re-serialization with a
// different field order produces no spurious drift. yaml.v3 decodes
// into map[string]any whose iteration order is randomized; the
// reflect.DeepEqual gate compares by value, not source byte order.
// Without this contract, a Talos restart that re-emitted fields in
// a different order would surface as 'drift' every time.
func TestDiff_KeyOrderIrrelevant(t *testing.T) {
	t.Parallel()

	canonical := []byte("apiVersion: v1alpha1\nkind: LinkConfig\nname: eth0\nup: true\nmtu: 1500\n")
	shuffled := []byte("kind: LinkConfig\nmtu: 1500\nname: eth0\napiVersion: v1alpha1\nup: true\n")

	changes, err := applycheck.Diff(canonical, shuffled)
	if err != nil {
		t.Fatalf("Diff error: %v", err)
	}

	for _, c := range changes {
		if c.Op != applycheck.OpEqual {
			t.Errorf("key reorder produced %v change on %+v; want OpEqual", c.Op, c.ID)
		}
	}
}

// TestDiff_InvalidYAML_ErrorsCleanly pins that malformed YAML
// surfaces as a wrapped error rather than a panic or silent skip.
// Diff is called from the post-apply verify hook; a panic here
// would crash the apply chain mid-way.
func TestDiff_InvalidYAML_ErrorsCleanly(t *testing.T) {
	t.Parallel()

	invalid := []byte("not: valid\n  - mixed\n: yaml\n")
	good := []byte("machine:\n  type: controlplane\n")

	_, err := applycheck.Diff(invalid, good)
	if err == nil {
		t.Errorf("expected error on invalid YAML, got nil")
	}

	_, err = applycheck.Diff(good, invalid)
	if err == nil {
		t.Errorf("expected error on invalid desired YAML, got nil")
	}
}

// TestDiff_DistinguishesAbsentFromNil pins the HasOld/HasNew flags so a
// missing field and a present-but-null field do not collapse onto the
// same FieldChange shape. Without these flags, formatters can't tell
// `key: ~` from "key not in this side".
func TestDiff_DistinguishesAbsentFromNil(t *testing.T) {
	t.Parallel()

	cur := []byte(`apiVersion: v1alpha1
kind: HostnameConfig
hostname: cp-01
`)
	des := []byte(`apiVersion: v1alpha1
kind: HostnameConfig
hostname: cp-02
extraField: null
`)

	changes, err := applycheck.Diff(cur, des)
	if err != nil {
		t.Fatalf("Diff error: %v", err)
	}

	var update *applycheck.Change

	for i := range changes {
		if changes[i].Op == applycheck.OpUpdate {
			update = &changes[i]

			break
		}
	}

	if update == nil {
		t.Fatalf("expected one OpUpdate, got %+v", changes)
	}

	var sawExtraAddition bool

	for _, f := range update.Fields {
		if f.Path != "extraField" {
			continue
		}

		sawExtraAddition = true

		if f.HasOld {
			t.Errorf("extraField HasOld=true on absent-then-null change, want false")
		}

		if !f.HasNew {
			t.Errorf("extraField HasNew=false but value present (literal null), want true")
		}
	}

	if !sawExtraAddition {
		t.Errorf("expected a FieldChange at path 'extraField', got %+v", update.Fields)
	}
}

// TestDiff_EmptyNestedMap_SurfacesAsLeafChange pins the empty-map
// edge case in flatten. Without the fix, a current document with an
// empty nested map (e.g. `kubelet.extraArgs: {}` — a real Talos
// default shape) and a desired document that omits the field
// entirely produce Op=OpUpdate with Fields=[] — the operator sees
// "~ MachineConfig" with no leaf lines and has no idea what
// changed. flatten() must emit empty nested maps as leaf entries
// so the diff surfaces the addition/removal.
func TestDiff_EmptyNestedMap_SurfacesAsLeafChange(t *testing.T) {
	t.Parallel()

	cur := []byte(`---
apiVersion: v1alpha1
kind: TestKind
name: foo
extraArgs: {}
`)
	des := []byte(`---
apiVersion: v1alpha1
kind: TestKind
name: foo
`)

	changes, err := applycheck.Diff(cur, des)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}

	changed := applycheck.FilterChanged(changes)
	if len(changed) != 1 {
		t.Fatalf("expected one change, got %+v", changed)
	}

	if changed[0].Op != applycheck.OpUpdate {
		t.Fatalf("expected OpUpdate, got %v", changed[0].Op)
	}

	if len(changed[0].Fields) == 0 {
		t.Errorf("empty-nested-map removal must surface as a leaf FieldChange — got Fields=[], operator would see a content-free '~' line. fields=%+v", changed[0].Fields)
	}

	sawEmptyMapField := false

	for _, f := range changed[0].Fields {
		if f.Path == "extraArgs" {
			sawEmptyMapField = true

			if !f.HasOld || f.HasNew {
				t.Errorf("expected HasOld=true, HasNew=false for the disappearing empty map; got %+v", f)
			}
		}
	}

	if !sawEmptyMapField {
		t.Errorf("expected a FieldChange at path 'extraArgs', got %+v", changed[0].Fields)
	}
}

// TestDiff_DuplicateDocID_Errors pins the dedup contract: two
// documents with the same (kind, name) in one stream are a config
// defect (Talos's behaviour on duplicates is unspecified and varies
// between versions). The current implementation silently keeps the
// last-decoded doc; the test asserts Diff surfaces the duplicate as
// an error so the operator sees the real problem instead of a
// misleading "everything is fine" / "one doc changed" line.
func TestDiff_DuplicateDocID_Errors(t *testing.T) {
	t.Parallel()

	dup := []byte(`---
apiVersion: v1alpha1
kind: LinkConfig
name: eth0
mtu: 1500
---
apiVersion: v1alpha1
kind: LinkConfig
name: eth0
mtu: 9000
`)
	des := []byte(`---
apiVersion: v1alpha1
kind: LinkConfig
name: eth0
mtu: 1500
`)

	_, err := applycheck.Diff(dup, des)
	if err == nil {
		t.Fatal("Diff must error on duplicate (kind, name) in the current snapshot — silent overwrite hides a real config defect")
	}

	if !strings.Contains(err.Error(), "LinkConfig") || !strings.Contains(err.Error(), "eth0") {
		t.Errorf("error should cite the offending kind+name, got: %v", err)
	}
}
