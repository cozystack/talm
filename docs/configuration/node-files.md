# Node files

A node file is what `talm template -i` writes and what every later command reads back: one YAML document per node, carrying a `# talm:` modeline on the first line, the discovered hardware as comments, and optionally your own per-node config below it. See [Quickstart](../getting-started/quickstart.md) for the command that produces one.

## Anatomy

A freshly written `nodes/node1.yaml` for a single control-plane node at `192.0.2.4`:

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

## Per-node patches inside node files

A node file can carry Talos config below its modeline (for example, a custom `hostname`, secondary interfaces with `deviceSelector`, VIP placement, or extra etcd args). When `talm apply -f node.yaml` runs the template-rendering branch, that body is applied as a strategic merge patch on top of the rendered template before the result is sent to the node — so per-node fields survive even when the template auto-generates conflicting values (e.g. `hostname: talos-XXXXX`).

!!! warning "Talos v1.12+ caveat"

    The multi-document output format introduced in v1.12 splits network configuration into typed documents (`LinkConfig`, `BondConfig`, `VLANConfig`, `Layer2VIPConfig`, `HostnameConfig`, `ResolverConfig`). Legacy node-body fields under `machine.network.interfaces` have no safe 1:1 mapping to those types and the chart cannot translate them yet — pin per-node network settings by patching the typed resources (e.g. a `LinkConfig` document below the modeline) rather than legacy `machine.network.interfaces`. Fields outside the network area (`machine.network.hostname` via `HostnameConfig`, `machine.install.disk`, extra etcd args, etc.) still merge as expected.

!!! warning "Upgrade-from-legacy guardrail"

    Nodes originally bootstrapped on a chart that emitted the legacy schema still carry `machine.network.interfaces[]` in their running `MachineConfig`. On v1.12 multi-doc rendering the chart cannot reconstruct equivalent typed documents from those entries automatically, and silently dropping them on the next apply would erase the user's network declarations. The renderer therefore fails the render with `talm: the multi-doc renderer cannot translate legacy machine.network.interfaces[] from the running MachineConfig...`, spelling out the migration path: move the interfaces, vlans, and addresses into per-node body overlays as v1.12 typed documents (`LinkConfig`, `VLANConfig`, `BondConfig`, `RouteConfig`) before re-running `talm apply`, or pin `templateOptions.talosVersion: "v1.11"` in `Chart.yaml` until the translator lands.

!!! warning "One body, one node"

    A non-empty body is a per-node pin, so the modeline for that file must target exactly one node. `talm apply` refuses a multi-node modeline when the body is non-empty; modeline-only files (no body) are still allowed and drive the same rendered template on every listed target.

## Idempotent applies

Repeated `talm apply` runs against an already-configured node do not duplicate entries. Before the strategic merge runs, the engine prunes from the body every primitive-list entry the rendered template already carries (e.g. certSANs, nameservers, validSubnets). For object arrays the upstream patcher merges by identity (machine.network.interfaces by `interface:` or `deviceSelector:`, vlans by `vlanId:`, apiServer admissionControl by `name:`), the prune descends into matched pairs and dedupes the inner primitive lists too — so re-applying after `talm template -I` does not double interface addresses, vlan addresses, or admission-control exemption namespaces. For object arrays without an upstream identity merge (extraVolumes, kernel.modules, wireguard.peers, ...), body items that deep-equal a rendered counterpart are dropped, covering the dominant full-restate case. Fields tagged `merge:"replace"` upstream are passed through verbatim — pruning them would let the upstream replace silently drop the rendered entries on a partial edit. This covers v1alpha1 root paths `cluster.network.podSubnets`, `cluster.network.serviceSubnets`, `cluster.apiServer.auditPolicy`, and the typed `NetworkRuleConfig` paths `ingress` and `portSelector.ports`.

## What `talm template` does not do

`talm template -f node.yaml` (with or without `-I`) does **not** apply the same overlay: its output is the rendered template plus the modeline and the auto-generated warning, byte-identical to what the template alone would produce. Routing it through the patcher would drop every YAML comment (including the modeline) and re-sort keys, breaking downstream commands that read the file back. Use [`apply --dry-run`](../operations/safety-gates.md) if you want to preview the exact bytes that will be sent to the node.
