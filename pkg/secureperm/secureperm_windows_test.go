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
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/cozystack/talm/pkg/secureperm"
)

// TestWriteFile_DACL_OwnerOnly confirms that WriteFile produces a
// protected DACL (inheritance blocked) containing exactly one Allow ACE
// for the current user SID. Without the DACL the file inherits
// BUILTIN\Users from the parent — that is the whole point of the
// Windows variant.
//
// Assertion strategy: read the SDDL string of the security descriptor
// and match structurally. The SDDL for a protected, owner-only DACL
// looks like:  D:PAI(A;;FA;;;<USER-SID>)
//   - "D:P" marks the DACL as protected (the PROTECTED flag we set)
//   - "(A;" appears exactly once (one Allow ACE, no others)
//   - the current user SID appears in that ACE
func TestWriteFile_DACL_OwnerOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.txt")

	if err := secureperm.WriteFile(path, []byte("x")); err != nil {
		t.Fatalf("WriteFile: %v", err)
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

	// Current user SID for the expected trustee.
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
