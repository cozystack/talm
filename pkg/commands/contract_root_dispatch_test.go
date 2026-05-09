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

// Contract: the dispatch layer above DetectProjectRoot —
// getFlagValues, detectRootFromFiles/Templates/CWD,
// checkRootConflict, DetectAndSetRoot, DetectAndSetRootFromFiles,
// EnsureTalosconfigPath. These functions decide which directory
// becomes Config.RootDir based on cobra flags + os.Args + CWD.
// They mutate package-level Config and GlobalArgs, so each test
// captures and restores the prior state.

package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// withConfigSnapshot captures the package-level Config and GlobalArgs
// state before the test and restores them on cleanup. Without this
// every test that mutates Config.RootDir / RootDirExplicit /
// GlobalArgs leaks state into the next test in the same `go test`
// process.
func withConfigSnapshot(t *testing.T) {
	t.Helper()
	rootDir := Config.RootDir
	rootDirExplicit := Config.RootDirExplicit
	talosconfig := GlobalArgs.Talosconfig
	talosconfigCfg := Config.GlobalOptions.Talosconfig
	t.Cleanup(func() {
		Config.RootDir = rootDir
		Config.RootDirExplicit = rootDirExplicit
		GlobalArgs.Talosconfig = talosconfig
		Config.GlobalOptions.Talosconfig = talosconfigCfg
	})
}

// withOSArgs replaces os.Args for the duration of the test. cobra's
// ParseFlags reads os.Args; some root-detection paths fall back to
// parseFlagFromArgs(os.Args[1:]) when cobra's slice is empty.
func withOSArgs(t *testing.T, args []string) {
	t.Helper()
	original := os.Args
	t.Cleanup(func() { os.Args = original })
	os.Args = args
}

// makeCmdWithStringSliceFlag creates a cobra.Command with a
// `StringSliceP` flag named `flagName`. Used to exercise getFlagValues.
func makeCmdWithStringSliceFlag(flagName, shortFlag string, defaults []string, persistent bool) *cobra.Command {
	cmd := &cobra.Command{Use: "test"}
	if persistent {
		cmd.PersistentFlags().StringSliceP(flagName, shortFlag, defaults, "")
	} else {
		cmd.Flags().StringSliceP(flagName, shortFlag, defaults, "")
	}
	return cmd
}

// === getFlagValues ===

// Contract: when the flag is registered as a non-persistent
// StringSlice flag and the user has set values, getFlagValues
// returns those values.
func TestContract_GetFlagValues_NonPersistent(t *testing.T) {
	cmd := makeCmdWithStringSliceFlag("file", "f", nil, false)
	if err := cmd.Flags().Set("file", "a.yaml,b.yaml"); err != nil {
		t.Fatal(err)
	}
	got := getFlagValues(cmd, "file")
	if len(got) != 2 || got[0] != "a.yaml" || got[1] != "b.yaml" {
		t.Errorf("expected [a.yaml b.yaml], got %v", got)
	}
}

// Contract: persistent flags work too. cobra splits flags into
// command-local and persistent buckets; getFlagValues checks both.
func TestContract_GetFlagValues_Persistent(t *testing.T) {
	cmd := makeCmdWithStringSliceFlag("template", "t", nil, true)
	if err := cmd.PersistentFlags().Set("template", "templates/cp.yaml"); err != nil {
		t.Fatal(err)
	}
	got := getFlagValues(cmd, "template")
	if len(got) != 1 || got[0] != "templates/cp.yaml" {
		t.Errorf("expected [templates/cp.yaml], got %v", got)
	}
}

// Contract: when the flag is not registered at all, the function
// returns an empty (non-nil) slice — never nil — so callers can
// `range` it without a guard. Pin the non-nil property explicitly.
func TestContract_GetFlagValues_AbsentFlagReturnsEmpty(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	got := getFlagValues(cmd, "nope")
	if got == nil {
		t.Error("expected non-nil empty slice")
	}
	if len(got) != 0 {
		t.Errorf("expected empty slice, got %v", got)
	}
}

