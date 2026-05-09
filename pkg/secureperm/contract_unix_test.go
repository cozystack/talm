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

// Contract: extra scenarios for the secureperm Unix implementation
// that complement secureperm_unix_test.go. The package writes secrets
// files (age private keys, secrets.yaml, talosconfig, kubeconfig)
// atomically with mode 0o600. These tests pin behaviours an operator
// observes when running talm on a single workstation: bytes-on-disk
// match input, repeated writes are idempotent, missing parent
// directories surface a precise error, no leftover tmp files on
// successful or failed writes.

package secureperm_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cozystack/talm/pkg/secureperm"
)

// Contract: bytes written to the file match the input exactly. The
// atomic-rename helper is sometimes mistaken for a serializer; pin
// that it is purely a byte-pipe to disk.
func TestContract_WriteFile_BytesIntegrity(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret")
	// Mix of NUL, high-bit bytes, and newlines — typical of binary
	// material like age private keys or certificate DER.
	want := []byte("AGE-SECRET-KEY-1\x00\x80\xffend\nline2\n")

	if err := secureperm.WriteFile(path, want); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("bytes mismatch\nwant %q\n got %q", want, got)
	}
}

// Contract: an empty payload writes a zero-length file (still
// mode-0o600). Some callers serialize "no entries" into an empty
// buffer; the helper must accept it without special-casing.
func TestContract_WriteFile_EmptyPayload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty")
	if err := secureperm.WriteFile(path, nil); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Size() != 0 {
		t.Errorf("expected size 0, got %d", info.Size())
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("mode = %o, want 0600", got)
	}
}

// Contract: after a successful write, no `.secureperm-*` tmp file is
// left in the parent directory. The atomic strategy creates the tmp
// in the same dir as the target, then renames over the target —
// leftovers indicate the rename did not happen and would clutter the
// project root over time.
func TestContract_WriteFile_NoTmpLeftoverOnSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out")
	if err := secureperm.WriteFile(path, []byte("data")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".secureperm-") {
			t.Errorf("found leftover tmp file: %q", e.Name())
		}
	}
	if len(entries) != 1 {
		t.Errorf("expected exactly 1 entry (the target), got %d: %v", len(entries), entries)
	}
}

// Contract: after a FAILED write, no `.secureperm-*` tmp file is
// left. The deferred cleanup in WriteFile removes the tmp on any
// pre-rename failure. Failure is induced by passing a non-existent
// parent directory: CreateTemp fails before any tmp is made, so the
// directory contents stay empty.
//
// We can't induce a tmp-leftover scenario with the current API
// (errors land before the tmp is created). The test pins the
// happy-path absence and the non-existent-dir error path.
func TestContract_WriteFile_NoTmpLeftoverOnFailure(t *testing.T) {
	missingDir := filepath.Join(t.TempDir(), "does-not-exist")
	target := filepath.Join(missingDir, "out")

	err := secureperm.WriteFile(target, []byte("data"))
	if err == nil {
		t.Fatal("expected error for non-existent parent dir")
	}
	// The parent of missingDir DOES exist (it's t.TempDir()). Confirm
	// no tmp leaked there either.
	parent := filepath.Dir(missingDir)
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".secureperm-") {
			t.Errorf("found stray tmp: %q in %q", e.Name(), parent)
		}
	}
}

// Contract: WriteFile is idempotent in the operator-observable sense
// — writing the same bytes twice yields the same final file with
// mode 0o600 and no extra files. Verifies the atomic rename overwrite
// path completes cleanly on a target that already exists.
func TestContract_WriteFile_Idempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out")
	payload := []byte("payload-v1")

	for i := range 3 {
		if err := secureperm.WriteFile(path, payload); err != nil {
			t.Fatalf("WriteFile iteration %d: %v", i, err)
		}
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("after repeat writes: got %q, want %q", got, payload)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("mode after repeat writes = %o, want 0600", got)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("expected exactly 1 entry after 3 writes, got %d: %v", len(entries), entries)
	}
}

// Contract: LockDown on a non-existent file surfaces an os.PathError
// — callers wrap with their own context. No silent success is
// allowed: silently skipping the chmod would let secret material
// remain world-readable if the path was never created.
func TestContract_LockDown_MissingFileErrors(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent")
	err := secureperm.LockDown(missing)
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !os.IsNotExist(err) {
		t.Errorf("expected os.IsNotExist, got: %v", err)
	}
}

// Contract: LockDown is also a tightening operation, not a setting.
// Calling it on a file that is already 0o600 is a no-op (still 0o600,
// no error). Operators may invoke it defensively.
func TestContract_LockDown_AlreadyTightIsNoOp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "already")
	if err := os.WriteFile(path, []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := secureperm.LockDown(path); err != nil {
		t.Fatalf("LockDown on tight file: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("expected 0600 unchanged, got %o", got)
	}
}
