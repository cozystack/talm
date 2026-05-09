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
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/cozystack/talm/pkg/secureperm"
)

func TestWriteFile_PersistsContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.txt")
	payload := []byte("topsecret")

	err := secureperm.WriteFile(path, payload)
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("content mismatch: got %q, want %q", got, payload)
	}
}

func TestLockDown_OnExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "existing.txt")

	err := os.WriteFile(path, []byte("data"), 0o644)
	if err != nil {
		t.Fatalf("seed file: %v", err)
	}
	err = secureperm.LockDown(path)
	if err != nil {
		t.Fatalf("LockDown: %v", err)
	}

	// Re-read to confirm the file is still accessible to the owner after
	// tightening. On Unix this is trivial; on Windows this verifies the
	// DACL did not accidentally exclude the current user.
	_, err = os.ReadFile(path)
	if err != nil {
		t.Fatalf("owner cannot read locked file: %v", err)
	}
}