// Contract: a registered flag with no user value returns empty even
// when defaults are declared (the function checks `len(values) > 0`
// after `GetStringSlice`, so empty defaults stay empty).
func TestContract_GetFlagValues_RegisteredButUnset(t *testing.T) {
	cmd := makeCmdWithStringSliceFlag("file", "f", nil, false)
	got := getFlagValues(cmd, "file")
	if len(got) != 0 {
		t.Errorf("expected empty, got %v", got)
	}
}

// === detectRootFromFiles / detectRootFromTemplates / detectRootFromCWD ===

// Contract: empty input yields ('', nil) — caller falls through to
// the next detection strategy.
func TestContract_DetectRootFromFiles_EmptyInput(t *testing.T) {
	got, err := detectRootFromFiles(nil)
	if err != nil || got != "" {
		t.Errorf("expected ('', nil), got (%q, %v)", got, err)
	}
}

func TestContract_DetectRootFromTemplates_EmptyInput(t *testing.T) {
	got, err := detectRootFromTemplates(nil)
	if err != nil || got != "" {
		t.Errorf("expected ('', nil), got (%q, %v)", got, err)
	}
}

// Contract: with a real file under a project root, both functions
// return the abs path of the root.
func TestContract_DetectRootFromFiles_PositiveCase(t *testing.T) {
	root := t.TempDir()
	makeProjectRoot(t, root)
	file := filepath.Join(root, "node.yaml")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := detectRootFromFiles([]string{file})
	if err != nil {
		t.Fatal(err)
	}
	rootAbs, _ := filepath.Abs(root)
	if got != rootAbs {
		t.Errorf("got %q, want %q", got, rootAbs)
	}
}

