//go:build windows

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

package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// TestResolveEngineTemplatePaths_BackslashInput is the direct
// regression guard for the original reproducer: a PowerShell user
// invoking `talm template -t templates\controlplane.yaml` must end up
// with a forward-slash path, because the helm engine only indexes
// templates by forward-slash map keys. A backslash anywhere in the
// output string means the engine emits "template not found".
//
// Exercising the template command's own resolver (not apply's near-
// duplicate resolver in resolveTemplatePaths) is the point of this
// test — the two code paths are structurally different (apply
// resolves relative against rootDir; template resolves relative
// against CWD with a templates/<basename> fallback), so covering one
// does not cover the other.
func TestResolveEngineTemplatePaths_BackslashInput(t *testing.T) {
	rootDir := t.TempDir()
	absRoot, err := filepath.Abs(rootDir)
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}

	// Seed templates/controlplane.yaml so the fallback branch has
	// something to find.
	if err := os.MkdirAll(filepath.Join(absRoot, "templates"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(absRoot, "templates", "controlplane.yaml"), []byte{}, 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Chdir so relative paths resolve against rootDir exactly as they
	// would when a user cd's into their project directory and runs
	// `talm template -t templates\controlplane.yaml`.
	origCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(rootDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origCwd) })

	inputs := []string{
		`templates\controlplane.yaml`,
		`templates\nested\worker.yaml`,
		`templates\nested/mixed.yaml`,
		filepath.Join(absRoot, "templates", "controlplane.yaml"),
	}

	got := resolveEngineTemplatePaths(inputs, rootDir)
	if len(got) != len(inputs) {
		t.Fatalf("len mismatch: got %d, want %d", len(got), len(inputs))
	}
	for i, r := range got {
		if strings.ContainsRune(r, '\\') {
			t.Errorf("input[%d]=%q resolved to %q which contains backslash; helm engine lookup would fail",
				i, inputs[i], r)
		}
	}
}

// TestWriteInplaceRendered_ProtectedDACL_Windows pins that `talm
// template --inplace` writes the rendered Talos machine config (which
// embeds certs, PKI keys, and cluster join tokens) under an NTFS
// protected owner-only DACL, not under the directory's inherited
// permissive one. The SDDL assertion mirrors the shape used in
// pkg/secureperm/secureperm_windows_test.go — a protected DACL with
// exactly one Allow ACE naming the current user SID.
func TestWriteInplaceRendered_ProtectedDACL_Windows(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "node0.yaml")

	if err := writeInplaceRendered(path, "apiVersion: v1alpha1\nkind: MachineConfig\n"); err != nil {
		t.Fatalf("writeInplaceRendered: %v", err)
	}

	sd, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		t.Fatalf("GetNamedSecurityInfo: %v", err)
	}
	sddl := sd.String()

	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token); err != nil {
		t.Fatalf("OpenProcessToken: %v", err)
	}
	defer func() { _ = token.Close() }()
	tu, err := token.GetTokenUser()
	if err != nil {
		t.Fatalf("GetTokenUser: %v", err)
	}
	wantSid := tu.User.Sid.String()

	if !strings.Contains(sddl, "D:P") {
		t.Errorf("DACL not protected; SDDL=%q", sddl)
	}
	if got := strings.Count(sddl, "(A;"); got != 1 {
		t.Errorf("DACL has %d Allow ACEs, want 1; SDDL=%q", got, sddl)
	}
	if !strings.Contains(sddl, wantSid) {
		t.Errorf("DACL does not reference current user SID %q; SDDL=%q", wantSid, sddl)
	}
}
