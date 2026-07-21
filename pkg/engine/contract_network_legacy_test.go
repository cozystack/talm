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

// Contract: rendered legacy machine.network section for cozystack and
// generic charts (Talos pre-v1.12 / TalosVersion="" schema). The
// legacy renderer reconstructs network configuration into a single
// `machine.network` block: hostname, nameservers, and interfaces[]
// — the latter carrying addresses, routes, vlans, bond, and inline
// vip blocks. This file pins the inline shape, the VIP placement
// rules, and the override semantics that differ from the multi-doc
// path (separate Layer2VIPConfig document).

package engine

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// renderLegacyCozystackControlplane renders the cozystack controlplane
// template against the legacy schema (TalosVersion="") with the given
// lookup and overrides. Wraps renderLegacyChart with the cozystack
// chart path and template name baked in.
func renderLegacyCozystackControlplane(t *testing.T, lookup func(string, string, string) (map[string]any, error), overrides map[string]any) string {
	t.Helper()
	return renderLegacyChart(t, cozystackChartPath, "cozystack/templates/controlplane.yaml", lookup, overrides)
}

// === machine.network top-level ===

// Contract: legacy schema always emits machine.network with hostname
// (quoted) and nameservers (JSON array). hostname uses the same
// discovery / placeholder fallback as the multi-doc path.
func TestContract_NetworkLegacy_HostnameAndNameserversAlwaysEmitted(t *testing.T) {
	lookup := func(resource, _, id string) (map[string]any, error) {
		switch {
		case resource == "hostname" && id == "hostname":
			return map[string]any{"spec": map[string]any{"hostname": "node-prod-1"}}, nil
		case resource == "resolvers" && id == "resolvers":
			return map[string]any{"spec": map[string]any{"dnsServers": []any{"8.8.8.8", "1.1.1.1"}}}, nil
		}
		return map[string]any{}, nil
	}
	out := renderLegacyCozystackControlplane(t, lookup, map[string]any{
		"advertisedSubnets": []any{testAdvertisedSubnet},
	})
	assertContains(t, out, "network:")
	assertContains(t, out, `hostname: "node-prod-1"`)
	// Nameservers in legacy are a JSON-style list on a single line:
	// `nameservers: ["8.8.8.8","1.1.1.1"]`.
	assertContains(t, out, `nameservers: ["8.8.8.8","1.1.1.1"]`)
}

// Contract: legacy schema renders no machine.network.interfaces[]
// entry at all when discovery yields nothing AND the operator did not
// set vipLink. This is the fresh-boot case: the chart cannot fabricate
// an interface name, so it leaves machine.network without an
// interfaces block. Legacy Talos accepts this (it falls back to DHCP).
//
// The chart still emits a "# -- Discovered interfaces:" COMMENT under
// machine.network (debug aid from physical_links_info), so the
// assertion targets the actual `interfaces:` mapping key at indent 4
// — not the comment that contains the same substring.
func TestContract_NetworkLegacy_NoInterfacesOnFreshBoot(t *testing.T) {
	out := renderLegacyCozystackControlplane(t, freshNicLookup(), map[string]any{
		"advertisedSubnets": []any{testAdvertisedSubnet},
	})
	// machine.network must exist (the section header is unconditional).
	assertContains(t, out, "network:")
	// But the `interfaces:` mapping key must NOT appear under it.
	// Indent 4 is where chart emits it (machine.network.interfaces).
	if strings.Contains(out, "\n    interfaces:\n") {
		t.Errorf("expected output NOT to contain machine.network.interfaces:\n%s", out)
	}
}

// === interfaces[] for plain physical NIC ===

// Contract: a single physical NIC produces one
// machine.network.interfaces[] entry with `interface:`, `addresses:`,
// `routes:` (default route), and on controlplane an inline `vip:` if
// floatingIP is set.
func TestContract_NetworkLegacy_PlainNICInterfaceShape(t *testing.T) {
	out := renderLegacyCozystackControlplane(t, simpleNicLookup(), map[string]any{
		"advertisedSubnets": []any{testAdvertisedSubnet},
	})
	assertContains(t, out, "interfaces:")
	assertContains(t, out, "- interface: eth0")
	assertContains(t, out, `addresses: ["192.168.201.10/24"]`)
	assertContains(t, out, "routes:")
	assertContains(t, out, "- network: 0.0.0.0/0")
	assertContains(t, out, "gateway: 192.168.201.1")
}

