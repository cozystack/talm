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
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ownerOnlyDACL builds an ACL with a single Allow ACE granting full
// control to the current process user SID, with no inheritance. Used
// by both WriteFile (at create time) and LockDown (to rewrite an
// existing file's DACL).
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

// WriteFile writes data to path under a protected DACL granting only
// the current user SID.
//
// For new files the DACL is installed at creation via CreateFile's
// SECURITY_ATTRIBUTES. For existing files CreateFile + CREATE_ALWAYS
// silently ignores SECURITY_ATTRIBUTES (per MSDN), so we additionally
// call SetSecurityInfo on the open handle before writing any bytes —
// the handle was opened exclusive (no share-mode), so no other process
// can observe the file between the old DACL and the new one. After
// SetSecurityInfo returns, the write proceeds under the owner-only
// DACL and the final on-disk state is always protected.
func WriteFile(path string, data []byte) error {
	dacl, err := ownerOnlyDACL()
	if err != nil {
		return err
	}

	sd, err := windows.NewSecurityDescriptor()
	if err != nil {
		return fmt.Errorf("create security descriptor: %w", err)
	}
	if err := sd.SetDACL(dacl, true, false); err != nil {
		return fmt.Errorf("attach DACL: %w", err)
	}
	if err := sd.SetControl(windows.SE_DACL_PROTECTED, windows.SE_DACL_PROTECTED); err != nil {
		return fmt.Errorf("protect DACL: %w", err)
	}

	sa := windows.SecurityAttributes{
		Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		SecurityDescriptor: sd,
		InheritHandle:      0,
	}

	pathUTF16, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return fmt.Errorf("encode path: %w", err)
	}

	handle, err := windows.CreateFile(
		pathUTF16,
		windows.GENERIC_WRITE|windows.WRITE_DAC,
		0, // exclusive — no sharing during write
		&sa,
		windows.CREATE_ALWAYS,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		return fmt.Errorf("create secure file %s: %w", path, err)
	}

	f := os.NewFile(uintptr(handle), path)
	defer func() { _ = f.Close() }()

	// Force the DACL onto the handle before writing bytes. Covers the
	// overwrite case where SECURITY_ATTRIBUTES from CreateFile is
	// ignored. Uses SetSecurityInfo on the handle (not the path) so
	// nothing can re-open the file between the DACL switch and the
	// write — share mode on the handle is 0.
	if err := windows.SetSecurityInfo(
		handle,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil,
	); err != nil {
		return fmt.Errorf("set handle DACL on %s: %w", path, err)
	}

	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("write secure file %s: %w", path, err)
	}
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
