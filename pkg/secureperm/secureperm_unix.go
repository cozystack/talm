//go:build !windows

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

import "os"

// WriteFile writes data to path with mode 0o600.
func WriteFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0o600)
}

// LockDown narrows an existing file's permissions to 0o600.
func LockDown(path string) error {
	return os.Chmod(path, 0o600)
}
