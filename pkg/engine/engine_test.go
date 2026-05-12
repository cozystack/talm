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
	"bytes"
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/cockroachdb/errors"
)

// committedTextFiles returns absolute paths of every git-tracked file
// under root whose extension is in exts. Untracked working-tree
// artefacts (notes, build scratch, generated YAML, plan files) are
// excluded so a repo-wide scan stays hermetic with respect to working-
// tree state — `go test ./...` should be a function of committed
// source, not of whatever happens to be sitting in the user's checkout.
//
// Uses `git ls-files -z` to get the index-tracked file list. Returns
// an error if the command fails (no git, not a git repo). Empty or
// non-matching extensions yield an empty list, not an error.
func committedTextFiles(ctx context.Context, root string, exts map[string]bool) ([]string, error) {
	cmd := exec.CommandContext(ctx, "git", "ls-files", "-z")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return nil, errors.Wrapf(err, "git ls-files in %s", root)
	}
	var files []string
	for rel := range bytes.SplitSeq(out, []byte{0}) {
		if len(rel) == 0 {
			continue
		}
		path := filepath.Join(root, string(rel))
		if !exts[filepath.Ext(path)] {
			continue
		}
		files = append(files, path)
	}
	return files, nil
}

// danglingSubtestRef carries a flagged citation along with the file
// it was found in, so the consumer test can point a maintainer at the
// exact source location instead of forcing a grep across the package.
type danglingSubtestRef struct {
	Ref  string
	File string
}

// findDanglingSubtestReferences scans testFiles for prose citations
// of the shape TestParentName followed by a slash and a subtest slug
// (e.g. cited inline in a doc comment) and returns those whose
// parent+slug pair has no matching t.Run literal inside the same
// parent's function body. The previous flat-slug collector accepted
// any citation whose slug matched a subtest under ANY parent in the
// package — exactly the wrong-parent drift the guard claims to catch.
//
// Parent scopes are extracted via go/parser so a t.Run inside a
// helper called by TestParent does not leak into TestOther's scope.
// Slug matching mirrors Go's testing rewrite (spaces become
// underscores) and accepts an exact match or a prefix match within
// the same parent (a citation is allowed to truncate the subtest
// name in prose).
//
// Caveat: the t.Run regex captures only LITERAL subtest names. The
// table-driven pattern `t.Run(tt.name, func(...){...})` with the
// slug coming from a runtime variable is invisible to the collector
// — citations of those subtests would be flagged as dangling even
// though the runtime t.Run does match. If a maintainer adds such a
// citation, lift the slug into a literal at the t.Run call site or
// extend the collector to walk tt.name resolutions.
func findDanglingSubtestReferences(testFiles []string) ([]danglingSubtestRef, error) {
	subtestRe := regexp.MustCompile(`t\.Run\s*\(\s*"([^"\\]+)"`)
	refRe := regexp.MustCompile(`Test[A-Z][A-Za-z0-9_]+/[A-Za-z0-9_$.:\-]+`)
	parentSubtests := make(map[string]map[string]struct{})
	collectSubtest := func(parent, raw string) {
		slug := strings.ReplaceAll(raw, " ", "_")
		if parentSubtests[parent] == nil {
			parentSubtests[parent] = make(map[string]struct{})
		}
		parentSubtests[parent][slug] = struct{}{}
	}
	bodies := make(map[string][]byte, len(testFiles))
	fset := token.NewFileSet()
	for _, path := range testFiles {
		body, err := os.ReadFile(path)
		if err != nil {
			return nil, errors.Wrapf(err, "read %s", path)
		}
		bodies[path] = body
		file, err := parser.ParseFile(fset, path, body, parser.ParseComments)
		if err != nil {
			return nil, errors.Wrapf(err, "parse %s", path)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			if !strings.HasPrefix(fn.Name.Name, "Test") {
				continue
			}
			parent := fn.Name.Name
			start := fset.Position(fn.Body.Pos()).Offset
			end := fset.Position(fn.Body.End()).Offset
			if start < 0 || end > len(body) || start >= end {
				continue
			}
			for _, m := range subtestRe.FindAllSubmatch(body[start:end], -1) {
				collectSubtest(parent, string(m[1]))
			}
		}
	}
	var dangling []danglingSubtestRef
	type seenKey struct{ ref, file string }
	seen := make(map[seenKey]struct{})
	for _, path := range testFiles {
		data := bodies[path]
		for _, m := range refRe.FindAllSubmatch(data, -1) {
			ref := string(m[0])
			key := seenKey{ref: ref, file: path}
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			parts := strings.SplitN(ref, "/", 2)
			if len(parts) != 2 {
				continue
			}
			parent, slug := parts[0], parts[1]
			subs, ok := parentSubtests[parent]
			if !ok {
				dangling = append(dangling, danglingSubtestRef{Ref: ref, File: path})
				continue
			}
			if _, exact := subs[slug]; exact {
				continue
			}
			matched := false
			for known := range subs {
				if strings.HasPrefix(known, slug) {
					matched = true
					break
				}
			}
			if !matched {
				dangling = append(dangling, danglingSubtestRef{Ref: ref, File: path})
			}
		}
	}
	return dangling, nil
}

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

