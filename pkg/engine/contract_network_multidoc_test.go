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

// Contract: rendered multi-doc network documents for the cozystack
// and generic charts (Talos v1.12+ schema). The multi-doc renderer
// reconstructs network configuration from COSI discovery resources
// — links, routes, addresses, hostname, resolvers — and emits one
// typed document per configurable link plus the always-on
// HostnameConfig and ResolverConfig pair. This file pins the per-link
// document shape (LinkConfig / BondConfig / VLANConfig), the gateway-
// only routing rule, the bond-slave filtering rule, the floatingIP
// stripping rule, and the Layer2VIPConfig override semantics.
//
// The chart × machineType matrix here is narrower than other contract
// files because multi-doc shape is independent of the chart (cozystack
// and generic share the same multi-doc renderer block byte-for-byte
// in their respective _helpers.tpl). Each test pins the contract on
// at least one chart; the cross-chart consistency tests in
// contract_schema_test.go cover the rest.
//
// Reuses existing lookup fixtures from render_test.go (simpleNicLookup,
// multiNicLookup, bondTopologyLookup, vlanOnBondTopologyLookup, etc.).
// The fixtures are stable contracts in their own right; if a fixture
// changes, all contract tests that use it surface the drift.

package engine

import (
	"strings"
	"testing"
)

// === HostnameConfig ===

// Contract: HostnameConfig.hostname uses the discovered hostname when
// it is a "real" name (not in the placeholder set: rescue, talos,
// localhost, localhost.localdomain). Operators who set a meaningful
// hostname on the host get it surfaced in the rendered config.
func TestContract_NetworkMultidoc_HostnameUsesDiscoveredName(t *testing.T) {
	lookup := func(resource, _, id string) (map[string]any, error) {
		if resource == "hostname" && id == "hostname" {
			return map[string]any{
				"spec": map[string]any{"hostname": "node-prod-1"},
			}, nil
		}
		return map[string]any{}, nil
	}
	out := renderCozystackWith(t, lookup, map[string]any{
		"advertisedSubnets": []any{testAdvertisedSubnet},
	})
	assertContains(t, out, "kind: HostnameConfig")
	assertContains(t, out, `hostname: "node-prod-1"`)
}

// Contract: HostnameConfig.hostname falls back to a synthesized
// `talos-<5-char-hash>` name when discovery returns a placeholder
// (rescue, talos, localhost, localhost.localdomain) or no hostname
// at all. The synthesized name is deterministic per address set so a
// re-render yields the same hostname (no churn on every apply). The
// test verifies the prefix, not the hash digits, so the contract
// survives changes to the hashed input.
func TestContract_NetworkMultidoc_HostnameFallbackToSynthesized(t *testing.T) {
	cases := []struct {
		name               string
		discoveredHostname string
	}{
		{"placeholder/talos", "talos"},
		{"placeholder/localhost", "localhost"},
		{"placeholder/rescue", "rescue"},
		{"empty/no-hostname-resource", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lookup := func(resource, _, id string) (map[string]any, error) {
				if resource == "hostname" && id == "hostname" && tc.discoveredHostname != "" {
					return map[string]any{
						"spec": map[string]any{"hostname": tc.discoveredHostname},
					}, nil
				}
				return map[string]any{}, nil
			}
			out := renderCozystackWith(t, lookup, map[string]any{
				"advertisedSubnets": []any{testAdvertisedSubnet},
			})
			assertContains(t, out, "kind: HostnameConfig")
			assertContains(t, out, `hostname: "talos-`)
		})
	}
}

// === ResolverConfig ===

// Contract: ResolverConfig.nameservers is emitted with one
// `- address: "..."` line per dnsServer when discovery returns a
// resolvers spec, and falls back to a YAML-empty list `[]` when
// resolvers are unknown. The empty fallback keeps the document
// well-formed; Talos accepts empty nameservers (DHCP-supplied).
func TestContract_NetworkMultidoc_ResolverConfigPopulated(t *testing.T) {
	lookup := func(resource, _, id string) (map[string]any, error) {
		if resource == "resolvers" && id == "resolvers" {
			return map[string]any{
				"spec": map[string]any{
					"dnsServers": []any{"8.8.8.8", "1.1.1.1"},
				},
			}, nil
		}
		return map[string]any{}, nil
	}
	out := renderCozystackWith(t, lookup, map[string]any{
		"advertisedSubnets": []any{testAdvertisedSubnet},
	})
	assertContains(t, out, "kind: ResolverConfig")
	assertContains(t, out, `- address: "8.8.8.8"`)
	assertContains(t, out, `- address: "1.1.1.1"`)
}

