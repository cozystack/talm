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

// Contract: encrypted user value files in loadValues. A value file named
// *.encrypted.yaml is age-decrypted in memory with the project's talm.key
// (located via Options.Root) and merged like any other value source. Plaintext
// files are unaffected. This is what lets a secret be stored encrypted-at-rest
// in git yet injected at template / apply without ever writing plaintext.

package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cozystack/talm/pkg/age"
	"gopkg.in/yaml.v3"
)

// encryptValuesFileInDir writes plain as values-secret.yaml under dir, encrypts
// it (generating talm.key in dir), and returns the encrypted file path.
func encryptValuesFileInDir(t *testing.T, dir string, plain map[string]any) string {
	t.Helper()

	plainBytes, err := yaml.Marshal(plain)
	if err != nil {
		t.Fatalf("marshal plain: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "values-secret.yaml"), plainBytes, 0o600); err != nil {
		t.Fatalf("write plain: %v", err)
	}

	if err := age.EncryptYAMLFile(dir, "values-secret.yaml", "values-secret.encrypted.yaml"); err != nil {
		t.Fatalf("EncryptYAMLFile: %v", err)
	}

	return filepath.Join(dir, "values-secret.encrypted.yaml")
}

// TestContract_LoadValues_EncryptedFileDecryptedAndMerged pins that a
// *.encrypted.yaml value file is decrypted in memory and its plaintext values
// land in the merged map.
func TestContract_LoadValues_EncryptedFileDecryptedAndMerged(t *testing.T) {
	dir := t.TempDir()
	encPath := encryptValuesFileInDir(t, dir, map[string]any{"registryPassword": "s3cr3t"})

	out, err := loadValues(Options{Root: dir, ValueFiles: []string{encPath}})
	if err != nil {
		t.Fatalf("loadValues: %v", err)
	}

	if out["registryPassword"] != "s3cr3t" {
		t.Errorf("registryPassword = %v, want s3cr3t (encrypted file must be decrypted and merged)", out["registryPassword"])
	}
}

// TestContract_LoadValues_PlaintextFileStillPlaintext pins that the encrypted
// path is gated on the .encrypted.yaml suffix only — an ordinary .yaml value
// file is still read verbatim, with no key required.
func TestContract_LoadValues_PlaintextFileStillPlaintext(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plain-values.yaml")
	if err := os.WriteFile(path, []byte("clusterDomain: plain.example\n"), 0o600); err != nil {
		t.Fatalf("write plaintext: %v", err)
	}

	out, err := loadValues(Options{Root: dir, ValueFiles: []string{path}})
	if err != nil {
		t.Fatalf("loadValues: %v", err)
	}

	if out["clusterDomain"] != "plain.example" {
		t.Errorf("clusterDomain = %v, want plain.example", out["clusterDomain"])
	}
}

// TestContract_LoadValues_EncryptedMissingKeyErrors pins that a missing
// talm.key surfaces as a load error naming the encrypted file (not a silent
// plaintext fallthrough).
func TestContract_LoadValues_EncryptedMissingKeyErrors(t *testing.T) {
	dir := t.TempDir()
	encPath := encryptValuesFileInDir(t, dir, map[string]any{"password": "topsecret"})

	if err := os.Remove(filepath.Join(dir, "talm.key")); err != nil {
		t.Fatalf("remove talm.key: %v", err)
	}

	_, err := loadValues(Options{Root: dir, ValueFiles: []string{encPath}})
	if err == nil {
		t.Fatal("expected error when talm.key is missing for an encrypted value file")
	}
	if !strings.Contains(err.Error(), "values-secret.encrypted.yaml") {
		t.Errorf("error must name the encrypted file; got %v", err)
	}
}

// TestContract_LoadValues_EncryptedUsesRootForKey pins that the key is located
// via Options.Root, not the encrypted file's own directory. apply can target a
// node file by absolute path from any CWD, so the value file may sit under a
// directory that has no talm.key — only the project root does.
func TestContract_LoadValues_EncryptedUsesRootForKey(t *testing.T) {
	root := t.TempDir()
	encInRoot := encryptValuesFileInDir(t, root, map[string]any{"password": "topsecret"})

	// Move the encrypted file into a sibling directory that has no key.
	elsewhere := t.TempDir()
	moved := filepath.Join(elsewhere, "values-secret.encrypted.yaml")

	data, err := os.ReadFile(encInRoot)
	if err != nil {
		t.Fatalf("read encrypted: %v", err)
	}
	if err := os.WriteFile(moved, data, 0o600); err != nil {
		t.Fatalf("write moved: %v", err)
	}

	out, err := loadValues(Options{Root: root, ValueFiles: []string{moved}})
	if err != nil {
		t.Fatalf("loadValues must locate talm.key via Root, not the file dir: %v", err)
	}
	if out["password"] != "topsecret" {
		t.Errorf("password = %v, want topsecret", out["password"])
	}
}