// Contract: floatingIP on controlplane adds an inline `vip: { ip: ...}`
// block to the interface entry (NOT to a separate Layer2VIPConfig —
// that is multi-doc). Worker template never adds vip even when
// floatingIP is set (Talos enforces VIP on controlplane only).
func TestContract_NetworkLegacy_FloatingIPInlineVipOnControlplane(t *testing.T) {
	out := renderLegacyCozystackControlplane(t, simpleNicLookup(), map[string]any{
		"floatingIP":        "192.168.201.99",
		"advertisedSubnets": []any{testAdvertisedSubnet},
	})
	assertContains(t, out, "vip:")
	assertContains(t, out, "ip: 192.168.201.99")
}

// Contract: legacy worker template never emits a vip block, even if
// floatingIP is set. Pinning this prevents an accidental "drop the
// machineType check" regression that would fragment cluster identity.
func TestContract_NetworkLegacy_NoVipOnWorker(t *testing.T) {
	out := renderLegacyChart(t, cozystackChartPath, "cozystack/templates/worker.yaml", simpleNicLookup(), map[string]any{
		"floatingIP":        "192.168.201.99",
		"advertisedSubnets": []any{testAdvertisedSubnet},
	})
	assertContains(t, out, "interfaces:")
	assertContains(t, out, "- interface: eth0")
	assertNotContains(t, out, "vip:")
}

// === interfaces[] for VLAN ===

// Contract: when discovery's IPv4 default-gateway-bearing link is a
// VLAN, the chart emits the parent link name as the top-level
// `interface:` and nests the VLAN under `vlans:` with
// vlanId/addresses/routes. Legacy schema cannot represent a VLAN as a
// top-level interface — it lives inside its parent's `vlans:` list.
func TestContract_NetworkLegacy_VLANNestedUnderParentInterface(t *testing.T) {
	out := renderLegacyCozystackControlplane(t, multiNicWithVLANLookup(), map[string]any{
		"advertisedSubnets": []any{testAdvertisedSubnet},
	})
	// multiNicWithVLANLookup carries the default route on a VLAN link
	// stacked on a physical NIC; legacy renders the parent NIC at top
	// level with vlans: nested.
	assertContains(t, out, "interfaces:")
	assertContains(t, out, "vlans:")
	assertContains(t, out, "vlanId:")
	assertContains(t, out, "- network: 0.0.0.0/0")
}

// === interfaces[] for bond ===

// Contract: when discovery's default link is a bond, the chart emits
// `- interface: <bond-name>` at the top level with a nested `bond:`
// block carrying interfaces (slaves), mode, and any other configured
// bondMaster fields. Slaves do NOT appear as separate entries — the
// bond owns them.
func TestContract_NetworkLegacy_BondNestedBondBlock(t *testing.T) {
	out := renderLegacyCozystackControlplane(t, bondTopologyLookup(), map[string]any{
		"advertisedSubnets": []any{testAdvertisedSubnet},
	})
	assertContains(t, out, "- interface: bond0")
	assertContains(t, out, "bond:")
	// bond slaves listed under bond.interfaces.
	assertContains(t, out, "- eth0")
	assertContains(t, out, "- eth1")
	assertContains(t, out, "mode: 802.3ad")
}

// === vipLink override ===