// Contract: ResolverConfig falls back to YAML empty list when
// resolvers are not discoverable (no DHCP yet, no static config).
func TestContract_NetworkMultidoc_ResolverConfigEmptyFallback(t *testing.T) {
	out := renderCozystackWith(t, helmEngineEmptyLookup, map[string]any{
		"advertisedSubnets": []any{testAdvertisedSubnet},
	})
	assertContains(t, out, "kind: ResolverConfig")
	assertContains(t, out, "nameservers:")
	assertContains(t, out, "[]")
}

// === LinkConfig (single physical NIC) ===

// Contract: a single physical NIC produces exactly one LinkConfig
// document whose name matches the discovered link, addresses match
// discovery, routes carry the default-route gateway, and MTU is NOT
// emitted when discovery reports no MTU (Talos uses its own default).
//
// The simpleNicLookup fixture provides one eth0 with 192.168.201.10/24
// and a default route to 192.168.201.1 — the canonical happy path.
func TestContract_NetworkMultidoc_SinglePhysicalNICRendersLinkConfig(t *testing.T) {
	out := renderCozystackWith(t, simpleNicLookup(), map[string]any{
		"advertisedSubnets": []any{testAdvertisedSubnet},
	})
	assertContains(t, out, "kind: LinkConfig")
	assertContains(t, out, "name: eth0")
	assertContains(t, out, "- address: 192.168.201.10/24")
	assertContains(t, out, "routes:")
	assertContains(t, out, "- gateway: 192.168.201.1")
}

// Contract: when a multi-NIC node has only one default-gateway-bearing
// link, ONLY that link's LinkConfig carries `routes:`. Non-gateway
// links get LinkConfig with addresses but no routes block — emitting
// routes on every link would inject duplicate default routes that
// shadow each other.
func TestContract_NetworkMultidoc_MultiNICRoutesOnGatewayLinkOnly(t *testing.T) {
	out := renderCozystackWith(t, multiNicLookup(), map[string]any{
		"advertisedSubnets": []any{testAdvertisedSubnet},
	})
	// Both links rendered.
	assertContains(t, out, "name: eth0")
	assertContains(t, out, "name: eth1")
	// Gateway link's address.
	assertContains(t, out, "- address: 192.168.201.10/24")
	// Non-gateway link's address (private subnet on eth1).
	assertContains(t, out, "- address: 10.0.0.5/24")
	// Exactly one `routes:` block in the multi-doc network section.
	// Both legacy and multi-doc paths use the same LinkConfig structure
	// here, so substring counting is stable.
	if got := strings.Count(out, "routes:"); got != 1 {
		t.Errorf("expected exactly 1 routes: block (gateway link only), got %d in:\n%s", got, out)
	}
}

// === BondConfig ===

// Contract: a bond master link produces a BondConfig document. The
// document carries `links: [<slave>, <slave>]` (the slaves' metadata
// IDs) and the bondMaster fields verbatim: bondMode, xmitHashPolicy,
// lacpRate, miimon. Slaves themselves do NOT get standalone
// LinkConfig documents — emitting one alongside BondConfig conflicts
// with Talos's link controller convergence.
func TestContract_NetworkMultidoc_BondRendersBondConfig(t *testing.T) {
	out := renderCozystackWith(t, bondTopologyLookup(), map[string]any{
		"advertisedSubnets": []any{testAdvertisedSubnet},
	})
	assertContains(t, out, "kind: BondConfig")
	assertContains(t, out, "name: bond0")
	assertContains(t, out, "links:")
	assertContains(t, out, "- eth0")
	assertContains(t, out, "- eth1")
	assertContains(t, out, "bondMode: 802.3ad")
	assertContains(t, out, "xmitHashPolicy: layer3+4")
	assertContains(t, out, "lacpRate: fast")
	assertContains(t, out, "miimon: 100")
}

