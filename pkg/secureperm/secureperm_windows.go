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

package secureperm

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ownerOnlyDACL builds an ACL with a single Allow ACE granting full
// control to the current process user SID, with no inheritance. Used
// by both WriteFile (when creating the tmp sibling) and LockDown.
func ownerOnlyDACL() (*windows.ACL, error) {
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token); err != nil {
		return nil, fmt.Errorf("open process token: %w", err)
	}
	defer func() { _ = token.Close() }()

	tokenUser, err := token.GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("get token user: %w", err)
	}

	entries := []windows.EXPLICIT_ACCESS{
		{
			AccessPermissions: windows.GENERIC_ALL,
			AccessMode:        windows.GRANT_ACCESS,
			Inheritance:       windows.NO_INHERITANCE,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_USER,
				TrusteeValue: windows.TrusteeValueFromSID(tokenUser.User.Sid),
			},
		},
	}
	acl, err := windows.ACLFromEntries(entries, nil)
	if err != nil {
		return nil, fmt.Errorf("build DACL: %w", err)
	}
	return acl, nil
}

// ownerOnlyDescriptor bundles the owner-only DACL into a protected
// SECURITY_DESCRIPTOR ready to hand to CreateFile.
func ownerOnlyDescriptor() (*windows.SECURITY_DESCRIPTOR, error) {
	dacl, err := ownerOnlyDACL()
	if err != nil {
		return nil, err
	}
	sd, err := windows.NewSecurityDescriptor()
	if err != nil {
		return nil, fmt.Errorf("create security descriptor: %w", err)
	}
	if err := sd.SetDACL(dacl, true, false); err != nil {
		return nil, fmt.Errorf("attach DACL: %w", err)
	}
	// Mark the DACL protected so inherited ACEs are stripped rather
	// than merged.
	if err := sd.SetControl(windows.SE_DACL_PROTECTED, windows.SE_DACL_PROTECTED); err != nil {
		return nil, fmt.Errorf("protect DACL: %w", err)
	}
	return sd, nil
}

// createSecureTmp picks an unused path in dir and creates the file via
// CreateFile with CREATE_NEW + a protected owner-only SECURITY_-
// ATTRIBUTES. CREATE_NEW is important: CreateFile ignores the SA
// member when opening an existing file, so using the NEW variant on a
// filename we already verified didn't exist is what makes the DACL
// actually apply at creation time.
func createSecureTmp(dir string) (tmpPath string, handle windows.Handle, err error) {
	sd, err := ownerOnlyDescriptor()
	if err != nil {
		return "", 0, err
	}
	sa := windows.SecurityAttributes{
		Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		SecurityDescriptor: sd,
		InheritHandle:      0,
	}

	for range 100 {
		candidate := filepath.Join(dir, fmt.Sprintf(".secureperm-%d-%d", os.Getpid(), rand.Uint32()))
		utf16, err := windows.UTF16PtrFromString(candidate)
		if err != nil {
			return "", 0, fmt.Errorf("encode tmp path: %w", err)
		}
		h, err := windows.CreateFile(
			utf16,
			windows.GENERIC_WRITE,
			0, // exclusive — no sharing during write
			&sa,
			windows.CREATE_NEW,
			windows.FILE_ATTRIBUTE_NORMAL,
			0,
		)
		if err == nil {
			return candidate, h, nil
		}
		if !errors.Is(err, windows.ERROR_FILE_EXISTS) && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
			return "", 0, fmt.Errorf("create tmp %s: %w", candidate, err)
		}
		// Name already in use — pick another.
	}
	return "", 0, errors.New("could not find unused tmp filename after 100 attempts")
}

// WriteFile writes data to path atomically under a protected DACL
// granting only the current user SID.
//
// Atomic in the sense that either the write fully succeeds and path
// references the new content, or it fails and the prior file is left
// intact. Secrets files are not reconstructible (losing secrets.yaml
// means reissuing cluster PKI), so the helper must not destroy the
// existing file if the write can't complete.
//
// Strategy: create a hidden sibling tmp with CreateFile + CREATE_NEW +
// SECURITY_ATTRIBUTES (protected owner-only DACL), write the bytes,
// close the handle, rename over the target. Rename uses Win32
// MoveFileEx with MOVEFILE_REPLACE_EXISTING under the hood (os.Rename
// on Windows), which preserves the tmp's owner-only DACL on the final
// file. At no point do the new bytes exist on disk under a permissive
// DACL, and the old bytes remain readable by the owner until the final
// rename succeeds.
func WriteFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if dir == "" {
		dir = "."
	}

	tmpPath, handle, err := createSecureTmp(dir)
	if err != nil {
		return err
	}

	f := os.NewFile(uintptr(handle), tmpPath)
	committed := false
	defer func() {
		if !committed {
			_ = f.Close()
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("write tmp %s: %w", tmpPath, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close tmp %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename tmp %s -> %s: %w", tmpPath, path, err)
	}
	committed = true
	return nil
}

// LockDown replaces the DACL on an existing file with a single ACE
// granting the current user full control, and marks the DACL as
// protected so inherited ACEs are stripped.
func LockDown(path string) error {
	dacl, err := ownerOnlyDACL()
	if err != nil {
		return err
	}
	if err := windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil,
	); err != nil {
		return fmt.Errorf("set file DACL: %w", err)
	}
	return nil
}