// Contract: when both floatingIP and vipLink are set on a
// controlplane AND vipLink differs from the discovery-derived default
// link, the chart emits a SECOND interfaces[] entry whose `interface:`
// is the operator's vipLink and whose body is exclusively the vip
// block. The discovery-derived interface is preserved unchanged
// EXCEPT its inline vip block is suppressed (otherwise the same
// floatingIP would be pinned on two different links — a guaranteed
// arbitration loss in Talos's VIP operator). The override is the
// legacy-schema mirror of multi-doc Layer2VIPConfig override.
func TestContract_NetworkLegacy_VipLinkOverrideEmitsSeparateEntry(t *testing.T) {
	out := renderLegacyCozystackControlplane(t, simpleNicLookup(), map[string]any{
		"floatingIP":        "192.168.201.99",
		"vipLink":           "eth0.4000",
		"advertisedSubnets": []any{testAdvertisedSubnet},
	})
	// Both interfaces[] entries present.
	assertContains(t, out, "- interface: eth0")
	assertContains(t, out, "- interface: eth0.4000")
	// VIP appears under the override entry.
	assertContains(t, out, "ip: 192.168.201.99")
	// Discovery-derived eth0 entry must NOT carry an inline vip.
	// The legacy chart suppresses it via $suppressInlineVip.
	// Substring "ip: 192.168.201.99" appears once total.
	if got := strings.Count(out, "ip: 192.168.201.99"); got != 1 {
		t.Errorf("expected exactly 1 vip ip line (override only); got %d in:\n%s", got, out)
	}
}

// Contract: vipLink override that EQUALS the discovery-derived
// default link produces NO separate entry. The chart re-uses the
// existing inline vip on the discovered interface — emitting a second
// identical entry would be redundant.
func TestContract_NetworkLegacy_VipLinkOverrideMatchingDefaultLinkNoExtraEntry(t *testing.T) {
	out := renderLegacyCozystackControlplane(t, simpleNicLookup(), map[string]any{
		"floatingIP":        "192.168.201.99",
		"vipLink":           "eth0",
		"advertisedSubnets": []any{testAdvertisedSubnet},
	})
	// Only one interface entry total, with inline vip.
	if got := strings.Count(out, "- interface: eth0"); got != 1 {
		t.Errorf("expected exactly 1 - interface: eth0 entry; got %d in:\n%s", got, out)
	}
	assertContains(t, out, "ip: 192.168.201.99")
}

// === existing legacy interfaces from running config ===

// Contract: when the running MachineConfig already declares
// machine.network.interfaces[], the legacy renderer copies that block
// verbatim (via existing_interfaces_configuration → toYaml) and skips
// its own discovery-driven generation. This preserves operator-
// declared overlays on existing nodes — the chart does not "rewrite"
// pre-existing network config.
//
// YAML round-trip from spec.machine.network.interfaces[] reorders map
// keys alphabetically (addresses before interface), so the rendered
// `interface: eth0` is NOT prefixed with `- ` (the dash belongs to
// the alphabetically-first key, addresses). The contract is "the
// existing block is preserved", not "the on-wire format matches the
// chart's auto-derived format" — those are different paths.
func TestContract_NetworkLegacy_ExistingInterfacesShortCircuit(t *testing.T) {
	out := renderLegacyCozystackControlplane(t, legacyInterfacesLookup(), map[string]any{
		"advertisedSubnets": []any{testAdvertisedSubnet},
	})
	assertContains(t, out, "interfaces:")
	// Operator's interface name surfaces (without the leading `- `:
	// addresses sorts first in the YAML round-trip).
	assertContains(t, out, "interface: eth0")
	assertContains(t, out, "192.168.1.10/24")
}

// Contract: the legacy schema emits one interface entry with an
// inline vip per vips entry (no Layer2VIPConfig document pre-1.12). Links
// are non-primary (simpleNicLookup's primary is eth0) so they don't hit
// the primary-collision guard exercised below.
func TestContract_NetworkLegacy_MultiVIP_Cozystack(t *testing.T) {
	out := renderLegacyChart(t, cozystackChartPath, "cozystack/templates/controlplane.yaml", simpleNicLookup(), map[string]any{
		"advertisedSubnets": []any{testAdvertisedSubnet},
		"vips": []any{
			map[string]any{"link": "eth1", "ip": "192.0.2.254"},
			map[string]any{"link": "eth2", "ip": "203.0.113.254"},
		},
	})
	assertContains(t, out, "interface: eth1")
	assertContains(t, out, "interface: eth2")
	assertContains(t, out, "ip: 192.0.2.254")
	assertContains(t, out, "ip: 203.0.113.254")
}

