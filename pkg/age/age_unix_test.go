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

package age_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cozystack/talm/pkg/age"
)

// TestGenerateKey_Mode0600_Unix pins that the age private key file
// is written with owner-only permissions. The file contains the raw
// X25519 private key that protects every encrypted secret in the
// project — if a future refactor ever swaps secureperm.WriteFile
// back to os.WriteFile with a different mode, this test fails.
func TestGenerateKey_Mode0600_Unix(t *testing.T) {
	dir := t.TempDir()

	identity, created, err := age.GenerateKey(dir)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	if !created {
		t.Fatal("expected GenerateKey to create a new key in an empty dir")
	}
	if identity == nil {
		t.Fatal("nil identity from GenerateKey")
	}

	keyPath := filepath.Join(dir, "talm.key")
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("talm.key mode = %o, want 0600", got)
	}
}
