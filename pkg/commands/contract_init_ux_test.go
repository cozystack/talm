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

// Contract: `talm init` UX guards.
//
// 1. PreRunE refuses when CWD is inside an existing talm project but
//    --root was not set explicitly. Otherwise DetectAndSetRoot would
//    walk up to the ancestor project and init would silently write
//    new files into it.
//
// 2. RunE pre-checks every destination path BEFORE the first write
//    so the command is all-or-nothing — no partial-init state if any
//    destination already exists.

package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// === PreRunE refusal on ancestor project ===

// Contract: when CWD is inside an existing talm project (ancestor
// has Chart.yaml + secrets.yaml) and the operator did NOT pass
// --root, init refuses with an error that names both paths and
// tells the operator how to proceed (--root . to create here,
// or move up to re-init the ancestor).
func TestContract_InitPreRun_RefusesWhenInsideAncestorProject(t *testing.T) {
	withInitFlagsSnapshot(t)
	withConfigSnapshot(t)

	// Stage an ancestor talm project, chdir into a sub-dir of it.
	root := t.TempDir()
	makeProjectRoot(t, root)
	subdir := filepath.Join(root, "deep", "deeper")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(subdir)

	// Simulate DetectAndSetRoot having walked up to the ancestor.
	rootAbs, _ := filepath.Abs(root)
	Config.RootDir = rootAbs
	Config.RootDirExplicit = false

	initCmdFlags.preset = "cozystack"
	initCmdFlags.name = "test-cluster"
	initCmdFlags.encrypt = false
	initCmdFlags.decrypt = false
	initCmdFlags.update = false
	initCmdFlags.image = ""

	err := initCmd.PreRunE(initCmd, nil)
	if err == nil {
		t.Fatal("expected PreRunE to refuse when CWD is inside an ancestor project")
	}
	if !strings.Contains(err.Error(), "inside an existing talm project") {
		t.Errorf("error must explain CWD-is-inside-project, got: %v", err)
	}
	if !strings.Contains(err.Error(), "--root .") {
		t.Errorf("error must point at --root . as the workaround, got: %v", err)
	}
	// Both paths should appear so the operator can see what was detected.
	subdirAbs, _ := filepath.Abs(subdir)
	if !strings.Contains(err.Error(), subdirAbs) {
		t.Errorf("error must name the CWD path %q, got: %v", subdirAbs, err)
	}
	if !strings.Contains(err.Error(), rootAbs) {
		t.Errorf("error must name the ancestor project path %q, got: %v", rootAbs, err)
	}
}

// Contract: when --root was set explicitly, the ancestor-project
// guard does NOT fire — the operator has clearly opted in to a
// specific root. This is the escape hatch the refusal error
// suggests ("pass --root . explicitly").
func TestContract_InitPreRun_RootExplicitSkipsAncestorCheck(t *testing.T) {
	withInitFlagsSnapshot(t)
	withConfigSnapshot(t)

	root := t.TempDir()
	makeProjectRoot(t, root)
	subdir := filepath.Join(root, "deep")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(subdir)

	// --root explicit, pointing at a different location than the
	// detected ancestor.
	Config.RootDir = subdir
	Config.RootDirExplicit = true

	initCmdFlags.preset = "cozystack"
	initCmdFlags.name = "test-cluster"
	initCmdFlags.encrypt = false
	initCmdFlags.decrypt = false
	initCmdFlags.update = false
	initCmdFlags.image = ""

	if err := initCmd.PreRunE(initCmd, nil); err != nil {
		t.Errorf("expected PreRunE to pass with --root explicit, got: %v", err)
	}
}