// Contract: a vips entry whose link is the discovered primary link
// fails fast on legacy — the primary already has an interfaces[] entry, so
// a second one would double-pin the device (Talos won't merge them).
func TestContract_NetworkLegacy_MultiVIP_LinkEqualsPrimary_Fails_Cozystack(t *testing.T) {
	err := renderCozystackExpectError(t, simpleNicLookup(), map[string]any{
		"advertisedSubnets": []any{testAdvertisedSubnet},
		"vips": []any{
			map[string]any{"link": "eth0", "ip": "192.0.2.254"},
		},
	}, "v1.11")
	if err == nil {
		t.Fatal("expected a fail-fast for a vips link equal to the primary link on the legacy schema")
	}
	if !strings.Contains(err.Error(), "primary link") {
		t.Errorf("error should explain the primary-link collision, got %v", err)
	}
}

// Contract: two vips entries on the same link fail fast on legacy —
// interfaces[].vip holds a single IP per interface, so they cannot both be
// expressed.
func TestContract_NetworkLegacy_MultiVIP_DuplicateLink_Fails_Cozystack(t *testing.T) {
	err := renderCozystackExpectError(t, simpleNicLookup(), map[string]any{
		"advertisedSubnets": []any{testAdvertisedSubnet},
		"vips": []any{
			map[string]any{"link": "eth1", "ip": "192.0.2.254"},
			map[string]any{"link": "eth1", "ip": "192.0.2.253"},
		},
	}, "v1.11")
	if err == nil {
		t.Fatal("expected a fail-fast for two vips entries on the same legacy link")
	}
	if !strings.Contains(err.Error(), "already carries a VIP") {
		t.Errorf("error should explain the duplicate link, got %v", err)
	}
}

// Contract: a vips entry on the same link as the vipLink override
// fails fast on legacy — the override already pins a vip there.
func TestContract_NetworkLegacy_MultiVIP_LinkEqualsVipLink_Fails_Cozystack(t *testing.T) {
	err := renderCozystackExpectError(t, simpleNicLookup(), map[string]any{
		"advertisedSubnets": []any{testAdvertisedSubnet},
		"floatingIP":        "192.0.2.99",
		"vipLink":           "eth1",
		"vips": []any{
			map[string]any{"link": "eth1", "ip": "192.0.2.50"},
		},
	}, "v1.11")
	if err == nil {
		t.Fatal("expected a fail-fast for a vips link equal to the vipLink override on legacy")
	}
	if !strings.Contains(err.Error(), "already carries a VIP") {
		t.Errorf("error should explain the link already carries a VIP, got %v", err)
	}
}

// Contract: a vips ip equal to floatingIP fails fast on legacy —
// the same VIP ip on two links loses arbitration on apply.
func TestContract_NetworkLegacy_MultiVIP_DuplicateIP_Fails_Cozystack(t *testing.T) {
	err := renderCozystackExpectError(t, simpleNicLookup(), map[string]any{
		"advertisedSubnets": []any{testAdvertisedSubnet},
		"floatingIP":        "192.0.2.99",
		"vipLink":           "eth1",
		"vips": []any{
			map[string]any{"link": "eth2", "ip": "192.0.2.99"},
		},
	}, "v1.11")
	if err == nil {
		t.Fatal("expected a fail-fast for a vips ip duplicating floatingIP on legacy")
	}
	if !strings.Contains(err.Error(), "more than once") {
		t.Errorf("error should explain the duplicate ip, got %v", err)
	}
}

// Contract: a malformed vips[].ip fails the legacy render too,
// matching the multidoc fail-fast.
func TestContract_NetworkLegacy_MultiVIP_InvalidIP_Cozystack(t *testing.T) {
	err := renderCozystackExpectError(t, simpleNicLookup(), map[string]any{
		"advertisedSubnets": []any{testAdvertisedSubnet},
		"vips": []any{
			map[string]any{"link": "eth0", "ip": "not-an-ip"},
		},
	}, "v1.11")
	if err == nil {
		t.Fatal("expected a validation error for a malformed vips[].ip in the legacy schema")
	}
	if !strings.Contains(err.Error(), "not-an-ip") {
		t.Errorf("error should name the malformed ip, got %v", err)
	}
}

