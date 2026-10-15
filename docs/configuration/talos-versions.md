# Talos versions and output format

`templateOptions.talosVersion` in `Chart.yaml` (or `--talos-version` on the command line) selects which Talos config schema talm renders. It changes the shape of every node file.

## What an unset version means

Leaving `templateOptions.talosVersion` empty means the version of Talos the `talm` binary was built against. It is not frozen at a release: upgrade talm, and an unpinned project follows it to whatever Talos that build carries.

That is why the shipped presets pin it. The charts choose the config shape from the pinned version: from v1.14 Talos keeps the Kubernetes settings in documents of their own and rejects a config that also carries the v1alpha1 fields, and the charts only know to emit those documents when the version says so. An unpinned project renders the v1alpha1 shape against whatever contract the binary carries, and on v1.14 the node would refuse it with `kubelet config is already set in v1alpha1 config (.machine.kubelet)` and siblings. Network settings made the same move earlier, in v1.12.

Rather than emit that config, the render stops. With both keys empty the Kubernetes version is reported first, since the bundle cannot be serialized without one; pin it and the contract conflict is reported next:

```text
rendered config mixes v1alpha1 fields with the documents that superseded them: ...
hint: templateOptions.talosVersion is unset, so the render targets the Talos version talm was built from.
      Pin it to v1.13 or lower in Chart.yaml; above that contract Talos keeps these settings in documents of their own,
      which the charts do not emit.
```

A chart can also fail earlier than either guard: the cozystack preset deletes a default node label with `$patch: delete`, and on a contract where `machine.nodeLabels` has moved into `KubeNodeConfig` that path no longer exists, so patching stops with `failed to delete path ...: lookup failed` before any version check runs. The preset happens to pin `v1.12`, which predates that move and sidesteps it.

