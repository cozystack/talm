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

package applycheck

import (
	"strings"
	"testing"
)

// TestWalkNetAddrFindings_StaticHostConfig pins the contract for
// StaticHostConfig.name validation. In Talos's v1alpha1 schema
// (StaticHostConfigV1Alpha1) the IP literal lives in the `name`
// field — the document's meta-name doubling as the IP the
// hostnames map to. There is NO separate `address` field. Tests
// feed the schema's actual shape (the `name:` line carries the IP)
// so the walker fires on what Talos really emits, not on a
// hand-crafted shape that would never appear on the wire.
func TestWalkNetAddrFindings_StaticHostConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		yaml        string
		wantFinding bool
	}{
		{
			name:        "valid IPv4 in name",
			yaml:        "apiVersion: v1alpha1\nkind: StaticHostConfig\nname: 192.0.2.10\nhostnames: [foo.example]\n",
			wantFinding: false,
		},
		{
			name:        "valid IPv6 in name",
			yaml:        "apiVersion: v1alpha1\nkind: StaticHostConfig\nname: 2001:db8::1\nhostnames: [foo.example]\n",
			wantFinding: false,
		},
		{
			name:        "malformed IPv4 in name (octet >255)",
			yaml:        "apiVersion: v1alpha1\nkind: StaticHostConfig\nname: 999.999.0.1\nhostnames: [foo.example]\n",
			wantFinding: true,
		},
		{
			name:        "hostname-shaped name (not an IP)",
			yaml:        "apiVersion: v1alpha1\nkind: StaticHostConfig\nname: example.invalid\nhostnames: [foo.example]\n",
			wantFinding: true,
		},
		{
			name:        "missing name field — no finding",
			yaml:        "apiVersion: v1alpha1\nkind: StaticHostConfig\nhostnames: [foo.example]\n",
			wantFinding: false,
		},
		{
			name:        "non-string name (int) — no finding",
			yaml:        "apiVersion: v1alpha1\nkind: StaticHostConfig\nname: 42\nhostnames: [foo.example]\n",
			wantFinding: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			findings, err := WalkNetAddrFindings([]byte(tc.yaml))
			if err != nil {
				t.Fatalf("WalkNetAddrFindings: %v", err)
			}

			gotFinding := len(findings) > 0
			if gotFinding != tc.wantFinding {
				t.Errorf("findings=%v, want finding=%v; got: %+v", gotFinding, tc.wantFinding, findings)
			}

			if tc.wantFinding && len(findings) > 0 {
				f := findings[0]
				if f.Severity != SeverityBlocker {
					t.Errorf("StaticHostConfig.name malformed should be a blocker, got %v", f.Severity)
				}

				if !strings.Contains(f.Reason, "StaticHostConfig.name") {
					t.Errorf("Reason should name the field; got %q", f.Reason)
				}
			}
		})
	}
}

// TestWalkNetAddrFindings_NetworkRuleConfig pins the CIDR validation
// for ingress[].subnet and ingress[].except. In Talos's v1alpha1
// schema (RuleConfigV1Alpha1) the CIDR-shaped fields live inside
// each `ingress` entry as `subnet` (required) and `except`
// (optional) — NOT at a top-level `matchSourceAddress[]`. Tests
// feed the real shape.
func TestWalkNetAddrFindings_NetworkRuleConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		yaml             string
		wantFindingCount int
	}{
		{
			name: "all valid subnets",
			yaml: "apiVersion: v1alpha1\nkind: NetworkRuleConfig\nname: r1\n" +
				"ingress:\n  - subnet: 192.0.2.0/24\n  - subnet: 2001:db8::/32\n  - subnet: 10.0.0.0/8\n",
			wantFindingCount: 0,
		},
		{
			name: "subnet with except (both valid)",
			yaml: "apiVersion: v1alpha1\nkind: NetworkRuleConfig\nname: r2\n" +
				"ingress:\n  - subnet: 192.0.2.0/24\n    except: 192.0.2.128/25\n",
			wantFindingCount: 0,
		},
		{
			name: "one malformed subnet in a list of three",
			yaml: "apiVersion: v1alpha1\nkind: NetworkRuleConfig\nname: r3\n" +
				"ingress:\n  - subnet: 192.0.2.0/24\n  - subnet: notacidr\n  - subnet: 10.0.0.0/8\n",
			wantFindingCount: 1,
		},
		{
			name: "two malformed subnets",
			yaml: "apiVersion: v1alpha1\nkind: NetworkRuleConfig\nname: r4\n" +
				"ingress:\n  - subnet: 192.0.2.999/24\n  - subnet: notacidr\n",
			wantFindingCount: 2,
		},
		{
			name: "bare IP without /N in subnet",
			yaml: "apiVersion: v1alpha1\nkind: NetworkRuleConfig\nname: r5\n" +
				"ingress:\n  - subnet: 192.0.2.10\n",
			wantFindingCount: 1,
		},
		{
			name: "malformed except next to valid subnet",
			yaml: "apiVersion: v1alpha1\nkind: NetworkRuleConfig\nname: r6\n" +
				"ingress:\n  - subnet: 192.0.2.0/24\n    except: notacidr\n",
			wantFindingCount: 1,
		},
		{
			name: "empty ingress list — no finding",
			yaml: "apiVersion: v1alpha1\nkind: NetworkRuleConfig\nname: r7\n" +
				"ingress: []\n",
			wantFindingCount: 0,
		},
		{
			name:             "missing ingress field — no finding",
			yaml:             "apiVersion: v1alpha1\nkind: NetworkRuleConfig\nname: r8\n",
			wantFindingCount: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			findings, err := WalkNetAddrFindings([]byte(tc.yaml))
			if err != nil {
				t.Fatalf("WalkNetAddrFindings: %v", err)
			}

			if len(findings) != tc.wantFindingCount {
				t.Errorf("got %d findings, want %d; findings: %+v", len(findings), tc.wantFindingCount, findings)
			}

			for i := range findings {
				f := &findings[i]
				if !strings.Contains(f.Reason, "ingress") {
					t.Errorf("Reason should name the ingress path; got %q", f.Reason)
				}
			}
		})
	}
}

