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

Download binary from Github [releases page](https://github.com/cozystack/talm/releases/latest)

```bash
chmod +x ./talm-linux-amd64
sudo mv talm-linux-amd64 /usr/local/bin/talm
```

## Getting Started

Create new project
```bash
mkdir newcluster
cd newcluster
talm init
mkdir nodes
```

Boot Talos Linux node, let's say it has address `1.2.3.4`

Gather node information:
```bash
talm -n 1.2.3.4 -e 1.2.3.4 template -t templates/controlplane.yaml -i > nodes/node1.yaml
```

Edit `nodes/node1.yaml` file:
```yaml
# talm: nodes=["1.2.3.4"], endpoints=["1.2.3.4"], templates=["templates/controlplane.yaml"]
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
                - 1.2.3.4/26
              routes:
                - network: 0.0.0.0/0
                  gateway: 1.2.3.1
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
        endpoint: https://192.168.0.1:6443
```

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

\- is equiualent to:

```bash
talosctl get nodeaddresses --namespace=network default
```


Querying disks map example:

```helm
{{ range .Disks }}{{ if .system_disk }}{{ .device_name }}{{ end }}{{ end }}
```

\- will return the system disk device name


## Encryption

Currently, Talm does not have built-in encryption support, but you can transparently encrypt your secrets using the [git-crypt](https://github.com/AGWA/git-crypt) extension.

Example `.gitattributes` file:

```
kubeconfig filter=git-crypt diff=git-crypt
secrets.yaml filter=git-crypt diff=git-crypt
talosconfig filter=git-crypt diff=git-crypt
.gitattributes !filter !diff
```
