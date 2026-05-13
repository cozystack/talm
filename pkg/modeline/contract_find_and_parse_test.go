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

package modeline

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	cerrors "github.com/cockroachdb/errors"
)

// TestFindAndParseModeline_ModelineOnFirstLine_NoLeadingComments
// pins the degenerate case: the conventional shape (modeline on
// line 1) parses correctly and returns an empty leading-comments
// slice. The new helper must subsume ReadAndParseModeline's
// behaviour for this canonical path.
func TestFindAndParseModeline_ModelineOnFirstLine_NoLeadingComments(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "node.yaml")
	body := "# talm: nodes=[\"1.2.3.4\"], endpoints=[\"1.2.3.4\"], templates=[\"templates/controlplane.yaml\"]\n" +
		"machine:\n  type: controlplane\n"
	if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	leading, config, err := FindAndParseModeline(file)
	if err != nil {
		t.Fatalf("FindAndParseModeline: %v", err)
	}

	if len(leading) != 0 {
		t.Errorf("expected zero leading comments when modeline is on line 1; got %d: %v", len(leading), leading)
	}

	want := &Config{
		Nodes:     []string{"1.2.3.4"},
		Endpoints: []string{"1.2.3.4"},
		Templates: []string{"templates/controlplane.yaml"},
	}
	if !reflect.DeepEqual(config, want) {
		t.Errorf("Config = %+v, want %+v", config, want)
	}
}

// TestFindAndParseModeline_PreservesLeadingCommentLines pins the
// new contract for `talm template -I`: operator-authored comment
// lines above the modeline are returned verbatim so the rewrite
// path can prepend them back to the regenerated file.
func TestFindAndParseModeline_PreservesLeadingCommentLines(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "node.yaml")
	body := "# Note 1: reset 2026-05-12 after ticket OPS-1234\n" +
		"# Note 2: DO NOT edit values directly\n" +
		"# talm: nodes=[\"1.2.3.4\"], endpoints=[\"1.2.3.4\"], templates=[\"templates/controlplane.yaml\"]\n" +
		"machine:\n  type: controlplane\n"
	if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	leading, config, err := FindAndParseModeline(file)
	if err != nil {
		t.Fatalf("FindAndParseModeline: %v", err)
	}

	wantLeading := []string{
		"# Note 1: reset 2026-05-12 after ticket OPS-1234",
		"# Note 2: DO NOT edit values directly",
	}
	if !reflect.DeepEqual(leading, wantLeading) {
		t.Errorf("leading comments mismatch\ngot:  %q\nwant: %q", leading, wantLeading)
	}

	if config == nil || len(config.Nodes) != 1 || config.Nodes[0] != "1.2.3.4" {
		t.Errorf("config not parsed correctly: %+v", config)
	}
}

// TestFindAndParseModeline_AllowsBlankLinesBeforeModeline pins
// the relaxed prefix shape: blank lines and comments are allowed
// in any order before the modeline.
func TestFindAndParseModeline_AllowsBlankLinesBeforeModeline(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "node.yaml")
	body := "# Note A\n" +
		"\n" +
		"# Note B\n" +
		"\n" +
		"# talm: nodes=[\"1.2.3.4\"], endpoints=[\"1.2.3.4\"], templates=[\"t.yaml\"]\n" +
		"body: ...\n"
	if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	leading, _, err := FindAndParseModeline(file)
	if err != nil {
		t.Fatalf("FindAndParseModeline: %v", err)
	}

	wantLeading := []string{
		"# Note A",
		"",
		"# Note B",
		"",
	}
	if !reflect.DeepEqual(leading, wantLeading) {
		t.Errorf("leading lines (comments + blanks) mismatch\ngot:  %q\nwant: %q", leading, wantLeading)
	}
}

