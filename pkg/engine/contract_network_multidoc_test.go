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

// Contract: a bridge link discovered on a node is rendered as a
// typed BridgeConfig document, symmetric to BondConfig for bonds.
// The non-gateway case lands BridgeConfig with addresses and mtu
// but no routes entry (no default gateway to emit). The gateway
// case adds the routes.gateway entry — pinned by
// TestMultiDocEmitsBridgeConfigWhenBridgeCarriesDefaultRoute.
//
// Prior to BridgeConfig support landing, a non-gateway bridge was
// silently skipped (no document emitted) on the premise that the
// feature was unimplemented; this contract pins the current
// "always emit" shape.
func TestContract_NetworkMultidoc_BridgeConfigEmitted(t *testing.T) {
	out := renderCozystackWith(t, bridgeLookup(), map[string]any{
		"advertisedSubnets": []any{testAdvertisedSubnet},
	})
	assertContains(t, out, "kind: BridgeConfig")
	assertContains(t, out, "name: br0")
}

// Contract: a controlplane floatingIP that lives inside the subnet
// configured on a bridge link now legitimately lands on the bridge —
// the bridge is fully rendered as a typed BridgeConfig document, so
// pinning the VIP there no longer leaves it dangling without a
// surrounding network document. Symmetric to the VLAN-child case
// pinned in HetznerTopology_VIPOnPrivateVLAN.
//
// Fixture: bridgeWithClusterSubnetLookup has br0 carrying
// 10.5.0.10/24 (global scope) and the IPv4 default route. floatingIP
// 10.5.0.99 is inside that subnet, so link_name_for_address resolves
// to br0; the discovery-derived Layer2VIPConfig pins link=br0.
// Without BridgeConfig emission (the prior shape), this would have
// been a "VIP on undocumented link" symptom; now BridgeConfig
// documents the link explicitly and the chart also emits STP
// settings carried in spec.bridgeMaster.
func TestContract_NetworkMultidoc_VIPOnBridge(t *testing.T) {
	out := renderCozystackWith(t, bridgeWithClusterSubnetLookup(), map[string]any{
		"floatingIP":        "10.5.0.99",
		"advertisedSubnets": []any{"10.5.0.0/24"},
	})

	// BridgeConfig must be emitted alongside Layer2VIPConfig — that is
	// the whole reason landing the VIP on a bridge is now safe.
	assertContains(t, out, "kind: BridgeConfig")
	assertContains(t, out, "name: br0")
	assertContains(t, out, "- address: 10.5.0.10/24")
	assertContains(t, out, "gateway: 10.5.0.1")
	// STP setting from spec.bridgeMaster.stp.enabled must surface.
	assertContains(t, out, "stp:")
	assertContains(t, out, "enabled: true")
	// VLAN filtering from spec.bridgeMaster.vlan.filteringEnabled
	// must surface as the shorter yaml key (filtering) the
	// BridgeConfig output schema uses.
	assertContains(t, out, "vlan:")
	assertContains(t, out, "filtering: true")
	// Bridge port discovered via spec.slaveKind=="bridge" must be
	// listed under the BridgeConfig.links.
	assertContains(t, out, "links:")
	assertContains(t, out, "  - eth0")
	assertContains(t, out, "kind: Layer2VIPConfig")
	assertContains(t, out, `name: "10.5.0.99"`)
	assertContains(t, out, "link: br0")
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
// Layer2VIPConfig, NOT to the default-route link. The relevant
// real-world topology is a deployment where a single physical NIC
// carries the public-internet default gateway and a VLAN child
// carries the private cluster network where the VIP belongs.
//
// Why this needs a dedicated pin: the discovery-derived
// Layer2VIPConfig.link selection in the multi-doc chart must not
// short-circuit to the IPv4-default-route link. Doing so puts the
// VIP on the public NIC; Talos installs the VIP there and the
// cluster never sees the leader on the private subnet.
//
// The test pins three independent contracts:
//
//  1. VLANConfig for the private VLAN child is emitted with the
//     correct parent, vlanID, and addresses. If discovery state has
//     classified the link correctly and configurable_link_names
//     enumerates it, the existing VLAN branch in the multi-doc
//     template fires. Surfacing this assertion as part of the
//     same test means a regression in either the iteration filter
//     or the kind classification surfaces immediately, not via a
//     separate downstream symptom.
//
//  2. Layer2VIPConfig is emitted with link=enp0s31f6.4000.
//     Helper talm.discovered.link_name_for_address picks the link
//     whose CIDR encompasses the floatingIP.
//
//  3. There is no Layer2VIPConfig with link=enp0s31f6 — guards
//     against an alternate implementation that emits both
//     documents, leaving the operator's apply with a duplicate VIP
//     target.
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

// Contract: the IPv6 counterpart of the Hetzner-topology test.
// Same shape — public NIC carries the IPv4 default route, VLAN
// child carries the private cluster network — but the private
// subnet and the floatingIP are IPv6 (ULA /64 and a host inside
// it). net/netip's Prefix.Contains is family-agnostic, so the
// VIP-link helper resolves the same way as for IPv4. Without this
// pin, a future swap of cidrContains for a per-family
// implementation could silently regress only the IPv6 path.
//
// Real-world relevance: dual-stack Hetzner / colo deployments where
// IPv4 reaches the upstream gateway and the cluster speaks IPv6
// internally over a private VLAN. The cluster VIP must land on the
// VLAN's IPv6 ULA, not on the public NIC.
func TestContract_NetworkMultidoc_HetznerTopology_IPv6VIPOnPrivateVLAN(t *testing.T) {
	out := renderCozystackWith(t, hetznerPublicNICWithPrivateIPv6VLANLookup(), map[string]any{
		"floatingIP":        "2001:db8:cafe::10",
		"advertisedSubnets": []any{"2001:db8:cafe::/64"},
	})

	// VLANConfig for the private VLAN child must still be emitted
	// with the IPv6 ULA as a global-scope address.
	assertContains(t, out, "kind: VLANConfig")
	assertContains(t, out, "name: enp0s31f6.4000")
	assertContains(t, out, "vlanID: 4000")
	assertContains(t, out, "parent: enp0s31f6")
	assertContains(t, out, "- address: 2001:db8:cafe::4/64")

	// Layer2VIPConfig pinned to the VLAN child via subnet membership.
	assertContains(t, out, "kind: Layer2VIPConfig")
	assertContains(t, out, `name: "2001:db8:cafe::10"`)
	assertContains(t, out, "link: enp0s31f6.4000")

	// Must not pin the IPv6 VIP to the public NIC; exactly one
	// Layer2VIPConfig document.
	if strings.Contains(out, "link: enp0s31f6\n") {
		t.Errorf("Layer2VIPConfig points at the public IPv4-default-route NIC; should be the VLAN child:\n%s", out)
	}
	if got := strings.Count(out, "kind: Layer2VIPConfig"); got != 1 {
		t.Errorf("expected exactly 1 Layer2VIPConfig document, got %d:\n%s", got, out)
	}

	// Public NIC keeps its IPv4 address and default route — the IPv6
	// fix must not collaterally drop the IPv4 uplink.
	assertContains(t, out, "name: enp0s31f6\n")
	assertContains(t, out, "- address: 88.99.210.37/26")
	assertContains(t, out, "gateway: 88.99.210.1")
}

// Contract: a non-configurable link (Wireguard, kernel-managed
// loopback, or other interface the chart does not emit a per-link
// document for) cannot win VIP-link selection even if its
// discovered address CIDR contains the floatingIP.
// configurable_link_names is the gate — addresses on links outside
// that set are skipped before CIDR membership is even checked. The
// chart does not emit LinkConfig for non-configurable links, so a
// VIP pinned to one would have no surrounding network document and
// would race the link's own address management on apply.
//
// Fixture: Wireguard subnet 10.244.0.0/16 on wg0 (kind=ether, no
// busPath, not in configurable_link_names). floatingIP 10.244.0.5
// is INSIDE wg0's subnet. The helper must skip wg0 and fall back
// to $defaultLinkName (the IPv4-default-route physical NIC).
func TestContract_NetworkMultidoc_VIPSkipsNonConfigurableLink(t *testing.T) {
	out := renderCozystackWith(t, hetznerWithWireguardLookup(), map[string]any{
		"floatingIP":        "10.244.0.5",
		"advertisedSubnets": []any{"192.168.100.0/24"},
	})

	assertContains(t, out, "kind: Layer2VIPConfig")
	assertContains(t, out, `name: "10.244.0.5"`)
	// Falls back to the IPv4-default-route NIC because wg0 is not
	// in configurable_link_names — even though its /16 contains the
	// VIP.
	assertContains(t, out, "link: enp0s31f6\n")
	if strings.Contains(out, "link: wg0") {
		t.Errorf("Layer2VIPConfig stole link=wg0 (non-configurable Wireguard); must skip non-configurable links:\n%s", out)
	}
	if got := strings.Count(out, "kind: Layer2VIPConfig"); got != 1 {
		t.Errorf("expected exactly 1 Layer2VIPConfig, got %d:\n%s", got, out)
	}
}

// Contract: when two configurable links both carry addresses whose
// CIDR contains the floatingIP, the link with the more specific
// (longer) prefix wins. Mirrors the kernel's longest-prefix rule for
// route decisions. Without this, iteration order silently picks the
// "winning" link.
//
// Fixture: enp0s31f6 has 192.168.0.10/16 (broad) listed first,
// enp0s31f6.4000 has 192.168.100.4/24 (narrow) listed second. Both
// CIDRs contain floatingIP 192.168.100.10. The /24 must win.
func TestContract_NetworkMultidoc_VIPLinkLongestPrefixMatch(t *testing.T) {
	out := renderCozystackWith(t, overlappingSubnetsLookup(), map[string]any{
		"floatingIP":        "192.168.100.10",
		"advertisedSubnets": []any{"192.168.100.0/24"},
	})

	assertContains(t, out, "kind: Layer2VIPConfig")
	assertContains(t, out, `name: "192.168.100.10"`)
	assertContains(t, out, "link: enp0s31f6.4000")
	if strings.Contains(out, "link: enp0s31f6\n") {
		t.Errorf("Layer2VIPConfig picked the broader /16 instead of the more specific /24 — longest-prefix-match regressed:\n%s", out)
	}
	if got := strings.Count(out, "kind: Layer2VIPConfig"); got != 1 {
		t.Errorf("expected exactly 1 Layer2VIPConfig, got %d:\n%s", got, out)
	}
}

// Contract: BridgeConfig emits the vlan sub-block even when
// spec.bridgeMaster carries no stp setting. Pins the independence
// of the two BridgeConfig sub-blocks (stp / vlan) against a future
// refactor that accidentally nests one inside the other or
// conditions one on the other.
func TestContract_NetworkMultidoc_BridgeConfig_VLANOnlyNoStp(t *testing.T) {
	out := renderCozystackWith(t, bridgeWithVLANOnlyLookup(), map[string]any{
		"advertisedSubnets": []any{"10.5.0.0/24"},
	})
	assertContains(t, out, "kind: BridgeConfig")
	assertContains(t, out, "vlan:")
	assertContains(t, out, "filtering: true")
	if strings.Contains(out, "stp:") {
		t.Errorf("BridgeConfig emits stp: block when spec.bridgeMaster.stp is unset; sub-blocks must be independent:\n%s", out)
	}
}

// Contract: BridgeConfig emits the stp sub-block even when
// spec.bridgeMaster carries no vlan setting. Mirror of the
// VLAN-only contract above.
func TestContract_NetworkMultidoc_BridgeConfig_StpOnlyNoVlan(t *testing.T) {
	out := renderCozystackWith(t, bridgeWithSTPOnlyLookup(), map[string]any{
		"advertisedSubnets": []any{"10.5.0.0/24"},
	})
	assertContains(t, out, "kind: BridgeConfig")
	assertContains(t, out, "stp:")
	assertContains(t, out, "enabled: true")
	if strings.Contains(out, "vlan:") {
		t.Errorf("BridgeConfig emits vlan: block when spec.bridgeMaster.vlan is unset; sub-blocks must be independent:\n%s", out)
	}
}

// Contract: malformed entries in COSI's addresses table do not
// propagate into the rendered LinkConfig / VLANConfig / BridgeConfig
// `addresses` blocks. The chart's addresses_by_link helper filters
// out entries whose `.spec.address` fails to parse as a CIDR
// (cidrPrefixLen returns -1), so a corrupt or future-format entry
// stays inside discovery and never reaches a typed document Talos
// would reject on apply.
//
// Fixture: malformedAddressEntryLookup carries
// "definitely-not-a-cidr" on enp0s31f6.4000 sandwiched between two
// well-formed entries. The test asserts the bad value is absent
// from any `- address:` line and the valid sibling
// 192.168.100.4/24 IS present.
func TestContract_NetworkMultidoc_LinkAddressesFilterMalformedCidr(t *testing.T) {
	out := renderCozystackWith(t, malformedAddressEntryLookup(), map[string]any{
		"advertisedSubnets": []any{"192.168.100.0/24"},
	})
	if strings.Contains(out, "definitely-not-a-cidr") {
		t.Errorf("malformed CIDR leaked into LinkConfig/VLANConfig.addresses; corrupt COSI entries must be filtered at addresses_by_link:\n%s", out)
	}
	if !strings.Contains(out, "- address: 192.168.100.4/24") {
		t.Errorf("valid sibling CIDR 192.168.100.4/24 missing from VLANConfig.addresses; filter must not drop well-formed entries:\n%s", out)
	}
}

// Contract: a malformed address entry in COSI's addresses table does
// not crash the chart render. cidrContains is lenient on parse
// failures (returns false), so the helper skips the bad entry and
// continues iterating; the rest of the addresses table is processed
// normally and the VIP-link still resolves correctly.
//
// Fixture: a "definitely-not-a-cidr" entry sandwiched between two
// valid ones. The render must produce the same Layer2VIPConfig as
// the well-formed Hetzner topology.
func TestContract_NetworkMultidoc_VIPLinkSurvivesMalformedAddressEntry(t *testing.T) {
	out := renderCozystackWith(t, malformedAddressEntryLookup(), map[string]any{
		"floatingIP":        "192.168.100.10",
		"advertisedSubnets": []any{"192.168.100.0/24"},
	})

	assertContains(t, out, "kind: Layer2VIPConfig")
	assertContains(t, out, `name: "192.168.100.10"`)
	assertContains(t, out, "link: enp0s31f6.4000")
}

// Contract: a link-scoped address (scope=link, RFC 3927-style
// 169.254/16 link-local) on a configurable link must NOT win VIP-
// link selection even when its CIDR is the most specific match.
// Filter 2 in link_name_for_address skips host/link/nowhere-scoped
// addresses, mirroring the filter addresses_by_link applies before
// emitting LinkConfig.addresses. Without this filter, link-local
// noise on a configurable VLAN could trump the operator's intent.
//
// Fixture: VLAN child carries 192.168.100.4/24 (global) AND
// 169.254.0.1/16 (link-local). floatingIP 169.254.0.5 is INSIDE
// the link-local /16. The helper must skip that entry; the global
// /24 doesn't match the VIP, so the helper returns "" and the
// caller falls back to the default-route link (enp0s31f6).
func TestContract_NetworkMultidoc_VIPSkipsLinkScopedAddress(t *testing.T) {
	out := renderCozystackWith(t, hetznerWithLinkScopedAddressLookup(), map[string]any{
		"floatingIP":        "169.254.0.5",
		"advertisedSubnets": []any{"192.168.100.0/24"},
	})

	assertContains(t, out, "kind: Layer2VIPConfig")
	assertContains(t, out, `name: "169.254.0.5"`)
	// Falls back to the default-route NIC because the only link
	// whose subnet contains the VIP is link-scoped, which is
	// filtered out before the longest-prefix comparison runs.
	assertContains(t, out, "link: enp0s31f6\n")
	if got := strings.Count(out, "kind: Layer2VIPConfig"); got != 1 {
		t.Errorf("expected exactly 1 Layer2VIPConfig, got %d:\n%s", got, out)
	}
}

// Contract: a malformed floatingIP fails the chart render at
// template time with a clear hint that names the bad value. The
// previous shape silently fell through cidrContains (lenient on
// parse failure) into the default-link fallback, shipping a
// Layer2VIPConfig with a nonsense `name:` value that surfaced only
// when Talos rejected it on apply. Render-time fail is much cheaper
// to debug.
//
// Fixture: simple Hetzner topology with floatingIP "10.0.0.300"
// (octet > 255). Render must fail; the error message must include
// the bad literal so the operator can correlate.
func TestContract_NetworkMultidoc_VIPFailsOnInvalidFloatingIP(t *testing.T) {
	err := renderCozystackExpectError(t, hetznerPublicNICWithPrivateVLANLookup(), map[string]any{
		"floatingIP":        "10.0.0.300",
		"advertisedSubnets": []any{testAdvertisedSubnet},
	})

	if err == nil {
		t.Fatal("expected render to fail on malformed floatingIP, got nil error")
	}
	if !strings.Contains(err.Error(), "10.0.0.300") {
		t.Errorf("error must echo the bad floatingIP literal so the operator can correlate; got: %v", err)
	}
	if !strings.Contains(err.Error(), "floatingIP") {
		t.Errorf("error must mention the offending field name; got: %v", err)
	}
}

// Contract: when the default-route-link fallback resolves to a
// non-configurable link (Wireguard, slave NIC, anything outside the
// {physical, bond, vlan, bridge} set), the chart MUST NOT emit
// Layer2VIPConfig — the chart does not emit a per-link document
// for such links, so the VIP would dangle on a link the chart
// never configures. The fallback path mirrors the configurable-
// link gate that link_name_for_address applies inside its own
// iteration; matched-link selection and fallback-link selection
// have to honour the same renderable-link set.
//
// Fixture: IPv4 default route on wg0 (Wireguard, not configurable).
// floatingIP 10.99.99.99 falls outside every discovered subnet, so
// link_name_for_address returns empty. The fallback would have
// picked wg0 before the guard landed; with the guard it skips and
// no Layer2VIPConfig is emitted at all.
func TestContract_NetworkMultidoc_VIPSkipsNonConfigurableDefaultRouteLink(t *testing.T) {
	out := renderCozystackWith(t, defaultRouteOnNonConfigurableLinkLookup(), map[string]any{
		"floatingIP":        "10.99.99.99",
		"advertisedSubnets": []any{testAdvertisedSubnet},
	})
	if strings.Contains(out, "kind: Layer2VIPConfig") {
		t.Errorf("Layer2VIPConfig must not emit when the only resolvable link is non-configurable; got:\n%s", out)
	}
	if strings.Contains(out, "link: wg0") {
		t.Errorf("VIP pinned to non-configurable wg0 — fallback must honour the configurable-link gate; got:\n%s", out)
	}
}

// Generic-chart mirror of TestContract_NetworkMultidoc_VIPSkipsNonConfigurableDefaultRouteLink.
func TestContract_NetworkMultidoc_Generic_VIPSkipsNonConfigurableDefaultRouteLink(t *testing.T) {
	out := renderGenericWith(t, defaultRouteOnNonConfigurableLinkLookup(), map[string]any{
		"floatingIP":        "10.99.99.99",
		"advertisedSubnets": []any{testAdvertisedSubnet},
	})
	if strings.Contains(out, "kind: Layer2VIPConfig") {
		t.Errorf("generic chart: Layer2VIPConfig must not emit when fallback target is non-configurable; got:\n%s", out)
	}
	if strings.Contains(out, "link: wg0") {
		t.Errorf("generic chart: VIP pinned to non-configurable wg0:\n%s", out)
	}
}

// Contract: discovery has populated the addresses table with a
// configurable link whose subnet contains the floatingIP, but the
// routes table has no IPv4 default route yet (fresh-boot before
// the gateway is reachable, or policy-routing-only topology).
// Layer2VIPConfig must still emit pinned to the subnet-matching
// link — a successful link_name_for_address resolution does not
// depend on $defaultLinkName being non-empty.
//
// Pinning this case catches a future refactor that reintroduces an
// outer $defaultLinkName guard around the whole VIP-emission block
// and silently regresses the fresh-boot / policy-routing path.
func TestContract_NetworkMultidoc_VIPEmitsWithMatchingSubnetEvenWithoutDefaultRoute(t *testing.T) {
	out := renderCozystackWith(t, noDefaultRouteWithSubnetMatchLookup(), map[string]any{
		"floatingIP":        "192.168.100.10",
		"advertisedSubnets": []any{"192.168.100.0/24"},
	})

	assertContains(t, out, "kind: Layer2VIPConfig")
	assertContains(t, out, `name: "192.168.100.10"`)
	assertContains(t, out, "link: enp0s31f6.4000")
	if got := strings.Count(out, "kind: Layer2VIPConfig"); got != 1 {
		t.Errorf("expected exactly 1 Layer2VIPConfig, got %d:\n%s", got, out)
	}
}

// Contract: a nil / unset floatingIP on a controlplane node
// renders without error and emits no Layer2VIPConfig. The
// validation block must gate on the RAW .Values.floatingIP
// truthiness before any toString coercion — Sprig's
// `nil | toString` returns the literal string "<nil>", which is
// truthy and not a valid IP, so a naive predicate on the
// toString'd value would fail-fast on every controlplane render
// where the operator left floatingIP unset (single-node
// clusters, LB-fronted multi-node, anything Helm coalesces out
// of the values table).
func TestContract_NetworkMultidoc_VIPGracefulWhenFloatingIPNil(t *testing.T) {
	out := renderCozystackWith(t, hetznerPublicNICWithPrivateVLANLookup(), map[string]any{
		"floatingIP":        nil,
		"advertisedSubnets": []any{testAdvertisedSubnet},
	})
	if strings.Contains(out, "kind: Layer2VIPConfig") {
		t.Errorf("Layer2VIPConfig must not emit when floatingIP is nil; got:\n%s", out)
	}
	if strings.Contains(out, "<nil>") {
		t.Errorf("rendered output leaks the Sprig <nil> literal — fail-fast misfired on nil floatingIP:\n%s", out)
	}
}

// Generic-chart mirror of the nil-safe contract above.
func TestContract_NetworkMultidoc_Generic_VIPGracefulWhenFloatingIPNil(t *testing.T) {
	out := renderGenericWith(t, hetznerPublicNICWithPrivateVLANLookup(), map[string]any{
		"floatingIP":        nil,
		"advertisedSubnets": []any{testAdvertisedSubnet},
	})
	if strings.Contains(out, "kind: Layer2VIPConfig") {
		t.Errorf("generic chart: Layer2VIPConfig must not emit when floatingIP is nil; got:\n%s", out)
	}
	if strings.Contains(out, "<nil>") {
		t.Errorf("generic chart: rendered output leaks the Sprig <nil> literal:\n%s", out)
	}
}

// Contract: a numeric (non-string) floatingIP — typed without
// quotes in values.yaml so YAML parses it as int — must produce
// the friendly fail-fast error, NOT a Go-template
// "wrong type for value; expected string; got int" panic.
//
// The ipIsValid template function is registered with a string
// parameter; passing an int through Go text/template raises a
// type-mismatch panic that surfaces as a stack trace at the line
// of the `if` predicate, defeating the whole point of the
// validation block. The chart guards against this by coercing
// .Values.floatingIP through `toString` before the predicate.
//
// Pinning this here ensures a future refactor that drops the
// toString coercion does not silently regress into the panic —
// which is exactly the kind of latent failure mode the CLAUDE.md
// "Helm/Go template numeric scalar" rule flags as a recurring
// trap.
func TestContract_NetworkMultidoc_VIPFailsOnNumericFloatingIP(t *testing.T) {
	err := renderCozystackExpectError(t, hetznerPublicNICWithPrivateVLANLookup(), map[string]any{
		"floatingIP":        192168, // int, not string
		"advertisedSubnets": []any{testAdvertisedSubnet},
	})

	if err == nil {
		t.Fatal("expected render to fail on numeric floatingIP, got nil error")
	}
	if strings.Contains(err.Error(), "wrong type for value") {
		t.Errorf("got Go-template type-mismatch panic instead of friendly fail; the toString coercion must run before the predicate: %v", err)
	}
	if !strings.Contains(err.Error(), "floatingIP") {
		t.Errorf("error must mention the offending field name; got: %v", err)
	}
	if !strings.Contains(err.Error(), "192168") {
		t.Errorf("error must echo the bad value (stringified); got: %v", err)
	}
}

// Contract: the malformed-floatingIP fail-fast block runs BEFORE
// either VIP-emission branch, so even an operator who set vipLink
// (which would normally suppress the discovery branch entirely)
// still gets a clear render-time error rather than a Layer2VIPConfig
// document with a nonsense `name:` value. Pin the validation order
// here — the override block is at the top of the multi-doc network
// section, so a future refactor that moves the validation BELOW the
// override would silently regress this case.
func TestContract_NetworkMultidoc_VIPFailsOnInvalidFloatingIPEvenWithVipLinkOverride(t *testing.T) {
	err := renderCozystackExpectError(t, hetznerPublicNICWithPrivateVLANLookup(), map[string]any{
		"floatingIP":        "192.168.300.10",
		"vipLink":           "enp0s31f6.4000",
		"advertisedSubnets": []any{testAdvertisedSubnet},
	})

	if err == nil {
		t.Fatal("expected render to fail on malformed floatingIP, got nil error (vipLink override must NOT bypass validation)")
	}
	if !strings.Contains(err.Error(), "192.168.300.10") {
		t.Errorf("error must echo the bad floatingIP literal so the operator can correlate; got: %v", err)
	}
	if !strings.Contains(err.Error(), "floatingIP") {
		t.Errorf("error must mention the offending field name; got: %v", err)
	}
}

// === Generic-chart mirror tests ===
//
// The generic chart carries a verbatim copy of the multi-doc
// Layer2VIPConfig selection block from the cozystack chart, but
// every cozystack contract test routes through renderCozystackWith.
// These mirrors exist so a regression in only the generic chart's
// helper hunk surfaces immediately. If the generic chart ever
// drifts from cozystack on this contract, the symmetric pair fails
// and the gap is obvious.

// Generic-chart mirror of TestContract_NetworkMultidoc_HetznerTopology_VIPOnPrivateVLAN.
func TestContract_NetworkMultidoc_Generic_HetznerTopology_VIPOnPrivateVLAN(t *testing.T) {
	out := renderGenericWith(t, hetznerPublicNICWithPrivateVLANLookup(), map[string]any{
		"floatingIP":        "192.168.100.10",
		"advertisedSubnets": []any{"192.168.100.0/24"},
	})
	assertContains(t, out, "kind: VLANConfig")
	assertContains(t, out, "kind: Layer2VIPConfig")
	assertContains(t, out, `name: "192.168.100.10"`)
	assertContains(t, out, "link: enp0s31f6.4000")
	if strings.Contains(out, "link: enp0s31f6\n") {
		t.Errorf("generic chart: Layer2VIPConfig points at the public NIC instead of the VLAN child:\n%s", out)
	}
}

// Generic-chart mirror of TestContract_NetworkMultidoc_HetznerTopology_IPv6VIPOnPrivateVLAN.
func TestContract_NetworkMultidoc_Generic_HetznerTopology_IPv6VIPOnPrivateVLAN(t *testing.T) {
	out := renderGenericWith(t, hetznerPublicNICWithPrivateIPv6VLANLookup(), map[string]any{
		"floatingIP":        "2001:db8:cafe::10",
		"advertisedSubnets": []any{"2001:db8:cafe::/64"},
	})
	assertContains(t, out, "kind: Layer2VIPConfig")
	assertContains(t, out, `name: "2001:db8:cafe::10"`)
	assertContains(t, out, "link: enp0s31f6.4000")
}

// Generic-chart mirror of TestContract_NetworkMultidoc_VIPLinkLongestPrefixMatch.
func TestContract_NetworkMultidoc_Generic_VIPLinkLongestPrefixMatch(t *testing.T) {
	out := renderGenericWith(t, overlappingSubnetsLookup(), map[string]any{
		"floatingIP":        "192.168.100.10",
		"advertisedSubnets": []any{"192.168.100.0/24"},
	})
	assertContains(t, out, "link: enp0s31f6.4000")
	if strings.Contains(out, "link: enp0s31f6\n") {
		t.Errorf("generic chart: longest-prefix match regressed:\n%s", out)
	}
}

// Generic-chart mirror of TestContract_NetworkMultidoc_FloatingIPNotInDiscoveredSubnetFallsBackToGateway.
func TestContract_NetworkMultidoc_Generic_FloatingIPNotInDiscoveredSubnetFallsBackToGateway(t *testing.T) {
	out := renderGenericWith(t, simpleNicLookup(), map[string]any{
		"floatingIP":        "10.99.99.99",
		"advertisedSubnets": []any{testAdvertisedSubnet},
	})
	assertContains(t, out, "kind: Layer2VIPConfig")
	assertContains(t, out, "link: eth0")
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
