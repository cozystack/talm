# Talm

Manage Talos the GitOps Way!

Talm is just like Helm, but for Talos Linux

## Features

While developing Talm, we aimed to achieve the following goals:

- **Automatic Discovery**: In a bare-metal environment, each server may vary slightly in aspects such as disks and network interfaces. Talm enables discovery of node information, which is then used to generate patches.

- **Ease of Customization**: You can customize templates to create your unique configuration based on your environment. The templates use the standard Go templates syntax, enhanced with widely-known Helm templating logic.

- **GitOps Friendly**: The patches generated do not contain sensitive data, allowing them to be stored in Git in an unencrypted, open format. For scenarios requiring complete configurations, the `--full` option allows the obtain a complete config that can be used for matchbox and other solutions.

- **Simplicity of Use**: You no longer need to pass connection options for each specific server; they are saved along with the templating results into a separate file. This allows you to easily apply one or multiple files in batch using a syntax similar to `kubectl apply -f node1.yaml -f node2.yaml`.

- **Compatibility with talosctl**: We strive to maintain compatibility with the upstream project in patches and configurations. The configurations you obtain can be used with the official tools like talosctl and Omni.


## Installation

### Homebrew
For macOS and Linux users, the recommended way to install talm is with Homebrew.


```bash
brew install talm
```

### Binary

