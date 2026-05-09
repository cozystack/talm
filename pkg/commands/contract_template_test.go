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

// Contract: `talm template` rendering layer above engine.Render.
// generateOutput composes a leading modeline + warning banner with
// the engine-rendered config; resolveEngineTemplatePaths converts
// user-supplied template paths (absolute, relative-from-CWD, outside
// root) into the forward-slash relative form the helm engine uses
// to key into its render map. Both are user-observable through
// `talm template` stdout and `talm template --in-place`.

package commands

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	"gopkg.in/yaml.v3"
)

// withTemplateFlagsSnapshot captures and restores the package-level
// templateCmdFlags + GlobalArgs.{Nodes,Endpoints} so each test can
// mutate freely without poisoning subsequent tests. Mirror of the
// withConfigSnapshot helper for root_dispatch tests.
func withTemplateFlagsSnapshot(t *testing.T) {
	t.Helper()
	flagsSave := templateCmdFlags
	nodesSave := append([]string(nil), GlobalArgs.Nodes...)
	endpointsSave := append([]string(nil), GlobalArgs.Endpoints...)
	rootSave := Config.RootDir
	rootExplicitSave := Config.RootDirExplicit
	t.Cleanup(func() {
		templateCmdFlags = flagsSave
		GlobalArgs.Nodes = nodesSave
		GlobalArgs.Endpoints = endpointsSave
		Config.RootDir = rootSave
		Config.RootDirExplicit = rootExplicitSave
	})
}

// === resolveEngineTemplatePaths ===