// Contract: bond slaves never appear as standalone LinkConfig
// documents. configurable_link_names filters them out via spec.slaveKind.
func TestContract_NetworkMultidoc_BondSlavesNotEmittedAsLinkConfig(t *testing.T) {
	out := renderCozystackWith(t, bondTopologyLookup(), map[string]any{
		"advertisedSubnets": []any{testAdvertisedSubnet},
	})
	// Each slave appears under BondConfig.links, but not as a
	// LinkConfig document. Substring "kind: LinkConfig\nname: eth0"
	// would indicate the regression.
	if strings.Contains(out, "kind: LinkConfig\nname: eth0") {
		t.Errorf("eth0 (bond slave) leaked as LinkConfig:\n%s", out)
	}
	if strings.Contains(out, "kind: LinkConfig\nname: eth1") {
		t.Errorf("eth1 (bond slave) leaked as LinkConfig:\n%s", out)
	}
}

// === VLANConfig ===

// Contract: a VLAN link with a resolvable parent and a vlanID
// produces a VLANConfig document with name, vlanID, parent, and (if
// addresses present) addresses block. Bond-as-parent is supported:
// the VLAN's parent name is the bond's metadata.id.
func TestContract_NetworkMultidoc_VLANOnBondRendersVLANConfig(t *testing.T) {
	out := renderCozystackWith(t, vlanOnBondTopologyLookup(), map[string]any{
		"advertisedSubnets": []any{testAdvertisedSubnet},
	})
	assertContains(t, out, "kind: VLANConfig")
	assertContains(t, out, "vlanID:")
	assertContains(t, out, "parent: bond0")
}

// === Bridge non-gateway: silent skip ===

// Contract: a bridge link that is NOT the IPv4 default route is
// skipped silently — no BridgeConfig is emitted (chart does not yet
// support BridgeConfig output), and no LinkConfig is emitted (it is
// not a physical NIC). The expectation is that operators who run
// bridges declare them via per-node body overlays. The non-gateway
// case is the silent path; the gateway case is a hard fail (pinned
// in contract_errors_test.go).
func TestContract_NetworkMultidoc_NonGatewayBridgeSkipped(t *testing.T) {
	out := renderCozystackWith(t, bridgeLookup(), map[string]any{
		"advertisedSubnets": []any{testAdvertisedSubnet},
	})
	// No BridgeConfig — feature unimplemented.
	assertNotContains(t, out, "kind: BridgeConfig")
}

// === Layer2VIPConfig: discovery-derived ===

// Contract: when floatingIP is set on a controlplane and discovery
// resolves a default-gateway-bearing link, Layer2VIPConfig is emitted
// with name=<floatingIP> and link=<discovered-link>. Worker templates
// never emit Layer2VIPConfig regardless of floatingIP. The simpleNic
// fixture carries the gateway on eth0, so the test pins link=eth0.
func TestContract_NetworkMultidoc_Layer2VIPFromDiscovery(t *testing.T) {
	out := renderCozystackWith(t, simpleNicLookup(), map[string]any{
		"floatingIP":        "192.168.201.99",
		"advertisedSubnets": []any{testAdvertisedSubnet},
	})
	assertContains(t, out, "kind: Layer2VIPConfig")
	assertContains(t, out, `name: "192.168.201.99"`)
	assertContains(t, out, "link: eth0")
}

// Contract: floatingIP without controlplane (worker template) does
// NOT emit Layer2VIPConfig.
func TestContract_NetworkMultidoc_Layer2VIPNeverOnWorker(t *testing.T) {
	chrt, lookup := cozystackChartPath, simpleNicLookup()
	// Render worker template manually (renderCozystackWith renders
	// controlplane). Use renderChartTemplateWithLookup which honours
	// templateFile + LookupFunc, then assert the absence.
	out := renderChartTemplateWithLookup(t, chrt, workerTpl, lookup, multidocTalos)
	assertNotContains(t, out, "kind: Layer2VIPConfig")
}

// === Layer2VIPConfig: vipLink override ===