// Contract: vips are emitted in the legacy schema even when
// discovery resolves no default-route link — the interfaces block opens
// on vips too, so a declared VIP is not silently dropped.
func TestContract_NetworkLegacy_MultiVIP_NoDefaultLink_Cozystack(t *testing.T) {
	out := renderLegacyChart(t, cozystackChartPath, "cozystack/templates/controlplane.yaml", noDefaultRouteWithSubnetMatchLookup(), map[string]any{
		"advertisedSubnets": []any{testAdvertisedSubnet},
		"vips": []any{
			map[string]any{"link": "eth0", "ip": "192.0.2.254"},
		},
	})
	assertContains(t, out, "interfaces:")
	assertContains(t, out, "interface: eth0")
	assertContains(t, out, "ip: 192.0.2.254")
}

// Contract: preserveExisting does NOT add a second
// machine.network block on the legacy schema — the legacy renderer
// already emits one, so a duplicate mapping key would make yaml.v3
// reject the config on load ("mapping key already defined"). The knob's
// machine.common block is multi-doc only. Asserting the document decodes
// cleanly pins exactly the failure the duplicate would cause.
func TestContract_NetworkLegacy_PreserveExisting_NoDuplicateNetwork_Cozystack(t *testing.T) {
	out := renderLegacyChart(t, cozystackChartPath, "cozystack/templates/controlplane.yaml", legacyInterfacesLookup(), map[string]any{
		"advertisedSubnets": []any{testAdvertisedSubnet},
		"network":           map[string]any{"preserveExisting": true},
	})

	var doc map[string]any
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("legacy preserveExisting render must be valid YAML (no duplicate machine.network), got: %v\n%s", err, out)
	}
}

// Contract: extraLinks fails fast on the legacy schema (it is
// multi-doc only) rather than silently dropping the declared links.
func TestContract_NetworkLegacy_ExtraLinks_FailsFast_Cozystack(t *testing.T) {
	err := renderCozystackExpectError(t, simpleNicLookup(), map[string]any{
		"advertisedSubnets": []any{testAdvertisedSubnet},
		"network": map[string]any{
			"extraLinks": []any{
				map[string]any{"interface": "bond1", "bond": map[string]any{"interfaces": []any{"eth0"}}},
			},
		},
	}, "v1.11")
	if err == nil {
		t.Fatal("expected extraLinks to fail fast on the legacy schema")
	}
	if !strings.Contains(err.Error(), "multi-doc") {
		t.Errorf("error should explain the multi-doc-only limitation, got %v", err)
	}
}

// Contract: the generic preset carries its own copy of the legacy
// vips loop, so pin its happy path too — each vips entry becomes one
// interface with an inline vip. Links are non-primary (simpleNicLookup's
// primary is eth0) so they don't hit the primary-collision guard.
func TestContract_NetworkLegacy_MultiVIP_Generic(t *testing.T) {
	out := renderLegacyChart(t, genericChartPath, "generic/templates/controlplane.yaml", simpleNicLookup(), map[string]any{
		"advertisedSubnets": []any{testAdvertisedSubnet},
		"vips": []any{
			map[string]any{"link": "eth1", "ip": "192.0.2.254"},
			map[string]any{"link": "eth2", "ip": "203.0.113.254"},
		},
	})
	assertContains(t, out, "interface: eth1")
	assertContains(t, out, "interface: eth2")
	assertContains(t, out, "ip: 192.0.2.254")
	assertContains(t, out, "ip: 203.0.113.254")
}

// Contract: extraLinks fails fast on the generic legacy schema
// too — the fail-fast is duplicated per preset, so the generic copy needs
// its own guard test.
func TestContract_NetworkLegacy_ExtraLinks_FailsFast_Generic(t *testing.T) {
	err := renderGenericExpectError(t, simpleNicLookup(), map[string]any{
		"advertisedSubnets": []any{testAdvertisedSubnet},
		"network": map[string]any{
			"extraLinks": []any{
				map[string]any{"interface": "bond1", "bond": map[string]any{"interfaces": []any{"eth0"}}},
			},
		},
	}, "v1.11")
	if err == nil {
		t.Fatal("expected extraLinks to fail fast on the generic legacy schema")
	}
	if !strings.Contains(err.Error(), "multi-doc") {
		t.Errorf("error should explain the multi-doc-only limitation, got %v", err)
	}
}

