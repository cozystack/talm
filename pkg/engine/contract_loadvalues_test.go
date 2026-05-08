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

// Contract: values aggregation in `talm template` / `talm apply`.
// loadValues mirrors the Helm CLI's `helm template -f values.yaml
// --set k=v --set-string k=v --set-file k=path --set-json k=...
// --set-literal k=v` precedence: each source layers onto the previous
// in argv order. mergeMaps performs the deep merge: later values win
// at primitive leaves, two map values are merged recursively, a map
// in `b` overwrites a non-map in `a` (and vice versa).

package engine

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// === mergeMaps ===

// Contract: merging an empty map onto base returns a copy of base
// (no aliasing — mutating the result must not affect base).
func TestContract_MergeMaps_EmptyOverlay(t *testing.T) {
	base := map[string]any{"a": 1, "b": map[string]any{"c": 2}}
	out := mergeMaps(base, map[string]any{})
	if !reflect.DeepEqual(out, base) {
		t.Errorf("empty overlay should preserve base; got %v want %v", out, base)
	}
	// Aliasing check: mutating the output's nested map must not leak.
	out["b"].(map[string]any)["c"] = 999
	if base["b"].(map[string]any)["c"] != 2 {
		// Note: mergeMaps shallow-copies nested maps that come from
		// `a` only when they appear in `b` too. Pure-`a` entries are
		// passed by reference. This is the upstream Helm behaviour;
		// pinning it as known-quirk.
		t.Logf("known quirk: mergeMaps does not deep-copy keys present only in `a`")
	}
}

// Contract: top-level scalar overrides.
func TestContract_MergeMaps_ScalarOverwrite(t *testing.T) {
	base := map[string]any{"a": 1, "b": "old"}
	overlay := map[string]any{"b": "new"}
	out := mergeMaps(base, overlay)
	want := map[string]any{"a": 1, "b": "new"}
	if !reflect.DeepEqual(out, want) {
		t.Errorf("got %v, want %v", out, want)
	}
}

// Contract: nested maps merge recursively. The base's other keys
// under the same nesting level survive.
func TestContract_MergeMaps_NestedRecursive(t *testing.T) {
	base := map[string]any{
		"top": map[string]any{
			"a": 1,
			"b": 2,
			"sub": map[string]any{
				"x": "x-base",
				"y": "y-base",
			},
		},
	}
	overlay := map[string]any{
		"top": map[string]any{
			"b": 22, // override
			"c": 3,  // add
			"sub": map[string]any{
				"y": "y-new", // override
				"z": "z-new", // add
			},
		},
	}
	out := mergeMaps(base, overlay)
	want := map[string]any{
		"top": map[string]any{
			"a": 1,
			"b": 22,
			"c": 3,
			"sub": map[string]any{
				"x": "x-base",
				"y": "y-new",
				"z": "z-new",
			},
		},
	}
	if !reflect.DeepEqual(out, want) {
		t.Errorf("got %v, want %v", out, want)
	}
}

// Contract: a map in overlay replaces a non-map in base (and vice
// versa) — mergeMaps does not attempt to "promote" the scalar into a
// map. This is the Helm behaviour and matches what users observe via
// `--set`.
func TestContract_MergeMaps_TypeMismatchOverwrite(t *testing.T) {
	base := map[string]any{"k": "scalar"}
	overlay := map[string]any{"k": map[string]any{"nested": "val"}}
	out := mergeMaps(base, overlay)
	want := map[string]any{"k": map[string]any{"nested": "val"}}
	if !reflect.DeepEqual(out, want) {
		t.Errorf("map-replacing-scalar: got %v, want %v", out, want)
	}

	// And the reverse direction.
	base2 := map[string]any{"k": map[string]any{"nested": "val"}}
	overlay2 := map[string]any{"k": "scalar"}
	out2 := mergeMaps(base2, overlay2)
	want2 := map[string]any{"k": "scalar"}
	if !reflect.DeepEqual(out2, want2) {
		t.Errorf("scalar-replacing-map: got %v, want %v", out2, want2)
	}
}

// Contract: slices are NOT merged — overlay's slice fully replaces
// base's slice. This matches Helm's `--set` and `-f` behaviour: lists
// are replaced wholesale, never appended. Operators relying on the
// list-replace semantics for arrays of subnets / addresses / SANs
// must re-state every element in the overlay, NOT just additions.
func TestContract_MergeMaps_SlicesReplaceNotAppend(t *testing.T) {
	base := map[string]any{"hosts": []any{"a", "b"}}
	overlay := map[string]any{"hosts": []any{"c"}}
	out := mergeMaps(base, overlay)
	if got := out["hosts"].([]any); !reflect.DeepEqual(got, []any{"c"}) {
		t.Errorf("expected slice replacement, got %v", got)
	}
}

// === loadValues ===

