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
	"regexp"
	"strings"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/cozystack/talm/pkg/secureperm"
)

// extractAllowACETrustee pulls the trustee string out of the first
// Allow ACE in a SDDL string. SDDL ACE shape:
//
//	(ace_type;ace_flags;rights;object_guid;inherit_object_guid;account_sid)
//
// The returned string may be a literal SID ("S-1-5-21-...") or a
// well-known alias ("LA", "BA", "SY", ...) — caller feeds it to
// windows.StringToSid which accepts both forms.
func extractAllowACETrustee(sddl string) (string, error) {
	re := regexp.MustCompile(`\(A;([^)]+)\)`)
	m := re.FindStringSubmatch(sddl)
	if len(m) < 2 {
		return "", &sddlParseError{sddl: sddl, reason: "no Allow ACE"}
	}
	fields := strings.Split(m[1], ";")
	// After the leading "A;" that lives in the regex literal, the inner
	// fields are: ace_flags, rights, object_guid, inherit_object_guid,
	// account_sid[, resource_attribute]. Minimum 5 fields.
	if len(fields) < 5 {
		return "", &sddlParseError{sddl: sddl, reason: "ACE has fewer than 5 fields"}
	}
	return fields[4], nil
}

type sddlParseError struct{ sddl, reason string }

func (e *sddlParseError) Error() string { return e.reason + ": " + e.sddl }

// assertTrusteeMatches resolves the ACE trustee string (literal SID or
// SDDL alias) to a SID and compares to wantSid via EqualSid. SDDL
// output on GitHub Actions runners returns the RID-500 admin as the
// alias "LA" rather than the literal SID, so a string-contains check
// against wantSid.String() is not robust. Resolving both sides to
// canonical *SID and comparing with EqualSid is.
func assertTrusteeMatches(t *testing.T, trusteeStr string, wantSid *windows.SID) {
	t.Helper()
	aceSid, err := windows.StringToSid(trusteeStr)
	if err != nil {
		t.Fatalf("StringToSid(%q): %v", trusteeStr, err)
	}
	if !windows.EqualSid(aceSid, wantSid) {
		t.Errorf("ACE trustee %q (SID %s) != current user SID %s",
			trusteeStr, aceSid.String(), wantSid.String())
	}
}

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

	if !strings.Contains(sddl, "D:P") {
		t.Errorf("DACL is not protected (missing D:P flag); SDDL=%q", sddl)
	}
	if got := strings.Count(sddl, "(A;"); got != 1 {
		t.Errorf("DACL has %d Allow ACEs, want exactly 1; SDDL=%q", got, sddl)
	}
	trusteeStr, err := extractAllowACETrustee(sddl)
	if err != nil {
		t.Fatalf("extract trustee: %v", err)
	}
	assertTrusteeMatches(t, trusteeStr, tu.User.Sid)
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

// TestWriteFile_Overwrite_DACL_Windows pins that overwriting a
// pre-existing file with a permissive inherited DACL leaves the
// final on-disk file with a protected owner-only DACL. secureperm
// achieves this via tmp + rename: the tmp is created fresh with
// CREATE_NEW + SECURITY_ATTRIBUTES so its DACL is owner-only from
// birth, and os.Rename on Windows (MoveFileEx with MOVEFILE_REPLACE_
// EXISTING) carries the tmp's DACL over in place of whatever the
// destination held. Any future regression — for example switching the
// tmp to CREATE_ALWAYS, which silently ignores SECURITY_ATTRIBUTES,
// or writing directly to the destination instead of renaming — would
// cause this test to fail.
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

// TestWriteFile_PreservesOriginalOnFailure_Windows asserts the atomic-
// write contract on Windows: if the rename step fails, the original
// file content is left intact. Secrets files are not reconstructible
// — corrupting secrets.yaml costs the user a cluster PKI reissue.
//
// Failure is induced by setting FILE_ATTRIBUTE_READONLY on the
// destination: MoveFileEx with MOVEFILE_REPLACE_EXISTING fails with
// ERROR_ACCESS_DENIED against a read-only target, so os.Rename
// returns an error and the deferred cleanup in WriteFile removes the
// tmp. The original content must still be readable afterwards.
func TestWriteFile_PreservesOriginalOnFailure_Windows(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secrets.yaml")

	original := []byte("critical-content-DO-NOT-LOSE")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	pathUTF16, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatalf("UTF16PtrFromString: %v", err)
	}
	if err := windows.SetFileAttributes(pathUTF16, windows.FILE_ATTRIBUTE_READONLY); err != nil {
		t.Fatalf("SetFileAttributes: %v", err)
	}
	t.Cleanup(func() {
		// Drop the readonly flag so t.TempDir can clean up.
		_ = windows.SetFileAttributes(pathUTF16, windows.FILE_ATTRIBUTE_NORMAL)
	})

	err = secureperm.WriteFile(path, []byte("replacement"))
	if err == nil {
		t.Fatal("expected WriteFile to fail when destination is read-only")
	}

	got, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("original file missing after failure: %v", readErr)
	}
	if string(got) != string(original) {
		t.Errorf("original content corrupted on failure:\nwant %q\n got %q", original, got)
	}
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
