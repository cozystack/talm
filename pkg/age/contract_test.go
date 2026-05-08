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

// Contract: age key management and YAML-value encryption for talm.
// pkg/age implements `talm init --encrypt` / `--decrypt` flows: it
// generates an age X25519 keypair, persists it as `talm.key` (mode
// 0600 via secureperm), encrypts/decrypts secrets.yaml round-trip,
// and supports key rotation. Encryption is per-string-value (keys
// stay readable, values become `ENC[AGE,data:<base64>]`) so an
// encrypted secrets file remains diffable in git.
//
// Tests in this file pin user-observable contracts: file format
// (talm.key layout, ENC[...] envelope), round-trip integrity,
// incremental re-encryption (unchanged values stay byte-stable
// between runs — important for git history), key rotation
// preserving plaintext, and the load-or-generate idempotency that
// `talm init` relies on.

package age_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cozystack/talm/pkg/age"
	"gopkg.in/yaml.v3"
)

// === GenerateKey + LoadKey ===

// Contract: GenerateKey on an empty directory creates a fresh
// age X25519 identity AND writes talm.key with the canonical age
// keygen layout: a `# created:` comment, a `# public key:` comment,
// and the AGE-SECRET-KEY-1... line.
func TestContract_Age_GenerateKey_FileLayout(t *testing.T) {
	dir := t.TempDir()
	id, created, err := age.GenerateKey(dir)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	if !created {
		t.Fatal("expected created=true on empty dir")
	}
	if id == nil {
		t.Fatal("nil identity")
	}

	keyFile := filepath.Join(dir, "talm.key")
	data, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatalf("read talm.key: %v", err)
	}
	content := string(data)
	for _, want := range []string{
		"# created: ",
		"# public key: ",
		"AGE-SECRET-KEY-1",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("talm.key missing %q in:\n%s", want, content)
		}
	}
}

// Contract: GenerateKey is idempotent — a second call against the
// same directory returns created=false and the SAME identity (same
// public key). This is the load-or-generate semantics talm relies on
// across `init`, `apply`, and `talosconfig` flows.
func TestContract_Age_GenerateKey_IdempotentReturnsSameIdentity(t *testing.T) {
	dir := t.TempDir()
	first, _, err := age.GenerateKey(dir)
	if err != nil {
		t.Fatalf("first GenerateKey: %v", err)
	}
	second, created, err := age.GenerateKey(dir)
	if err != nil {
		t.Fatalf("second GenerateKey: %v", err)
	}
	if created {
		t.Error("expected created=false on second call")
	}
	if first.Recipient().String() != second.Recipient().String() {
		t.Errorf("public keys differ across calls:\nfirst:  %s\nsecond: %s",
			first.Recipient(), second.Recipient())
	}
}

// Contract: LoadKey reads talm.key in the canonical layout (with
// comments) and returns the identity. The function picks the line
// starting with AGE-SECRET-KEY- regardless of where it sits in the
// file (works with both age keygen and an old plain-key format).
func TestContract_Age_LoadKey_AcceptsKeygenFormat(t *testing.T) {
	dir := t.TempDir()
	first, _, err := age.GenerateKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := age.LoadKey(dir)
	if err != nil {
		t.Fatalf("LoadKey: %v", err)
	}
	if first.Recipient().String() != loaded.Recipient().String() {
		t.Errorf("public keys differ\ngenerated: %s\nloaded:    %s", first.Recipient(), loaded.Recipient())
	}
}

