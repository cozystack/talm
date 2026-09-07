# Endpoints and VIPs

`values.yaml` carries three related network knobs: `endpoint` (the Kubernetes control-plane URL), `floatingIP` (the shared VIP address, if any), and `vipLink` (the link that VIP is pinned to). This page covers how they combine and what the chart derives when you leave them alone.

## Endpoint and floatingIP combinations

- **cozystack VIP setup**: set `endpoint` and `floatingIP` together to the same IP — single shared VIP.
- **single-node cluster**: set `endpoint` to the node's routable IP and leave `floatingIP` blank.
- **multi-node with external load balancer**: set `endpoint` to the LB URL and leave `floatingIP` blank.

## Automatic vipLink selection

When `vipLink` is left empty the chart picks the link automatically using a two-step rule:

1. **Longest-prefix match across configurable links.** If `floatingIP` falls inside the CIDR of any address on a configurable link (physical NIC, bond, VLAN, bridge), the most specific subnet wins. This handles the Hetzner-style topology where a public NIC carries the default route and a VLAN child carries the private cluster subnet — the VIP lands on the VLAN child.
2. **Fallback to the IPv4-default-gateway-bearing link.** Used when no configurable link's CIDR contains the `floatingIP` — typical for upstream-routable VIPs that arrive via the default route.

Addresses on links the chart does not emit a per-link document for (Wireguard, kernel-managed loopback, slave NICs of a bond, anything outside the configurable set) are skipped — a VIP pinned there would have no surrounding network document.

## Overriding vipLink

Set `vipLink` explicitly when the target link does not yet exist on the live system at first apply (typically a VLAN sub-interface). The chart pins `Layer2VIPConfig.link` to it directly and emits the document even on a fresh node where discovery has not yet populated the addresses table. The chart does not auto-emit a `LinkConfig` or `VLANConfig` for the override link; the operator is responsible for ensuring the link comes up, typically by adding a `LinkConfig` or `VLANConfig` for that link to the per-node body overlay alongside `vipLink`.

The [per-node body overlay](node-files.md) is the node file's own Talos config, below its modeline.

## Subnet selectors

Subnet-selector fields (`kubelet.validSubnets`, `etcd.advertisedSubnets`) are derived automatically from the node's default-gateway-bearing link, so no override is needed unless you have a multi-homed node that requires a specific subnet pinned.
