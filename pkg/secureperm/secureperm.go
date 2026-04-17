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
// On Unix the implementation is a thin wrapper over os.WriteFile and
// os.Chmod with mode 0o600. On Windows os.Chmod does not translate to
// NTFS DACLs — files inherit ACLs from their parent and remain readable
// by BUILTIN\Users. The Windows implementation (secureperm_windows.go)
// additionally sets a protected DACL granting only the current user SID.
package secureperm