Pin `templateOptions.talosVersion` in `Chart.yaml`, and keep it no newer than the Talos running on the node (see [Version compatibility](#version-compatibility)). Projects created from the shipped presets already carry a pin.

## The Kubernetes version

`templateOptions.kubernetesVersion` picks the Kubernetes release the generated config targets: the kubelet image and, on contracts that keep them in v1alpha1, the control-plane component images.

Leaving it empty means the node keeps deciding, on contracts where it still can: the render emits no image fields at all, and each component uses the default of the Talos release it runs. The render says so on stderr, because that is a real choice and not a no-op — a node on a newer Talos release moves kubelet and the control plane with it, and the node file shows nothing either way. This is new behaviour, not a restored default: earlier talm substituted its own built-in Kubernetes version whenever the key was empty, so an unpinned project silently followed the binary. It no longer does.

From v1.14 the contract takes that choice away. The Kubernetes settings live in documents of their own and those documents require an image, so an unset version is reported rather than rendered — pin the key.

Pin it in `Chart.yaml` when you want to choose the version yourself, which is what both shipped presets do. A pinned version reaches every component, kubelet included, so they never split across releases.

The presets do not agree on a value. Generic pins `v1.37.0`, the default of the Talos v1.14 line it targets, so a fresh project lands on what that line ships. Cozystack pins `v1.36.5`. Either way the pin is a starting point: set the key to the version your own cluster runs rather than inheriting the preset's.

A project created before those pins existed carries empty values for this key and for `talosVersion`. On the first render after upgrading talm it stops with an error naming the key to set; add both pins to its `Chart.yaml` and it renders again.

## Which format each version gets

Selected via `Chart.yaml` (`templateOptions.talosVersion`) or `--talos-version`:

- **Talos < v1.12** — the machine config is a single v1alpha1 document with `machine.network` and `machine.registries` sections (the shape shown in [Node files](node-files.md)). Standalone documents that older Talos already accepts, such as the cozystack preset's `ExtensionServiceConfig`, follow it.
- **Talos >= v1.12** — multi-document format with separate typed documents instead of the deprecated monolithic network and registry fields.
- **Talos >= v1.14** — a second round of the same move, this time for the Kubernetes settings: `machine.kubelet`, `machine.nodeLabels` and the control-plane components each get a document of their own, and a config carrying both shapes is rejected. The charts emit those documents, described below.

## Kubernetes documents from v1.14

On a v1.14 or newer contract the charts write the Kubernetes settings as typed documents, and only `cluster.etcd` stays in v1alpha1, since it has no typed counterpart:

| Setting | Document |
| --- | --- |
| `clusterName`, `endpoint` | `KubeClusterConfig` |
| `advertisedSubnets` for the kubelet node IP | `KubeNodeConfig` (`nodeIP.validSubnets`) |
| `extraKubeletExtraArgs` | `KubeletConfig` (`config`) |
| `podSubnets`, `serviceSubnets`, `clusterDomain` (cozystack) | `KubeNetworkConfig` |
| `extraApiServerArgs`, `certSANs` | `KubeAPIServerConfig` (`extraArgs`, `certExtraSANs`) |
| `extraControllerManagerArgs` | `KubeControllerManagerConfig` |
| `extraSchedulerArgs` | `KubeSchedulerConfig` |

These documents carry only what the chart sets. The config Talos generates already contains every one of them, with the component images, the control-plane node label and taint, and the other defaults, and talm merges the chart's documents into those rather than replacing them, the same way a `talosctl` patch would. A delete directive works the same way: the cozystack preset removes the flannel and discovery-service documents, the control-plane taint and the external-load-balancer exclusion label, and sets `KubeProxyConfig` to `enabled: false`, which is what it turned off on earlier contracts. Every other document kind the chart emits (`HostnameConfig`, `LinkConfig` and so on) still replaces the generated one of the same name. A default render keeps the chart's documents as written in the node file; apply merges them again.

kube-apiserver no longer takes authentication or authorization flags on this contract. The cozystack preset's `oidcIssuerUrl` becomes a JWT authenticator in `KubeAuthenticationConfig` with the same audience (`kubernetes`), claims (`preferred_username`, `groups`) and username prefix (`<issuer>#`) the flags defaulted to, so existing RBAC bindings keep matching. An `extraApiServerArgs` key starting with `oidc-`, `anonymous-auth`, `authentication-config`, `authorization-config`, `authorization-mode` or `authorization-webhook-` fails the render; put the equivalent `KubeAuthenticationConfig` or `KubeAuthorizerConfig` document in a per-node patch instead.

## Documents emitted in multi-doc mode

For v1.12+ multi-doc output, one document is emitted per configurable link on the node, plus any link declared through `network.extraLinks` and a fixed pair on every render:

- `HostnameConfig` and `ResolverConfig` — always emitted.
- `LinkConfig` — physical NICs.
- `BondConfig` — bond masters. Bond slaves are filtered out so they do not collide with the master's document.
- `VLANConfig` — VLAN sub-interfaces.
- `BridgeConfig` — bridges, symmetric to `BondConfig` for bonds. Ports discovered via `spec.slaveKind == "bridge"` + `spec.masterIndex`; STP / VLAN-filtering settings reach the output when the bridge controller reports them on `spec.bridgeMaster`.
- `Layer2VIPConfig` — one per VIP. The `floatingIP` shorthand emits it on controlplane nodes; each `vips` entry emits one on any node, so a storage VIP works on a worker.
- `RegistryMirrorConfig` and `RegistryTLSConfig` — from the `registryMirrors` and `registryTLS` values, available on both charts.
- `ExtensionServiceConfig` for `zfs-service` — cozystack chart only, emitted on the legacy format too. It sets `ZFS_EXPORT_TIMEOUT=0` so the ZFS extension skips the pool export on shutdown, and is dropped by `zfs.exportOnShutdown: true`.

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

!!! danger "The configured version must not be newer than the Talos running on the node"

    This setting must not exceed the **Talos version actually running on the target node** — i.e. the maintenance ISO/PXE the node booted from for `apply -i`, or the installed Talos for an authenticated apply. It is **not** the same as `install.image`, which only controls what gets written to disk after a successful apply. When the configured contract is newer than the running binary, machinery injects fields (e.g. `machine.install.grubUseUKICmdline` from v1.12) that the running parser does not know, and the apply fails on the node side with `failed to parse config: unknown keys found during decoding: ...`. `talm apply` runs a best-effort pre-flight check against the running version and prints a `warning: pre-flight: ...` line with a hint when it detects this mismatch; if the warning is missed, the same hint is appended to the apply error. Either reboot the node into a maintenance image that matches the configured contract, or lower `templateOptions.talosVersion` / `--talos-version` to match what is running.
