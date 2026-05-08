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

// Contract: pure helpers across the commands package that do not
// require a live Talos client. Endpoint normalisation, kubeconfig
// server-field rewriting, .gitignore single-entry append,
// no-confirmation-needed file write paths, and Chart.yaml top-level
// name extraction. All user-observable through `talm` CLI flows.

package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// === normalizeEndpoint ===

// Contract: every endpoint variant collapses to a canonical
// `https://<host>:6443` form. This is the URL `talm talosctl` and
// `talm` apply paths use to talk to the apiserver. Any port that the
// user passes (e.g. the maintenance-mode 50000) is dropped — the
// kubelet/kube-proxy port is hardcoded to 6443.
func TestContract_NormalizeEndpoint(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"1.2.3.4", "https://1.2.3.4:6443"},
		{"1.2.3.4:50000", "https://1.2.3.4:6443"},
		{"https://1.2.3.4:50000", "https://1.2.3.4:6443"},
		{"http://1.2.3.4", "https://1.2.3.4:6443"},
		{"https://node.example.com:6443", "https://node.example.com:6443"},
		{"node.example.com", "https://node.example.com:6443"},
		// IPv6 with brackets — net.SplitHostPort returns the bracket-stripped host.
		{"[2001:db8::1]:6443", "https://2001:db8::1:6443"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := normalizeEndpoint(tc.in)
			if got != tc.want {
				t.Errorf("normalizeEndpoint(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// === updateKubeconfigServer ===

// Contract: every cluster's `server:` field in the kubeconfig is
// rewritten to the normalised endpoint. Multi-cluster kubeconfigs
// (talm-managed plus other clusters the operator has) are touched
// indiscriminately — the function does NOT scope by current-context;
// callers needing per-context rewrite must pass a kubeconfig with
// only the relevant cluster.
func TestContract_UpdateKubeconfigServer_RewritesAllClusters(t *testing.T) {
	dir := t.TempDir()
	kcPath := filepath.Join(dir, "kubeconfig")

	cfg := clientcmdapi.NewConfig()
	cfg.Clusters["one"] = &clientcmdapi.Cluster{Server: "https://1.2.3.4:6443"}
	cfg.Clusters["two"] = &clientcmdapi.Cluster{Server: "https://5.6.7.8:6443"}
	if err := clientcmd.WriteToFile(*cfg, kcPath); err != nil {
		t.Fatal(err)
	}

	if err := updateKubeconfigServer(kcPath, "10.0.0.1:50000"); err != nil {
		t.Fatalf("updateKubeconfigServer: %v", err)
	}

	got, err := clientcmd.LoadFromFile(kcPath)
	if err != nil {
		t.Fatal(err)
	}
	for name, c := range got.Clusters {
		if c.Server != "https://10.0.0.1:6443" {
			t.Errorf("cluster %q server = %q, want https://10.0.0.1:6443", name, c.Server)
		}
	}
}

// Contract: when every cluster already points at the normalised
// endpoint, the function is a no-op (does not rewrite the file).
// Pinning prevents a future change that always rewrites and bumps
// mtime/git status spuriously.
func TestContract_UpdateKubeconfigServer_NoChangeWhenAlreadyNormalised(t *testing.T) {
	dir := t.TempDir()
	kcPath := filepath.Join(dir, "kubeconfig")

	cfg := clientcmdapi.NewConfig()
	cfg.Clusters["one"] = &clientcmdapi.Cluster{Server: "https://10.0.0.1:6443"}
	if err := clientcmd.WriteToFile(*cfg, kcPath); err != nil {
		t.Fatal(err)
	}
	infoBefore, _ := os.Stat(kcPath)

	if err := updateKubeconfigServer(kcPath, "10.0.0.1"); err != nil {
		t.Fatal(err)
	}
	infoAfter, _ := os.Stat(kcPath)
	if !infoBefore.ModTime().Equal(infoAfter.ModTime()) {
		t.Errorf("expected no rewrite for already-normalised kubeconfig; mtime changed")
	}
}

// Contract: missing kubeconfig surfaces a precise error.
func TestContract_UpdateKubeconfigServer_MissingFileError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-kubeconfig")
	err := updateKubeconfigServer(missing, "1.2.3.4")
	if err == nil {
		t.Fatal("expected error for missing kubeconfig")
	}
}

// === addToGitignore ===

// Contract: addToGitignore appends a single entry to .gitignore,
// creating the file if absent. Idempotent: a second call with the
// same entry leaves the file unchanged.
func TestContract_AddToGitignore_CreatesAndAppends(t *testing.T) {
	dir := t.TempDir()
	setRoot(t, dir)

	if err := addToGitignore("artifacts/"); err != nil {
		t.Fatalf("addToGitignore: %v", err)
	}
	gitignore := filepath.Join(dir, ".gitignore")
	got, err := os.ReadFile(gitignore)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "artifacts/") {
		t.Errorf("entry missing in .gitignore:\n%s", got)
	}

	// Second call should be idempotent (file unchanged byte-for-byte).
	if err := addToGitignore("artifacts/"); err != nil {
		t.Fatal(err)
	}
	again, _ := os.ReadFile(gitignore)
	if string(got) != string(again) {
		t.Errorf("idempotent call rewrote file:\nbefore:\n%s\nafter:\n%s", got, again)
	}
}

// Contract: when .gitignore already exists with unrelated entries,
// the new entry is appended on its own line (and the existing
// content is preserved verbatim).
func TestContract_AddToGitignore_PreservesExisting(t *testing.T) {
	dir := t.TempDir()
	setRoot(t, dir)

	gitignore := filepath.Join(dir, ".gitignore")
	if err := os.WriteFile(gitignore, []byte("# Sensitive\nsecrets.yaml\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := addToGitignore("artifacts/"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(gitignore)
	for _, want := range []string{"# Sensitive", "secrets.yaml", "artifacts/"} {
		if !strings.Contains(string(got), want) {
			t.Errorf("expected %q in:\n%s", want, got)
		}
	}
}

// Contract: an entry that appears as a path-prefix
// (e.g. existing `dist` matches a request to add `dist/`) is treated
// as already-present. The match is `entry+"/"`-aware, so `dist`
// covers the request `dist`. Pin so a refactor that uses pure equality
// surfaces here.
func TestContract_AddToGitignore_PathPrefixMatch(t *testing.T) {
	dir := t.TempDir()
	setRoot(t, dir)
	gitignore := filepath.Join(dir, ".gitignore")
	if err := os.WriteFile(gitignore, []byte("dist\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := addToGitignore("dist"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(gitignore)
	if strings.Count(string(got), "dist") != 1 {
		t.Errorf("expected exactly one 'dist' entry, got:\n%s", got)
	}
}

// === getClusterNameFromChart ===

// Contract: getClusterNameFromChart reads the top-level `name` from
// Chart.yaml. Distinct from readChartYamlPreset (which reads
// dependencies): this returns the chart's OWN name. Used as a
// fallback in the talosconfig regenerate path.
func TestContract_GetClusterNameFromChart_ReadsTopLevelName(t *testing.T) {
	dir := t.TempDir()
	setRoot(t, dir)
	yaml := "apiVersion: v2\nname: my-cluster\nversion: 0.1.0\n"
	if err := os.WriteFile(filepath.Join(dir, "Chart.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	got := getClusterNameFromChart()
	if got != "my-cluster" {
		t.Errorf("expected 'my-cluster', got %q", got)
	}
}

// Contract: missing Chart.yaml returns empty string (NOT an error).
// Callers chain with a fallback default — silent empty is the
// signal that the chart was not found.
func TestContract_GetClusterNameFromChart_MissingReturnsEmpty(t *testing.T) {
	setRoot(t, t.TempDir())
	if got := getClusterNameFromChart(); got != "" {
		t.Errorf("expected empty string for missing Chart.yaml, got %q", got)
	}
}

// Contract: malformed YAML also returns empty string. Same silent
// fallback as missing — the caller does not need to distinguish.
func TestContract_GetClusterNameFromChart_MalformedReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	setRoot(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "Chart.yaml"), []byte(":bad: yaml :"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := getClusterNameFromChart(); got != "" {
		t.Errorf("expected empty string for malformed YAML, got %q", got)
	}
}

// === updateFileWithConfirmation: no-prompt happy paths ===

// Contract: when the target file does NOT exist, the function
// creates it (and any missing parent directories) without prompting.
// The intended `talm init` flow lays down many files at once; each
// new path is materialised silently.
func TestContract_UpdateFileWithConfirmation_CreatesNew(t *testing.T) {
	dir := t.TempDir()
	setRoot(t, dir)
	target := filepath.Join(dir, "nested", "deep", "file.txt")

	if err := updateFileWithConfirmation(target, []byte("hello"), 0o644); err != nil {
		t.Fatalf("updateFileWithConfirmation: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("file not created: %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("content mismatch: got %q", got)
	}
}

// Contract: when the target file exists with byte-identical
// content, the function is a no-op (does not prompt, does not
// rewrite). Pin so a regression that always touches the file would
// surface as a spurious mtime change.
func TestContract_UpdateFileWithConfirmation_SameContentSkips(t *testing.T) {
	dir := t.TempDir()
	setRoot(t, dir)
	target := filepath.Join(dir, "f")
	if err := os.WriteFile(target, []byte("same"), 0o644); err != nil {
		t.Fatal(err)
	}
	infoBefore, _ := os.Stat(target)
	if err := updateFileWithConfirmation(target, []byte("same"), 0o644); err != nil {
		t.Fatal(err)
	}
	infoAfter, _ := os.Stat(target)
	if !infoBefore.ModTime().Equal(infoAfter.ModTime()) {
		t.Errorf("identical content should not touch mtime")
	}
}
