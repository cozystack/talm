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

package commands

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWriteInplaceRendered_Mode0600_Unix pins that `talm template -i`
// writes the rendered machine config through secureperm and not
// os.WriteFile with 0o644. The rendered content embeds certs, PKI
// keys, and cluster join tokens — leaving it world-readable would
// defeat the whole secureperm migration.
func TestWriteInplaceRendered_Mode0600_Unix(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "node0.yaml")

	if err := writeInplaceRendered(path, "apiVersion: v1alpha1\nkind: MachineConfig\n"); err != nil {
		t.Fatalf("writeInplaceRendered: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("mode = %o, want 0600 (rendered machine config is secret)", got)
	}
}

// TestWriteInplaceRendered_OverwriteDowngrades_Unix pins that running
// `talm template -i` over a pre-existing node config with lax
// permissions tightens the mode. An older talm may have written the
// file at 0o644; on update the user expects the secret bits to be
// tightened, not preserved.
func TestWriteInplaceRendered_OverwriteDowngrades_Unix(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "node0.yaml")

	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := writeInplaceRendered(path, "new"); err != nil {
		t.Fatalf("writeInplaceRendered: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("mode after overwrite = %o, want 0600", got)
	}
}
