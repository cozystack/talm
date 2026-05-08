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
	lookup := func(resource, namespace, id string) (map[string]any, error) {
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
