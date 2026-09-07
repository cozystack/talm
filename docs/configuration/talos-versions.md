# Talos versions and output format

`templateOptions.talosVersion` in `Chart.yaml` (or `--talos-version` on the command line) selects which Talos config schema talm renders. It changes the shape of every node file.

## Which format each version gets

Selected via `Chart.yaml` (`templateOptions.talosVersion`) or `--talos-version`:

- **Talos < v1.12** — single YAML document with `machine.network` and `machine.registries` sections (the shape shown in [Node files](node-files.md)).
- **Talos >= v1.12** — multi-document format with separate typed documents instead of the deprecated monolithic fields.

## Documents emitted in multi-doc mode

For v1.12+ multi-doc output, one document is emitted per configurable link on the node, plus any link declared through `network.extraLinks` and a fixed pair on every render:

- `HostnameConfig` and `ResolverConfig` — always emitted.
- `LinkConfig` — physical NICs.
- `BondConfig` — bond masters. Bond slaves are filtered out so they do not collide with the master's document.
- `VLANConfig` — VLAN sub-interfaces.
- `BridgeConfig` — bridges, symmetric to `BondConfig` for bonds. Ports discovered via `spec.slaveKind == "bridge"` + `spec.masterIndex`; STP / VLAN-filtering settings reach the output when the bridge controller reports them on `spec.bridgeMaster`.
- `Layer2VIPConfig` — one per VIP. The `floatingIP` shorthand emits it on controlplane nodes; each `vips` entry emits one on any node, so a storage VIP works on a worker.
- `RegistryMirrorConfig` and `RegistryTLSConfig` — from the `registryMirrors` and `registryTLS` values, available on both charts.

### Per-link emission rules

- The link carrying the IPv4 default route gets the `routes.gateway` entry on its document; every other link is emitted gateway-less. Applies uniformly to `LinkConfig`, `BondConfig`, `VLANConfig`, `BridgeConfig`.
- Both IPv4 and IPv6 global-scope addresses on a link are surfaced.
- Every declared VIP ip (`floatingIP` plus each `vips[].ip`) is stripped from the addresses discovery reports, so a VIP currently held by a leader does not leak into the static document. Addresses are compared canonically, so a VIP spelled differently from what the node reports (`2001:0DB8::5` against `2001:db8::5/64`) is still recognised. An address the operator writes by hand under `network.extraLinks` is refused rather than stripped — the render does not silently drop what it was told to emit.

Multi-NIC nodes therefore produce one document per NIC, not one document total.

### Declaring links discovery cannot see

`network.extraLinks` declares links that discovery cannot see — a bond, VLAN or address the node does not carry yet — so documents are emitted for links that are not (or not yet) on the node. A link named in `bond.interfaces` becomes a slave and stops getting a document of its own, the same filter discovery applies once the bond exists. Moving an already-addressed NIC into a bond works, provided the entry restates everything the slave was carrying — its `addresses`, a destination-less `routes` entry when it held the default route, and a matching `routes` entry per static route. Anything left behind fails the render rather than silently costing the node its connectivity or a reachable subnet.

### Preserving links talm does not manage

`network.preserveExisting` goes the other way: the running `machine.network.interfaces` block is carried over verbatim and the typed per-link rebuild is skipped entirely, so no per-link document is emitted at all. VIP and registry documents are unaffected by either.

## Version compatibility

!!! danger "The configured version must match the Talos actually running on the node"

    This setting must match the **Talos version actually running on the target node** — i.e. the maintenance ISO/PXE the node booted from for `apply -i`, or the installed Talos for an authenticated apply. It is **not** the same as `install.image`, which only controls what gets written to disk after a successful apply. When the configured contract is newer than the running binary, machinery injects fields (e.g. `machine.install.grubUseUKICmdline` from v1.12) that the running parser does not know, and the apply fails on the node side with `failed to parse config: unknown keys found during decoding: ...`. `talm apply` runs a best-effort pre-flight check against the running version and prints a `warning: pre-flight: ...` line with a hint when it detects this mismatch; if the warning is missed, the same hint is appended to the apply error. Either reboot the node into a maintenance image that matches the configured contract, or lower `templateOptions.talosVersion` / `--talos-version` to match what is running.