Download binary from Github [releases page](https://github.com/cozystack/talm/releases/latest)

Or use simple script to install it:
```bash
curl -sSL https://github.com/cozystack/talm/raw/refs/heads/main/hack/install.sh | sh -s
```

### Windows

Windows is supported. Download the `talm-windows-*.zip` archive from the [releases page](https://github.com/cozystack/talm/releases/latest) and extract `talm.exe`. On Windows, template paths passed to the `-t` / `--template` flag accept either `\` or `/` separators, so `-t templates\controlplane.yaml` and `-t templates/controlplane.yaml` are equivalent. Other path flags (`--talosconfig`, `-f` / `--file`) are delegated to the underlying OS file loader and follow standard Windows path rules.

## Getting Started

Create new project
```bash
mkdir newcluster
cd newcluster
talm init -p cozystack -N myawesomecluster
```

`talm init` refuses to run when the current directory is inside an existing talm project (it would otherwise walk up and partially overwrite the parent). To create a project under the current directory anyway — e.g. a sub-project nested inside another talm project — pass `--root .` explicitly. To re-initialise the parent itself, run `talm init` from the parent directory.

To pin a specific Talos installer image at init time (e.g. a [Talos Factory](https://factory.talos.dev/) image with extensions), pass `--image`:

```bash
talm init -p cozystack -N myawesomecluster --image factory.talos.dev/installer/<sha256>:<version>
```

`--image` rewrites the top-level `image:` field in the preset's `values.yaml` before write. The flag is honored on initial `init` only — for an existing project, edit `values.yaml` directly. The `cozystack` preset declares `image:`; the `generic` preset does not, so `--image --preset generic` is rejected up front.

Edit `values.yaml` to set your cluster's control-plane endpoint. This is the URL every node's kubelet and kube-proxy will dial. The chart leaves it empty on purpose so a missed override fails loudly instead of silently embedding a placeholder.

Endpoint / floatingIP combinations:

- **cozystack VIP setup**: set `endpoint` and `floatingIP` together to the same IP — single shared VIP.
- **single-node cluster**: set `endpoint` to the node's routable IP and leave `floatingIP` blank.
- **multi-node with external load balancer**: set `endpoint` to the LB URL and leave `floatingIP` blank.

When `vipLink` is left empty the chart picks the link automatically using a two-step rule:

1. **Longest-prefix match across configurable links.** If `floatingIP` falls inside the CIDR of any address on a configurable link (physical NIC, bond, VLAN, bridge), the most specific subnet wins. This handles the Hetzner-style topology where a public NIC carries the default route and a VLAN child carries the private cluster subnet — the VIP lands on the VLAN child.
2. **Fallback to the IPv4-default-gateway-bearing link.** Used when no configurable link's CIDR contains the `floatingIP` — typical for upstream-routable VIPs that arrive via the default route.

Addresses on links the chart does not emit a per-link document for (Wireguard, kernel-managed loopback, slave NICs of a bond, anything outside the configurable set) are skipped — a VIP pinned there would have no surrounding network document.

Set `vipLink` explicitly when the target link does not yet exist on the live system at first apply (typically a VLAN sub-interface). The chart pins `Layer2VIPConfig.link` to it directly and emits the document even on a fresh node where discovery has not yet populated the addresses table. The chart does not auto-emit a `LinkConfig` or `VLANConfig` for the override link; the operator is responsible for ensuring the link comes up, typically by adding a `LinkConfig` or `VLANConfig` for that link to the per-node body overlay alongside `vipLink`.

Subnet-selector fields (`kubelet.validSubnets`, `etcd.advertisedSubnets`) are derived automatically from the node's default-gateway-bearing link, so no override is needed unless you have a multi-homed node that requires a specific subnet pinned.

Boot Talos Linux node, let's say it has address `192.0.2.4`. Then:

```yaml
# values.yaml (single-node example matching the 192.0.2.4 node below)
endpoint: "https://192.0.2.4:6443"
floatingIP: ""
```

Gather node information:
```bash
talm -n 192.0.2.4 -e 192.0.2.4 template -t templates/controlplane.yaml -i > nodes/node1.yaml
```

Edit `nodes/node1.yaml` file:
```yaml
# talm: nodes=["192.0.2.4"], endpoints=["192.0.2.4"], templates=["templates/controlplane.yaml"]
machine:
    network:
        # -- Discovered interfaces:
        # enx9c6b0047066c:
        #   name: enp193s0f0
        #   mac:9c:6b:00:47:06:6c
        #   bus:0000:c1:00.0
        #   driver:bnxt_en
        #   vendor: Broadcom Inc. and subsidiaries
        #   product: BCM57414 NetXtreme-E 10Gb/25Gb RDMA Ethernet Controller)
        # enx9c6b0047066d:
        #   name: enp193s0f1
        #   mac:9c:6b:00:47:06:6d
        #   bus:0000:c1:00.1
        #   driver:bnxt_en
        #   vendor: Broadcom Inc. and subsidiaries
        #   product: BCM57414 NetXtreme-E 10Gb/25Gb RDMA Ethernet Controller)
        interfaces:
            - interface: enx9c6b0047066c
              addresses:
                - 192.0.2.4/26
              routes:
                - network: 0.0.0.0/0
                  gateway: 192.0.2.1
        nameservers:
            - 8.8.8.8
            - 8.8.4.4
    install:
        # -- Discovered disks:
        # /dev/nvme0n1:
        #    model: SAMSUNG MZQL21T9HCJR-00A07
        #    serial: S64GNE0RB00153
        #    wwid: eui.3634473052b001530025384500000001
        #    size: 1.75 TB
        # /dev/nvme1n1:
        #    model: SAMSUNG MZQL21T9HCJR-00A07
        #    serial: S64GNE0R811820
        #    wwid: eui.36344730528118200025384500000001
        #    size: 1.75 TB
        disk: /dev/nvme0n1
    type: controlplane
cluster:
    clusterName: talm
    controlPlane:
        endpoint: https://192.0.2.4:6443
```

> **Note: output format depends on Talos version.**
>
> Selected via `Chart.yaml` (`templateOptions.talosVersion`) or `--talos-version`:
>
> - **Talos < v1.12** — single YAML document with `machine.network` and `machine.registries` sections (as shown above).
> - **Talos >= v1.12** — multi-document format with separate typed documents instead of the deprecated monolithic fields.
>
> For v1.12+ multi-doc output, one document is emitted per configurable link on the node, plus a fixed pair on every render:
>
> - `HostnameConfig` and `ResolverConfig` — always emitted.
> - `LinkConfig` — physical NICs.
> - `BondConfig` — bond masters. Bond slaves are filtered out so they do not collide with the master's document.
> - `VLANConfig` — VLAN sub-interfaces.
> - `BridgeConfig` — bridges, symmetric to `BondConfig` for bonds. Ports discovered via `spec.slaveKind == "bridge"` + `spec.masterIndex`; STP / VLAN-filtering settings reach the output when the bridge controller reports them on `spec.bridgeMaster`.
> - `Layer2VIPConfig` — controlplane nodes when `floatingIP` is set.
> - `RegistryMirrorConfig` — cozystack chart only.
>
> Per-link emission rules:
>
> - The link carrying the IPv4 default route gets the `routes.gateway` entry on its document; every other link is emitted gateway-less. Applies uniformly to `LinkConfig`, `BondConfig`, `VLANConfig`, `BridgeConfig`.
> - Both IPv4 and IPv6 global-scope addresses on a link are surfaced.
> - The operator-declared `floatingIP` is stripped from per-link addresses so the VIP currently held by a leader does not leak into the static document.
>
> Multi-NIC nodes therefore produce one document per NIC, not one document total.

> **Version compatibility (`templateOptions.talosVersion` / `--talos-version`).** This setting must match the **Talos version actually running on the target node** — i.e. the maintenance ISO/PXE the node booted from for `apply -i`, or the installed Talos for an authenticated apply. It is **not** the same as `install.image`, which only controls what gets written to disk after a successful apply. When the configured contract is newer than the running binary, machinery injects fields (e.g. `machine.install.grubUseUKICmdline` from v1.12) that the running parser does not know, and the apply fails on the node side with `failed to parse config: unknown keys found during decoding: ...`. `talm apply` runs a best-effort pre-flight check against the running version and prints a `warning: pre-flight: ...` line with a hint when it detects this mismatch; if the warning is missed, the same hint is appended to the apply error. Either reboot the node into a maintenance image that matches the configured contract, or lower `templateOptions.talosVersion` / `--talos-version` to match what is running.

> **Apply-time safety gates.** `talm apply` and `talm upgrade` run additional gates around each operation:
>
> 1. **Declared-resource existence** (`--skip-resource-validation` opt-out, default on). Before sending the config to the node, the gate walks the rendered MachineConfig, extracts every reference to a host-side resource (network links from v1.12 multi-doc — `LinkConfig.name`, `BondConfig.links[]`, `VLANConfig.parent`, `BridgeConfig.links[]`, `Layer2VIPConfig.link`, `HCloudVIPConfig.link`, `DHCPv4Config.name` / `DHCPv6Config.name` / `EthernetConfig.name`; v1.11 legacy `machine.network.interfaces[].interface`; install disk via `machine.install.disk` literal or `machine.install.diskSelector`; `UserVolumeConfig.provisioning.diskSelector`), and verifies each against the node's COSI `LinkStatus`/`Disk` snapshots. A reference that doesn't resolve fails the apply with a `[blocker]` line listing the available names so the typo or migration miss is fixable from the values without re-running discovery. Disk selectors must match at least one (non-readonly, non-CDROM, non-virtual) disk — zero matches block, multiple matches warn (install picks the first). Virtual-link-creator documents (`BondConfig.name`, `VLANConfig.name`, `BridgeConfig.name`, `WireguardConfig.name`, `DummyLinkConfig.name`, `LinkAliasConfig.name`) are intentionally NOT validated against existing links — those `.name` fields describe new virtual links the apply is creating, not references to pre-existing host resources. Out of scope today: `machine.disks[].device` (extra-disk partitioning); track in a follow-up if you need it. Pass `--skip-resource-validation` for recovery into a maintenance image with mismatched hardware or pre-staging values for hardware that isn't installed yet.
>
> 2. **Pre-apply drift preview** (`--skip-drift-preview` opt-out, default on). Reads the node's current MachineConfig via COSI and prints a `+`/`-`/`~`/`=` diff of what's about to change, keyed by `(kind, name)`. Informational only — never blocks. The `-` lines are the most useful: they surface stale documents from a previous apply that the new render no longer emits (e.g. an `eth1` LinkConfig lingering after a migration to `eth0`). Reading the current config requires the auth path — `MachineConfig` is a Sensitive COSI resource and is unreachable on the `--insecure` maintenance connection; the gate prints `drift verification unavailable on maintenance connection` and proceeds in that case. **`--dry-run` runs this gate** — the diff is read-only and "show me what would change" is exactly the dry-run contract.
>
> 3. **Post-apply state verification** (`--skip-post-apply-verify` opt-out, **default off** until the Talos-mutated-field allowlist lands — see [#172](https://github.com/cozystack/talm/issues/172)). After `ApplyConfiguration` returns success, re-reads the on-node MachineConfig and structurally compares it against the bytes that were sent. Divergence blocks the apply chain with a per-document diff, primarily catching silent doc drops (Talos parser ignored an unknown field) and controller reverts. Disabled by default because Talos mutates a handful of leaf fields post-apply (cert hashes, timestamps) that would surface as false-positive divergence without an allowlist. The verify runs only on `--mode=no-reboot`. `--mode=staged`, `--mode=try`, `--mode=reboot`, and `--mode=auto` all skip the gate — each for a documented reason: staged stores rather than activates; try auto-rolls back; reboot kills the COSI connection mid-verify; auto is promoted by Talos to REBOOT internally when the change requires it, so the verify would race the reboot. `--dry-run` skips it too.
>
> 4. **Post-upgrade version verify** (`--skip-post-upgrade-verify` opt-out, default on — the gate runs). After `talm upgrade` reports success, waits 90s for the node to finish booting then reads `runtime.Version` COSI and compares the running version's `(Major, Minor)` contract against the contract parsed from the target image tag. Point releases share a minor contract; cross-minor mismatch surfaces as a hint-bearing blocker. Catches the silent A/B rollback case where the upgrade RPC acks success but Talos rolled back to the previous partition (cross-vendor image, missing extensions, failed boot readiness check, slow boot exceeding the reconcile window). Best-effort surrender on digest-pinned images and unparseable tags. See [#175](https://github.com/cozystack/talm/issues/175) for the reproduction.
>
> The skip flags don't suppress each other — pass them independently. On the `--insecure` (maintenance) path the gates are functionally unreachable for charts that drive discovery via `lookup` — those COSI lookups require an authenticated connection and the render itself errors before any gate runs. Charts that render fully offline (no `lookup` calls) reach the gates on `--insecure` as well, with the Phase 2 hooks degrading gracefully because the `MachineConfig` resource is Sensitive.

Apply config:
```bash
talm apply -f nodes/node1.yaml -i
```

Upgrade node:
```bash
talm upgrade -f nodes/node1.yaml
```

Show diff:
```bash
talm apply -f nodes/node1.yaml --dry-run
```

Re-template and update generated file in place (this will overwrite it):
```
talm template -f nodes/node1.yaml -I
```

> **Per-node patches inside node files.** A node file can carry Talos config below its modeline (for example, a custom `hostname`, secondary interfaces with `deviceSelector`, VIP placement, or extra etcd args). When `talm apply -f node.yaml` runs the template-rendering branch, that body is applied as a strategic merge patch on top of the rendered template before the result is sent to the node — so per-node fields survive even when the template auto-generates conflicting values (e.g. `hostname: talos-XXXXX`).
>
> **Talos v1.12+ caveat.** The multi-document output format introduced in v1.12 splits network configuration into typed documents (`LinkConfig`, `BondConfig`, `VLANConfig`, `Layer2VIPConfig`, `HostnameConfig`, `ResolverConfig`). Legacy node-body fields under `machine.network.interfaces` have no safe 1:1 mapping to those types and the chart cannot translate them yet — pin per-node network settings by patching the typed resources (e.g. a `LinkConfig` document below the modeline) rather than legacy `machine.network.interfaces`. Fields outside the network area (`machine.network.hostname` via `HostnameConfig`, `machine.install.disk`, extra etcd args, etc.) still merge as expected.
>
> **Upgrade-from-legacy guardrail.** Nodes originally bootstrapped on a chart that emitted the legacy schema still carry `machine.network.interfaces[]` in their running `MachineConfig`. On v1.12 multi-doc rendering the chart cannot reconstruct equivalent typed documents from those entries automatically, and silently dropping them on the next apply would erase the user's network declarations. The renderer therefore fails the render with `talm: the multi-doc renderer cannot translate legacy machine.network.interfaces[] from the running MachineConfig...`, spelling out the migration path: move the interfaces, vlans, and addresses into per-node body overlays as v1.12 typed documents (`LinkConfig`, `VLANConfig`, `BondConfig`, `RouteConfig`) before re-running `talm apply`, or pin `templateOptions.talosVersion: "v1.11"` in `Chart.yaml` until the translator lands.
>
> **One body, one node.** A non-empty body is a per-node pin, so the modeline for that file must target exactly one node. `talm apply` refuses a multi-node modeline when the body is non-empty; modeline-only files (no body) are still allowed and drive the same rendered template on every listed target.
>
> **Idempotent applies.** Repeated `talm apply` runs against an already-configured node do not duplicate entries. Before the strategic merge runs, the engine prunes from the body every primitive-list entry the rendered template already carries (e.g. certSANs, nameservers, validSubnets). For object arrays the upstream patcher merges by identity (machine.network.interfaces by `interface:` or `deviceSelector:`, vlans by `vlanId:`, apiServer admissionControl by `name:`), the prune descends into matched pairs and dedupes the inner primitive lists too — so re-applying after `talm template -I` does not double interface addresses, vlan addresses, or admission-control exemption namespaces. For object arrays without an upstream identity merge (extraVolumes, kernel.modules, wireguard.peers, ...), body items that deep-equal a rendered counterpart are dropped, covering the dominant full-restate case. Fields tagged `merge:"replace"` upstream are passed through verbatim — pruning them would let the upstream replace silently drop the rendered entries on a partial edit. This covers v1alpha1 root paths `cluster.network.podSubnets`, `cluster.network.serviceSubnets`, `cluster.apiServer.auditPolicy`, and the typed `NetworkRuleConfig` paths `ingress` and `portSelector.ports`.
>
> `talm template -f node.yaml` (with or without `-I`) does **not** apply the same overlay: its output is the rendered template plus the modeline and the auto-generated warning, byte-identical to what the template alone would produce. Routing it through the patcher would drop every YAML comment (including the modeline) and re-sort keys, breaking downstream commands that read the file back. Use `apply --dry-run` if you want to preview the exact bytes that will be sent to the node.

## Using talosctl commands

Talm offers a similar set of commands to those provided by talosctl. However, you can specify the --file option for them.

For example, to run a dashboard for three nodes:

```
talm dashboard -f node1.yaml -f node2.yaml -f node3.yaml
```

## Customization

You're free to edit template files in `./templates` directory.

All the [Helm](https://helm.sh/docs/chart_template_guide/functions_and_pipelines/) and [Sprig](https://masterminds.github.io/sprig/) functions are supported, including lookup for talos resources!

Lookup function example:

```helm
{{ lookup "nodeaddresses" "network" "default" }}
```

\- is equivalent to:

```bash
talosctl get nodeaddresses --namespace=network default
```


Querying disks map example:

```helm
{{ range .Disks }}{{ if .system_disk }}{{ .device_name }}{{ end }}{{ end }}
```

\- will return the system disk device name


## Encryption

Talm provides built-in encryption support using [age](https://age-encryption.org/) encryption. Sensitive files are encrypted with their values stored in SOPS format (`ENC[AGE,data:...]`), while YAML keys remain unencrypted for better readability.

### Encrypting Files

To encrypt all sensitive files (secrets.yaml, talosconfig, kubeconfig):

```bash
talm init --encrypt
# or
talm init -e
```

This command will:
- Generate `talm.key` if it doesn't exist
- Encrypt `secrets.yaml` → `secrets.encrypted.yaml`
- Encrypt `talosconfig` → `talosconfig.encrypted`
- Encrypt `kubeconfig` → `kubeconfig.encrypted` (if exists)
- Update `.gitignore` with sensitive files

### Decrypting Files

To decrypt all encrypted files:

```bash
talm init --decrypt
# or
talm init -d
```

This command will:
- Decrypt `secrets.encrypted.yaml` → `secrets.yaml`
- Decrypt `talosconfig.encrypted` → `talosconfig`
- Decrypt `kubeconfig.encrypted` → `kubeconfig` (if exists)
- Update `.gitignore` with sensitive files

### Key Management

The `talm.key` file is generated in age keygen format and contains:
- Creation timestamp
- Public key (for sharing)
- Private key (keep secure!)

**Important**: Always backup your `talm.key` file! Without it, you won't be able to decrypt your encrypted secrets. The key file is automatically added to `.gitignore` to prevent accidental commits.

Encrypted files (`*.encrypted.yaml`, `*.encrypted`) can be safely committed to Git, while plain files (`secrets.yaml`, `talosconfig`, `kubeconfig`, `talm.key`) are ignored.
