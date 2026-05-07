# Talm

Manage Talos the GitOps Way!

Talm is just like Helm, but for Talos Linux

## Features

While developing Talm, we aimed to achieve the following goals:

- **Automatic Discovery**: In a bare-metal environment, each server may vary
slightly in aspects such as disks and network interfaces.
Talm enables discovery of node information, which is then used to generate patches.

- **Ease of Customization**: You can customize templates to create your unique
configuration based on your environment. The templates use the standard
Go templates syntax, enhanced with widely-known Helm templating logic.

- **GitOps Friendly**: The patches generated do not contain sensitive data,
allowing them to be stored in Git in an unencrypted, open format. For scenarios
requiring complete configurations, the `--full` option allows the obtain
a complete config that can be used for matchbox and other solutions.

- **Simplicity of Use**: You no longer need to pass connection options for each
specific server; they are saved along with the templating results into
a separate file. This allows you to easily apply one or multiple files in batch
using a syntax similar to `kubectl apply -f node1.yaml -f node2.yaml`.

- **Compatibility with talosctl**: We strive to maintain compatibility with the upstream
project in patches and configurations. The configurations you obtain can be used
with the official tools like talosctl and Omni.


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

Windows is supported. Download the `talm-windows-*.zip` archive from the
[releases page](https://github.com/cozystack/talm/releases/latest) and
extract `talm.exe`. On Windows, template paths passed to the `-t` /
`--template` flag accept either `\` or `/` separators, so
`-t templates\controlplane.yaml` and `-t templates/controlplane.yaml`
are equivalent. Other path flags (`--talosconfig`, `-f` / `--file`)
are delegated to the underlying OS file loader and follow standard
Windows path rules.

## Getting Started

Create new project
```bash
mkdir newcluster
cd newcluster
talm init -p cozystack -N myawesomecluster
```

Edit `values.yaml` to set your cluster's control-plane endpoint. This
is the URL every node's kubelet and kube-proxy will dial. The chart
leaves it empty on purpose so a missed override fails loudly instead
of silently embedding a placeholder. For cozystack VIP setups set
`endpoint` and `floatingIP` together (same IP, single shared VIP);
for single-node clusters use that node's routable IP and leave
`floatingIP` blank; for multi-node with an external load balancer
use the LB URL and leave `floatingIP` blank. When the VIP must sit
on a link that does not yet exist on the live system at first apply
(typically a VLAN sub-interface), set `vipLink` to that link name —
the chart pins `Layer2VIPConfig.link` to it instead of the default-
gateway link that discovery would otherwise pick, and emits the
document even on a totally fresh node where no default-gateway link
has been discovered yet. The chart does not auto-emit a `LinkConfig`
or `VLANConfig` for the override link; the operator is responsible
for ensuring the link comes up, typically by adding a `LinkConfig`
or `VLANConfig` for that link to the per-node body overlay alongside
`vipLink`. Subnet-selector fields
(`kubelet.validSubnets`, `etcd.advertisedSubnets`) are derived
automatically from the node's default-gateway-bearing link, so no
override is needed unless you have a multi-homed node that requires
a specific subnet pinned.

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

> **Note:** The output format depends on the Talos version configured in `Chart.yaml` (`templateOptions.talosVersion`) or via the `--talos-version` CLI flag.
> For Talos < v1.12, the output is a single YAML document with `machine.network` and `machine.registries` sections (as shown above).
> For Talos >= v1.12, the output uses the multi-document format with separate typed documents instead of the deprecated monolithic fields. `HostnameConfig`, `ResolverConfig` and a network interface document (`LinkConfig`, `BondConfig`, or `VLANConfig` — depending on topology) are always emitted; `Layer2VIPConfig` appears on controlplane nodes when `floatingIP` is set; `RegistryMirrorConfig` is emitted only by the cozystack chart.

> **Version compatibility (`templateOptions.talosVersion` / `--talos-version`).** This setting must match the **Talos version actually running on the target node** — i.e. the maintenance ISO/PXE the node booted from for `apply -i`, or the installed Talos for an authenticated apply. It is **not** the same as `install.image`, which only controls what gets written to disk after a successful apply. When the configured contract is newer than the running binary, machinery injects fields (e.g. `machine.install.grubUseUKICmdline` from v1.12) that the running parser does not know, and the apply fails on the node side with `failed to parse config: unknown keys found during decoding: ...`. `talm apply` runs a best-effort pre-flight check against the running version and prints a `warning: pre-flight: ...` line with a hint when it detects this mismatch; if the warning is missed, the same hint is appended to the apply error. Either reboot the node into a maintenance image that matches the configured contract, or lower `templateOptions.talosVersion` / `--talos-version` to match what is running.

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

> **Per-node patches inside node files.** A node file can carry Talos config
> below its modeline (for example, a custom `hostname`, secondary
> interfaces with `deviceSelector`, VIP placement, or extra etcd args).
> When `talm apply -f node.yaml` runs the template-rendering branch, that
> body is applied as a strategic merge patch on top of the rendered
> template before the result is sent to the node — so per-node fields
> survive even when the template auto-generates conflicting values
> (e.g. `hostname: talos-XXXXX`).
>
> **Talos v1.12+ caveat.** The multi-document output format introduced
> in v1.12 splits network configuration into typed documents
> (`LinkConfig`, `BondConfig`, `VLANConfig`, `Layer2VIPConfig`,
> `HostnameConfig`, `ResolverConfig`). Legacy node-body fields under
> `machine.network.interfaces` have no safe 1:1 mapping to those types,
> so the multi-doc path does not translate them — if you target a
> v1.12+ Talos node, pin per-node network settings by patching the
> typed resources (e.g. a `LinkConfig` document below the modeline)
> rather than legacy `machine.network.interfaces`. Fields outside the
> network area (`machine.network.hostname` via `HostnameConfig`,
> `machine.install.disk`, extra etcd args, etc.) still merge as
> expected.
>
> **One body, one node.** A non-empty body is a per-node pin, so the
> modeline for that file must target exactly one node. `talm apply`
> refuses a multi-node modeline when the body is non-empty; modeline-
> only files (no body) are still allowed and drive the same rendered
> template on every listed target.
>
> **Idempotent applies.** Repeated `talm apply` runs against an
> already-configured node do not duplicate entries. Before the strategic
> merge runs, the engine prunes from the body every primitive-list
> entry the rendered template already carries (e.g. certSANs,
> nameservers, validSubnets). For object arrays the upstream patcher
> merges by identity (machine.network.interfaces by `interface:` or
> `deviceSelector:`, vlans by `vlanId:`, apiServer admissionControl by
> `name:`), the prune descends into matched pairs and dedupes the inner
> primitive lists too — so re-applying after `talm template -I` does not
> double interface addresses, vlan addresses, or admission-control
> exemption namespaces. For object arrays without an upstream identity
> merge (extraVolumes, kernel.modules, wireguard.peers, ...), body items
> that deep-equal a rendered counterpart are dropped, covering the
> dominant full-restate case. Fields tagged `merge:"replace"` upstream
> are passed through verbatim — pruning them would let the upstream
> replace silently drop the rendered entries on a partial edit. This
> covers v1alpha1 root paths `cluster.network.podSubnets`,
> `cluster.network.serviceSubnets`, `cluster.apiServer.auditPolicy`,
> and the typed `NetworkRuleConfig` paths `ingress` and
> `portSelector.ports`.
>
> `talm template -f node.yaml` (with or without `-I`) does **not** apply
> the same overlay: its output is the rendered template plus the modeline
> and the auto-generated warning, byte-identical to what the template
> alone would produce. Routing it through the patcher would drop every
> YAML comment (including the modeline) and re-sort keys, breaking
> downstream commands that read the file back. Use `apply --dry-run` if
> you want to preview the exact bytes that will be sent to the node.

## Using talosctl commands

Talm offers a similar set of commands to those provided by talosctl.
However, you can specify the --file option for them.

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