// TestWalkNetAddrFindings_WireguardConfig pins peers[].endpoint
// validation. Empty endpoint is NOT a finding — peers without an
// endpoint are listener-only remote peers (this side won't initiate;
// it will accept connections from the peer). Malformed endpoints
// (missing port, bad IP, plain hostname) are blockers.
func TestWalkNetAddrFindings_WireguardConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		yaml             string
		wantFindingCount int
	}{
		{
			name: "valid IPv4 host:port",
			yaml: "apiVersion: v1alpha1\nkind: WireguardConfig\nname: wg0\n" +
				"peers:\n  - publicKey: AAA\n    endpoint: 192.0.2.10:51820\n",
			wantFindingCount: 0,
		},
		{
			name: "valid IPv6 [host]:port",
			yaml: "apiVersion: v1alpha1\nkind: WireguardConfig\nname: wg1\n" +
				"peers:\n  - publicKey: BBB\n    endpoint: \"[2001:db8::1]:51820\"\n",
			wantFindingCount: 0,
		},
		{
			name: "missing port",
			yaml: "apiVersion: v1alpha1\nkind: WireguardConfig\nname: wg2\n" +
				"peers:\n  - publicKey: CCC\n    endpoint: 192.0.2.10\n",
			wantFindingCount: 1,
		},
		{
			name: "hostname:port (not IP) — flagged",
			yaml: "apiVersion: v1alpha1\nkind: WireguardConfig\nname: wg3\n" +
				"peers:\n  - publicKey: DDD\n    endpoint: example.invalid:51820\n",
			wantFindingCount: 1,
		},
		{
			name: "empty endpoint — listener-only peer, no finding",
			yaml: "apiVersion: v1alpha1\nkind: WireguardConfig\nname: wg4\n" +
				"peers:\n  - publicKey: EEE\n    endpoint: \"\"\n",
			wantFindingCount: 0,
		},
		{
			name: "missing endpoint field — listener-only peer, no finding",
			yaml: "apiVersion: v1alpha1\nkind: WireguardConfig\nname: wg5\n" +
				"peers:\n  - publicKey: FFF\n",
			wantFindingCount: 0,
		},
		{
			name: "two peers, one malformed",
			yaml: "apiVersion: v1alpha1\nkind: WireguardConfig\nname: wg6\n" +
				"peers:\n  - publicKey: GGG\n    endpoint: 192.0.2.10:51820\n" +
				"  - publicKey: HHH\n    endpoint: bad:notanumber\n",
			wantFindingCount: 1,
		},
		{
			name:             "no peers list — no finding",
			yaml:             "apiVersion: v1alpha1\nkind: WireguardConfig\nname: wg7\n",
			wantFindingCount: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			findings, err := WalkNetAddrFindings([]byte(tc.yaml))
			if err != nil {
				t.Fatalf("WalkNetAddrFindings: %v", err)
			}

			if len(findings) != tc.wantFindingCount {
				t.Errorf("got %d findings, want %d; findings: %+v", len(findings), tc.wantFindingCount, findings)
			}

			for i := range findings {
				f := &findings[i]
				if !strings.Contains(f.Reason, "endpoint") {
					t.Errorf("Reason should name 'endpoint' for peers[].endpoint findings; got %q", f.Reason)
				}
			}
		})
	}
}

