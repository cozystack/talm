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

package secureperm_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/cozystack/talm/pkg/secureperm"
)

// assertProtectedOwnerOnlyDACL reads the security descriptor back and
// asserts it is structurally D:P(A;;FA;;;<current-user-SID>) — a
// protected DACL with exactly one Allow ACE naming the current user.
// The SDDL for a protected owner-only DACL looks like
//
//	D:PAI(A;;FA;;;<USER-SID>)
//
// where "D:P" marks the DACL as protected, "(A;" prefixes an Allow
// ACE, and the trailing SID names the trustee.
func assertProtectedOwnerOnlyDACL(t *testing.T, path string) {
	t.Helper()

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
		t.Errorf("DACL is not protected (missing D:P flag); SDDL=%q", sddl)
	}
	if got := strings.Count(sddl, "(A;"); got != 1 {
		t.Errorf("DACL has %d Allow ACEs, want exactly 1; SDDL=%q", got, sddl)
	}
	if !strings.Contains(sddl, wantSid) {
		t.Errorf("DACL does not reference current user SID %q; SDDL=%q", wantSid, sddl)
	}
}

// TestWriteFile_NewFile_DACL_Windows pins the happy path: a brand-new
// file created by WriteFile ends up with a protected, owner-only DACL.
func TestWriteFile_NewFile_DACL_Windows(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.txt")

	if err := secureperm.WriteFile(path, []byte("x")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	assertProtectedOwnerOnlyDACL(t, path)
}

// TestWriteFile_Overwrite_DACL_Windows pins the fix for the CreateFile
// + CREATE_ALWAYS + SECURITY_ATTRIBUTES gotcha: per MSDN, the
// SECURITY_ATTRIBUTES descriptor is silently ignored when an existing
// file is opened. If secureperm only relied on CreateFile to apply the
// DACL then a pre-existing secrets.yaml left with a permissive
// inherited DACL would stay permissive after rewrite. The fix
// (SetSecurityInfo on the handle before writing bytes) must make the
// post-write DACL protected and owner-only regardless of prior state.
func TestWriteFile_Overwrite_DACL_Windows(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pre-existing.txt")

	// Seed the file via os.WriteFile — it inherits the TempDir DACL,
	// which on a GitHub Actions runner typically includes BUILTIN\Users.
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := secureperm.WriteFile(path, []byte("new")); err != nil {
		t.Fatalf("WriteFile (overwrite): %v", err)
	}
	assertProtectedOwnerOnlyDACL(t, path)
}

// TestLockDown_DACL_Windows mirrors the overwrite assertion for
// LockDown alone — a file left permissive by some earlier process must
// end up with a protected owner-only DACL after LockDown.
func TestLockDown_DACL_Windows(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "to-tighten.txt")

	if err := os.WriteFile(path, []byte("data"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := secureperm.LockDown(path); err != nil {
		t.Fatalf("LockDown: %v", err)
	}
	assertProtectedOwnerOnlyDACL(t, path)
}
