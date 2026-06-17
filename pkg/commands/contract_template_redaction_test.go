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
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cozystack/talm/pkg/age"
	"gopkg.in/yaml.v3"
)

// encryptedValuesFile writes values-secret.yaml under dir, encrypts it
// (generating talm.key), and returns the encrypted file path.
func encryptedValuesFile(t *testing.T, dir string, plain map[string]any) string {
	t.Helper()

	plainBytes, err := yaml.Marshal(plain)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "values-secret.yaml"), plainBytes, 0o600); err != nil {
		t.Fatalf("write plain: %v", err)
	}
	if err := age.EncryptYAMLFile(dir, "values-secret.yaml", "values-secret.encrypted.yaml"); err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	return filepath.Join(dir, "values-secret.encrypted.yaml")
}

// TestContract_CollectEncryptedValueLeaves pins that only *.encrypted.yaml
// files contribute to the secret set, and their decrypted string leaves are
// collected.
func TestContract_CollectEncryptedValueLeaves(t *testing.T) {
	dir := t.TempDir()
	enc := encryptedValuesFile(t, dir, map[string]any{"registryPassword": "hunter2"})

	plain := filepath.Join(dir, "plain.yaml")
	if err := os.WriteFile(plain, []byte("notASecret: visible\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	secrets, err := collectEncryptedValueLeaves([]string{enc, plain}, dir)
	if err != nil {
		t.Fatalf("collectEncryptedValueLeaves: %v", err)
	}

	if _, ok := secrets["hunter2"]; !ok {
		t.Errorf("decrypted secret leaf must be collected; got %v", secrets)
	}
	if _, ok := secrets["visible"]; ok {
		t.Errorf("plaintext value file must NOT contribute secrets; got %v", secrets)
	}
}

// TestContract_CollectStringLeaves_SkipsEmptyStrings pins the load-bearing
// empty-string guard: an encrypted file with an empty-string leaf must NOT
// contribute "" to the secret set. Without the skip, sealing on "" would match
// (and omit/redact) every empty field across the rendered config.
func TestContract_CollectStringLeaves_SkipsEmptyStrings(t *testing.T) {
	secrets := make(map[string]struct{})
	collectStringLeaves(map[string]any{
		"empty":  "",
		"real":   "s3cr3t",
		"nested": map[string]any{"alsoEmpty": "", "tok": "abc"},
		"list":   []any{"", "xyz"},
	}, secrets)

	if _, ok := secrets[""]; ok {
		t.Error("empty string must not be collected as a secret")
	}
	for _, want := range []string{"s3cr3t", "abc", "xyz"} {
		if _, ok := secrets[want]; !ok {
			t.Errorf("non-empty leaf %q must be collected; got %v", want, secrets)
		}
	}
}

// TestContract_SealRenderedSecrets_ModeMatrix pins the three output modes:
// -I omits the secret field, plain stdout redacts it, and --show-secrets
// prints it verbatim. No encrypted file in scope is a verbatim no-op.
func TestContract_SealRenderedSecrets_ModeMatrix(t *testing.T) {
	dir := t.TempDir()
	enc := encryptedValuesFile(t, dir, map[string]any{"registryPassword": "hunter2"})

	rendered := []byte("version: v1alpha1\nmachine:\n  type: controlplane\n  registries:\n    config:\n      r.example:\n        auth:\n          username: bob\n          password: hunter2\n")

	t.Run("inplace omits", func(t *testing.T) {
		out, err := sealRenderedSecrets(rendered, []string{enc}, []string{enc}, dir, true, false)
		if err != nil {
			t.Fatalf("seal: %v", err)
		}
		if strings.Contains(string(out), "hunter2") || strings.Contains(string(out), "password") {
			t.Errorf("-I must omit the secret field:\n%s", out)
		}
	})

	t.Run("stdout redacts by default", func(t *testing.T) {
		out, err := sealRenderedSecrets(rendered, []string{enc}, []string{enc}, dir, false, false)
		if err != nil {
			t.Fatalf("seal: %v", err)
		}
		if strings.Contains(string(out), "hunter2") {
			t.Errorf("default stdout must redact the secret:\n%s", out)
		}
		if !strings.Contains(string(out), "***") {
			t.Errorf("redacted output must carry the sentinel:\n%s", out)
		}
	})

	t.Run("show-secrets prints verbatim", func(t *testing.T) {
		out, err := sealRenderedSecrets(rendered, []string{enc}, []string{enc}, dir, false, true)
		if err != nil {
			t.Fatalf("seal: %v", err)
		}
		if !strings.Contains(string(out), "hunter2") {
			t.Errorf("--show-secrets must print the secret verbatim:\n%s", out)
		}
	})

	t.Run("no encrypted file is verbatim", func(t *testing.T) {
		out, err := sealRenderedSecrets(rendered, nil, nil, dir, true, false)
		if err != nil {
			t.Fatalf("seal: %v", err)
		}
		if string(out) != string(rendered) {
			t.Errorf("no encrypted value files => verbatim render;\n got: %s", out)
		}
	})
}

// TestContract_WarnUnpersistedEncryptedFiles pins the foot-gun guard: when
// `template -I` omits secrets from an encrypted value file that `apply` won't
// re-read (not in Chart.yaml templateOptions.valueFiles), a warning must name
// the file — otherwise the omitted secret is silently lost from the applied
// config. A file that IS persisted, or a plaintext file, warns nothing.
func TestContract_WarnUnpersistedEncryptedFiles(t *testing.T) {
	t.Run("unpersisted encrypted file warns", func(t *testing.T) {
		var buf bytes.Buffer
		warnUnpersistedEncryptedFiles([]string{"values-secret.encrypted.yaml"}, nil, &buf)

		got := buf.String()
		if !strings.Contains(got, "values-secret.encrypted.yaml") {
			t.Errorf("warning must name the unpersisted file; got:\n%s", got)
		}
		if !strings.Contains(got, "templateOptions.valueFiles") {
			t.Errorf("warning must tell the operator how to fix it; got:\n%s", got)
		}
	})

	t.Run("persisted encrypted file is silent", func(t *testing.T) {
		var buf bytes.Buffer
		enc := "values-secret.encrypted.yaml"
		warnUnpersistedEncryptedFiles([]string{enc}, []string{enc}, &buf)

		if buf.Len() != 0 {
			t.Errorf("a Chart.yaml-persisted encrypted file must not warn; got:\n%s", buf.String())
		}
	})

	t.Run("plaintext value file is silent", func(t *testing.T) {
		var buf bytes.Buffer
		warnUnpersistedEncryptedFiles([]string{"plain.yaml"}, nil, &buf)

		if buf.Len() != 0 {
			t.Errorf("a plaintext value file carries no omitted secrets; must not warn; got:\n%s", buf.String())
		}
	})
}
