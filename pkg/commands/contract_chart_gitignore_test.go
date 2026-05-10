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

// Contract: readChartYamlPreset (Chart.yaml -> preset name) and
// writeGitignoreFile (.gitignore management of secrets-bearing
// files). Both are user-facing: an operator running `talm init`
// against an existing project sees the preset detection result
// echoed in command behaviour, and they expect the secrets files
// the chart will write to be added to .gitignore so an accidental
// `git add .` does not commit private keys.

package commands

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setRoot temporarily replaces Config.RootDir for the duration of a
// test, restoring the previous value on cleanup. The package-level
// Config is shared state — tests must not leak it.
func setRoot(t *testing.T, dir string) {
	t.Helper()
	original := Config.RootDir
	t.Cleanup(func() { Config.RootDir = original })
	Config.RootDir = dir
}

// === readChartYamlPreset ===

// Contract: Chart.yaml's first non-talm dependency name is the
// preset. talm itself is the library chart (always present); the
// "active preset" is whichever other dependency the project chose
// at `talm init -p <preset>`.
func TestContract_ReadChartYamlPreset_PicksFirstNonTalmDependency(t *testing.T) {
	dir := t.TempDir()
	setRoot(t, dir)
	chartYaml := `apiVersion: v2
name: my-cluster
version: 0.1.0
dependencies:
  - name: talm
    version: ">=0"
  - name: cozystack
    version: ">=0"
`
	if err := os.WriteFile(filepath.Join(dir, "Chart.yaml"), []byte(chartYaml), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := readChartYamlPreset()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != presetCozystack {
		t.Errorf("expected preset 'cozystack', got %q", got)
	}
}

// Contract: order of dependencies in Chart.yaml decides which
// preset is reported. The first non-talm entry wins. Pin so a
// future refactor that returned the LAST entry would surface here.
func TestContract_ReadChartYamlPreset_OrderMatters(t *testing.T) {
	dir := t.TempDir()
	setRoot(t, dir)
	chartYaml := `apiVersion: v2
name: my-cluster
version: 0.1.0
dependencies:
  - name: generic
    version: ">=0"
  - name: talm
    version: ">=0"
  - name: cozystack
    version: ">=0"
`
	if err := os.WriteFile(filepath.Join(dir, "Chart.yaml"), []byte(chartYaml), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := readChartYamlPreset()
	if err != nil {
		t.Fatal(err)
	}
	if got != "generic" {
		t.Errorf("expected first non-talm dep 'generic', got %q", got)
	}
}

// Contract: a Chart.yaml with only the talm library dependency (no
// preset) surfaces a precise error. talm init's update flow uses
// this to detect "no preset configured" without mistaking talm
// itself for a preset.
func TestContract_ReadChartYamlPreset_NoPresetError(t *testing.T) {
	dir := t.TempDir()
	setRoot(t, dir)
	chartYaml := `apiVersion: v2
name: my-cluster
version: 0.1.0
dependencies:
  - name: talm
    version: ">=0"
`
	if err := os.WriteFile(filepath.Join(dir, "Chart.yaml"), []byte(chartYaml), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := readChartYamlPreset()
	if err == nil {
		t.Fatal("expected error for talm-only deps")
	}
	if !strings.Contains(err.Error(), "preset not found") {
		t.Errorf("error must mention 'preset not found', got: %v", err)
	}
}

// Contract: missing Chart.yaml is an error mentioning the file.
func TestContract_ReadChartYamlPreset_MissingChartYamlError(t *testing.T) {
	dir := t.TempDir()
	setRoot(t, dir)
	_, err := readChartYamlPreset()
	if err == nil {
		t.Fatal("expected error for missing Chart.yaml")
	}
	if !strings.Contains(err.Error(), "Chart.yaml") {
		t.Errorf("error must mention Chart.yaml, got: %v", err)
	}
}

// Contract: malformed YAML in Chart.yaml is an error. Without this
// guard, the unmarshal returns nil dependencies, the loop yields
// "preset not found", and the operator sees a misleading message.
func TestContract_ReadChartYamlPreset_MalformedYAMLError(t *testing.T) {
	dir := t.TempDir()
	setRoot(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "Chart.yaml"), []byte(":\n  bad:\n: yaml"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := readChartYamlPreset()
	if err == nil {
		t.Fatal("expected error for malformed YAML")
	}
}

// === writeGitignoreFile ===

// Contract: writeGitignoreFile creates .gitignore from scratch
// containing the four secrets-bearing files talm manages:
// secrets.yaml, talosconfig, talm.key, kubeconfig (default name).
// Without this list a fresh `talm init` followed by `git init &&
// git add .` would commit private cluster material.
func TestContract_WriteGitignoreFile_CreatesWithRequiredEntries(t *testing.T) {
	dir := t.TempDir()
	setRoot(t, dir)
	// Force default kubeconfig name (no override).
	originalKube := Config.GlobalOptions.Kubeconfig
	t.Cleanup(func() { Config.GlobalOptions.Kubeconfig = originalKube })
	Config.GlobalOptions.Kubeconfig = ""

	if err := writeGitignoreFile(); err != nil {
		t.Fatalf("writeGitignoreFile: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	content := string(data)
	for _, want := range []string{"secrets.yaml", "talosconfig", "talm.key", "kubeconfig"} {
		if !strings.Contains(content, want) {
			t.Errorf(".gitignore missing %q in:\n%s", want, content)
		}
	}
}

// Contract: when Config.GlobalOptions.Kubeconfig is set to a path
// (e.g. "/etc/kubernetes/admin.kubeconfig"), .gitignore receives
// only the BASE NAME (admin.kubeconfig). The directory portion is
// dropped — paths in .gitignore are repo-relative; absolute paths
// are useless. Pinning this prevents a regression that would write
// the full host path into a project's .gitignore.
func TestContract_WriteGitignoreFile_KubeconfigBaseNameOnly(t *testing.T) {
	dir := t.TempDir()
	setRoot(t, dir)
	originalKube := Config.GlobalOptions.Kubeconfig
	t.Cleanup(func() { Config.GlobalOptions.Kubeconfig = originalKube })
	Config.GlobalOptions.Kubeconfig = "/etc/kubernetes/admin.kubeconfig"

	if err := writeGitignoreFile(); err != nil {
		t.Fatalf("writeGitignoreFile: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	content := string(data)
	if !strings.Contains(content, "admin.kubeconfig") {
		t.Errorf("expected base name 'admin.kubeconfig' in .gitignore:\n%s", content)
	}
	if strings.Contains(content, "/etc/kubernetes") {
		t.Errorf("absolute path leaked into .gitignore:\n%s", content)
	}
}

// Contract: an existing .gitignore with extra (operator-supplied)
// entries is preserved. writeGitignoreFile is additive — it appends
// missing required entries, never rewrites the file from scratch
// (which would clobber the operator's customizations).
func TestContract_WriteGitignoreFile_PreservesExistingEntries(t *testing.T) {
	dir := t.TempDir()
	setRoot(t, dir)
	originalKube := Config.GlobalOptions.Kubeconfig
	t.Cleanup(func() { Config.GlobalOptions.Kubeconfig = originalKube })
	Config.GlobalOptions.Kubeconfig = ""

	existing := "# Custom rules\nnotes/\n*.log\n"
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeGitignoreFile(); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	content := string(data)
	for _, want := range []string{"notes/", "*.log", "secrets.yaml", "talosconfig", "talm.key", "kubeconfig"} {
		if !strings.Contains(content, want) {
			t.Errorf(".gitignore missing %q after append:\n%s", want, content)
		}
	}
}

// Contract: when all required entries are already present,
// writeGitignoreFile is a no-op — does not rewrite the file. This
// keeps mtime / git status stable across repeat `talm init`
// invocations. The behaviour is observable via `git diff` showing
// no changes.
func TestContract_WriteGitignoreFile_IdempotentOnFullList(t *testing.T) {
	dir := t.TempDir()
	setRoot(t, dir)
	originalKube := Config.GlobalOptions.Kubeconfig
	t.Cleanup(func() { Config.GlobalOptions.Kubeconfig = originalKube })
	Config.GlobalOptions.Kubeconfig = ""

	full := `# Sensitive files
secrets.yaml
talosconfig
talm.key
kubeconfig
`
	gitignore := filepath.Join(dir, ".gitignore")
	if err := os.WriteFile(gitignore, []byte(full), 0o644); err != nil {
		t.Fatal(err)
	}
	infoBefore, _ := os.Stat(gitignore)

	if err := writeGitignoreFile(); err != nil {
		t.Fatalf("writeGitignoreFile: %v", err)
	}
	data, _ := os.ReadFile(gitignore)
	if string(data) != full {
		t.Errorf("idempotent invocation should not change content\nbefore:\n%s\nafter:\n%s", full, data)
	}
	infoAfter, _ := os.Stat(gitignore)
	if !infoBefore.ModTime().Equal(infoAfter.ModTime()) {
		t.Errorf("idempotent invocation must not touch mtime")
	}
}

// Contract: an entry that appears in .gitignore as part of a
// commented-out OR pattern-extended form (e.g. `secrets.yaml # backup`,
// `talosconfig#`) counts as already-present. The match is
// prefix-based on the trimmed line, allowing operators to annotate
// .gitignore entries without triggering duplicate appends.
func TestContract_WriteGitignoreFile_TolerantOfAnnotatedEntries(t *testing.T) {
	dir := t.TempDir()
	setRoot(t, dir)
	originalKube := Config.GlobalOptions.Kubeconfig
	t.Cleanup(func() { Config.GlobalOptions.Kubeconfig = originalKube })
	Config.GlobalOptions.Kubeconfig = ""

	annotated := `# Sensitive files
secrets.yaml # never commit this
talosconfig#TODO move to vault
talm.key
kubeconfig
`
	gitignore := filepath.Join(dir, ".gitignore")
	if err := os.WriteFile(gitignore, []byte(annotated), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeGitignoreFile(); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(gitignore)
	// The annotated entries must NOT have been duplicated.
	if strings.Count(string(data), "secrets.yaml") != 1 {
		t.Errorf("annotated 'secrets.yaml' duplicated:\n%s", data)
	}
	if strings.Count(string(data), "talosconfig") != 1 {
		t.Errorf("annotated 'talosconfig' duplicated:\n%s", data)
	}
}

// Contract: writeGitignoreFile prints "Created <path>" the first
// time it actually writes the file and "Updated <path>" on every
// later write. Pinning this guards against a regression where the
// existence-check happened AFTER WriteFile (when the file always
// exists), so a fresh init reported "Updated" for a file it just
// created. The second pass forces a write by changing the required
// kubeconfig basename — writeGitignoreFile early-returns when no
// new entries are needed.
func TestContract_WriteGitignoreFile_CreatedVsUpdatedReporting(t *testing.T) {
	dir := t.TempDir()
	setRoot(t, dir)
	originalKube := Config.GlobalOptions.Kubeconfig
	t.Cleanup(func() { Config.GlobalOptions.Kubeconfig = originalKube })

	captureStderr := func(t *testing.T, fn func()) string {
		t.Helper()
		origStderr := os.Stderr
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatalf("pipe: %v", err)
		}
		os.Stderr = w
		t.Cleanup(func() { os.Stderr = origStderr })
		fn()
		_ = w.Close()
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		return buf.String()
	}

	// First call: fresh tempdir, no .gitignore exists.
	Config.GlobalOptions.Kubeconfig = ""
	first := captureStderr(t, func() {
		if err := writeGitignoreFile(); err != nil {
			t.Fatalf("first writeGitignoreFile: %v", err)
		}
	})
	if !strings.Contains(first, "Created ") {
		t.Errorf("first invocation must print 'Created ...', got:\n%s", first)
	}
	if strings.Contains(first, "Updated ") {
		t.Errorf("first invocation must NOT print 'Updated ...', got:\n%s", first)
	}

	// Second call: change kubeconfig basename so a NEW required entry
	// is added (otherwise writeGitignoreFile returns early without
	// printing anything).
	Config.GlobalOptions.Kubeconfig = "/etc/kubernetes/admin.kubeconfig"
	second := captureStderr(t, func() {
		if err := writeGitignoreFile(); err != nil {
			t.Fatalf("second writeGitignoreFile: %v", err)
		}
	})
	if !strings.Contains(second, "Updated ") {
		t.Errorf("second invocation must print 'Updated ...', got:\n%s", second)
	}
	if strings.Contains(second, "Created ") {
		t.Errorf("second invocation must NOT print 'Created ...', got:\n%s", second)
	}
}
