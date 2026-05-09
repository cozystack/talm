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

// File-local fixtures for the helpers contract tests. The literals
// these constants stand in for show up across many table-driven and
// scenario tests; centralising them lets goconst stop firing.
const (
	fixtureEndpointIPv4Canonical     = "https://1.2.3.4:6443"
	fixtureEndpointHostnameCanonical = "https://node.example.com:6443"
	fixtureEndpointIPv6Canonical     = "https://[2001:db8::1]:6443"
	fixtureEndpointRotatedCanonical  = "https://10.0.0.1:6443"
	fixtureClusterNameMy             = "my-cluster"
	fixtureClusterNameFallback       = "chart-fallback"
)

// === normalizeEndpoint ===

// Contract: every endpoint variant collapses to a canonical
// `https://<host>:6443` form. This is the URL `talm talosctl` and
// `talm` apply paths use to talk to the apiserver. Any port that the
// user passes (e.g. the maintenance-mode 50000) is dropped — the
// kubelet/kube-proxy port is hardcoded to 6443.
func TestContract_NormalizeEndpoint(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"ipv4_no_port", "1.2.3.4", fixtureEndpointIPv4Canonical},
		{"ipv4_with_port", "1.2.3.4:50000", fixtureEndpointIPv4Canonical},
		{"ipv4_https_with_port", "https://1.2.3.4:50000", fixtureEndpointIPv4Canonical},
		{"ipv4_http", "http://1.2.3.4", fixtureEndpointIPv4Canonical},
		{"hostname_https_canonical", fixtureEndpointHostnameCanonical, fixtureEndpointHostnameCanonical},
		{"hostname_no_port", "node.example.com", fixtureEndpointHostnameCanonical},
		// IPv6 with brackets — net.JoinHostPort re-adds them for any
		// host containing a colon, so the canonical output is
		// "https://[2001:db8::1]:6443". URI-bracketed IPv6 literals
		// per RFC 3986 §3.2.2.
		{"ipv6_bracketed_with_port", "[2001:db8::1]:6443", fixtureEndpointIPv6Canonical},
		// IPv6 without explicit port — bare bracketed literal. The
		// no-port branch strips the outer brackets so JoinHostPort
		// can add exactly one pair back.
		{"ipv6_bracketed_no_port", "[2001:db8::1]", fixtureEndpointIPv6Canonical},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
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
	cfg.Clusters["one"] = &clientcmdapi.Cluster{Server: fixtureEndpointIPv4Canonical}
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
		if c.Server != fixtureEndpointRotatedCanonical {
			t.Errorf("cluster %q server = %q, want %s", name, c.Server, fixtureEndpointRotatedCanonical)
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
	cfg.Clusters["one"] = &clientcmdapi.Cluster{Server: fixtureEndpointRotatedCanonical}
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
	if err := os.WriteFile(gitignore, []byte("# Sensitive\n"+localSecretsYamlName+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := addToGitignore("artifacts/"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(gitignore)
	for _, want := range []string{"# Sensitive", localSecretsYamlName, "artifacts/"} {
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

// Contract: getClusterNameFromChart resolves the cluster name with
// `values.yaml: clusterName` taking precedence over `Chart.yaml: name`.
// Distinct from readChartYamlPreset (which reads dependencies): this
// returns the chart's OWN name. Used as a fallback in the talosconfig
// regenerate path so a re-generated talosconfig matches the cluster
// name baked into the rendered chart output.
//
// The resolution order is values.yaml.clusterName -> Chart.yaml.name
// -> "" (fallback). The order matters: an operator who overrides
// clusterName via values.yaml expects the regenerated talosconfig
// context to use that override, not the chart-directory name.

// Contract: when only Chart.yaml exists (no values.yaml), the
// function returns Chart.yaml.name. This is the legacy behaviour
// preserved for projects that have not yet adopted the values.yaml
// override.
func TestContract_GetClusterNameFromChart_ReadsTopLevelName(t *testing.T) {
	dir := t.TempDir()
	setRoot(t, dir)
	yaml := "apiVersion: v2\nname: " + fixtureClusterNameMy + "\nversion: 0.1.0\n"
	if err := os.WriteFile(filepath.Join(dir, "Chart.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	got := getClusterNameFromChart()
	if got != fixtureClusterNameMy {
		t.Errorf("expected %q, got %q", fixtureClusterNameMy, got)
	}
}

// Contract: when both files exist and values.yaml declares a
// non-empty clusterName, the values.yaml override wins. Pin the
// priority so a regression that re-orders the lookup surfaces here.
func TestContract_GetClusterNameFromChart_ValuesYamlOverridesChartYaml(t *testing.T) {
	dir := t.TempDir()
	setRoot(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "Chart.yaml"), []byte("apiVersion: v2\nname: chart-name-loser\nversion: 0.1.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "values.yaml"), []byte("clusterName: values-name-winner\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := getClusterNameFromChart()
	if got != "values-name-winner" {
		t.Errorf("expected values.yaml clusterName to win, got %q", got)
	}
}

// Contract: when values.yaml exists but clusterName is empty (the
// shipped default in cozystack/generic charts), the function falls
// through to Chart.yaml.name. Pinning so the empty-string short
// circuit is preserved — without it, every fresh install with the
// default values.yaml would resolve to "" and downstream callers
// would silently substitute their own placeholder.
func TestContract_GetClusterNameFromChart_EmptyValuesClusterNameFallsBack(t *testing.T) {
	dir := t.TempDir()
	setRoot(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "Chart.yaml"), []byte("apiVersion: v2\nname: "+fixtureClusterNameFallback+"\nversion: 0.1.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "values.yaml"), []byte("clusterName: \"\"\nendpoint: \"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := getClusterNameFromChart()
	if got != fixtureClusterNameFallback {
		t.Errorf("expected fallback to Chart.yaml name, got %q", got)
	}
}

// Contract: when values.yaml has no clusterName key at all (any
// shape that does not declare it), the function falls through to
// Chart.yaml.name. The yaml.Unmarshal into the typed struct returns
// the zero string for the missing field, which must be treated the
// same as an explicit empty value.
func TestContract_GetClusterNameFromChart_AbsentValuesKeyFallsBack(t *testing.T) {
	dir := t.TempDir()
	setRoot(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "Chart.yaml"), []byte("apiVersion: v2\nname: "+fixtureClusterNameFallback+"\nversion: 0.1.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "values.yaml"), []byte("endpoint: \"https://example.com:6443\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := getClusterNameFromChart()
	if got != fixtureClusterNameFallback {
		t.Errorf("expected fallback to Chart.yaml name, got %q", got)
	}
}

// Contract: a malformed values.yaml does not poison the lookup —
// the function silently moves on to Chart.yaml.name. Treating a
// values.yaml syntax error as "no override" means the regenerate
// path stays usable even when the operator has a half-edited
// values.yaml on disk.
func TestContract_GetClusterNameFromChart_MalformedValuesFallsBack(t *testing.T) {
	dir := t.TempDir()
	setRoot(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "Chart.yaml"), []byte("apiVersion: v2\nname: "+fixtureClusterNameFallback+"\nversion: 0.1.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "values.yaml"), []byte(":bad: yaml :"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := getClusterNameFromChart()
	if got != fixtureClusterNameFallback {
		t.Errorf("expected fallback on malformed values.yaml, got %q", got)
	}
}

// Contract: missing Chart.yaml AND missing values.yaml returns
// empty string (NOT an error). Callers chain with a fallback default
// — silent empty is the signal that no chart context exists.
func TestContract_GetClusterNameFromChart_MissingReturnsEmpty(t *testing.T) {
	setRoot(t, t.TempDir())
	if got := getClusterNameFromChart(); got != "" {
		t.Errorf("expected empty string for missing Chart.yaml, got %q", got)
	}
}

// Contract: malformed Chart.yaml returns empty string (when
// values.yaml does not provide an override). Same silent fallback
// as missing — the caller does not need to distinguish.
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