// Contract: a vips entry whose link is already present in the
// running interfaces block (a re-applied legacy node emits that block
// verbatim) fails fast — a second interfaces[] entry for the same device
// is a duplicate Talos won't merge. legacyMultiInterfaceLookup preserves
// eth0+eth1; the VIP targets eth1.
func TestContract_NetworkLegacy_MultiVIP_LinkInPreservedBlock_Fails_Cozystack(t *testing.T) {
	err := renderCozystackExpectError(t, legacyMultiInterfaceLookup(), map[string]any{
		"advertisedSubnets": []any{testAdvertisedSubnet},
		"vips": []any{
			map[string]any{"link": "eth1", "ip": "192.0.2.254"},
		},
	}, "v1.11")
	if err == nil {
		t.Fatal("expected a fail-fast for a vips link already in the preserved interfaces block")
	}
	if !strings.Contains(err.Error(), "already present") {
		t.Errorf("error should explain the link is already in the preserved block, got %v", err)
	}
}

// Contract: the generic preset carries its own copy of the vips
// loop, so pin the same preserved-link collision on it.
func TestContract_NetworkLegacy_MultiVIP_LinkInPreservedBlock_Fails_Generic(t *testing.T) {
	err := renderGenericExpectError(t, legacyMultiInterfaceLookup(), map[string]any{
		"advertisedSubnets": []any{testAdvertisedSubnet},
		"vips": []any{
			map[string]any{"link": "eth1", "ip": "192.0.2.254"},
		},
	}, "v1.11")
	if err == nil {
		t.Fatal("expected a fail-fast for a vips link already in the preserved interfaces block")
	}
	if !strings.Contains(err.Error(), "already present") {
		t.Errorf("error should explain the link is already in the preserved block, got %v", err)
	}
}

// Contract: the vipLink override path has the same hole — a
// vipLink naming a link already in the preserved block would also
// double-declare the device. eth1 is preserved; eth0 is the primary.
func TestContract_NetworkLegacy_VipLinkOverride_InPreservedBlock_Fails_Cozystack(t *testing.T) {
	err := renderCozystackExpectError(t, legacyMultiInterfaceLookup(), map[string]any{
		"advertisedSubnets": []any{testAdvertisedSubnet},
		"floatingIP":        "192.0.2.254",
		"vipLink":           "eth1",
	}, "v1.11")
	if err == nil {
		t.Fatal("expected a fail-fast for a vipLink override naming a preserved link")
	}
	if !strings.Contains(err.Error(), "already present") {
		t.Errorf("error should explain the vipLink is already in the preserved block, got %v", err)
	}
}

// Contract: generic counterpart of the vipLink-override collision.
func TestContract_NetworkLegacy_VipLinkOverride_InPreservedBlock_Fails_Generic(t *testing.T) {
	err := renderGenericExpectError(t, legacyMultiInterfaceLookup(), map[string]any{
		"advertisedSubnets": []any{testAdvertisedSubnet},
		"floatingIP":        "192.0.2.254",
		"vipLink":           "eth1",
	}, "v1.11")
	if err == nil {
		t.Fatal("expected a fail-fast for a vipLink override naming a preserved link")
	}
	if !strings.Contains(err.Error(), "already present") {
		t.Errorf("error should explain the vipLink is already in the preserved block, got %v", err)
	}
}

// Contract: the preserved-link guard must not over-fire — a vips
// entry on a link that is NOT in the preserved block (a genuinely new
// device) still renders one interface entry alongside the preserved ones.
func TestContract_NetworkLegacy_MultiVIP_NewLinkWithPreservedBlock_Cozystack(t *testing.T) {
	out := renderLegacyChart(t, cozystackChartPath, "cozystack/templates/controlplane.yaml", legacyMultiInterfaceLookup(), map[string]any{
		"advertisedSubnets": []any{testAdvertisedSubnet},
		"vips": []any{
			map[string]any{"link": "eth2", "ip": "192.0.2.254"},
		},
	})
	// The preserved interfaces survive.
	assertContains(t, out, "interface: eth0")
	assertContains(t, out, "interface: eth1")
	// The new VIP link is emitted exactly once.
	if got := strings.Count(out, "interface: eth2"); got != 1 {
		t.Errorf("expected exactly one eth2 interface entry, got %d:\n%s", got, out)
	}
	assertContains(t, out, "ip: 192.0.2.254")
}