// Contract: an absolute path that lies INSIDE the project root is
// returned as a forward-slash path relative to root. The helm
// engine indexes its render map by relative path with forward
// slashes regardless of OS, so any other form would fail lookup.
func TestContract_ResolveEngineTemplatePaths_AbsoluteInsideRoot(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tmpl := filepath.Join(root, "templates", "controlplane.yaml")
	if err := os.MkdirAll(filepath.Dir(tmpl), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tmpl, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	got := resolveEngineTemplatePaths([]string{tmpl}, root)
	if len(got) != 1 || got[0] != "templates/controlplane.yaml" {
		t.Errorf("got %v, want [templates/controlplane.yaml]", got)
	}
}

// Contract: a relative path resolved from CWD that ends up INSIDE
// root is returned as the relative form (no leading slash, no
// duplicated prefix).
func TestContract_ResolveEngineTemplatePaths_RelativeFromCWD(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "templates", "worker.yaml"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	got := resolveEngineTemplatePaths([]string{"templates/worker.yaml"}, root)
	if len(got) != 1 || got[0] != "templates/worker.yaml" {
		t.Errorf("got %v, want [templates/worker.yaml]", got)
	}
}

// Contract: when a `..` path resolves OUTSIDE root but a file with
// the same basename exists under <root>/templates/, the function
// falls back to `templates/<basename>`. This is the documented
// fallback so an operator running from a sibling directory still
// hits the canonical templates/ location.
func TestContract_ResolveEngineTemplatePaths_OutsideRootFallbackToTemplatesBasename(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "templates", "controlplane.yaml"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// Chdir into a directory NEXT to root, then pass a relative path
	// that climbs out and back: ../<rootname>/templates/controlplane.yaml
	// — but craft it so it lies outside `root` per Rel().
	sibling := filepath.Join(filepath.Dir(root), "sibling")
	if err := os.MkdirAll(sibling, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sibling)

	got := resolveEngineTemplatePaths([]string{"../some/other/controlplane.yaml"}, root)
	if len(got) != 1 || got[0] != "templates/controlplane.yaml" {
		t.Errorf("got %v, want [templates/controlplane.yaml] (fallback)", got)
	}
}

// Contract: when a `..` path resolves OUTSIDE root AND no
// templates/<basename> exists, the function returns the input
// normalized through forward slashes. This is the "operator
// supplied a real outside-root absolute path" case — let the helm
// engine error precisely.
func TestContract_ResolveEngineTemplatePaths_OutsideRootNoFallback(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	got := resolveEngineTemplatePaths([]string{"/totally/outside/missing.yaml"}, root)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	// Must not have been silently rewritten to `templates/missing.yaml`
	// (because that file does not exist).
	if got[0] == "templates/missing.yaml" {
		t.Errorf("did not expect templates/ fallback, got %q", got[0])
	}
}

// Contract: an empty input list yields an empty (non-nil) slice
// of the same length — so callers can range without a guard.
func TestContract_ResolveEngineTemplatePaths_EmptyInput(t *testing.T) {
	root := t.TempDir()
	got := resolveEngineTemplatePaths(nil, root)
	if got == nil {
		t.Error("expected non-nil empty slice")
	}
	if len(got) != 0 {
		t.Errorf("expected empty slice, got %v", got)
	}
}

// === generateOutput happy path (offline) ===

// Contract: generateOutput composes three pieces in order — modeline,
// warning banner, engine-rendered bytes — separated by newlines.
// The modeline is the first line so subsequent `talm apply` /
// `talm template` invocations against the rendered file can pick
// up nodes/endpoints/templates without explicit flags.
func TestContract_GenerateOutput_ComposesModelineWarningAndRender(t *testing.T) {
	withTemplateFlagsSnapshot(t)

	chartRoot := makeMinimalChart(t)
	Config.RootDir = chartRoot
	templateCmdFlags = struct {
		insecure          bool
		configFiles       []string
		valueFiles        []string
		templateFiles     []string
		stringValues      []string
		values            []string
		fileValues        []string
		jsonValues        []string
		literalValues     []string
		talosVersion      string
		withSecrets       string
		full              bool
		debug             bool
		offline           bool
		kubernetesVersion string
		inplace           bool
		nodesFromArgs     bool
		endpointsFromArgs bool
		templatesFromArgs bool
	}{
		offline:       true,
		templateFiles: []string{"templates/config.yaml"},
	}
	GlobalArgs.Nodes = []string{testNodeAddrA}
	GlobalArgs.Endpoints = []string{testNodeAddrA}

	got, err := generateOutput(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("generateOutput: %v", err)
	}
	lines := strings.SplitN(got, "\n", 3)
	if len(lines) < 3 {
		t.Fatalf("expected >=3 lines, got %d:\n%s", len(lines), got)
	}
	// Line 1: modeline.
	if !strings.HasPrefix(lines[0], "# talm: ") {
		t.Errorf("expected modeline as first line, got %q", lines[0])
	}
	if !strings.Contains(lines[0], `"10.0.0.1"`) {
		t.Errorf("modeline missing nodes: %q", lines[0])
	}
	if !strings.Contains(lines[0], "templates/config.yaml") {
		t.Errorf("modeline missing template path: %q", lines[0])
	}
	// Line 2: warning banner.
	if !strings.Contains(lines[1], "AUTOGENERATED") {
		t.Errorf("expected AUTOGENERATED warning as second line, got %q", lines[1])
	}
	// Body: rendered config.
	if !strings.Contains(lines[2], "machine:") || !strings.Contains(lines[2], "type: worker") {
		t.Errorf("expected rendered machine config in body, got:\n%s", lines[2])
	}
}

// Contract: a missing template path surfaces a wrapped error with
// the `failed to render templates` prefix. The wrap helps callers
// (and operators reading stderr) distinguish chart-render failures
// from earlier parse failures.
func TestContract_GenerateOutput_MissingTemplateError(t *testing.T) {
	withTemplateFlagsSnapshot(t)

	chartRoot := makeMinimalChart(t)
	Config.RootDir = chartRoot
	templateCmdFlags.offline = true
	templateCmdFlags.templateFiles = []string{"templates/does-not-exist.yaml"}
	GlobalArgs.Nodes = []string{testNodeAddrA}

	_, err := generateOutput(context.Background(), nil, nil)
	if err == nil {
		t.Fatal("expected error for missing template")
	}
	if !strings.Contains(err.Error(), "failed to render templates") {
		t.Errorf("error must wrap with 'failed to render templates', got: %v", err)
	}
}

// Contract: an empty templates list surfaces the engine-side
// 'templates are not set' error wrapped with the same prefix.
func TestContract_GenerateOutput_NoTemplatesError(t *testing.T) {
	withTemplateFlagsSnapshot(t)

	chartRoot := makeMinimalChart(t)
	Config.RootDir = chartRoot
	templateCmdFlags.offline = true
	templateCmdFlags.templateFiles = nil
	GlobalArgs.Nodes = []string{testNodeAddrA}

	_, err := generateOutput(context.Background(), nil, nil)
	if err == nil {
		t.Fatal("expected error for empty templates")
	}
}

// === template ===

// Contract: template(args) returns a function that runs
// generateOutput and prints the result to stdout. Capture stdout
// and assert the modeline header lands.
func TestContract_Template_PrintsToStdout(t *testing.T) {
	withTemplateFlagsSnapshot(t)

	chartRoot := makeMinimalChart(t)
	Config.RootDir = chartRoot
	templateCmdFlags.offline = true
	templateCmdFlags.templateFiles = []string{"templates/config.yaml"}
	GlobalArgs.Nodes = []string{testNodeAddrA}
	GlobalArgs.Endpoints = []string{testNodeAddrA}

	out := captureStdout(t, func() {
		if err := template(nil)(context.Background(), nil); err != nil {
			t.Fatalf("template: %v", err)
		}
	})
	if !strings.Contains(out, "# talm: ") {
		t.Errorf("expected modeline header on stdout, got:\n%s", out)
	}
	if !strings.Contains(out, "AUTOGENERATED") {
		t.Errorf("expected warning banner on stdout, got:\n%s", out)
	}
	if !strings.Contains(out, "type: worker") {
		t.Errorf("expected rendered body on stdout, got:\n%s", out)
	}
}

// Contract: template(args) propagates the wrapped error from
// generateOutput unchanged — does not double-wrap or swallow.
func TestContract_Template_PropagatesError(t *testing.T) {
	withTemplateFlagsSnapshot(t)

	chartRoot := makeMinimalChart(t)
	Config.RootDir = chartRoot
	templateCmdFlags.offline = true
	templateCmdFlags.templateFiles = []string{"templates/missing.yaml"}
	GlobalArgs.Nodes = []string{testNodeAddrA}

	err := template(nil)(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "failed to render templates") {
		t.Errorf("expected 'failed to render templates' wrap, got: %v", err)
	}
}

// === helpers ===

// sharedSecretsBundle is generated once per test process —
// secrets.NewBundle populates a fresh PKI tree (~half a second on a
// laptop), so generating it inside every test would dominate the
// suite runtime. The serialized bytes are reused as the
// secrets.yaml fixture for every minimal chart.
//
//nolint:gochecknoglobals // sync.Once + cached PKI bundle, scoped to the test process; instantiating per-test would dominate runtime
var (
	sharedSecretsOnce sync.Once
	sharedSecretsYAML []byte
	errSharedSecrets  error
)

func loadSharedSecretsYAML(t *testing.T) []byte {
	t.Helper()
	sharedSecretsOnce.Do(func() {
		bundle, err := secrets.NewBundle(secrets.NewClock(), nil)
		if err != nil {
			errSharedSecrets = err
			return
		}
		sharedSecretsYAML, errSharedSecrets = yaml.Marshal(bundle)
	})
	if errSharedSecrets != nil {
		t.Fatalf("generate shared secrets bundle: %v", errSharedSecrets)
	}
	return sharedSecretsYAML
}

// makeMinimalChart writes a chart layout sufficient for `talm
// template --offline`: Chart.yaml, values.yaml, templates/config.yaml
// emitting a worker machine config patch, plus a real serialized
// secrets bundle so engine.Render's bundle.NewBundle path completes.
// Returns the chart root path.
func makeMinimalChart(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Chart.yaml"), []byte("apiVersion: v2\nname: tc\nversion: 0.1.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "values.yaml"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "secrets.yaml"), loadSharedSecretsYAML(t), 0o600); err != nil {
		t.Fatal(err)
	}
	tmplDir := filepath.Join(root, "templates")
	if err := os.MkdirAll(tmplDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmplDir, "config.yaml"), []byte("machine:\n  type: worker\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// captureStdout redirects os.Stdout to a pipe for the duration of
// fn, returns whatever fn printed.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stdout
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = original })

	done := make(chan string, 1)
	go func() {
		buf := make([]byte, 0, 64*1024)
		readBuf := make([]byte, 4096)
		for {
			n, err := r.Read(readBuf)
			if n > 0 {
				buf = append(buf, readBuf[:n]...)
			}
			if err != nil {
				break
			}
		}
		done <- string(buf)
	}()

	fn()
	_ = w.Close()
	return <-done
}