// TestWalkNetAddrFindings_UnknownKind_NoFinding pins the no-op
// behaviour for kinds outside the dispatch map. The net-addr walker
// must never error on an unknown kind — Talos extensions and future
// kinds should not break the gate.
func TestWalkNetAddrFindings_UnknownKind_NoFinding(t *testing.T) {
	t.Parallel()

	yaml := "apiVersion: v1alpha1\nkind: SomeFutureKind\nname: x\naddress: bogus\n"

	findings, err := WalkNetAddrFindings([]byte(yaml))
	if err != nil {
		t.Fatalf("WalkNetAddrFindings: %v", err)
	}

	if len(findings) != 0 {
		t.Errorf("unknown kind must produce no findings; got %+v", findings)
	}
}

// TestWalkNetAddrFindings_EmptyInput_NoError pins the empty-bytes path:
// zero-length input is not a YAML decode error; it's the trivial
// "nothing to walk" case.
func TestWalkNetAddrFindings_EmptyInput_NoError(t *testing.T) {
	t.Parallel()

	findings, err := WalkNetAddrFindings(nil)
	if err != nil {
		t.Errorf("empty input should not error; got %v", err)
	}

	if len(findings) != 0 {
		t.Errorf("empty input should produce no findings; got %+v", findings)
	}
}

// TestWalkNetAddrFindings_RealSchema_StaticHostConfig pins the
// walker against the actual v1alpha1 schema shape that the Talos
// machinery package emits. The original walker iteration assumed
// fields that don't exist in StaticHostConfigV1Alpha1 (e.g.
// `address`) and tests with hand-crafted YAML passed because they
// were self-consistent. A YAML body that matches the real schema
// (`apiVersion`, `kind`, `name` carrying the IP) must trigger the
// walker on a malformed IP literal — without this pin, the walker
// could silently revert to validating non-existent fields again.
func TestWalkNetAddrFindings_RealSchema_StaticHostConfig(t *testing.T) {
	t.Parallel()

	// Schema-shape body: name field carries the IP literal, hostnames
	// list is unrelated.
	yamlBody := "apiVersion: v1alpha1\nkind: StaticHostConfig\nname: 999.999.0.1\nhostnames:\n  - foo.example\n  - bar.example\n"

	findings, err := WalkNetAddrFindings([]byte(yamlBody))
	if err != nil {
		t.Fatalf("WalkNetAddrFindings on real-schema body: %v", err)
	}

	if len(findings) != 1 {
		t.Fatalf("real-schema body with malformed name must produce exactly one finding; got %d: %+v", len(findings), findings)
	}

	if !strings.Contains(findings[0].Ref.Source, "name") || strings.Contains(findings[0].Ref.Source, "address") {
		t.Errorf("finding source path must reference `name`, not `address`; got %q", findings[0].Ref.Source)
	}
}

// TestWalkNetAddrFindings_RealSchema_NetworkRuleConfig pins the
// walker against the actual ingress[].subnet shape. Original
// walker iteration validated a top-level `matchSourceAddress[]`
// that doesn't exist in RuleConfigV1Alpha1; this pin keeps the
// walker aligned with Talos's real type.
func TestWalkNetAddrFindings_RealSchema_NetworkRuleConfig(t *testing.T) {
	t.Parallel()

	yamlBody := "apiVersion: v1alpha1\nkind: NetworkRuleConfig\nname: rule1\n" +
		"portSelector:\n  ports: [22]\n  protocol: tcp\n" +
		"ingress:\n  - subnet: notacidr\n  - subnet: 192.0.2.0/24\n    except: 192.0.2.999/30\n"

	findings, err := WalkNetAddrFindings([]byte(yamlBody))
	if err != nil {
		t.Fatalf("WalkNetAddrFindings on real-schema body: %v", err)
	}

	// Two malformed: ingress[0].subnet and ingress[1].except.
	if len(findings) != 2 {
		t.Fatalf("expected exactly 2 findings (ingress[0].subnet + ingress[1].except); got %d: %+v", len(findings), findings)
	}

	for i := range findings {
		src := findings[i].Ref.Source
		if !strings.Contains(src, "ingress") || strings.Contains(src, "matchSourceAddress") {
			t.Errorf("finding source path must reference `ingress`, not `matchSourceAddress`; got %q", src)
		}
	}
}

// TestMultidocNetAddrHandlers_NoOverlapWithRefHandlers pins the
// dispatch-map disjointness contract: net-addr handlers run in a
// parallel walker, so a kind that appears in BOTH maps would get
// double-walked (one finding from each pipeline) — silent
// duplication. None of the three net-addr kinds (StaticHostConfig,
// NetworkRuleConfig, WireguardConfig) are in multidocHandlers today;
// pin that contract so a future entry doesn't create overlap.
func TestMultidocNetAddrHandlers_NoOverlapWithRefHandlers(t *testing.T) {
	t.Parallel()

	for kind := range multidocNetAddrHandlers {
		if _, exists := multidocHandlers[kind]; exists {
			t.Errorf("kind %q registered in BOTH multidocHandlers (ref-based) and multidocNetAddrHandlers (syntactic) — duplicate findings; pick one pipeline", kind)
		}
	}
}
