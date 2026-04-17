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

// Package secureperm writes and locks down files that hold secrets
// (age private keys, decrypted secrets.yaml, talosconfig, kubeconfig,
// rendered Talos machine configs) so that only the file owner can
// read them.
//
// WriteFile is atomic on both platforms via write-to-tmp + rename:
// it creates a hidden sibling tmp file in the same directory with the
// final permissions already in place, writes the bytes, then renames
// the tmp over the target. os.Rename is atomic on POSIX and on NTFS
// when source and destination share a filesystem (which they do by
// construction here). On any pre-rename failure the tmp is removed
// and the destination is left in its prior state — important because
// secrets are not reconstructible (a corrupted secrets.yaml forces a
// cluster PKI reissue).
//
// On Unix the tmp is created via os.CreateTemp (O_CREATE|O_EXCL|O_RDWR
// with mode 0o600 by construction) plus an explicit Chmod so the
// contract survives any future stdlib change.
//
// On Windows os.Chmod does not translate to NTFS DACLs — files
// ordinarily inherit ACLs from their parent, which may leave secrets
// readable by non-owner principals such as BUILTIN\Users. The tmp is
// created via CreateFile with CREATE_NEW and a SECURITY_ATTRIBUTES
// descriptor carrying a protected owner-only DACL. CREATE_NEW is the
// key: per MSDN, CreateFile only honors the lpSecurityDescriptor
// argument when the file is newly created, not when it opens an
// existing file — so the retry loop picks a name that does not
// exist, guaranteeing the DACL actually lands. The rename step then
// overwrites the destination with the tmp, carrying the tmp's
// owner-only DACL over in place of whatever permissive inherited
// DACL the destination held. The bytes never exist on disk under a
// lax DACL, and because the tmp handle is opened with share mode 0
// no other process can open it between creation and rename.
package secureperm