// Contract: when CWD itself IS the project root (no ancestor walk
// needed), init proceeds normally. Pin so a regression that fires
// the refusal whenever ancestor != "" surfaces here.
func TestContract_InitPreRun_AcceptsWhenCWDIsRoot(t *testing.T) {
	withInitFlagsSnapshot(t)
	withConfigSnapshot(t)

	dir := t.TempDir()
	t.Chdir(dir)
	dirAbs, _ := filepath.Abs(dir)

	Config.RootDir = dirAbs
	Config.RootDirExplicit = false

	initCmdFlags.preset = "cozystack"
	initCmdFlags.name = "test-cluster"
	initCmdFlags.encrypt = false
	initCmdFlags.decrypt = false
	initCmdFlags.update = false
	initCmdFlags.image = ""

	if err := initCmd.PreRunE(initCmd, nil); err != nil {
		t.Errorf("expected PreRunE to pass when CWD is the root, got: %v", err)
	}
}

// === RunE pre-check before any write ===

// Contract: when ANY destination path already exists (without
// --force), `talm init` aborts BEFORE the first write. Files that
// the previous behaviour would have created (talosconfig, talm.key,
// secrets.encrypted.yaml) MUST NOT be on disk after the failed
// init. The error lists every conflict so the operator sees the
// full picture.
//
// Stage one conflict (Chart.yaml) inside a fresh root and assert:
// 1. RunE returns an error mentioning the conflict.
// 2. None of the otherwise-created files (talosconfig, talm.key,
//    .gitignore, secrets.encrypted.yaml) exist after the run.
func TestContract_InitRun_PreCheckRejectsBeforeAnyWrite(t *testing.T) {
	withInitFlagsSnapshot(t)
	withConfigSnapshot(t)

	dir := t.TempDir()
	dirAbs, _ := filepath.Abs(dir)
	t.Chdir(dirAbs)
	Config.RootDir = dirAbs
	Config.RootDirExplicit = true // bypass the ancestor check
	Config.InitOptions.Version = "v0.0.0-test"

	// Stage one conflict.
	if err := os.WriteFile(filepath.Join(dirAbs, "Chart.yaml"), []byte("name: pre-existing\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	initCmdFlags.preset = "cozystack"
	initCmdFlags.name = "test-cluster"
	initCmdFlags.force = false
	initCmdFlags.encrypt = false
	initCmdFlags.decrypt = false
	initCmdFlags.update = false
	initCmdFlags.image = ""

	err := initCmd.RunE(initCmd, nil)
	if err == nil {
		t.Fatal("expected RunE to fail when a destination already exists")
	}
	if !strings.Contains(err.Error(), "refusing to init") {
		t.Errorf("error must mention 'refusing to init', got: %v", err)
	}
	if !strings.Contains(err.Error(), "Chart.yaml") {
		t.Errorf("error must name the conflicting Chart.yaml, got: %v", err)
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Errorf("error must point at --force as the workaround, got: %v", err)
	}

	// None of the otherwise-created files must exist on disk.
	for _, name := range []string{"talosconfig", "talm.key", "secrets.encrypted.yaml", "secrets.yaml", "values.yaml"} {
		if _, statErr := os.Stat(filepath.Join(dirAbs, name)); statErr == nil {
			t.Errorf("partial-write detected: %q exists after pre-check rejection", name)
		}
	}
}

// Contract: --encrypt operates on an ALREADY INITIALISED project,
// where every preset file is expected to exist on disk. The
// pre-check must NOT fire for --encrypt — running it there would
// refuse the very flow the flag is designed for. Pin so a
// regression that drops the encrypt-skip surfaces here.
//
// The test stages a "looks initialised" project (Chart.yaml,
// values.yaml, secrets.yaml, charts/talm/Chart.yaml) and asserts
// that RunE under --encrypt does NOT return a "refusing to init"
// error. The full encrypt flow may still fail on adjacent
// steps unrelated to the pre-check; the contract here is
// specifically that the pre-check does not block.
func TestContract_InitRun_EncryptBypassesPreCheck(t *testing.T) {
	withInitFlagsSnapshot(t)
	withConfigSnapshot(t)

	dir := t.TempDir()
	dirAbs, _ := filepath.Abs(dir)
	t.Chdir(dirAbs)
	Config.RootDir = dirAbs
	Config.RootDirExplicit = true

	// Stage looks-initialised state so the pre-check would fire if
	// it were not gated.
	for _, name := range []string{"Chart.yaml", "values.yaml", "secrets.yaml"} {
		if err := os.WriteFile(filepath.Join(dirAbs, name), []byte("seed\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dirAbs, "charts", "talm"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirAbs, "charts", "talm", "Chart.yaml"), []byte("name: talm\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	initCmdFlags.encrypt = true
	initCmdFlags.force = false
	initCmdFlags.decrypt = false
	initCmdFlags.update = false
	initCmdFlags.preset = ""
	initCmdFlags.name = ""
	initCmdFlags.image = ""

	err := initCmd.RunE(initCmd, nil)
	if err != nil && strings.Contains(err.Error(), "refusing to init") {
		t.Errorf("--encrypt must bypass the preset pre-check, got: %v", err)
	}
}

// Contract: --decrypt operates on the same ALREADY INITIALISED
// project shape and likewise must not fire the pre-check. Pin so
// the decrypt-skip is enforced symmetrically with --encrypt.
func TestContract_InitRun_DecryptBypassesPreCheck(t *testing.T) {
	withInitFlagsSnapshot(t)
	withConfigSnapshot(t)

	dir := t.TempDir()
	dirAbs, _ := filepath.Abs(dir)
	t.Chdir(dirAbs)
	Config.RootDir = dirAbs
	Config.RootDirExplicit = true

	for _, name := range []string{"Chart.yaml", "values.yaml", "secrets.encrypted.yaml"} {
		if err := os.WriteFile(filepath.Join(dirAbs, name), []byte("seed\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dirAbs, "charts", "talm"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirAbs, "charts", "talm", "Chart.yaml"), []byte("name: talm\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	initCmdFlags.decrypt = true
	initCmdFlags.force = false
	initCmdFlags.encrypt = false
	initCmdFlags.update = false
	initCmdFlags.preset = ""
	initCmdFlags.name = ""
	initCmdFlags.image = ""

	err := initCmd.RunE(initCmd, nil)
	if err != nil && strings.Contains(err.Error(), "refusing to init") {
		t.Errorf("--decrypt must bypass the preset pre-check, got: %v", err)
	}
}

// Contract: when --force is set, the pre-check is bypassed and the
// existing destinations are overwritten. Pin so a regression that
// always pre-checks (ignoring --force) surfaces here. The full
// init flow involves PKI generation and chart writes that take a
// real second; we only need to assert the pre-check did NOT
// reject — the existing TestWriteToDestination_* / Init flow tests
// cover the rest.
func TestContract_InitRun_ForceBypassesPreCheck(t *testing.T) {
	withInitFlagsSnapshot(t)
	withConfigSnapshot(t)

	dir := t.TempDir()
	dirAbs, _ := filepath.Abs(dir)
	t.Chdir(dirAbs)
	Config.RootDir = dirAbs
	Config.RootDirExplicit = true
	Config.InitOptions.Version = "v0.0.0-test"

	// Stage one conflict — Chart.yaml.
	if err := os.WriteFile(filepath.Join(dirAbs, "Chart.yaml"), []byte("name: pre-existing\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	initCmdFlags.preset = "cozystack"
	initCmdFlags.name = "test-cluster"
	initCmdFlags.force = true // <- bypass
	initCmdFlags.encrypt = false
	initCmdFlags.decrypt = false
	initCmdFlags.update = false
	initCmdFlags.image = ""

	err := initCmd.RunE(initCmd, nil)
	// The full init flow may succeed or fail for unrelated reasons
	// (PKI generation cost, secrets bundle generation). The contract
	// here is specifically that the pre-check did NOT reject — i.e.
	// the error, if any, does NOT mention "refusing to init".
	if err != nil && strings.Contains(err.Error(), "refusing to init") {
		t.Errorf("--force should bypass the pre-check, got: %v", err)
	}
}
