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

package secureperm_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cozystack/talm/pkg/secureperm"
)

func TestWriteFile_Mode0600_Unix(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.txt")

	if err := secureperm.WriteFile(path, []byte("x")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("mode = %o, want 0600", got)
	}
}

func TestLockDown_Mode0600_Unix(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "existing.txt")

	if err := os.WriteFile(path, []byte("data"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := secureperm.LockDown(path); err != nil {
		t.Fatalf("LockDown: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("mode = %o, want 0600", got)
	}
}

// TestWriteFile_OverwriteDowngrades_Unix pins the behavior that
// rewriting an existing file with lax permissions tightens them. The
// atomic tmp+rename strategy achieves this because the renamed tmp
// file was created with 0o600.
func TestWriteFile_OverwriteDowngrades_Unix(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lax.txt")

	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := secureperm.WriteFile(path, []byte("new")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("mode after overwrite = %o, want 0600", got)
	}
}

// TestWriteFile_PreservesOriginalOnFailure_Unix asserts the atomic
// write contract: if the write cannot complete, the original file's
// content is left intact. Secrets files are not reconstructible —
// corrupting secrets.yaml costs the user a cluster rebuild.
//
// Failure is induced by marking the parent directory read-only, which
// makes os.CreateTemp fail with EACCES inside the tmp-creation step.
// The existing target file has content that must survive.
func TestWriteFile_PreservesOriginalOnFailure_Unix(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root — directory permission bits are ignored")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "secrets.yaml")

	original := []byte("critical-content-DO-NOT-LOSE")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Make the directory non-writable so os.CreateTemp fails.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	err := secureperm.WriteFile(path, []byte("replacement"))
	if err == nil {
		t.Fatal("expected WriteFile to fail on read-only directory")
	}

	got, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("original file missing after failure: %v", readErr)
	}
	if string(got) != string(original) {
		t.Errorf("original content corrupted on failure:\nwant %q\n got %q", original, got)
	}
}
