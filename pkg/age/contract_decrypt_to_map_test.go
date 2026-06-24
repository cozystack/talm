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
	"strings"
	"testing"

	cerrors "github.com/cockroachdb/errors"
	"github.com/cozystack/talm/pkg/age"
	"gopkg.in/yaml.v3"
)

// writeEncryptedValuesFile writes plain as values-secret.yaml under dir,
// encrypts it (generating talm.key), and returns the encrypted file path.
func writeEncryptedValuesFile(t *testing.T, dir string, plain map[string]any) string {
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

// TestContract_DecryptYAMLToMap_RoundTrip pins the core in-memory decrypt:
// the map returned must equal the plaintext that was encrypted, without any
// file being written back to disk.
func TestContract_DecryptYAMLToMap_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	plain := map[string]any{"registryPassword": "s3cr3t", "nested": map[string]any{"token": "abc123"}}

	encPath := writeEncryptedValuesFile(t, dir, plain)

	got, err := age.DecryptYAMLToMap(dir, encPath)
	if err != nil {
		t.Fatalf("DecryptYAMLToMap: %v", err)
	}

	if got["registryPassword"] != "s3cr3t" {
		t.Errorf("registryPassword = %v, want s3cr3t", got["registryPassword"])
	}

	nested, ok := got["nested"].(map[string]any)
	if !ok || nested["token"] != "abc123" {
		t.Errorf("nested.token = %v, want abc123 (got nested=%v)", nested["token"], got["nested"])
	}

	// No plaintext sibling must be written — decryption is in-memory only.
	if _, err := os.Stat(filepath.Join(dir, "values-secret.yaml.decrypted")); err == nil {
		t.Error("DecryptYAMLToMap must not write any file to disk")
	}
}

// TestContract_DecryptYAMLToMap_PartialEncryption pins that a file mixing an
// encrypted leaf with a plaintext leaf decrypts the envelope and passes the
// plaintext through unchanged. Operators hand-editing an encrypted file rely
// on this.
func TestContract_DecryptYAMLToMap_PartialEncryption(t *testing.T) {
	dir := t.TempDir()
	encPath := writeEncryptedValuesFile(t, dir, map[string]any{"password": "topsecret"})

	// Append a plaintext leaf to the encrypted file.
	raw, err := os.ReadFile(encPath)
	if err != nil {
		t.Fatalf("read encrypted: %v", err)
	}

	var mixed map[string]any
	if err := yaml.Unmarshal(raw, &mixed); err != nil {
		t.Fatalf("unmarshal encrypted: %v", err)
	}

	mixed["note"] = "a plaintext note"

	mixedBytes, err := yaml.Marshal(mixed)
	if err != nil {
		t.Fatalf("marshal mixed: %v", err)
	}

	if err := os.WriteFile(encPath, mixedBytes, 0o600); err != nil {
		t.Fatalf("write mixed: %v", err)
	}

	got, err := age.DecryptYAMLToMap(dir, encPath)
	if err != nil {
		t.Fatalf("DecryptYAMLToMap on partial file: %v", err)
	}

	if got["password"] != "topsecret" {
		t.Errorf("password = %v, want topsecret (envelope must decrypt)", got["password"])
	}
	if got["note"] != "a plaintext note" {
		t.Errorf("note = %v, want verbatim plaintext", got["note"])
	}
}

// TestContract_DecryptYAMLToMap_NoEnvelopeErrors pins the content-validation
// contract: a file with no full ENC[AGE,...] envelope returns
// ErrNoEncryptedValues even though it parses cleanly. A plaintext value that
// merely mentions the envelope prefix mid-string MUST NOT be mistaken for
// ciphertext (the prefix-AND-suffix guard).
func TestContract_DecryptYAMLToMap_NoEnvelopeErrors(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := age.GenerateKey(dir); err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	plaintext := "desc: \"the format is ENC[AGE,data:...] but this is not encrypted\"\nport: \"6443\"\n"
	path := filepath.Join(dir, "not-really.encrypted.yaml")
	if err := os.WriteFile(path, []byte(plaintext), 0o600); err != nil {
		t.Fatalf("write plaintext: %v", err)
	}

	_, err := age.DecryptYAMLToMap(dir, path)
	if !cerrors.Is(err, age.ErrNoEncryptedValues) {
		t.Errorf("expected ErrNoEncryptedValues for an envelope-less file; got %v", err)
	}
}

// TestContract_DecryptYAMLToMap_MissingKeySurfacesHint pins that when the
// file is genuinely encrypted but talm.key is gone, the error names the file
// and carries LoadKey's recovery hint — not a bare read failure.
func TestContract_DecryptYAMLToMap_MissingKeySurfacesHint(t *testing.T) {
	dir := t.TempDir()
	encPath := writeEncryptedValuesFile(t, dir, map[string]any{"password": "topsecret"})

	if err := os.Remove(filepath.Join(dir, "talm.key")); err != nil {
		t.Fatalf("remove talm.key: %v", err)
	}

	_, err := age.DecryptYAMLToMap(dir, encPath)
	if err == nil {
		t.Fatal("expected error when talm.key is missing")
	}

	if !strings.Contains(err.Error(), "values-secret.encrypted.yaml") {
		t.Errorf("error must name the offending file; got %v", err)
	}

	hints := strings.ToLower(strings.Join(cerrors.GetAllHints(err), "\n"))
	if !strings.Contains(hints, "talm.key") {
		t.Errorf("missing-key error must surface LoadKey's recovery hint; got hints: %s", hints)
	}
}