// TestCommittedTextFilesIgnoresUntrackedArtefacts pins the
// hermeticity property of committedTextFiles: a banned-phrase-bearing
// file that exists in the working tree but has not been added to the
// git index must not appear in the returned list. Without this, the
// repo-wide workflow-leakage scan fails on transient working-tree
// state (notes, build scratch, generated YAML), turning `go test
// ./...` into a function of the user's working directory rather than
// of committed source.
//
// The test creates a self-contained temporary git repository with one
// tracked file and one untracked file, both containing a banned
// phrase. committedTextFiles must return only the tracked file.
func TestCommittedTextFilesIgnoresUntrackedArtefacts(t *testing.T) {
	repo := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = repo
		// Disable any user gitconfig that could interfere with the
		// minimal test repo (commit signing, hooks, etc.).
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test",
			"GIT_AUTHOR_EMAIL=test@example.invalid",
			"GIT_COMMITTER_NAME=test",
			"GIT_COMMITTER_EMAIL=test@example.invalid",
			"GIT_CONFIG_GLOBAL=/dev/null",
			"GIT_CONFIG_SYSTEM=/dev/null",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("init", "-b", "main")
	tracked := filepath.Join(repo, "tracked.md")
	untracked := filepath.Join(repo, "untracked.md")
	if err := os.WriteFile(tracked, []byte("hello world\n"), 0o644); err != nil {
		t.Fatalf("write tracked: %v", err)
	}
	runGit("add", "tracked.md")
	runGit("commit", "-m", "init", "--no-gpg-sign")
	if err := os.WriteFile(untracked, []byte("untracked artefact\n"), 0o644); err != nil {
		t.Fatalf("write untracked: %v", err)
	}

	files, err := committedTextFiles(t.Context(), repo, map[string]bool{".md": true})
	if err != nil {
		t.Fatalf("committedTextFiles: %v", err)
	}
	have := make(map[string]bool, len(files))
	for _, f := range files {
		have[filepath.Base(f)] = true
	}
	if !have["tracked.md"] {
		t.Errorf("expected tracked.md in result, got %v", files)
	}
	if have["untracked.md"] {
		t.Errorf("untracked.md leaked into result; the helper must filter the working tree against git ls-files: %v", files)
	}
}

