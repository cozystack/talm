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
	"strings"
	"testing"
)

// TestContract_InitEncryptDecrypt_ValuesSecretRoundTrip pins that
// `talm init --encrypt` / `--decrypt` covers values-secret.yaml alongside
// the fixed secret set: the plaintext encrypts to values-secret.encrypted.yaml
// (an ENC[AGE,...] envelope, safe to commit) and decrypts back byte-for-byte.
// This is the turnkey way an operator produces the encrypted user-values file
// that templateOptions.valueFiles then references.
func TestContract_InitEncryptDecrypt_ValuesSecretRoundTrip(t *testing.T) {
	withInitFlagsSnapshot(t)
	withConfigSnapshot(t)

	dir := t.TempDir()
	dirAbs, _ := filepath.Abs(dir)
	t.Chdir(dirAbs)
	Config.RootDir = dirAbs
	Config.RootDirExplicit = true
	Config.GlobalOptions.Kubeconfig = ""

	// Minimal initialised project so the encrypt/decrypt RunE root check
	// passes. secrets.yaml must be a valid YAML map (EncryptSecretsFile
	// unmarshals it).
	writeFile(t, dirAbs, "Chart.yaml", "name: test\n")
	writeFile(t, dirAbs, "secrets.yaml", "secret: dummy\n")
	if err := os.MkdirAll(filepath.Join(dirAbs, "charts", "talm"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dirAbs, "charts", "talm"), "Chart.yaml", "name: talm\n")

	const valuesSecretBody = "registryPassword: hunter2\n"
	writeFile(t, dirAbs, "values-secret.yaml", valuesSecretBody)

	// Encrypt.
	resetInitFlags()
	initCmdFlags.encrypt = true
	if err := initCmd.RunE(initCmd, nil); err != nil {
		t.Fatalf("init --encrypt: %v", err)
	}

	encBytes, err := os.ReadFile(filepath.Join(dirAbs, "values-secret.encrypted.yaml"))
	if err != nil {
		t.Fatalf("values-secret.encrypted.yaml not produced: %v", err)
	}
	if !strings.Contains(string(encBytes), "ENC[AGE,data:") {
		t.Errorf("encrypted file must contain an age envelope; got:\n%s", encBytes)
	}
	if strings.Contains(string(encBytes), "hunter2") {
		t.Errorf("plaintext secret leaked into encrypted file:\n%s", encBytes)
	}

	// Remove the plaintext and decrypt it back.
	if err := os.Remove(filepath.Join(dirAbs, "values-secret.yaml")); err != nil {
		t.Fatal(err)
	}

	resetInitFlags()
	initCmdFlags.decrypt = true
	if err := initCmd.RunE(initCmd, nil); err != nil {
		t.Fatalf("init --decrypt: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dirAbs, "values-secret.yaml"))
	if err != nil {
		t.Fatalf("values-secret.yaml not restored: %v", err)
	}
	if !strings.Contains(string(got), "registryPassword: hunter2") {
		t.Errorf("decrypted values-secret.yaml lost its value; got:\n%s", got)
	}
}

// TestContract_SecurityInfoBox_NamesEveryGitignoredFile pins that the
// `talm init` Security Information box enumerates every secret-bearing file
// writeGitignoreFile adds to .gitignore (default kubeconfig name). The box and
// the gitignore set must not drift — an under-listed box would make an
// operator believe a plaintext file (e.g. values-secret.yaml) is unprotected.
func TestContract_SecurityInfoBox_NamesEveryGitignoredFile(t *testing.T) {
	withConfigSnapshot(t)

	dir := t.TempDir()
	Config.RootDir = dir
	Config.GlobalOptions.Kubeconfig = "" // default kubeconfig name

	// printSecretsWarning early-returns unless talm.key exists.
	writeFile(t, dir, talmKeyName, "AGE-SECRET-KEY-1PLACEHOLDER\n")

	out := captureStderr(t, printSecretsWarning)

	for _, name := range []string{secretsYamlName, talosconfigName, talmKeyName, valuesSecretYamlName, defaultKubeconfigName} {
		if !strings.Contains(out, name) {
			t.Errorf("Security Information box must name %q (it is git-ignored by writeGitignoreFile); box:\n%s", name, out)
		}
	}
}

func resetInitFlags() {
	initCmdFlags.encrypt = false
	initCmdFlags.decrypt = false
	initCmdFlags.force = false
	initCmdFlags.update = false
	initCmdFlags.preset = ""
	initCmdFlags.name = ""
	initCmdFlags.image = ""
}

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}
