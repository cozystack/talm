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

// Contract: `talm apply --dry-run` prints two diffs — talm's own structured
// drift preview (redacted by path + user value) AND the server-returned
// ModeDetails diff. The second one is opaque text, so it is redacted by VALUE.
// These tests pin that ModeDetails never leaks a Talos bootstrap secret or a
// user encrypted-value secret unless --show-secrets-in-drift is set.

package commands

import (
	"bytes"
	"strings"
	"testing"

	machineapi "github.com/siderolabs/talos/pkg/machinery/api/machine"
)

// TestContract_CollectConfigSecretValues pins which rendered-config leaves feed
// the ModeDetails redaction set: Talos bootstrap material (fixed paths) and
// Wireguard key material (by leaf name), but NOT ordinary public fields.
func TestContract_CollectConfigSecretValues(t *testing.T) {
	rendered := []byte(`machine:
  type: controlplane
  token: MACHINE-TOKEN-AAAA
  ca:
    crt: MACHINE-CA-CRT
    key: MACHINE-CA-KEY-BBBB
  network:
    hostname: node0
    interfaces:
      - interface: eth0
        wireguard:
          privateKey: WG-PRIVATE-CCCC
          peers:
            - publicKey: WG-PUBLIC-PUB
              presharedKey: WG-PSK-DDDD
              allowedIPs:
                - 10.0.0.0/8
cluster:
  secret: CLUSTER-SECRET-EEEE
  token: CLUSTER-TOKEN-FFFF
  ca:
    crt: CLUSTER-CA-CRT
    key: CLUSTER-CA-KEY-GGGG
  acceptedCAs:
    - crt: ACCEPTED-CA-CRT
      key: ACCEPTED-CA-KEY-HHHH
`)

	got, err := collectConfigSecretValues(rendered)
	if err != nil {
		t.Fatalf("collectConfigSecretValues: %v", err)
	}

	mustCollect := []string{
		"MACHINE-TOKEN-AAAA",
		"MACHINE-CA-KEY-BBBB",
		"WG-PRIVATE-CCCC",
		"WG-PSK-DDDD",
		"CLUSTER-SECRET-EEEE",
		"CLUSTER-TOKEN-FFFF",
		"CLUSTER-CA-KEY-GGGG",
		"ACCEPTED-CA-KEY-HHHH",
	}
	for _, want := range mustCollect {
		if _, ok := got[want]; !ok {
			t.Errorf("secret value %q must be collected for ModeDetails redaction; got %v", want, keysOf(got))
		}
	}

	// Public / low-entropy fields must NOT enter the value set, so they stay
	// readable in the dry-run diff and never clobber unrelated text.
	mustNotCollect := []string{"node0", "eth0", "WG-PUBLIC-PUB", "10.0.0.0/8"}
	for _, unwanted := range mustNotCollect {
		if _, ok := got[unwanted]; ok {
			t.Errorf("non-secret field %q must NOT be collected (would over-redact the dry-run diff)", unwanted)
		}
	}
}

// TestContract_CollectConfigSecretValues_AcceptedCAsCrt pins the documented
// bounded over-collection: acceptedCAs is an allowlisted slice path, so its
// public crt is collected alongside the key. This is intentional — the slice is
// redacted whole — and pinned so a future change to the granularity is a
// conscious decision.
func TestContract_CollectConfigSecretValues_AcceptedCAsCrt(t *testing.T) {
	rendered := []byte(`cluster:
  acceptedCAs:
    - crt: ACCEPTED-CA-CRT-XXXX
      key: ACCEPTED-CA-KEY-YYYY
`)

	got, err := collectConfigSecretValues(rendered)
	if err != nil {
		t.Fatalf("collectConfigSecretValues: %v", err)
	}

	if _, ok := got["ACCEPTED-CA-CRT-XXXX"]; !ok {
		t.Error("acceptedCAs crt is collected whole with the slice (documented bounded over-collection)")
	}
}

// TestContract_RedactValuesInText pins the by-value text masking used on
// ModeDetails: every occurrence of every secret value becomes the sentinel, an
// empty value set is a no-op (the --show-secrets-in-drift path), and a value
// that is a substring of another does not survive as a fragment.
func TestContract_RedactValuesInText(t *testing.T) {
	text := "value: high-entropy-secret\nother: high-entropy-secret-LONGER\n"
	values := secretSetOf("high-entropy-secret", "high-entropy-secret-LONGER")

	got := redactValuesInText(text, values)
	if strings.Contains(got, "high-entropy-secret") {
		t.Errorf("no secret fragment may survive redaction:\n%s", got)
	}
	if !strings.Contains(got, modeDetailsRedactionSentinel) {
		t.Errorf("redacted text must carry the sentinel:\n%s", got)
	}

	if redactValuesInText(text, nil) != text {
		t.Error("empty value set must leave the text verbatim (show-secrets path)")
	}
}

// TestContract_PrintApplyResultsRedacted pins the end-to-end print path: a
// ModeDetails diff carrying a secret is masked when the value is in scope and
// printed verbatim when the value set is empty (show-secrets).
func TestContract_PrintApplyResultsRedacted(t *testing.T) {
	const secret = "high-entropy-value-do-not-collide-abcdef0123456789"
	resp := &machineapi.ApplyConfigurationResponse{
		Messages: []*machineapi.ApplyConfiguration{
			{ModeDetails: "Config diff:\n+        value: " + secret + "\n"},
		},
	}

	var redacted bytes.Buffer
	printApplyResultsRedacted(resp, secretSetOf(secret), &redacted)
	if strings.Contains(redacted.String(), secret) {
		t.Errorf("ModeDetails must redact an in-scope secret:\n%s", redacted.String())
	}

	var shown bytes.Buffer
	printApplyResultsRedacted(resp, nil, &shown)
	if !strings.Contains(shown.String(), secret) {
		t.Errorf("empty value set (show-secrets) must print ModeDetails verbatim:\n%s", shown.String())
	}
}

func keysOf(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}

	return out
}