func TestContract_DetectRootFromTemplates_PositiveCase(t *testing.T) {
	root := t.TempDir()
	makeProjectRoot(t, root)
	tmpl := filepath.Join(root, "templates", "controlplane.yaml")
	if err := os.MkdirAll(filepath.Dir(tmpl), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tmpl, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := detectRootFromTemplates([]string{tmpl})
	if err != nil {
		t.Fatal(err)
	}
	rootAbs, _ := filepath.Abs(root)
	if got != rootAbs {
		t.Errorf("got %q, want %q", got, rootAbs)
	}
}

// Contract: detectRootFromCWD walks up from the current working
// directory. Test by chdir-ing into a sub-directory of a project
// root, then asserting recovery.
func TestContract_DetectRootFromCWD_WalksUp(t *testing.T) {
	root := t.TempDir()
	makeProjectRoot(t, root)
	subdir := filepath.Join(root, "deep", "deeper")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(subdir)

	got, err := detectRootFromCWD()
	if err != nil {
		t.Fatal(err)
	}
	rootAbs, _ := filepath.Abs(root)
	gotAbs, _ := filepath.Abs(got)
	// On macOS, t.TempDir lives under /var/folders/... which is a
	// symlink to /private/var/folders/... — compare via Abs to be
	// robust to that resolution.
	if !strings.HasSuffix(gotAbs, rootAbs) && !strings.HasSuffix(rootAbs, gotAbs) {
		// Fallback: relative-equality after EvalSymlinks.
		gotEval, _ := filepath.EvalSymlinks(gotAbs)
		rootEval, _ := filepath.EvalSymlinks(rootAbs)
		if gotEval != rootEval {
			t.Errorf("got %q, want %q (eval got %q, want %q)", gotAbs, rootAbs, gotEval, rootEval)
		}
	}
}

// === checkRootConflict ===

// Contract: when --root is not explicit, conflict check is a no-op
// regardless of detected root. Pin so a regression that always
// errors on mismatch surfaces here.
func TestContract_CheckRootConflict_NotExplicit(t *testing.T) {
	withConfigSnapshot(t)
	Config.RootDir = "/some/path"
	if err := checkRootConflict("/different/path", false); err != nil {
		t.Errorf("expected nil error when --root not explicit, got %v", err)
	}
}

// Contract: when --root IS explicit and matches the detected root,
// no error.
func TestContract_CheckRootConflict_ExplicitMatching(t *testing.T) {
	withConfigSnapshot(t)
	Config.RootDir = "/some/path"
	if err := checkRootConflict("/some/path", true); err != nil {
		t.Errorf("matching paths should not error, got %v", err)
	}
}

// Contract: when --root IS explicit and DIFFERS from detected, error
// names both paths. The error guides the operator to either drop
// --root or move the files. The function emits filepath.Abs of each
// path, which on Windows resolves to a drive-prefixed `D:\...` form;
// the assertion checks for the trailing path components rather than
// the full literal so it survives both POSIX and Windows.
func TestContract_CheckRootConflict_ExplicitConflict(t *testing.T) {
	withConfigSnapshot(t)
	explicit := filepath.Join(string(filepath.Separator), "explicit", "root")
	detected := filepath.Join(string(filepath.Separator), "detected", "root")
	Config.RootDir = explicit
	err := checkRootConflict(detected, true)
	if err == nil {
		t.Fatal("expected error for conflicting roots")
	}
	explicitAbs, _ := filepath.Abs(explicit)
	detectedAbs, _ := filepath.Abs(detected)
	if !strings.Contains(err.Error(), explicitAbs) || !strings.Contains(err.Error(), detectedAbs) {
		t.Errorf("error must name both abs paths (%q, %q), got: %v", explicitAbs, detectedAbs, err)
	}
}

// === DetectAndSetRoot ===

// Contract: DetectAndSetRoot with a -f flag pointing at a file under
// a project root sets Config.RootDir to that root. The flag wins
// over CWD.
func TestContract_DetectAndSetRoot_FromFileFlag(t *testing.T) {
	withConfigSnapshot(t)

	root := t.TempDir()
	makeProjectRoot(t, root)
	nodeFile := filepath.Join(root, "node.yaml")
	if err := os.WriteFile(nodeFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := &cobra.Command{Use: "test"}
	cmd.PersistentFlags().String("root", "", "")
	cmd.Flags().StringSliceP("file", "f", nil, "")
	cmd.Flags().StringSliceP("template", "t", nil, "")
	if err := cmd.Flags().Set("file", nodeFile); err != nil {
		t.Fatal(err)
	}
	withOSArgs(t, []string{"talm"})

	if err := DetectAndSetRoot(cmd, nil); err != nil {
		t.Fatalf("DetectAndSetRoot: %v", err)
	}
	rootAbs, _ := filepath.Abs(root)
	if Config.RootDir != rootAbs {
		t.Errorf("Config.RootDir = %q, want %q", Config.RootDir, rootAbs)
	}
}

// Contract: when -f points at files under DIFFERENT project roots,
// the function errors. Pinning the strict-consistency rule.
func TestContract_DetectAndSetRoot_FilesInDifferentRootsError(t *testing.T) {
	withConfigSnapshot(t)

	rootA := t.TempDir()
	rootB := t.TempDir()
	makeProjectRoot(t, rootA)
	makeProjectRoot(t, rootB)
	fileA := filepath.Join(rootA, "a.yaml")
	fileB := filepath.Join(rootB, "b.yaml")
	for _, f := range []string{fileA, fileB} {
		if err := os.WriteFile(f, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	cmd := &cobra.Command{Use: "test"}
	cmd.PersistentFlags().String("root", "", "")
	cmd.Flags().StringSliceP("file", "f", nil, "")
	cmd.Flags().StringSliceP("template", "t", nil, "")
	if err := cmd.Flags().Set("file", fileA); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("file", fileB); err != nil {
		t.Fatal(err)
	}
	withOSArgs(t, []string{"talm"})

	err := DetectAndSetRoot(cmd, nil)
	if err == nil {
		t.Fatal("expected error for files in different roots")
	}
}

// Contract: with no flags and no project markers reachable from
// CWD, DetectAndSetRoot returns nil and leaves Config.RootDir
// untouched (or sets it to whatever the CWD walk-up yielded — the
// function tolerates "no project here").
func TestContract_DetectAndSetRoot_NoFlagsNoMarkersIsTolerated(t *testing.T) {
	withConfigSnapshot(t)

	emptyDir := t.TempDir()
	t.Chdir(emptyDir)

	cmd := &cobra.Command{Use: "test"}
	cmd.PersistentFlags().String("root", "", "")
	cmd.Flags().StringSliceP("file", "f", nil, "")
	cmd.Flags().StringSliceP("template", "t", nil, "")
	withOSArgs(t, []string{"talm"})

	if err := DetectAndSetRoot(cmd, nil); err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
}

// === DetectAndSetRootFromFiles ===

// Contract: DetectAndSetRootFromFiles with files all under one
// project root sets Config.RootDir to that root. Used by `talm
// apply` / `talm upgrade` to anchor on the operator's `-f` files
// when invoked without `--root`.
func TestContract_DetectAndSetRootFromFiles_HappyPath(t *testing.T) {
	withConfigSnapshot(t)
	Config.RootDirExplicit = false

	root := t.TempDir()
	makeProjectRoot(t, root)
	file := filepath.Join(root, "n.yaml")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := DetectAndSetRootFromFiles([]string{file}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rootAbs, _ := filepath.Abs(root)
	if Config.RootDir != rootAbs {
		t.Errorf("Config.RootDir = %q, want %q", Config.RootDir, rootAbs)
	}
}

// Contract: empty input + RootDirExplicit=false falls back to CWD
// detection. No error if CWD has no markers — the function leaves
// Config.RootDir untouched.
func TestContract_DetectAndSetRootFromFiles_EmptyInputUsesCWD(t *testing.T) {
	withConfigSnapshot(t)
	Config.RootDirExplicit = false
	root := t.TempDir()
	makeProjectRoot(t, root)
	t.Chdir(root)

	if err := DetectAndSetRootFromFiles(nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Config.RootDir should now be `root` (or its symlink-resolved
	// form on macOS).
	gotEval, _ := filepath.EvalSymlinks(Config.RootDir)
	rootEval, _ := filepath.EvalSymlinks(root)
	if gotEval != rootEval {
		t.Errorf("Config.RootDir resolved = %q, want %q", gotEval, rootEval)
	}
}

// Contract: when --root was explicit AND the files belong to a
// different root, the function errors and names both roots.
func TestContract_DetectAndSetRootFromFiles_ExplicitConflict(t *testing.T) {
	withConfigSnapshot(t)

	rootExplicit := t.TempDir()
	rootFiles := t.TempDir()
	makeProjectRoot(t, rootExplicit)
	makeProjectRoot(t, rootFiles)
	file := filepath.Join(rootFiles, "n.yaml")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	Config.RootDir = rootExplicit
	Config.RootDirExplicit = true

	err := DetectAndSetRootFromFiles([]string{file})
	if err == nil {
		t.Fatal("expected conflict error")
	}
	if !strings.Contains(err.Error(), "conflicting") {
		t.Errorf("error must mention 'conflicting', got: %v", err)
	}
}

// === EnsureTalosconfigPath ===

// Contract: when --talosconfig was explicitly set, EnsureTalosconfigPath
// is a no-op (does not overwrite GlobalArgs.Talosconfig).
func TestContract_EnsureTalosconfigPath_NoOpWhenChanged(t *testing.T) {
	withConfigSnapshot(t)

	cmd := &cobra.Command{Use: "test"}
	cmd.PersistentFlags().String("talosconfig", "", "")
	if err := cmd.PersistentFlags().Set("talosconfig", "/explicit/path"); err != nil {
		t.Fatal(err)
	}
	GlobalArgs.Talosconfig = "/explicit/path"

	EnsureTalosconfigPath(cmd)
	if GlobalArgs.Talosconfig != "/explicit/path" {
		t.Errorf("expected unchanged, got %q", GlobalArgs.Talosconfig)
	}
}

// Contract: when --talosconfig is unset and the chart-resolved
// GlobalArgs.Talosconfig is also empty, the path defaults to
// `<RootDir>/talosconfig`. Built via filepath.Join so the assertion
// matches the OS-native separator on Windows too.
func TestContract_EnsureTalosconfigPath_DefaultsToRoot(t *testing.T) {
	withConfigSnapshot(t)

	cmd := &cobra.Command{Use: "test"}
	cmd.PersistentFlags().String("talosconfig", "", "")
	root := filepath.Join(string(filepath.Separator), "some", "project")
	Config.RootDir = root
	Config.GlobalOptions.Talosconfig = ""
	GlobalArgs.Talosconfig = ""

	EnsureTalosconfigPath(cmd)
	want := filepath.Join(root, "talosconfig")
	if GlobalArgs.Talosconfig != want {
		t.Errorf("expected %q, got %q", want, GlobalArgs.Talosconfig)
	}
}

// Contract: when GlobalArgs.Talosconfig is already set (e.g. by
// Chart.yaml's globalOptions.talosconfig), that value is used. Pins
// the precedence: Chart.yaml > the literal "talosconfig" default.
// Relative paths still get anchored to RootDir.
func TestContract_EnsureTalosconfigPath_RelativeAnchoredToRoot(t *testing.T) {
	withConfigSnapshot(t)

	cmd := &cobra.Command{Use: "test"}
	cmd.PersistentFlags().String("talosconfig", "", "")
	root := filepath.Join(string(filepath.Separator), "some", "project")
	Config.RootDir = root
	GlobalArgs.Talosconfig = "talosconfig.encrypted"

	EnsureTalosconfigPath(cmd)
	want := filepath.Join(root, "talosconfig.encrypted")
	if GlobalArgs.Talosconfig != want {
		t.Errorf("expected %q, got %q", want, GlobalArgs.Talosconfig)
	}
}

// Contract: an absolute path in GlobalArgs.Talosconfig stays
// untouched (no anchoring).
func TestContract_EnsureTalosconfigPath_AbsolutePathPreserved(t *testing.T) {
	withConfigSnapshot(t)

	cmd := &cobra.Command{Use: "test"}
	cmd.PersistentFlags().String("talosconfig", "", "")
	Config.RootDir = filepath.Join(string(filepath.Separator), "some", "project")
	abs := filepath.Join(string(filepath.Separator), "etc", "talos", "config")
	GlobalArgs.Talosconfig = abs

	EnsureTalosconfigPath(cmd)
	if GlobalArgs.Talosconfig != abs {
		t.Errorf("expected %q preserved, got %q", abs, GlobalArgs.Talosconfig)
	}
}

// === updateKubeconfigEndpoint ===

// Contract: updateKubeconfigEndpoint takes raw kubeconfig bytes,
// rewrites every cluster's `server:` field to https://<host>:6443,
// and returns the reserialised bytes. Used by the rotate-CA flow
// where kubeconfig content lives in memory rather than on disk.
func TestContract_UpdateKubeconfigEndpoint_RewritesAllClusters(t *testing.T) {
	src := []byte(`apiVersion: v1
kind: Config
clusters:
- name: one
  cluster:
    server: https://1.2.3.4:6443
- name: two
  cluster:
    server: https://5.6.7.8:6443
contexts: []
users: []
`)
	out, err := updateKubeconfigEndpoint(src, "10.0.0.1:50000")
	if err != nil {
		t.Fatalf("updateKubeconfigEndpoint: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, "server: https://10.0.0.1:6443") {
		t.Errorf("expected rewritten server, got:\n%s", got)
	}
	if strings.Contains(got, "1.2.3.4") || strings.Contains(got, "5.6.7.8") {
		t.Errorf("old servers leaked:\n%s", got)
	}
}

// Contract: malformed kubeconfig surfaces a parse error.
func TestContract_UpdateKubeconfigEndpoint_MalformedError(t *testing.T) {
	_, err := updateKubeconfigEndpoint([]byte("this is not yaml"), "10.0.0.1")
	if err == nil {
		t.Fatal("expected error for malformed kubeconfig")
	}
}
