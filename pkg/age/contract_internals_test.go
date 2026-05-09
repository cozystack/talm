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

// Contract: edge cases for the incremental encrypt/decrypt machinery
// in age.go. EncryptSecretsFile uses mergeAndEncryptYAMLValues to
// preserve unchanged ciphertext; the round-trip pin sits in
// contract_test.go. This file exercises the type-mismatch / new-key
// / new-list-element / type-changed branches of the recursive merger
// — the surface area that decides what does and does NOT get
// re-encrypted.

package age

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cockroachdb/errors"
	"gopkg.in/yaml.v3"
)

// === decryptYAMLValuesString ===

// Contract: a string already wrapped in the ENC[AGE,data:...] envelope
// is decrypted to the plaintext it originally encoded.
func TestContract_DecryptYAMLValuesString_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	id, _, err := GenerateKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := encryptString("hello", id.Recipient())
	if err != nil {
		t.Fatal(err)
	}
	envelope := ageEncryptionPrefix + encrypted + ageEncryptionSuffix

	got, err := decryptYAMLValuesString(envelope, id)
	if err != nil {
		t.Fatalf("decryptYAMLValuesString: %v", err)
	}
	if got != "hello" {
		t.Errorf("expected 'hello', got %q", got)
	}
}

// Contract: a string that is NOT wrapped in the envelope is returned
// verbatim. This lets the merge logic feed both raw plaintext and
// already-encrypted values through the same helper without a type
// switch at the call site.
func TestContract_DecryptYAMLValuesString_PassthroughForUnencrypted(t *testing.T) {
	dir := t.TempDir()
	id, _, err := GenerateKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decryptYAMLValuesString("just-a-plain-string", id)
	if err != nil {
		t.Fatal(err)
	}
	if got != "just-a-plain-string" {
		t.Errorf("expected passthrough, got %q", got)
	}
}

// Contract: a string with the envelope prefix but corrupted base64
// payload errors precisely. Pin so a regression that swallows the
// inner error and silently returns the corrupted string surfaces.
func TestContract_DecryptYAMLValuesString_CorruptedEnvelope(t *testing.T) {
	dir := t.TempDir()
	id, _, err := GenerateKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	corrupted := ageEncryptionPrefix + "not-valid-base64!!!" + ageEncryptionSuffix
	_, err = decryptYAMLValuesString(corrupted, id)
	if err == nil {
		t.Fatal("expected error for corrupted envelope")
	}
}

// === mergeAndEncryptYAMLValues — through EncryptSecretsFile ===

// Contract: a NEW key added to plain secrets.yaml gets encrypted on
// the next encrypt round; existing keys keep their byte-stable
// ciphertext.
func TestContract_IncrementalEncrypt_NewKeyEncryptedOldKeyStable(t *testing.T) {
	dir := t.TempDir()
	if err := writeYAML(dir, "secrets.yaml", "a: alpha\n"); err != nil {
		t.Fatal(err)
	}
	if err := EncryptSecretsFile(dir); err != nil {
		t.Fatal(err)
	}
	first := readYAML(t, dir, "secrets.encrypted.yaml")

	// Add a new key b.
	if err := writeYAML(dir, "secrets.yaml", "a: alpha\nb: beta\n"); err != nil {
		t.Fatal(err)
	}
	if err := EncryptSecretsFile(dir); err != nil {
		t.Fatal(err)
	}
	second := readYAML(t, dir, "secrets.encrypted.yaml")

	// a's ciphertext must stay byte-stable.
	if first["a"] != second["a"] {
		t.Errorf("a's ciphertext rotated unnecessarily\n first:  %v\n second: %v", first["a"], second["a"])
	}
	// b must be present and encrypted.
	bStr, ok := second["b"].(string)
	if !ok {
		t.Fatalf("b missing or not a string: %v", second["b"])
	}
	if !strings.HasPrefix(bStr, ageEncryptionPrefix) {
		t.Errorf("b not encrypted: %q", bStr)
	}
}

// Contract: a deeply-nested change re-encrypts only the innermost
// changed leaf. Sibling values up and down the tree stay byte-stable.
func TestContract_IncrementalEncrypt_NestedChangeLocalised(t *testing.T) {
	dir := t.TempDir()
	initial := `top:
  unchanged: keep-me
  sub:
    inner1: original
    inner2: keep-me-too
`
	if err := writeYAML(dir, "secrets.yaml", initial); err != nil {
		t.Fatal(err)
	}
	if err := EncryptSecretsFile(dir); err != nil {
		t.Fatal(err)
	}
	first := readYAML(t, dir, "secrets.encrypted.yaml")

	// Change top.sub.inner1 only.
	changed := `top:
  unchanged: keep-me
  sub:
    inner1: NEW
    inner2: keep-me-too
`
	if err := writeYAML(dir, "secrets.yaml", changed); err != nil {
		t.Fatal(err)
	}
	if err := EncryptSecretsFile(dir); err != nil {
		t.Fatal(err)
	}
	second := readYAML(t, dir, "secrets.encrypted.yaml")

	// Walk both trees, compare leaf-by-leaf.
	firstTop := first["top"].(map[string]any)
	secondTop := second["top"].(map[string]any)
	if firstTop["unchanged"] != secondTop["unchanged"] {
		t.Errorf("top.unchanged ciphertext rotated unnecessarily")
	}
	firstSub := firstTop["sub"].(map[string]any)
	secondSub := secondTop["sub"].(map[string]any)
	if firstSub["inner2"] != secondSub["inner2"] {
		t.Errorf("top.sub.inner2 ciphertext rotated unnecessarily")
	}
	if firstSub["inner1"] == secondSub["inner1"] {
		t.Error("top.sub.inner1 ciphertext did NOT rotate after plaintext change")
	}
}