// TestFindAndParseModeline_MisplacedModelineBelowYAML pins the
// strictness budget: a `# talm:` line sitting *below* arbitrary
// YAML is rejected with a specific "modeline below non-comment
// content" hint, NOT classified as an orphan file. Without this
// gate the operator would never learn that their modeline is in
// the wrong place.
func TestFindAndParseModeline_MisplacedModelineBelowYAML(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "node.yaml")
	body := "machine:\n  type: controlplane\n" +
		"# talm: nodes=[\"1.2.3.4\"], endpoints=[\"1.2.3.4\"], templates=[\"t.yaml\"]\n"
	if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err := FindAndParseModeline(file)
	if err == nil {
		t.Fatal("expected error: modeline below YAML must be rejected")
	}
	if cerrors.Is(err, ErrModelineNotFound) {
		t.Errorf("misplaced modeline must NOT be classified as orphan (ErrModelineNotFound); got: %v", err)
	}
	if !strings.Contains(err.Error(), "modeline found below non-comment content") {
		t.Errorf("error must name the misplacement so operator can fix it; got: %v", err)
	}
}

// TestFindAndParseModeline_OrphanWithLeadingComments_ReturnsErrModelineNotFound
// pins the apply / upgrade side-patch dispatch: a file with
// `#`-prefixed comments above plain YAML, but NO `# talm:` line
// anywhere, is a legitimate orphan and must surface
// ErrModelineNotFound. The caller (apply.fileHasTalmModeline)
// routes orphan files into the direct-patch / side-patch path;
// any other error type collapses that dispatch into a misleading
// "parse error" surface.
func TestFindAndParseModeline_OrphanWithLeadingComments_ReturnsErrModelineNotFound(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "node.yaml")
	body := "# Note A\n# Note B\nmachine:\n  type: controlplane\n"
	if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err := FindAndParseModeline(file)
	if err == nil {
		t.Fatal("expected ErrModelineNotFound: orphan file with leading comments")
	}
	if !cerrors.Is(err, ErrModelineNotFound) {
		t.Errorf("orphan file with leading comments must surface ErrModelineNotFound so callers route to side-patch path; got: %v", err)
	}
}

// TestFindAndParseModeline_IndentedTalmCommentInBody_NotMisclassified
// pins the false-positive guard: a YAML body containing an operator-
// authored `# talm: …` comment line (indented under a YAML key, e.g.
// "  # talm: see docs for how this section interacts with the modeline")
// must NOT be classified as a misplaced modeline. The canonical
// modeline always sits at column 0; indented `# talm:` text inside
// the body is a regular YAML comment. Without this guard, legitimate
// node files with documentation comments would be rejected as
// "modeline found below non-comment content".
func TestFindAndParseModeline_IndentedTalmCommentInBody_NotMisclassified(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "node.yaml")
	body := "machine:\n" +
		"  type: controlplane\n" +
		"  # talm: see docs for how this section interacts with the modeline\n" +
		"  network:\n" +
		"    hostname: cp01\n"
	if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err := FindAndParseModeline(file)
	if err == nil {
		t.Fatal("expected ErrModelineNotFound: indented # talm: comment is not a modeline")
	}
	if !cerrors.Is(err, ErrModelineNotFound) {
		t.Errorf("indented # talm: comment in body must surface as ErrModelineNotFound (orphan), not misplaced; got: %v", err)
	}
}

// TestFindAndParseModeline_OrphanPlainYAML_ReturnsErrModelineNotFound
// pins the simplest orphan shape: plain YAML with no comments and
// no modeline. Same contract as the leading-comments orphan —
// must return ErrModelineNotFound.
func TestFindAndParseModeline_OrphanPlainYAML_ReturnsErrModelineNotFound(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "node.yaml")
	if err := os.WriteFile(file, []byte("machine: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err := FindAndParseModeline(file)
	if err == nil {
		t.Fatal("expected ErrModelineNotFound: plain-YAML orphan")
	}
	if !cerrors.Is(err, ErrModelineNotFound) {
		t.Errorf("plain-YAML orphan must surface ErrModelineNotFound; got: %v", err)
	}
}

// TestFindAndParseModeline_MissingFile pins the file-IO error
// path: a non-existent file surfaces an error, not a panic.
func TestFindAndParseModeline_MissingFile(t *testing.T) {
	_, _, err := FindAndParseModeline(filepath.Join(t.TempDir(), "nope.yaml"))
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}