// Contract: when both floatingIP and vipLink are set on a
// controlplane, Layer2VIPConfig is emitted with link=<vipLink> at the
// top of the multi-doc stream (right after HostnameConfig and
// ResolverConfig), regardless of discovery state. The discovery-
// derived Layer2VIPConfig path is suppressed — emitting both would
// pin the same VIP on two links.
func TestContract_NetworkMultidoc_Layer2VIPOverrideSuppressesDiscovery(t *testing.T) {
	out := renderCozystackWith(t, simpleNicLookup(), map[string]any{
		"floatingIP":        "192.168.201.99",
		"vipLink":           "eth0.4000",
		"advertisedSubnets": []any{testAdvertisedSubnet},
	})
	assertContains(t, out, "kind: Layer2VIPConfig")
	assertContains(t, out, `name: "192.168.201.99"`)
	assertContains(t, out, "link: eth0.4000")
	// Exactly one Layer2VIPConfig document. The discovery-derived
	// emission must be suppressed.
	if got := strings.Count(out, "kind: Layer2VIPConfig"); got != 1 {
		t.Errorf("expected exactly 1 Layer2VIPConfig, got %d in:\n%s", got, out)
	}
}

// Contract: vipLink override emits Layer2VIPConfig even when
// discovery yields no default link at all (fresh-boot case where the
// VLAN this template is about to bring up does not yet exist on the
// host).
func TestContract_NetworkMultidoc_Layer2VIPOverrideOnFreshNode(t *testing.T) {
	out := renderCozystackWith(t, freshNicLookup(), map[string]any{
		"floatingIP":        "192.168.201.99",
		"vipLink":           "eth0.4000",
		"advertisedSubnets": []any{testAdvertisedSubnet},
	})
	assertContains(t, out, "kind: Layer2VIPConfig")
	assertContains(t, out, "link: eth0.4000")
}

// === floatingIP stripping ===

// Contract: when floatingIP is set, the chart strips any address
// matching `<floatingIP>/...` from per-link addresses. The VIP is the
// Layer2VIPConfig target — re-declaring it as a regular address would
// race the VIP operator (Talos's VIP operator installs the VIP as a
// global-scope address indistinguishable from a permanent one in
// COSI; without the strip, a re-render against the VIP-active leader
// would declare the VIP both as a permanent address and as the
// Layer2VIPConfig target).
//
// Test setup: simpleNicLookup carries 192.168.201.10/24 on eth0. We
// deliberately set floatingIP to the SAME host address (192.168.201.10)
// so the strip path actually fires — the chart filters per-link
// addresses by `<floatingIP>/` prefix, so a VIP that matches no
// discovered address would not exercise the filter. The address must
// appear once at Layer2VIPConfig.name (the VIP declaration itself),
// but MUST NOT appear under LinkConfig.addresses as a regular address.
func TestContract_NetworkMultidoc_FloatingIPStrippedFromLinkAddresses(t *testing.T) {
	out := renderCozystackWith(t, simpleNicLookup(), map[string]any{
		"floatingIP":        "192.168.201.10",
		"advertisedSubnets": []any{testAdvertisedSubnet},
	})
	// Layer2VIPConfig declares the VIP.
	assertContains(t, out, `name: "192.168.201.10"`)
	// LinkConfig.addresses must NOT contain the VIP CIDR.
	assertNotContains(t, out, "- address: 192.168.201.10/24")
}

// === Hetzner-style topology: public NIC + private VLAN child ===

