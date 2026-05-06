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

package engine

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/cockroachdb/errors"
)

func TestIsTalosConfigPatch(t *testing.T) {
	tests := []struct {
		name      string
		doc       string
		expected  bool
		expectErr bool
	}{
		{
			name:     "machine config",
			doc:      "machine:\n  type: worker",
			expected: true,
		},
		{
			name:     "cluster config",
			doc:      "cluster:\n  name: test",
			expected: true,
		},
		{
			name:     "both machine and cluster",
			doc:      "machine:\n  type: worker\ncluster:\n  name: test",
			expected: true,
		},
		{
			name:     "UserVolumeConfig",
			doc:      "apiVersion: v1alpha1\nkind: UserVolumeConfig\nname: test",
			expected: false,
		},
		{
			name:     "SideroLinkConfig",
			doc:      "apiVersion: v1alpha1\nkind: SideroLinkConfig\napiUrl: https://example.com",
			expected: false,
		},
		{
			name:     "HostnameConfig",
			doc:      "apiVersion: v1alpha1\nkind: HostnameConfig\nhostname: worker-1",
			expected: false,
		},
		{
			name:     "LinkConfig",
			doc:      "apiVersion: v1alpha1\nkind: LinkConfig\nname: enp0s3\naddresses:\n  - address: 192.168.1.100/24",
			expected: false,
		},
		{
			name:     "BondConfig",
			doc:      "apiVersion: v1alpha1\nkind: BondConfig\nname: bond0\nlinks:\n  - eth0\n  - eth1\nbondMode: 802.3ad",
			expected: false,
		},
		{
			name:     "VLANConfig",
			doc:      "apiVersion: v1alpha1\nkind: VLANConfig\nname: bond0.100\nvlanID: 100\nparent: bond0",
			expected: false,
		},
		{
			name:     "ResolverConfig",
			doc:      "apiVersion: v1alpha1\nkind: ResolverConfig\nnameservers:\n  - address: 8.8.8.8",
			expected: false,
		},
		{
			name:     "RegistryMirrorConfig",
			doc:      "apiVersion: v1alpha1\nkind: RegistryMirrorConfig\nname: docker.io\nendpoints:\n  - url: https://mirror.gcr.io",
			expected: false,
		},
		{
			name:     "Layer2VIPConfig",
			doc:      "apiVersion: v1alpha1\nkind: Layer2VIPConfig\nname: 192.168.100.10\nlink: bond0",
			expected: false,
		},
		{
			name:     "empty document",
			doc:      "",
			expected: false,
		},
		{
			name:      "invalid yaml",
			doc:       "not: valid: yaml: here",
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := isTalosConfigPatch(tt.doc)
			if tt.expectErr {
				if err == nil {
					t.Errorf("isTalosConfigPatch() expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Errorf("isTalosConfigPatch() unexpected error: %v", err)
				return
			}
			if got != tt.expected {
				t.Errorf("isTalosConfigPatch() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestExtractExtraDocuments(t *testing.T) {
	tests := []struct {
		name      string
		patches   []string
		wantTalos int
		wantExtra int
		wantErr   bool
	}{
		{
			name:      "issue #66 scenario - talos config with UserVolumeConfig",
			patches:   []string{"machine:\n  type: worker\ncluster:\n  name: test\n---\napiVersion: v1alpha1\nkind: UserVolumeConfig\nname: databig"},
			wantTalos: 1,
			wantExtra: 1,
		},
		{
			name:      "only talos config",
			patches:   []string{"machine:\n  type: worker"},
			wantTalos: 1,
			wantExtra: 0,
		},
		{
			name:      "only extra document",
			patches:   []string{"apiVersion: v1alpha1\nkind: UserVolumeConfig\nname: test"},
			wantTalos: 0,
			wantExtra: 1,
		},
		{
			name:      "multiple extra documents",
			patches:   []string{"machine:\n  type: worker\n---\napiVersion: v1alpha1\nkind: UserVolumeConfig\nname: vol1\n---\napiVersion: v1alpha1\nkind: SideroLinkConfig\napiUrl: https://example.com"},
			wantTalos: 1,
			wantExtra: 2,
		},
		{
			name:      "empty patch",
			patches:   []string{""},
			wantTalos: 0,
			wantExtra: 0,
		},
		{
			name:      "multiple patches with mixed content",
			patches:   []string{"machine:\n  type: controlplane", "cluster:\n  name: prod"},
			wantTalos: 2,
			wantExtra: 0,
		},
		{
			name:      "CRLF line endings",
			patches:   []string{"machine:\r\n  type: worker\r\n---\r\napiVersion: v1alpha1\r\nkind: UserVolumeConfig"},
			wantTalos: 1,
			wantExtra: 1,
		},
		{
			name:      "v1.12 multi-doc: talos patch + network and registry documents",
			patches:   []string{"machine:\n  type: worker\ncluster:\n  name: test\n---\napiVersion: v1alpha1\nkind: HostnameConfig\nhostname: worker-1\n---\napiVersion: v1alpha1\nkind: LinkConfig\nname: enp0s3\naddresses:\n  - address: 192.168.1.100/24\n---\napiVersion: v1alpha1\nkind: RegistryMirrorConfig\nname: docker.io\nendpoints:\n  - url: https://mirror.gcr.io"},
			wantTalos: 1,
			wantExtra: 3,
		},
		{
			name:      "v1.12 multi-doc: talos patch + bond, vlan, vip documents",
			patches:   []string{"machine:\n  type: controlplane\ncluster:\n  name: prod\n---\napiVersion: v1alpha1\nkind: BondConfig\nname: bond0\nlinks:\n  - eth0\n  - eth1\nbondMode: 802.3ad\n---\napiVersion: v1alpha1\nkind: VLANConfig\nname: bond0.100\nvlanID: 100\nparent: bond0\n---\napiVersion: v1alpha1\nkind: Layer2VIPConfig\nname: 192.168.100.10\nlink: bond0"},
			wantTalos: 1,
			wantExtra: 3,
		},
		{
			name:      "v1.12 multi-doc: talos patch + resolver config",
			patches:   []string{"machine:\n  type: worker\n---\napiVersion: v1alpha1\nkind: ResolverConfig\nnameservers:\n  - address: 8.8.8.8\n  - address: 8.8.4.4"},
			wantTalos: 1,
			wantExtra: 1,
		},
		{
			name:    "invalid yaml should return error",
			patches: []string{"machine:\n  network:\n    interfaces:\n    \n    []"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			talos, extra, err := extractExtraDocuments(tt.patches)
			if tt.wantErr {
				if err == nil {
					t.Errorf("extractExtraDocuments() expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Errorf("extractExtraDocuments() unexpected error: %v", err)
				return
			}
			if len(talos) != tt.wantTalos {
				t.Errorf("talosPatches count = %d, want %d", len(talos), tt.wantTalos)
			}
			if len(extra) != tt.wantExtra {
				t.Errorf("extraDocs count = %d, want %d", len(extra), tt.wantExtra)
			}
		})
	}
}

func TestNormalizeTemplatePath(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"unix path", "templates/file.yaml", "templates/file.yaml"},
		{"nested path", "templates/nested/file.yaml", "templates/nested/file.yaml"},
		{"simple file", "file.yaml", "file.yaml"},
		{"empty string", "", ""},
		{"trailing slash", "templates/", "templates/"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeTemplatePath(tt.input); got != tt.want {
				t.Errorf("NormalizeTemplatePath(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// TestIsPrimitiveSlice_NarrowIntegerWidths pins that all Go integer
// widths plus floats/bools/strings/nil count as primitive. yaml.v3
// returns `int` and `float64` in practice, but the dedup pass is
// reused by callers that may decode bodies with other unmarshallers,
// so a narrow-width slice (e.g. []int8 from a manually built map)
// must not silently fall through to the default branch and skip the
// dedup.
func TestIsPrimitiveSlice_NarrowIntegerWidths(t *testing.T) {
	tests := []struct {
		name  string
		input []any
		want  bool
	}{
		{"int8", []any{int8(1), int8(2)}, true},
		{"int16", []any{int16(1), int16(2)}, true},
		{"int32", []any{int32(1), int32(2)}, true},
		{"int64", []any{int64(1), int64(2)}, true},
		{"uint8", []any{uint8(1), uint8(2)}, true},
		{"uint16", []any{uint16(1), uint16(2)}, true},
		{"uint32", []any{uint32(1), uint32(2)}, true},
		{"uint64", []any{uint64(1), uint64(2)}, true},
		{"float32", []any{float32(1.0), float32(2.0)}, true},
		{"float64", []any{1.0, 2.0}, true},
		{"int default", []any{1, 2}, true},
		{"strings", []any{"a", "b"}, true},
		{"bools", []any{true, false}, true},
		{"nils", []any{nil, nil}, true},
		{"mixed scalar", []any{int8(1), "two", true, nil}, true},
		{"map element", []any{map[string]any{"k": "v"}}, false},
		{"slice element", []any{[]any{1, 2}}, false},
		{"struct element", []any{struct{ A int }{A: 1}}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isPrimitiveSlice(tt.input); got != tt.want {
				t.Errorf("isPrimitiveSlice(%v) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// TestPruneIdenticalKeys_DedupsNarrowIntSlice pins that the dedup
// pass that callers rely on actually fires for a narrow-width
// primitive slice — not just that isPrimitiveSlice returns true in
// isolation.
func TestPruneIdenticalKeys_DedupsNarrowIntSlice(t *testing.T) {
	body := map[string]any{
		"k": []any{int8(1), int8(2)},
	}
	rendered := map[string]any{
		"k": []any{int8(1), int8(2)},
	}

	pruneIdenticalKeys(body, rendered)

	if _, ok := body["k"]; ok {
		t.Errorf("expected key %q to be deleted from body after identical narrow-int dedup, got %v", "k", body)
	}
}

// TestPruneIdenticalKeys_PreservesUserIntentEmptyMap pins that a
// body that explicitly sets a section to an empty map (typically to
// clear it) is not silently dropped by the dedup pass. Without this
// guard the recursive prune sees an already-empty bodySub, finds
// nothing to remove, then evaluates len(bodySub) == 0 and deletes
// the parent key — letting rendered's populated value survive
// untouched. The user's clear-this-section intent never reaches
// configpatcher.LoadPatch.
func TestPruneIdenticalKeys_PreservesUserIntentEmptyMap(t *testing.T) {
	body := map[string]any{
		"a": map[string]any{
			"b": map[string]any{},
		},
	}
	rendered := map[string]any{
		"a": map[string]any{
			"b": map[string]any{
				"c": "value",
			},
		},
	}

	pruneIdenticalKeys(body, rendered)

	a, ok := body["a"].(map[string]any)
	if !ok {
		t.Fatalf("expected body[a] to be retained as a map, got %#v", body["a"])
	}
	bv, ok := a["b"].(map[string]any)
	if !ok {
		t.Fatalf(`expected body["a"]["b"] to be retained as a map (user-intent empty override), got %#v`, a["b"])
	}
	if len(bv) != 0 {
		t.Errorf(`expected body["a"]["b"] to be empty (user-intent), got %#v`, bv)
	}
}

// TestPruneIdenticalKeys_RemovesIdenticalNestedMap pins the
// complementary case to TestPruneIdenticalKeys_PreservesUserIntentEmptyMap:
// when the body's nested map is fully covered by rendered (every
// child entry is deep-equal to rendered's counterpart), the dedup
// pass MUST collapse it. Without this, byte-identical bodies would
// leave behind a wrapping map that strategic-merge then re-stamps,
// re-introducing the duplicate-primitive-array-entries-per-round-trip
// regression this prune is the entire reason for existing.
func TestPruneIdenticalKeys_RemovesIdenticalNestedMap(t *testing.T) {
	body := map[string]any{
		"a": map[string]any{
			"b": "value",
		},
	}
	rendered := map[string]any{
		"a": map[string]any{
			"b": "value",
		},
	}

	pruneIdenticalKeys(body, rendered)

	if _, ok := body["a"]; ok {
		t.Errorf(`expected body["a"] to be removed (every child deep-equal to rendered), got %#v`, body["a"])
	}
}

// TestDocumentIdentityHelpersAgree pins that documentIdentity (which
// works on map[string]any) and documentIdentityFromNode (which works
// on *yaml.Node) produce byte-equal output for the same logical
// document. The strip-by-path-then-prune pipeline depends on this:
// the rendered scan emits paths prefixed by documentIdentityFromNode,
// the body strip routes by the same prefix, and the multi-doc prune
// keys rendered docs by documentIdentity. A drift between the two
// helpers would silently break strip-pairing on identity-tuple-
// matched docs, leaving directives behind in production while every
// integration test still passed (the bypass is a no-op on bodies
// the helpers happen to disagree on, not a hard failure).
func TestDocumentIdentityHelpersAgree(t *testing.T) {
	tests := []struct {
		name string
		yaml string
	}{
		{
			name: "legacy v1alpha1 root",
			yaml: "version: v1alpha1\nmachine:\n  type: worker\n",
		},
		{
			name: "typed doc with name",
			yaml: "apiVersion: v1alpha1\nkind: Layer2VIPConfig\nname: 192.168.0.1\nlink: eth0\n",
		},
		{
			name: "typed doc without name",
			yaml: "apiVersion: v1alpha1\nkind: HostnameConfig\nhostname: cozy-01\n",
		},
		{
			name: "anonymous map",
			yaml: "machine:\n  type: worker\n",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			docs, err := decodeAllYAMLDocuments([]byte(tc.yaml))
			if err != nil {
				t.Fatalf("decodeAllYAMLDocuments: %v", err)
			}
			if len(docs) != 1 {
				t.Fatalf("expected 1 document, got %d", len(docs))
			}
			fromNode := documentIdentityFromNode(docs[0])

			mapDocs, _, err := decodeAsMaps([]byte(tc.yaml))
			if err != nil {
				t.Fatalf("decodeAsMaps: %v", err)
			}
			if len(mapDocs) != 1 {
				t.Fatalf("expected 1 map document, got %d", len(mapDocs))
			}
			fromMap := documentIdentity(mapDocs[0])

			if fromNode != fromMap {
				t.Errorf("identity helpers disagree:\n  documentIdentityFromNode = %q\n  documentIdentity         = %q\n  yaml: %s",
					fromNode, fromMap, tc.yaml)
			}
		})
	}
}

// TestNoWorkflowLeakageInRepoSource walks every committed text file
// in the module and fails on phrases that describe the iteration
// process that produced the change rather than the change itself.
// Committed content must read as if the change was right the first
// time, with no "this PR adds…", "address review pass N",
// "branch-review caught…" provenance: a reader six months from now
// has no access to the chat session, the PR thread, or the planning
// doc, and process-leaking comments age into noise.
//
// Scope is the whole repo, not just this package. A leak in
// pkg/commands/, charts/, README.md, or anywhere else under the
// module root is just as much of a problem as one inside pkg/engine.
// The walk skips this test file (which has to spell the banlist
// out), the .git directory, and vendored/build artefacts.
func TestNoWorkflowLeakageInRepoSource(t *testing.T) {
	moduleRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve module root: %v", err)
	}
	selfPath, err := filepath.Abs("engine_test.go")
	if err != nil {
		t.Fatalf("resolve self path: %v", err)
	}

	banned := []string{
		"branch-review",
		"branch review",
		"this PR",
		"this branch fixes",
		"this branch adds",
		"this branch introduces",
		"review pass",
		"review fix",
		"review-fix",
		"address review",
		"in-flight rebase",
		"rebase notes",
	}
	scanExt := map[string]bool{
		".go":   true,
		".tpl":  true,
		".yaml": true,
		".yml":  true,
		".md":   true,
	}
	skipDirs := map[string]bool{
		".git":         true,
		"vendor":       true,
		"node_modules": true,
		".claude":      true, // worktrees, plans, memory — not committed source
	}
	if err := filepath.WalkDir(moduleRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if path == selfPath {
			return nil
		}
		if !scanExt[filepath.Ext(path)] {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		src := string(data)
		rel, _ := filepath.Rel(moduleRoot, path)
		for _, phrase := range banned {
			if strings.Contains(src, phrase) {
				t.Errorf("workflow-leaky phrase %q found in %s; committed content must read as self-contained, with no references to the iteration process that produced it", phrase, rel)
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("walk module root: %v", err)
	}
}

// TestNoDanglingSubtestReferencesInSource walks every test file in
// the package and checks that every parent-test plus slash plus
// subtest-slug citation in source comments resolves to a real
// subtest. Citations that lose their referent during refactoring
// turn into actively misleading documentation: a future maintainer
// chasing the citation reads the test file looking for an upstream
// guardrail that does not exist. This guard catches that class of
// drift on the next change.
//
// Slug matching mirrors Go's testing rewrite: spaces become
// underscores, other special characters pass through. We accept
// either an exact match or a prefix match (subtest names commonly
// get truncated when cited in prose).
func TestNoDanglingSubtestReferencesInSource(t *testing.T) {
	// Restricted to *_test.go: prose citations of the parent/subtest
	// shape are a test-file convention. Production code may legitimately
	// embed substrings like `Foo/bar` that match the reference regex
	// but have nothing to do with subtests (HTTP routes, file paths,
	// log entries), and we do not want to false-positive on them.
	files, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}

	// Collect every slugified subtest name seen in any t.Run literal.
	subtestRe := regexp.MustCompile(`t\.Run\("([^"\\]+)"`)
	subtests := map[string]struct{}{}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		for _, m := range subtestRe.FindAllSubmatch(data, -1) {
			slug := strings.ReplaceAll(string(m[1]), " ", "_")
			subtests[slug] = struct{}{}
		}
	}

	// Find every parent-plus-subtest-slug citation in any test file.
	// Conservative pattern: Test prefix followed by an identifier,
	// then slash, then a slug of non-whitespace/non-quote chars.
	refRe := regexp.MustCompile(`Test[A-Z][A-Za-z0-9_]+/[A-Za-z0-9_$.:\-]+`)
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		for _, m := range refRe.FindAllSubmatch(data, -1) {
			ref := string(m[0])
			parts := strings.SplitN(ref, "/", 2)
			if len(parts) != 2 {
				continue
			}
			slug := parts[1]
			if _, ok := subtests[slug]; ok {
				continue
			}
			matched := false
			for known := range subtests {
				if strings.HasPrefix(known, slug) {
					matched = true
					break
				}
			}
			if !matched {
				t.Errorf("dangling subtest reference in %s: %q has no matching t.Run subtest in this package", f, ref)
			}
		}
	}
}

// TestMergeFileAsPatch_QuotesPatchFileInHints pins that user-
// controlled file paths cannot inject shell metacharacters into the
// suggested-command hint. A `talm template -f $(rm -rf /).yaml`
// path that landed in a copy-pastable hint without quoting would
// turn the diagnostic into a footgun. The fix is `%q` quoting on
// every path interpolation; this test catches the next time a hint
// drops back to bare `%s` or string concatenation.
func TestMergeFileAsPatch_QuotesPatchFileInHints(t *testing.T) {
	rendered := []byte("version: v1alpha1\nmachine:\n  type: worker\n")
	dir := t.TempDir()
	// File path containing shell-meaningful characters: parentheses,
	// $, spaces. All three need quoting to be safe to copy-paste from
	// a hint into a terminal.
	patchFile := filepath.Join(dir, "$(rm -rf -).yaml")
	// JSON Patch body with a `test` op whose path is not present in
	// rendered: configpatcher.Apply runs the patch, the test fails,
	// and the resulting error reaches the patchFile-quoting hint
	// branch we want to exercise.
	body := []byte(`- op: test
  path: /machine/network/hostname
  value: never-rendered
`)
	if err := os.WriteFile(patchFile, body, 0o644); err != nil {
		t.Fatalf("write patch file: %v", err)
	}

	_, err := MergeFileAsPatch(rendered, patchFile)
	if err == nil {
		t.Fatal("expected MergeFileAsPatch to fail on a JSON Patch test op against a missing path")
	}
	hints := errors.GetAllHints(err)
	if len(hints) == 0 {
		t.Fatalf("expected at least one hint on the error, got %v", err)
	}
	// The patchFile must appear quoted somewhere in the hint chain;
	// the bare path embedded in plain prose (or worse, inside a
	// suggested shell command) would be a copy-paste hazard. The
	// production code emits the path via `%q`, which on Windows
	// escapes path separators (`C:\foo` becomes `"C:\\foo"`); compare
	// the same way so the test is not platform-sensitive.
	wantQuoted := strconv.Quote(patchFile)
	if !strings.Contains(strings.Join(hints, "\n"), wantQuoted) {
		t.Errorf("expected at least one hint to contain the path quoted as %s, got hints:\n%s", wantQuoted, strings.Join(hints, "\n"))
	}
}

// TestPruneBodyIdentitiesAgainstRendered_PropagatesRenderedParseError
// pins the contract that a malformed rendered template surfaces the
// real parse error to the caller, not a downstream configpatcher
// failure on the same malformed bytes. engine.Render produces the
// rendered input from chart helpers this binary owns, so a parse
// failure here points at a chart-helper bug; masking it as a
// LoadPatch error against malformed bytes wastes an entire diagnostic
// session.
func TestPruneBodyIdentitiesAgainstRendered_PropagatesRenderedParseError(t *testing.T) {
	body := []byte("version: v1alpha1\nmachine:\n  type: worker\n")
	rendered := []byte(": : :\n") // malformed YAML

	_, _, err := pruneBodyIdentitiesAgainstRendered(body, rendered)
	if err == nil {
		t.Fatal("expected parse error for malformed rendered template, got nil")
	}
	if !strings.Contains(err.Error(), "rendered") {
		t.Errorf("expected error to mention rendered template, got: %v", err)
	}
}

// TestPruneIdenticalKeys_DropsBodyEmptySliceAgainstPopulatedRendered
// pins the slice-path asymmetry vs the map-path empty-override guard.
// A body that explicitly sets `key: []` against rendered's populated
// `key: [a, b]` produces an empty primitiveSliceDifference, which the
// caller deletes — rendered's `[a, b]` survives the merge untouched.
// The map path preserves a user-intent empty map; the slice path
// does not. This is consistent with upstream strategic-merge primitive
// array semantics (append-only, cannot replace), so the user could
// not actually clear a primitive list via strategic merge regardless
// — clearing happens via $patch:delete on the parent or via JSON
// Patch. Pin the current behavior so a future maintainer who tries
// to add user-intent empty-slice handling has to also reckon with
// the upstream merge constraint.
func TestPruneIdenticalKeys_DropsBodyEmptySliceAgainstPopulatedRendered(t *testing.T) {
	body := map[string]any{
		"key": []any{},
	}
	rendered := map[string]any{
		"key": []any{"a", "b"},
	}

	pruneIdenticalKeys(body, rendered)

	if _, ok := body["key"]; ok {
		t.Errorf(`expected body["key"] to be deleted (consistent with strategic-merge's append-only primitive-array semantics; clearing must happen via $patch:delete on the parent or via JSON Patch), got %#v`, body["key"])
	}
}

// TestPrimitiveSliceDifference_ReorderCollapsesToEmpty pins the
// known trade-off documented on primitiveSliceDifference: a body
// that reorders rendered's primitive elements ([b, a] over [a, b])
// has the same element set as rendered, so the difference is empty,
// the caller deletes the key, and rendered's order survives. The
// test exists so a maintainer who later tries to "fix" the dedup to
// preserve body order has a fail surface that explains why the
// behavior is intentional (strategic-merge's own primitive-array
// semantics cannot replace, only append, so body-side order cannot
// be imposed regardless — the prune just makes the no-op visible).
func TestPrimitiveSliceDifference_ReorderCollapsesToEmpty(t *testing.T) {
	body := []any{"b", "a"}
	rendered := []any{"a", "b"}

	got := primitiveSliceDifference(body, rendered)

	if len(got) != 0 {
		t.Errorf("expected empty diff for reordered-but-identical-set slices, got %v", got)
	}
}