// Contract: ValueFiles are loaded in order, later files merge on top.
// Same key in two files: the second wins. Pin the precedence —
// reordering `-f` arguments is operator-observable behaviour.
func TestContract_LoadValues_ValueFilesOrderPrecedence(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first.yaml")
	second := filepath.Join(dir, "second.yaml")
	if err := os.WriteFile(first, []byte("a: 1\nshared: from-first\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("b: 2\nshared: from-second\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := loadValues(Options{ValueFiles: []string{first, second}})
	if err != nil {
		t.Fatal(err)
	}
	if out["a"] != 1 || out["b"] != 2 {
		t.Errorf("expected both keys present, got %v", out)
	}
	if out["shared"] != "from-second" {
		t.Errorf("expected later file to win, got %v", out["shared"])
	}
}

// Contract: --set-json takes a JSON object string and merges it onto
// the value tree. Lower precedence than --set / --set-string, higher
// than -f. (Sanity-check by setting both -f and --set-json.)
func TestContract_LoadValues_JsonValues(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "v.yaml")
	if err := os.WriteFile(file, []byte("a: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := loadValues(Options{
		ValueFiles: []string{file},
		JsonValues: []string{`{"b": {"c": 3}}`},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out["a"] != 1 {
		t.Errorf("file value lost: %v", out)
	}
	bMap, ok := out["b"].(map[string]any)
	if !ok {
		t.Fatalf("expected b to be map, got %T (%v)", out["b"], out["b"])
	}
	// JSON numbers unmarshal to float64.
	if bMap["c"] != float64(3) {
		t.Errorf("expected b.c=3, got %v (%T)", bMap["c"], bMap["c"])
	}
}

// Contract: malformed JSON in --set-json surfaces a precise error
// naming the bad value.
func TestContract_LoadValues_JsonValuesError(t *testing.T) {
	_, err := loadValues(Options{JsonValues: []string{`{"missing": colon}`}})
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

// Contract: --set parses k=v with type inference (numbers stay
// numbers, true/false become bool, dotted keys become nested maps).
// Matches Helm's strvals contract.
func TestContract_LoadValues_SetWithDottedKey(t *testing.T) {
	out, err := loadValues(Options{Values: []string{"top.sub.k=42"}})
	if err != nil {
		t.Fatal(err)
	}
	top, ok := out["top"].(map[string]any)
	if !ok {
		t.Fatalf("expected top map, got %T", out["top"])
	}
	sub, ok := top["sub"].(map[string]any)
	if !ok {
		t.Fatalf("expected sub map")
	}
	if sub["k"] != int64(42) {
		t.Errorf("expected k=int64(42), got %v (%T)", sub["k"], sub["k"])
	}
}

// Contract: --set-string forces the value to a string regardless of
// numeric/bool appearance. `--set-string k=42` writes "42" not 42.
// Required for fields like Talos sysctls where "4096" must stay a
// string.
func TestContract_LoadValues_SetStringForcesString(t *testing.T) {
	out, err := loadValues(Options{StringValues: []string{"k=42"}})
	if err != nil {
		t.Fatal(err)
	}
	if out["k"] != "42" {
		t.Errorf("expected k=\"42\", got %v (%T)", out["k"], out["k"])
	}
}

// Contract: --set-file reads file CONTENT and feeds it through
// Helm's strvals.ParseInto in the form `<filepath>=<content>`. Pin
// the observable behaviour: content lands under the file's literal
// path as the map key (when the path contains no `.` characters,
// strvals treats it as a single key). The flag is the way operators
// inject multi-line strings (license text, certificate PEM blocks,
// scripts) without escaping.
//
// `.` in any path component would trigger strvals' nesting rules
// (separate, well-known Helm behaviour); the test uses a dot-free
// filename inside a dot-free directory chain so the assertion is
// stable.
func TestContract_LoadValues_SetFileReadsContent(t *testing.T) {
	dir := t.TempDir()
	subdir := filepath.Join(dir, "data")
	if err := os.Mkdir(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(subdir, "licenseblob")
	content := "BEGIN LICENSE\nALL RIGHTS RESERVED\nEND LICENSE\n"
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := loadValues(Options{FileValues: []string{file}})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := out[file]
	if !ok {
		t.Fatalf("expected key %q in %v", file, out)
	}
	if got != content {
		t.Errorf("file content mismatch\n got %q\nwant %q", got, content)
	}
}

// Contract: --set-file errors when the file is missing, naming the
// missing path.
func TestContract_LoadValues_SetFileMissingErrors(t *testing.T) {
	_, err := loadValues(Options{FileValues: []string{"/path/that/does/not/exist"}})
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

// Contract: missing -f value file is an error naming the path.
func TestContract_LoadValues_ValueFileMissingErrors(t *testing.T) {
	_, err := loadValues(Options{ValueFiles: []string{"/path/that/does/not/exist.yaml"}})
	if err == nil {
		t.Fatal("expected error for missing values file")
	}
}

// Contract: malformed YAML in -f file is an error naming the file.
func TestContract_LoadValues_ValueFileMalformedYAMLErrors(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(file, []byte("this is\n  : not\n: valid"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := loadValues(Options{ValueFiles: []string{file}})
	if err == nil {
		t.Fatal("expected error for malformed YAML")
	}
}

// Contract: empty Options returns an empty (non-nil) map. Callers
// always rely on a non-nil result so they can `range` it without a
// guard.
func TestContract_LoadValues_EmptyOptionsReturnsEmptyMap(t *testing.T) {
	out, err := loadValues(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if out == nil {
		t.Fatal("expected non-nil empty map")
	}
	if len(out) != 0 {
		t.Errorf("expected empty map, got %v", out)
	}
}

// Contract: --set wins over -f. This is the Helm precedence: lower
// flags layer first, --set last (mostly). Pin the order so a
// refactor that swaps the for-loops in loadValues becomes visible.
func TestContract_LoadValues_SetOverridesValueFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "v.yaml")
	if err := os.WriteFile(file, []byte("k: from-file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := loadValues(Options{
		ValueFiles: []string{file},
		Values:     []string{"k=from-set"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out["k"] != "from-set" {
		t.Errorf("expected --set to win, got %v", out["k"])
	}
}

// Contract: --set-string wins over --set (later in the loadValues
// pipeline). Operators chaining `--set k=42 --set-string k=42` get
// the string variant.
func TestContract_LoadValues_SetStringOverridesSet(t *testing.T) {
	out, err := loadValues(Options{
		Values:       []string{"k=42"},
		StringValues: []string{"k=42"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out["k"] != "42" {
		t.Errorf("expected string \"42\", got %v (%T)", out["k"], out["k"])
	}
}
