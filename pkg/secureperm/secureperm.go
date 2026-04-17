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
// (age private keys, decrypted secrets.yaml, talosconfig, kubeconfig)
// so that only the file owner can read them.
//
// On Unix WriteFile is os.WriteFile(…, 0o600) followed by an explicit
// os.Chmod — the trailing Chmod is required because os.WriteFile keeps
// a pre-existing file's mode bits untouched on overwrite.
//
// On Windows os.Chmod does not translate to NTFS DACLs — files inherit
// ACLs from their parent, which may leave secrets readable by
// non-owner principals such as BUILTIN\Users. The Windows
// implementation creates the file with a protected owner-only DACL
// via CreateFile's SECURITY_ATTRIBUTES argument, then immediately
// calls SetSecurityInfo on the handle (before any bytes are written)
// to cover the overwrite case: per MSDN, CreateFile ignores
// SECURITY_ATTRIBUTES when opening an existing file, so the handle
// would otherwise keep the prior DACL. Because the handle is opened
// exclusive, no other process can observe the file between the DACL
// switch and the write.
package secureperm