// Contract: LoadKey accepts a legacy plain-text talm.key (just the
// AGE-SECRET-KEY-... line, no comments). Backward-compat: pre-1.0
// projects predate the keygen-format introduction.
func TestContract_Age_LoadKey_AcceptsPlainFormat(t *testing.T) {
	dir := t.TempDir()
	plainFile := filepath.Join(dir, "talm.key")
	// Generate one to extract the secret-key line.
	id, _, err := age.GenerateKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	plainSecret := id.String() + "\n"
	// Overwrite with plain format (no comments).
	if err := os.WriteFile(plainFile, []byte(plainSecret), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := age.LoadKey(dir)
	if err != nil {
		t.Fatalf("LoadKey on plain format: %v", err)
	}
	if id.Recipient().String() != loaded.Recipient().String() {
		t.Errorf("public keys differ on plain reload")
	}
}

// Contract: LoadKey errors precisely when talm.key has no
// AGE-SECRET-KEY-... line at all (random garbage, partial file,
// edited-by-mistake state). The error tells the operator the file
// is malformed without exposing key material.
func TestContract_Age_LoadKey_RejectsMalformedKeyFile(t *testing.T) {
	dir := t.TempDir()
	malformed := filepath.Join(dir, "talm.key")
	if err := os.WriteFile(malformed, []byte("# this is not a key\nrandom garbage\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := age.LoadKey(dir)
	if err == nil {
		t.Fatal("expected error for malformed key file")
	}
	if !strings.Contains(err.Error(), "AGE-SECRET-KEY") {
		t.Errorf("error must reference the missing AGE-SECRET-KEY marker, got: %v", err)
	}
}

// Contract: LoadKey errors when talm.key is missing entirely. The
// caller (talm init / apply) needs this to differentiate "no key
// yet, generate one" from "key was deleted, abort and warn".
func TestContract_Age_LoadKey_MissingFileErrors(t *testing.T) {
	dir := t.TempDir() // no talm.key inside
	_, err := age.LoadKey(dir)
	if err == nil {
		t.Fatal("expected error for missing talm.key")
	}
}

// === GetPublicKey / GetPublicKeyFromFile ===

// Contract: GetPublicKey returns the recipient string from an
// identity (matches the AGE-PUBLIC-... format used elsewhere in
// the age toolchain).
func TestContract_Age_GetPublicKey_FromIdentity(t *testing.T) {
	dir := t.TempDir()
	id, _, err := age.GenerateKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	pub := age.GetPublicKey(id)
	if !strings.HasPrefix(pub, "age1") {
		t.Errorf("expected age public key to start with 'age1', got %q", pub)
	}
}

// Contract: GetPublicKeyFromFile reads talm.key and returns the
// public key. Prefers the `# public key:` comment line (fast path,
// no key parsing); falls back to LoadKey when the comment is
// missing.
func TestContract_Age_GetPublicKeyFromFile_PrefersComment(t *testing.T) {
	dir := t.TempDir()
	id, _, err := age.GenerateKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := age.GetPublicKeyFromFile(dir)
	if err != nil {
		t.Fatalf("GetPublicKeyFromFile: %v", err)
	}
	if got != id.Recipient().String() {
		t.Errorf("public key mismatch\n got: %s\nwant: %s", got, id.Recipient())
	}
}

// Contract: GetPublicKeyFromFile recovers via LoadKey when the
// `# public key:` comment is absent (legacy plain format). No
// silent failure — the function returns the same value either way.
func TestContract_Age_GetPublicKeyFromFile_FallsBackToLoadKey(t *testing.T) {
	dir := t.TempDir()
	id, _, err := age.GenerateKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Strip the comment lines.
	plain := id.String() + "\n"
	if err := os.WriteFile(filepath.Join(dir, "talm.key"), []byte(plain), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := age.GetPublicKeyFromFile(dir)
	if err != nil {
		t.Fatalf("GetPublicKeyFromFile (no comment): %v", err)
	}
	if got != id.Recipient().String() {
		t.Errorf("fallback public key mismatch")
	}
}

// === EncryptSecretsFile / DecryptSecretsFile ===

// Contract: round-trip stability — encrypt then decrypt restores
// the original plaintext exactly. This is the basic correctness
// requirement of any encryption layer.
func TestContract_Age_SecretsFile_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	plain := []byte(`secrets:
  api_token: super-secret-1
  db_password: another-secret
nested:
  k1:
    k2: deeply-nested-value
`)
	plainFile := filepath.Join(dir, "secrets.yaml")
	if err := os.WriteFile(plainFile, plain, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := age.EncryptSecretsFile(dir); err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	// Remove plaintext to prove decrypt restores from encrypted file.
	if err := os.Remove(plainFile); err != nil {
		t.Fatal(err)
	}
	if err := age.DecryptSecretsFile(dir); err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	got, err := os.ReadFile(plainFile)
	if err != nil {
		t.Fatal(err)
	}
	// Compare semantically — YAML round-trip may reorder keys.
	var origMap, gotMap map[string]any
	if err := yaml.Unmarshal(plain, &origMap); err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(got, &gotMap); err != nil {
		t.Fatal(err)
	}
	if !mapsEqual(origMap, gotMap) {
		t.Errorf("round-trip mismatch\norig:\n%s\ngot:\n%s", plain, got)
	}
}

// Contract: encrypted file uses the `ENC[AGE,data:<base64>]`
// envelope per string value. Keys remain plaintext — this is what
// makes the encrypted file diffable in git: changing one secret
// produces a one-line diff, not a wholesale ciphertext rewrite.
func TestContract_Age_SecretsFile_EnvelopeFormat(t *testing.T) {
	dir := t.TempDir()
	plain := []byte("secret_value: hello-world\n")
	if err := os.WriteFile(filepath.Join(dir, "secrets.yaml"), plain, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := age.EncryptSecretsFile(dir); err != nil {
		t.Fatal(err)
	}
	encrypted, err := os.ReadFile(filepath.Join(dir, "secrets.encrypted.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	encStr := string(encrypted)
	// Key stays plaintext.
	if !strings.Contains(encStr, "secret_value:") {
		t.Errorf("expected key 'secret_value:' to remain plaintext, got:\n%s", encStr)
	}
	// Value is wrapped in the envelope.
	if !strings.Contains(encStr, "ENC[AGE,data:") {
		t.Errorf("expected ENC[AGE,data: envelope, got:\n%s", encStr)
	}
	if !strings.Contains(encStr, "]") {
		t.Errorf("expected ENC envelope closing ], got:\n%s", encStr)
	}
	// Plaintext value MUST NOT appear.
	if strings.Contains(encStr, "hello-world") {
		t.Errorf("plaintext leaked in encrypted output:\n%s", encStr)
	}
}

// Contract: incremental re-encryption — when secrets.yaml has not
// changed, calling EncryptSecretsFile twice produces the SAME
// encrypted file bytes. This makes an "encrypt-on-save" workflow
// safe under git: an untouched secret stays as the same ciphertext,
// so commits show only intended changes.
func TestContract_Age_SecretsFile_IncrementalReencryption(t *testing.T) {
	dir := t.TempDir()
	plain := []byte("a: alpha\nb: bravo\n")
	if err := os.WriteFile(filepath.Join(dir, "secrets.yaml"), plain, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := age.EncryptSecretsFile(dir); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(filepath.Join(dir, "secrets.encrypted.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := age.EncryptSecretsFile(dir); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(filepath.Join(dir, "secrets.encrypted.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Errorf("re-encrypt with unchanged plaintext produced different ciphertext\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

// Contract: changing one value produces a localized diff — only
// that key's ciphertext changes; the others stay byte-stable.
// Pinning this prevents a regression that ever-rotates the IV/nonce
// for unchanged values (the latter would defeat the point of
// per-value encryption).
func TestContract_Age_SecretsFile_ChangedValueLocalizedDiff(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "secrets.yaml"), []byte("a: alpha\nb: bravo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := age.EncryptSecretsFile(dir); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(filepath.Join(dir, "secrets.encrypted.yaml"))

	// Change b's value, leave a alone.
	if err := os.WriteFile(filepath.Join(dir, "secrets.yaml"), []byte("a: alpha\nb: charlie\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := age.EncryptSecretsFile(dir); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(filepath.Join(dir, "secrets.encrypted.yaml"))

	if string(first) == string(second) {
		t.Fatal("expected ciphertext to change after b's plaintext changed")
	}

	// Extract a's encrypted line from each — they must match (a was unchanged).
	aFirst := lineWithPrefix(string(first), "a: ENC[")
	aSecond := lineWithPrefix(string(second), "a: ENC[")
	if aFirst == "" || aSecond == "" {
		t.Fatalf("could not isolate a's ciphertext line\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	if aFirst != aSecond {
		t.Errorf("a's ciphertext rotated unnecessarily\n first:  %s\n second: %s", aFirst, aSecond)
	}
}

// === RotateKeys ===

// Contract: RotateKeys preserves plaintext round-trip after the
// call: whatever key happens to be on disk afterwards is sufficient
// to decrypt the encrypted file. This is the minimum integrity
// guarantee operators rely on.
//
// KNOWN BUG (not pinned, on purpose): RotateKeys at age.go:485 does
// NOT actually replace talm.key. It calls GenerateKey, which is a
// load-or-create operation: if talm.key already exists,
// GenerateKey loads and returns it instead of generating a fresh
// identity. So today RotateKeys re-encrypts the secrets file with
// the SAME key. The test does not assert "public key changes" — if
// it did, this would fail today, and pinning a passing assertion
// would lock in the bug. Track separately and fix in a dedicated
// commit.
func TestContract_Age_RotateKeys_PreservesPlaintextRoundTrip(t *testing.T) {
	dir := t.TempDir()
	plain := []byte("secret: rotate-me\n")
	if err := os.WriteFile(filepath.Join(dir, "secrets.yaml"), plain, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := age.EncryptSecretsFile(dir); err != nil {
		t.Fatal(err)
	}

	if err := age.RotateKeys(dir); err != nil {
		t.Fatalf("RotateKeys: %v", err)
	}

	// Decrypt with whatever key is on disk now — plaintext must round-trip.
	if err := os.Remove(filepath.Join(dir, "secrets.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := age.DecryptSecretsFile(dir); err != nil {
		t.Fatalf("Decrypt after rotation: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "secrets.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var orig, after map[string]any
	if err := yaml.Unmarshal(plain, &orig); err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(got, &after); err != nil {
		t.Fatal(err)
	}
	if !mapsEqual(orig, after) {
		t.Errorf("plaintext changed across rotation\norig: %v\nafter: %v", orig, after)
	}
}

// === EncryptYAMLFile / DecryptYAMLFile ===

// Contract: the generic file-pair encrypt/decrypt accepts arbitrary
// plain / encrypted file names (used for kubeconfig and other
// non-secrets.yaml files). Round-trip semantics identical to the
// secrets.yaml path.
func TestContract_Age_GenericYAMLFile_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	plain := []byte("kubeconfig:\n  server: https://api.example.com:6443\n  token: abc123\n")
	plainName := "kubeconfig.yaml"
	encName := "kubeconfig.encrypted.yaml"
	if err := os.WriteFile(filepath.Join(dir, plainName), plain, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := age.EncryptYAMLFile(dir, plainName, encName); err != nil {
		t.Fatalf("EncryptYAMLFile: %v", err)
	}
	if err := os.Remove(filepath.Join(dir, plainName)); err != nil {
		t.Fatal(err)
	}
	if err := age.DecryptYAMLFile(dir, encName, plainName); err != nil {
		t.Fatalf("DecryptYAMLFile: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, plainName))
	if err != nil {
		t.Fatal(err)
	}
	var origMap, gotMap map[string]any
	if err := yaml.Unmarshal(plain, &origMap); err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(got, &gotMap); err != nil {
		t.Fatal(err)
	}
	if !mapsEqual(origMap, gotMap) {
		t.Errorf("round-trip mismatch\norig:\n%s\ngot:\n%s", plain, got)
	}
}

// === helpers ===

// mapsEqual is a tiny structural comparison sufficient for YAML-
// derived map[string]any values used in these tests. Keeps the
// dependency surface minimal — no reflect.DeepEqual that would also
// pick up irrelevant struct-vs-map representation differences.
func mapsEqual(a, b map[string]any) bool {
	if len(a) != len(b) {
		return false
	}
	for k, av := range a {
		bv, ok := b[k]
		if !ok {
			return false
		}
		if !valuesEqual(av, bv) {
			return false
		}
	}
	return true
}

func valuesEqual(a, b any) bool {
	switch av := a.(type) {
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok {
			return false
		}
		return mapsEqual(av, bv)
	case []any:
		bv, ok := b.([]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for i := range av {
			if !valuesEqual(av[i], bv[i]) {
				return false
			}
		}
		return true
	default:
		return a == b
	}
}

func lineWithPrefix(content, prefix string) string {
	for line := range strings.SplitSeq(content, "\n") {
		if strings.HasPrefix(line, prefix) {
			return line
		}
	}
	return ""
}
