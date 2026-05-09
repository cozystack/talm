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

// Contract: project-root detection. talm decides which directory is
// "the project" by walking up from a file path, a template path, or
// the CWD until it finds Chart.yaml AND a secrets file (either
// secrets.yaml or secrets.encrypted.yaml). The two-marker rule is
// the contract — Chart.yaml alone matches every helm chart on disk;
// the secrets file is what makes a directory a TALM project.

package commands

import (
	"os"
	"path/filepath"
	"testing"
)

// makeProjectRoot creates a `Chart.yaml` and `secrets.yaml` (the two
// markers DetectProjectRoot looks for) inside dir.
func makeProjectRoot(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "Chart.yaml"), []byte("name: test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "secrets.yaml"), []byte("k: v\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// === DetectProjectRoot ===

// Contract: a directory containing both Chart.yaml AND secrets.yaml
// is a project root. The function returns the absolute path.
func TestContract_DetectProjectRoot_DirectMatch(t *testing.T) {
	dir := t.TempDir()
	makeProjectRoot(t, dir)
	got, err := DetectProjectRoot(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want, _ := filepath.Abs(dir)
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// Contract: secrets.encrypted.yaml is an acceptable substitute for
// secrets.yaml. Operators using `talm init --encrypt` will not have
// secrets.yaml at rest; root detection must still work.
func TestContract_DetectProjectRoot_EncryptedSecretsAccepted(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Chart.yaml"), []byte("name: test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "secrets.encrypted.yaml"), []byte("ENC[...]"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := DetectProjectRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got == "" {
		t.Errorf("expected match for dir with secrets.encrypted.yaml, got empty")
	}
}

// Contract: walking up from a sub-directory finds the project root.
// `talm apply -f nodes/cp1.yaml` invoked from anywhere inside the
// project must resolve back to the root.
func TestContract_DetectProjectRoot_WalksUpFromSubdir(t *testing.T) {
	root := t.TempDir()
	makeProjectRoot(t, root)
	subdir := filepath.Join(root, "nodes", "deep", "deeper")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := DetectProjectRoot(subdir)
	if err != nil {
		t.Fatal(err)
	}
	wantAbs, _ := filepath.Abs(root)
	if got != wantAbs {
		t.Errorf("walked up to %q, want %q", got, wantAbs)
	}
}

// Contract: when neither marker is found anywhere up the tree, the
// function returns ("", nil) — empty string, no error. The caller
// then surfaces a precise diagnostic. The function does NOT error
// itself because "no project here" is a valid input state (operator
// running `talm` outside any project).
func TestContract_DetectProjectRoot_NoMatchReturnsEmpty(t *testing.T) {
	// We cannot construct a guaranteed-empty walk-up tree on most
	// filesystems (the user may have markers in $HOME), so we use a
	// temp directory and assert "the function returns either empty or
	// some root, but the directory we passed is not it" — and verify
	// that with a fully empty start dir, traversal is bounded.
	dir := t.TempDir()
	got, err := DetectProjectRoot(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	gotAbs := got
	dirAbs, _ := filepath.Abs(dir)
	if gotAbs == dirAbs {
		t.Errorf("started in %q (no markers); should not match itself", dirAbs)
	}
	// got may be empty (clean test env) or some ancestor of $TMPDIR
	// containing markers (developer's $HOME-rooted project). Both are
	// fine — what matters is the function does not loop or error.
}

// Contract: a directory with Chart.yaml ONLY (no secrets file) is
// NOT a project root — many helm charts on disk are not talm
// projects. Pinning the both-markers rule prevents false positives.
func TestContract_DetectProjectRoot_ChartYamlAloneNotEnough(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Chart.yaml"), []byte("name: test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := DetectProjectRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	dirAbs, _ := filepath.Abs(dir)
	if got == dirAbs {
		t.Errorf("Chart.yaml alone matched as root: %q", got)
	}
}

// === DetectProjectRootForFile ===

// Contract: file-based detection takes a file path, derives the
// containing directory, then runs DetectProjectRoot on it.
func TestContract_DetectProjectRootForFile(t *testing.T) {
	root := t.TempDir()
	makeProjectRoot(t, root)
	nodes := filepath.Join(root, "nodes")
	if err := os.Mkdir(nodes, 0o755); err != nil {
		t.Fatal(err)
	}
	nodeFile := filepath.Join(nodes, "cp1.yaml")
	if err := os.WriteFile(nodeFile, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := DetectProjectRootForFile(nodeFile)
	if err != nil {
		t.Fatal(err)
	}
	rootAbs, _ := filepath.Abs(root)
	if got != rootAbs {
		t.Errorf("got %q, want %q", got, rootAbs)
	}
}

// === ValidateAndDetectRootsForFiles ===

// Contract: empty input returns ("", nil). Caller treats this as
// "no files to validate" and falls through to other detection
// strategies.
func TestContract_ValidateAndDetectRootsForFiles_EmptyInput(t *testing.T) {
	got, err := ValidateAndDetectRootsForFiles(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

// Contract: when all files share the same project root, the
// function returns that root.
func TestContract_ValidateAndDetectRootsForFiles_SingleRoot(t *testing.T) {
	root := t.TempDir()
	makeProjectRoot(t, root)
	nodes := filepath.Join(root, "nodes")
	if err := os.Mkdir(nodes, 0o755); err != nil {
		t.Fatal(err)
	}
	files := []string{
		filepath.Join(nodes, "cp1.yaml"),
		filepath.Join(nodes, "cp2.yaml"),
		filepath.Join(nodes, "cp3.yaml"),
	}
	for _, f := range files {
		if err := os.WriteFile(f, []byte(""), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := ValidateAndDetectRootsForFiles(files)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rootAbs, _ := filepath.Abs(root)
	if got != rootAbs {
		t.Errorf("got %q, want %q", got, rootAbs)
	}
}

// Contract: files spanning two project roots is an explicit error.
// The error names both roots so the operator can fix the
// commandline. talm refuses to apply a config built from
// inconsistent inputs (it cannot meaningfully merge two project
// configs in one apply).
func TestContract_ValidateAndDetectRootsForFiles_DifferentRootsError(t *testing.T) {
	rootA := t.TempDir()
	rootB := t.TempDir()
	makeProjectRoot(t, rootA)
	makeProjectRoot(t, rootB)
	fileA := filepath.Join(rootA, "node-a.yaml")
	fileB := filepath.Join(rootB, "node-b.yaml")
	if err := os.WriteFile(fileA, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fileB, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := ValidateAndDetectRootsForFiles([]string{fileA, fileB})
	if err == nil {
		t.Fatal("expected error for files in different roots")
	}
}

// Contract: a file whose directory has no Chart.yaml/secrets up the
// tree at all is reported by name in the error so the operator can
// see WHICH file failed root detection.
func TestContract_ValidateAndDetectRootsForFiles_OrphanFileError(t *testing.T) {
	root := t.TempDir()
	makeProjectRoot(t, root)

	// Orphan: file outside any project.
	orphanDir := t.TempDir()
	orphan := filepath.Join(orphanDir, "loose.yaml")
	if err := os.WriteFile(orphan, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	rootedFile := filepath.Join(root, "node.yaml")
	if err := os.WriteFile(rootedFile, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}

	// Note: this test passes only when $TMPDIR's ancestors do NOT
	// happen to contain Chart.yaml + secrets.yaml. On a developer
	// machine with such markers it would resolve to that. Skip if so.
	got, _ := DetectProjectRootForFile(orphan)
	if got != "" {
		t.Skipf("test environment has project markers above %q; skipping orphan-error path", orphanDir)
	}

	_, err := ValidateAndDetectRootsForFiles([]string{rootedFile, orphan})
	if err == nil {
		t.Fatal("expected error for orphan file")
	}
}

// === DetectRootForTemplate ===

// Contract: DetectRootForTemplate is a thin alias for
// DetectProjectRootForFile (both apply the file-then-walk-up
// strategy). Pin the equivalence so a future refactor that
// introduces a separate template-only path knows it is changing
// observable behaviour.
func TestContract_DetectRootForTemplate_EquivalentToFile(t *testing.T) {
	root := t.TempDir()
	makeProjectRoot(t, root)
	templatesDir := filepath.Join(root, "templates")
	if err := os.Mkdir(templatesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	tmpl := filepath.Join(templatesDir, "controlplane.yaml")
	if err := os.WriteFile(tmpl, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}

	gotFile, _ := DetectProjectRootForFile(tmpl)
	gotTmpl, _ := DetectRootForTemplate(tmpl)
	if gotFile != gotTmpl {
		t.Errorf("DetectRootForTemplate diverged from DetectProjectRootForFile:\n file: %q\n tmpl: %q", gotFile, gotTmpl)
	}
}