// Contract: a controlplane floatingIP that lives in a private subnet
// hosted on a VLAN sub-interface — while the IPv4 default route goes
// out the parent (public) NIC — must be pinned to the VLAN child in
// Layer2VIPConfig, NOT to the default-route link. This is the
// Hetzner topology Ivan reported on 10 May 2026: a single physical
// NIC with the public-internet default gateway, and a VLAN child
// carrying the private cluster network where the VIP belongs.
//
// The pre-fix renderer hardcodes Layer2VIPConfig.link to the IPv4-
// default-route link (charts/cozystack/templates/_helpers.tpl:363,
// charts/generic/templates/_helpers.tpl mirror site), so the VIP
// lands on the public NIC. Talos installs the VIP there and the
// cluster never sees the leader on the private subnet.
//
// Three independent assertions, each pinning a distinct contract:
//
//  1. VLANConfig for the private VLAN child is emitted with the
//     correct parent, vlanID, and addresses. If discovery state has
//     classified the link correctly and configurable_link_names
//     enumerates it, the existing VLAN branch in the multi-doc
//     template should fire. Surfacing this assertion as part of the
//     same test means a regression in either the iteration filter
//     or the kind classification surfaces immediately, not via a
//     separate downstream symptom.
//
//  2. Layer2VIPConfig is emitted with link=enp0s31f6.4000 — the
//     fix. Helper talm.discovered.link_name_for_address picks the
//     link whose CIDR encompasses the floatingIP.
//
//  3. There is no Layer2VIPConfig with link=enp0s31f6 — guards
//     against a fix that emits both documents, leaving the
//     operator's apply with a duplicate VIP target.
//
// The test will fail on main today: the renderer either omits
// VLANConfig entirely or pins the VIP onto enp0s31f6 (per Ivan's
// report, both symptoms appear together). The diagnostic output the
// test prints on failure tells us which sub-cause to drill into.
func TestContract_NetworkMultidoc_HetznerTopology_VIPOnPrivateVLAN(t *testing.T) {
	out := renderCozystackWith(t, hetznerPublicNICWithPrivateVLANLookup(), map[string]any{
		"floatingIP":        "192.168.100.10",
		"advertisedSubnets": []any{"192.168.100.0/24"},
	})

	// Assertion 1: VLANConfig for the private VLAN child is emitted.
	assertContains(t, out, "kind: VLANConfig")
	assertContains(t, out, "name: enp0s31f6.4000")
	assertContains(t, out, "vlanID: 4000")
	assertContains(t, out, "parent: enp0s31f6")
	assertContains(t, out, "- address: 192.168.100.4/24")

	// Assertion 2: Layer2VIPConfig pins the VIP to the VLAN child.
	assertContains(t, out, "kind: Layer2VIPConfig")
	assertContains(t, out, `name: "192.168.100.10"`)
	assertContains(t, out, "link: enp0s31f6.4000")

	// Assertion 3: no Layer2VIPConfig with the wrong link, and exactly one document total.
	if strings.Contains(out, "link: enp0s31f6\n") {
		t.Errorf("Layer2VIPConfig points at the public default-route NIC; should be the VLAN child:\n%s", out)
	}
	if got := strings.Count(out, "kind: Layer2VIPConfig"); got != 1 {
		t.Errorf("expected exactly 1 Layer2VIPConfig document, got %d:\n%s", got, out)
	}

	// Assertion 4: the public NIC retains its address and default
	// route — the fix must not collaterally drop public uplink config
	// while moving the VIP.
	assertContains(t, out, "name: enp0s31f6\n")
	assertContains(t, out, "- address: 88.99.210.37/26")
	assertContains(t, out, "gateway: 88.99.210.1")
}

// Contract: when the floatingIP isn't on any discovered subnet (e.g.
// an upstream-routable VIP that arrives via the default-route link,
// or an operator typo), Layer2VIPConfig falls back to the
// IPv4-default-route link rather than silently skipping or failing.
// Pre-fix behaviour was always-fall-back; the fix prefers
// subnet-membership but preserves the fallback for topologies where
// the new helper has nothing to match on. simpleNicLookup carries a
// gateway on eth0 with addresses 192.168.201.10/24; we set a
// floatingIP outside that subnet and assert it lands on eth0.
func TestContract_NetworkMultidoc_FloatingIPNotInDiscoveredSubnetFallsBackToGateway(t *testing.T) {
	out := renderCozystackWith(t, simpleNicLookup(), map[string]any{
		"floatingIP":        "10.99.99.99",
		"advertisedSubnets": []any{testAdvertisedSubnet},
	})
	assertContains(t, out, "kind: Layer2VIPConfig")
	assertContains(t, out, `name: "10.99.99.99"`)
	assertContains(t, out, "link: eth0")
	if got := strings.Count(out, "kind: Layer2VIPConfig"); got != 1 {
		t.Errorf("expected exactly 1 Layer2VIPConfig (fallback path), got %d:\n%s", got, out)
	}
}
