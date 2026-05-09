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

import (
	"fmt"
	"os"
	"path/filepath"
)

// WriteFile writes data to path atomically with mode 0o600.
//
// Atomic in the sense that either the write fully succeeds and path
// references the new content, or it fails and path is left in its
// prior state — secrets files are not reconstructible (losing
// secrets.yaml means reissuing cluster PKI), so the helper must not
// destroy the existing file if the write can't complete.
//
// Strategy: create a hidden sibling tmp file in the same directory via
// os.CreateTemp (which uses O_CREATE|O_EXCL|O_RDWR with mode 0o600,
// so the tmp is already owner-only), write the bytes, then rename
// over the target. Rename is atomic on POSIX when both paths live on
// the same filesystem, which they do by construction.
//
// Ownership note: tmp + rename produces a file owned by the calling
// process's uid/gid, which differs from os.WriteFile's open-with-
// O_TRUNC behaviour where the existing inode's owner is preserved.
// Running talm under a different uid than a previous invocation
// (e.g. once via sudo, then as the unprivileged user) will therefore
// change ownership on the secrets file. The single-user workstation
// flow this helper targets is unaffected; mixed-uid setups should
// invoke talm under a consistent identity.
func WriteFile(path string, data []byte) error {
	dir := filepath.Dir(path)

	f, err := os.CreateTemp(dir, ".secureperm-*")
	if err != nil {
		return fmt.Errorf("create tmp in %s: %w", dir, err)
	}

	tmpPath := f.Name()

	committed := false
	defer func() {
		if !committed {
			_ = f.Close()
			_ = os.Remove(tmpPath)
		}
	}()

	// os.CreateTemp already uses 0o600 but enforce explicitly so the
	// contract survives any future stdlib change.
	if err := f.Chmod(0o600); err != nil {
		return fmt.Errorf("chmod tmp: %w", err)
	}

	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("write tmp: %w", err)
	}
	// fsync the tmp file before rename so its contents are on stable
	// storage; otherwise a crash/power-loss between rename and the
	// delayed disk flush can surface the renamed inode pointing at
	// zero-length or stale data on reboot — the canonical failure mode
	// the atomic-rename pattern is meant to avoid. Secrets files are
	// not reconstructible, so the full fsync is warranted.
	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync tmp: %w", err)
	}

	if err := f.Close(); err != nil {
		return fmt.Errorf("close tmp: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename tmp -> %s: %w", path, err)
	}

	committed = true
	// Best-effort fsync of the parent dir so the rename entry itself is
	// durable. Ignored errors: dir fsync is unsupported on a few
	// filesystems; the tmp fsync above already protects the payload.
	if d, openErr := os.Open(dir); openErr == nil {
		_ = d.Sync()
		_ = d.Close()
	}

	return nil
}

// LockDown narrows an existing file's permissions to 0o600.
func LockDown(path string) error {
	return os.Chmod(path, 0o600)
}