// Contract: when the encrypted-file structure differs from the
// plain-file structure (type changed at a key — scalar -> map, or
// vice versa), the affected branch is fully re-encrypted from
// scratch. The chart's incremental rule degrades gracefully.
func TestContract_IncrementalEncrypt_TypeChangeFallsBackToFullEncrypt(t *testing.T) {
	dir := t.TempDir()
	// Encrypted file holds scalar at k.
	if err := writeYAML(dir, "secrets.yaml", "k: scalar-value\n"); err != nil {
		t.Fatal(err)
	}
	if err := EncryptSecretsFile(dir); err != nil {
		t.Fatal(err)
	}

	// Plaintext now has k as a map.
	if err := writeYAML(dir, "secrets.yaml", "k:\n  nested: value\n"); err != nil {
		t.Fatal(err)
	}
	if err := EncryptSecretsFile(dir); err != nil {
		t.Fatalf("re-encrypt with type change: %v", err)
	}

	out := readYAML(t, dir, "secrets.encrypted.yaml")
	kMap, ok := out["k"].(map[string]any)
	if !ok {
		t.Fatalf("expected k to be map after type change, got %T (%v)", out["k"], out["k"])
	}
	nested, ok := kMap["nested"].(string)
	if !ok || !strings.HasPrefix(nested, ageEncryptionPrefix) {
		t.Errorf("expected k.nested to be an encrypted string, got %v", kMap["nested"])
	}
}

// Contract: when a list value's length changes, the entire list is
// re-encrypted. Per-element merge requires a stable index mapping,
// which a length change invalidates.
func TestContract_IncrementalEncrypt_ListLengthChangeFullReencrypt(t *testing.T) {
	dir := t.TempDir()
	if err := writeYAML(dir, "secrets.yaml", "items:\n  - one\n  - two\n"); err != nil {
		t.Fatal(err)
	}
	if err := EncryptSecretsFile(dir); err != nil {
		t.Fatal(err)
	}
	first := readYAML(t, dir, "secrets.encrypted.yaml")

	if err := writeYAML(dir, "secrets.yaml", "items:\n  - one\n  - two\n  - three\n"); err != nil {
		t.Fatal(err)
	}
	if err := EncryptSecretsFile(dir); err != nil {
		t.Fatal(err)
	}
	second := readYAML(t, dir, "secrets.encrypted.yaml")

	firstItems := first["items"].([]any)
	secondItems := second["items"].([]any)
	if len(secondItems) != 3 {
		t.Fatalf("expected 3 items after append, got %d", len(secondItems))
	}
	// Existing items[0]/items[1] are EXPECTED to rotate ciphertext —
	// the list-length-changed branch re-encrypts the full slice.
	// Pin both that the new length holds AND that all entries are
	// freshly encrypted (envelope-wrapped).
	for i, e := range secondItems {
		s, ok := e.(string)
		if !ok || !strings.HasPrefix(s, ageEncryptionPrefix) {
			t.Errorf("items[%d] not encrypted: %v", i, e)
		}
	}
	_ = firstItems // referenced here to document the intentional non-comparison
}

// Contract: when an existing list element changes value but the
// length stays the same, only that element re-encrypts; siblings
// stay byte-stable.
func TestContract_IncrementalEncrypt_ListSameLengthLocalised(t *testing.T) {
	dir := t.TempDir()
	if err := writeYAML(dir, "secrets.yaml", "items:\n  - alpha\n  - bravo\n  - charlie\n"); err != nil {
		t.Fatal(err)
	}
	if err := EncryptSecretsFile(dir); err != nil {
		t.Fatal(err)
	}
	first := readYAML(t, dir, "secrets.encrypted.yaml")

	if err := writeYAML(dir, "secrets.yaml", "items:\n  - alpha\n  - DELTA\n  - charlie\n"); err != nil {
		t.Fatal(err)
	}
	if err := EncryptSecretsFile(dir); err != nil {
		t.Fatal(err)
	}
	second := readYAML(t, dir, "secrets.encrypted.yaml")

	firstItems := first["items"].([]any)
	secondItems := second["items"].([]any)
	if firstItems[0] != secondItems[0] {
		t.Errorf("items[0] rotated unnecessarily")
	}
	if firstItems[2] != secondItems[2] {
		t.Errorf("items[2] rotated unnecessarily")
	}
	if firstItems[1] == secondItems[1] {
		t.Errorf("items[1] did NOT rotate after value change")
	}
}

// === helpers ===

func writeYAML(dir, name, body string) error {
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		return errors.Wrap(err, "write YAML test fixture")
	}
	return nil
}

func readYAML(t *testing.T, dir, name string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	var out map[string]any
	if err := yaml.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal %s: %v", name, err)
	}
	return out
}