// TestFindDanglingSubtestReferencesIsParentAware pins the
// parent-keyed lookup contract: a citation TestX/slug must resolve
// only to a subtest declared inside TestX itself, not to a same-slug
// subtest under any other parent. The flat-map collector that
// preceded the fix accepted TestX/wrong as long as ANY test in the
// package had a subtest named "wrong", which was exactly the
// wrong-parent drift the guard claims to catch.
//
// The synthetic source is assembled via string concatenation so
// engine_test.go itself does not embed Test<Name>/slug literals
// that the production scanner (TestNoDanglingSubtestReferencesInSource)
// would then flag against this package's real test list.
func TestFindDanglingSubtestReferencesIsParentAware(t *testing.T) {
	dir := t.TempDir()
	// `tag` splits the Test prefix so the production-side `refRe`
	// scanner does not flag `Test<Name>/slug` literals from this
	// synthetic source against the real package; `runFn` does the
	// same for the t.Run call literal so the production-side
	// `subtestRe` collector does not register the synthetic
	// "alpha_extended" / "beta" subtests under the enclosing real
	// parent (this test function).
	tag := "Tes" + "t"
	runFn := "t.Ru" + "n"
	src := "package x\n" +
		"import \"testing\"\n" +
		"func " + tag + "Right(t *testing.T) { " + runFn + "(\"alpha_extended\", func(t *testing.T) {}) }\n" +
		"func " + tag + "Other(t *testing.T) { " + runFn + "(\"beta\", func(t *testing.T) {}) }\n" +
		"// Reference inside a comment: " + tag + "Right/alpha_extended (good — exact same-parent match).\n" +
		"// Reference inside a comment: " + tag + "Right/alpha (good — citation is a prefix of the same parent's alpha_extended subtest).\n" +
		"// Reference inside a comment: " + tag + "Right/beta (bad — beta is under " + tag + "Other, not " + tag + "Right).\n"
	path := filepath.Join(dir, "sample_test.go")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write sample: %v", err)
	}

	dangling, err := findDanglingSubtestReferences([]string{path})
	if err != nil {
		t.Fatalf("findDanglingSubtestReferences: %v", err)
	}
	have := make(map[string]bool, len(dangling))
	for _, d := range dangling {
		have[d.Ref] = true
	}
	if have[tag+"Right/alpha_extended"] {
		t.Errorf("%sRight/alpha_extended was flagged as dangling; the exact same-parent reference is valid", tag)
	}
	if have[tag+"Right/alpha"] {
		t.Errorf("%sRight/alpha was flagged as dangling; the citation is a prefix of the same parent's alpha_extended subtest and must be accepted", tag)
	}
	if !have[tag+"Right/beta"] {
		t.Errorf("%sRight/beta should be flagged as dangling: beta is a subtest under %sOther, not %sRight; got %v", tag, tag, tag, dangling)
	}
	for _, d := range dangling {
		if d.Ref == tag+"Right/beta" && d.File != path {
			t.Errorf("dangling ref records the wrong file: got %q, want %q", d.File, path)
		}
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
	if _, err := os.Stat(filepath.Join(moduleRoot, ".git")); err != nil {
		// The hermeticity scan shells out to `git ls-files`, which
		// requires a working tree backed by a git index. Source
		// release tarballs and vendored copies do not ship `.git`,
		// so skip rather than fatal — the scan is a developer-side
		// guard, not a property the published artefact must satisfy.
		t.Skipf("no .git directory at %s; hermeticity scan requires git ls-files", moduleRoot)
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
		// Private operator identifiers — test stand hostnames,
		// home-directory paths, internal cluster names. These are
		// reproducibility context that belongs in chat / memory,
		// not in published artefacts. Add new entries when a new
		// stand name or username surfaces.
		"dev17",
		"lexfrei",
	}
	scanExt := map[string]bool{
		".go":   true,
		".tpl":  true,
		".yaml": true,
		".yml":  true,
		".md":   true,
	}
	files, err := committedTextFiles(t.Context(), moduleRoot, scanExt)
	if err != nil {
		t.Fatalf("list committed files: %v", err)
	}
	for _, path := range files {
		if path == selfPath {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		src := string(data)
		rel, _ := filepath.Rel(moduleRoot, path)
		for _, phrase := range banned {
			if strings.Contains(src, phrase) {
				t.Errorf("workflow-leaky phrase %q found in %s; committed content must read as self-contained, with no references to the iteration process that produced it", phrase, rel)
			}
		}
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
	dangling, err := findDanglingSubtestReferences(files)
	if err != nil {
		t.Fatalf("findDanglingSubtestReferences: %v", err)
	}
	for _, d := range dangling {
		t.Errorf("dangling subtest reference in %s: %q has no matching t.Run subtest under that parent in this package", d.File, d.Ref)
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

// TestPruneIdenticalKeysAt_RecursesIntoObjectArrayMatchedByIdentityKey
// pins that pruneIdenticalKeysAt descends into object-array elements
// matched by their identity key (here `interface:` for the
// machine/network/interfaces path) and dedupes nested primitive
// arrays inside the matched pair. Without this descent,
// configpatcher.Apply matches the outer object-array element by
// `interface:` upstream, recurses into it, and appends the inner
// primitive list (`addresses`) — silently doubling every rendered
// address on every apply round-trip when the body re-states the
// rendered values plus a partial edit.
func TestPruneIdenticalKeysAt_RecursesIntoObjectArrayMatchedByIdentityKey(t *testing.T) {
	body := map[string]any{
		"interfaces": []any{
			map[string]any{
				"interface": "enp0s31f6",
				"addresses": []any{"88.99.249.47/26", "10.0.0.99/24"},
			},
		},
	}
	rendered := map[string]any{
		"interfaces": []any{
			map[string]any{
				"interface": "enp0s31f6",
				"addresses": []any{"88.99.249.47/26"},
			},
		},
	}

	pruneIdenticalKeysAt(body, rendered, "machine/network")

	ifaces, ok := body["interfaces"].([]any)
	if !ok {
		t.Fatalf("expected body[interfaces] to remain as a slice, got %#v", body["interfaces"])
	}
	if len(ifaces) != 1 {
		t.Fatalf("expected one interface item retained for the partial edit, got %d: %#v", len(ifaces), ifaces)
	}
	iface, ok := ifaces[0].(map[string]any)
	if !ok {
		t.Fatalf("expected interface item to be a map, got %#v", ifaces[0])
	}
	if iface["interface"] != "enp0s31f6" {
		t.Errorf("identity key `interface` must survive the prune so the upstream merge can match the element, got %#v", iface["interface"])
	}
	addrs, ok := iface["addresses"].([]any)
	if !ok {
		t.Fatalf("expected addresses to remain as a slice (only the user-add 10.0.0.99/24 should survive), got %#v", iface["addresses"])
	}
	if len(addrs) != 1 || addrs[0] != "10.0.0.99/24" {
		t.Errorf("expected addresses to be pruned to just [10.0.0.99/24], got %#v", addrs)
	}
}

// TestPruneIdenticalKeysAt_DropsObjectArrayItemReducedToIdentity pins
// that an object-array body item whose payload reduces to nothing
// after recursion is dropped entirely. Leaving an item that only
// carries its identity key would force configpatcher.Apply into a
// no-op match round, and (when the only rendered-side payload was a
// primitive list) re-trigger the strategic-merge append the prune is
// the entire reason for existing.
func TestPruneIdenticalKeysAt_DropsObjectArrayItemReducedToIdentity(t *testing.T) {
	body := map[string]any{
		"interfaces": []any{
			map[string]any{
				"interface": "enp0s31f6",
				"addresses": []any{"88.99.249.47/26"},
				"routes": []any{
					map[string]any{"network": "0.0.0.0/0", "gateway": "88.99.249.1"},
				},
			},
			map[string]any{
				"interface": "eth1",
				"addresses": []any{"10.0.0.5/24"},
			},
		},
	}
	rendered := map[string]any{
		"interfaces": []any{
			map[string]any{
				"interface": "enp0s31f6",
				"addresses": []any{"88.99.249.47/26"},
				"routes": []any{
					map[string]any{"network": "0.0.0.0/0", "gateway": "88.99.249.1"},
				},
			},
		},
	}

	pruneIdenticalKeysAt(body, rendered, "machine/network")

	ifaces, ok := body["interfaces"].([]any)
	if !ok {
		t.Fatalf("expected body[interfaces] to remain as a slice (eth1 is a user-add), got %#v", body["interfaces"])
	}
	if len(ifaces) != 1 {
		t.Fatalf("expected the matched-and-emptied enp0s31f6 to be dropped, leaving only the eth1 user-add, got %d items: %#v", len(ifaces), ifaces)
	}
	if iface := ifaces[0].(map[string]any); iface["interface"] != "eth1" {
		t.Errorf("expected the surviving interface item to be eth1 (the user-add), got %#v", iface["interface"])
	}
}

// TestPruneIdenticalKeysAt_ObjectArrayDeepEqualFallback pins the
// fallback behaviour for object arrays whose path is not in the
// known-merge-keys table: items that deep-equal a rendered item are
// dropped, even though the helper has no field name to match on.
// This catches the routes case (no single primary key in the Talos
// schema) and gracefully handles any future schema field that
// objectArrayMergeKeys forgets to enumerate.
func TestPruneIdenticalKeysAt_ObjectArrayDeepEqualFallback(t *testing.T) {
	body := map[string]any{
		"opaqueArray": []any{
			map[string]any{"a": "1", "b": "2"},
			map[string]any{"a": "3", "b": "4"},
		},
	}
	rendered := map[string]any{
		"opaqueArray": []any{
			map[string]any{"a": "1", "b": "2"},
		},
	}

	pruneIdenticalKeysAt(body, rendered, "unknown/path")

	arr, ok := body["opaqueArray"].([]any)
	if !ok {
		t.Fatalf("expected opaqueArray to remain as a slice, got %#v", body["opaqueArray"])
	}
	if len(arr) != 1 {
		t.Fatalf("expected the deep-equal duplicate to be pruned, got %d items: %#v", len(arr), arr)
	}
	if got, want := arr[0].(map[string]any)["a"], "3"; got != want {
		t.Errorf("expected the surviving item to be the user-add (a=3), got a=%v", got)
	}
}

// TestPruneIdenticalKeysAt_PreservesObjectArrayUserAdd pins the
// regression-safety property: an object-array item present only in
// body (no rendered counterpart by identity key) must reach the
// merge intact. The dedup must never drop user-add entries — the
// whole point is to neutralise the strategic-merge append for
// repeated values, not to replace it with silent drop-on-write.
func TestPruneIdenticalKeysAt_PreservesObjectArrayUserAdd(t *testing.T) {
	body := map[string]any{
		"interfaces": []any{
			map[string]any{
				"interface": "eth1",
				"addresses": []any{"10.0.0.5/24"},
			},
		},
	}
	rendered := map[string]any{
		"interfaces": []any{
			map[string]any{
				"interface": "enp0s31f6",
				"addresses": []any{"88.99.249.47/26"},
			},
		},
	}

	pruneIdenticalKeysAt(body, rendered, "machine/network")

	ifaces, ok := body["interfaces"].([]any)
	if !ok {
		t.Fatalf("expected body[interfaces] to remain as a slice, got %#v", body["interfaces"])
	}
	if len(ifaces) != 1 {
		t.Fatalf("expected the user-add interface to be preserved, got %d items: %#v", len(ifaces), ifaces)
	}
	iface := ifaces[0].(map[string]any)
	if iface["interface"] != "eth1" {
		t.Errorf("expected user-add interface eth1 to survive, got %#v", iface["interface"])
	}
}

// TestHasIdentityValue pins the boundary cases of the body-driven
// identity selector. Upstream's NetworkDeviceList.mergeDevice falls
// through `case device.DeviceInterface != "":` to
// `case device.DeviceSelector != nil:` when the first identity is the
// zero value of its type. matchObjectArrayItem must reject empty/zero
// identity fields the same way; otherwise an `interface: ""` body
// would chosen-key on `interface`, find no match in rendered, and
// preserve a body element that should have been matched via
// `deviceSelector` instead.
func TestHasIdentityValue(t *testing.T) {
	tests := []struct {
		name string
		v    any
		want bool
	}{
		{"nil", nil, false},
		{"empty string", "", false},
		{"non-empty string", "eth0", true},
		{"empty map", map[string]any{}, false},
		{"non-empty map", map[string]any{"hardwareAddr": "aa:bb:cc:dd:ee:ff"}, true},
		{"empty slice", []any{}, false},
		{"non-empty slice", []any{"a"}, true},
		{"int zero", 0, true},
		{"int non-zero", 4000, true},
		{"bool false", false, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasIdentityValue(tc.v); got != tc.want {
				t.Errorf("hasIdentityValue(%v) = %v, want %v", tc.v, got, tc.want)
			}
		})
	}
}

// TestObjectArrayMergeKeysMatchesUpstreamMergerSurface pins that the
// table covers exactly the upstream List types whose custom Merge
// method matches by identity AND whose elements would mishandle
// inner-primitive append on partial edit. A new entry to the table
// adds risk; a missing entry leaves the inner-primitive append
// regression reachable. Either drift surfaces here so a maintainer
// reconciles the table against the upstream merger surface
// deliberately.
//
// Upstream surface (verified against the cozystack/talos fork pinned
// in go.mod):
//
//   - NetworkDeviceList — yaml path `machine.network.interfaces`,
//     identity `interface` or `deviceSelector` (body-driven switch).
//   - VlanList — yaml path `machine.network.interfaces[].vlans`,
//     identity `vlanId`.
//   - AdmissionPluginConfigList — yaml path
//     `cluster.apiServer.admissionControl`, identity `name`.
//   - ConfigFileList (typed ExtensionServiceConfig.configFiles) —
//     intentionally OMITTED: ConfigFile carries only string fields
//     (hostPath, mountPath, content), so the upstream merge.Merge
//     field-by-field already produces the right result and the
//     deep-equal fallback in matchObjectArrayItem covers
//     full-restate idempotence. Adding it would not change
//     correctness; listing it would mislead readers about which
//     types are at risk for inner-primitive append.
func TestObjectArrayMergeKeysMatchesUpstreamMergerSurface(t *testing.T) {
	expected := map[string][]string{
		"machine/network/interfaces":         {"interface", "deviceSelector"},
		"machine/network/interfaces/vlans":   {"vlanId"},
		"cluster/apiServer/admissionControl": {"name"},
	}
	if len(objectArrayMergeKeys) != len(expected) {
		t.Fatalf("table size drift: have %d entries, want %d (verify against upstream merger surface in pkg/machinery/config/types/v1alpha1/v1alpha1_types.go and config/types/runtime/extensions/service_config.go)\nhave: %#v\nwant: %#v",
			len(objectArrayMergeKeys), len(expected), objectArrayMergeKeys, expected)
	}
	for path, wantKeys := range expected {
		gotKeys, ok := objectArrayMergeKeys[path]
		if !ok {
			t.Errorf("missing entry for %q", path)
			continue
		}
		if len(gotKeys) != len(wantKeys) {
			t.Errorf("entry for %q: got %d keys, want %d (got=%v want=%v)", path, len(gotKeys), len(wantKeys), gotKeys, wantKeys)
			continue
		}
		for i, want := range wantKeys {
			if gotKeys[i] != want {
				t.Errorf("entry for %q at index %d: got %q, want %q", path, i, gotKeys[i], want)
			}
		}
	}
}

// TestReplaceSemanticPathsMatchesUpstreamReplaceTags pins that the
// table covers exactly the upstream fields tagged `merge:"replace"`.
// A `merge:"replace"` field reachable through the prune that is
// missing from this table will silently drop rendered-side entries
// on a partial edit; a stray entry will skip otherwise-valid prune
// work. Either drift surfaces here.
//
// Upstream surface (verified against the cozystack/talos fork pinned
// in go.mod):
//
//   - cluster/network/podSubnets — v1alpha1 PodSubnet
//   - cluster/network/serviceSubnets — v1alpha1 ServiceSubnet
//   - cluster/apiServer/auditPolicy — v1alpha1 AuditPolicyConfig
//   - ingress — typed NetworkRuleConfig Ingress
//   - portSelector/ports — typed NetworkRuleConfig Ports
func TestReplaceSemanticPathsMatchesUpstreamReplaceTags(t *testing.T) {
	expected := map[string]struct{}{
		"cluster/network/podSubnets":     {},
		"cluster/network/serviceSubnets": {},
		"cluster/apiServer/auditPolicy":  {},
		"ingress":                        {},
		"portSelector/ports":             {},
	}
	if len(replaceSemanticPaths) != len(expected) {
		t.Fatalf("table size drift: have %d entries, want %d (re-grep upstream for `merge:\"replace\"` and reconcile)\nhave: %#v\nwant: %#v",
			len(replaceSemanticPaths), len(expected), replaceSemanticPaths, expected)
	}
	for path := range expected {
		if _, ok := replaceSemanticPaths[path]; !ok {
			t.Errorf("missing entry for %q", path)
		}
	}
}

// TestJoinYAMLPath pins joinYAMLPath's contract: the document root
// (empty parent) joins without a leading separator so the path lookup
// in objectArrayMergeKeys matches the documented form ("machine/...");
// every other join inserts exactly one slash.
func TestJoinYAMLPath(t *testing.T) {
	tests := []struct {
		parent string
		key    string
		want   string
	}{
		{"", "machine", "machine"},
		{"machine", "network", "machine/network"},
		{"machine/network", "interfaces", "machine/network/interfaces"},
	}
	for _, tc := range tests {
		t.Run(tc.parent+"+"+tc.key, func(t *testing.T) {
			if got := joinYAMLPath(tc.parent, tc.key); got != tc.want {
				t.Errorf("joinYAMLPath(%q, %q) = %q, want %q", tc.parent, tc.key, got, tc.want)
			}
		})
	}
}
