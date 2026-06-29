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

// Contract: secret-value sealing for `talm template`. OmitSecretValues strips
// fields whose value is a known secret from rendered config (so they never
// land in a committed node file); RedactSecretValues replaces them with a
// sentinel for the stdout preview. Both are structural (YAML-node) operations,
// never substring rewrites.

package engine

import (
	"bytes"
	"strings"
	"testing"
)

// configWithScalarSecret is a minimal config whose secret ("topsecret") sits
// at a nested MAP value (machine.registries.config.<reg>.auth.password) — the
// C1 case where the key itself is dropped.
const configWithScalarSecret = `version: v1alpha1
machine:
  type: controlplane
  registries:
    config:
      registry.example:
        auth:
          username: bob
          password: topsecret
`

func secretSet(values ...string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, v := range values {
		out[v] = struct{}{}
	}

	return out
}

// TestContract_OmitSecretValues_DropsScalarMapKey pins the C1 rule: a secret
// that is a direct map value has its key removed entirely (so apply's map
// merge leaves the rendered value authoritative), while sibling non-secret
// keys survive.
func TestContract_OmitSecretValues_DropsScalarMapKey(t *testing.T) {
	out, err := OmitSecretValues([]byte(configWithScalarSecret), secretSet("topsecret"))
	if err != nil {
		t.Fatalf("OmitSecretValues: %v", err)
	}

	got := string(out)
	if strings.Contains(got, "topsecret") {
		t.Errorf("secret value must be omitted:\n%s", got)
	}
	if strings.Contains(got, "password") {
		t.Errorf("the key holding the secret scalar must be dropped:\n%s", got)
	}
	if !strings.Contains(got, "username: bob") {
		t.Errorf("non-secret sibling key must survive:\n%s", got)
	}
}

// TestContract_RedactSecretValues_ReplacesWithSentinel pins that redaction
// keeps the structure (the key stays) but masks the value — the preview shows
// "a secret is here" without leaking it.
func TestContract_RedactSecretValues_ReplacesWithSentinel(t *testing.T) {
	out, err := RedactSecretValues([]byte(configWithScalarSecret), secretSet("topsecret"))
	if err != nil {
		t.Fatalf("RedactSecretValues: %v", err)
	}

	got := string(out)
	if strings.Contains(got, "topsecret") {
		t.Errorf("secret value must be redacted out of preview:\n%s", got)
	}
	if !strings.Contains(got, "password: '***'") && !strings.Contains(got, "password: \"***\"") && !strings.Contains(got, "password: ***") {
		t.Errorf("redacted value must keep the key with the sentinel:\n%s", got)
	}
	if !strings.Contains(got, "username: bob") {
		t.Errorf("non-secret value must survive redaction:\n%s", got)
	}
}

// TestContract_OmitSecretValues_EmptySetIsVerbatim pins that with no secrets
// the input bytes are returned unchanged — the common no-encrypted-values path
// must not reformat the render.
func TestContract_OmitSecretValues_EmptySetIsVerbatim(t *testing.T) {
	in := []byte(configWithScalarSecret)

	out, err := OmitSecretValues(in, secretSet())
	if err != nil {
		t.Fatalf("OmitSecretValues: %v", err)
	}

	if !bytes.Equal(in, out) {
		t.Errorf("empty secret set must return input verbatim;\n got: %s\nwant: %s", out, in)
	}
}

// TestContract_OmitSecretValues_Idempotent pins that sealing is deterministic:
// two runs over the same input produce byte-identical output. age encryption is
// randomized, so any churn here would dirty git on every `talm template -I`.
func TestContract_OmitSecretValues_Idempotent(t *testing.T) {
	secrets := secretSet("topsecret")

	first, err := OmitSecretValues([]byte(configWithScalarSecret), secrets)
	if err != nil {
		t.Fatalf("first OmitSecretValues: %v", err)
	}

	second, err := OmitSecretValues([]byte(configWithScalarSecret), secrets)
	if err != nil {
		t.Fatalf("second OmitSecretValues: %v", err)
	}

	if !bytes.Equal(first, second) {
		t.Errorf("sealing must be deterministic (no git churn);\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}
