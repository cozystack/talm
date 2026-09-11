---
hide:
  - navigation
---

# Talm

**Manage Talos the GitOps way.**

Talm is just like Helm, but for Talos Linux. It renders per-node machine configs from Go templates, discovers each node's real disks and interfaces, and applies the result with the flags you already know from `talosctl` — while the generated files stay clean enough to commit.

[Quickstart](getting-started/quickstart.md){ .md-button .md-button--primary }
[Install talm](getting-started/installation.md){ .md-button }

## Why Talm

<div class="grid cards" markdown>

-   :material-magnify-scan:{ .lg .middle } **Automatic discovery**

    ---

    Bare-metal servers differ in disks and interfaces. Talm reads the node's real hardware and writes the discovered names into the node file as comments you pick from.

    [:octicons-arrow-right-24: Node files](configuration/node-files.md)

-   :material-file-code-outline:{ .lg .middle } **Helm templating**

    ---

    Standard Go template syntax with the full Helm and Sprig function set — including `lookup` against live Talos COSI resources.

    [:octicons-arrow-right-24: Templates and values](configuration/templates.md)

-   :material-source-branch:{ .lg .middle } **GitOps friendly**

    ---

    Generated patches carry no sensitive data, so they live in Git in the clear. Secrets your templates need are age-encrypted at rest and decrypted in memory at render time.

    [:octicons-arrow-right-24: Encryption](configuration/encryption.md)

-   :material-shield-check-outline:{ .lg .middle } **Guarded applies**

    ---

    Every apply checks that the links and disks your config references exist on the node, and prints a diff of what is about to change before it changes it.

    [:octicons-arrow-right-24: Apply-time safety gates](operations/safety-gates.md)

-   :material-console-line:{ .lg .middle } **talosctl compatible**

    ---

    The same subcommands, plus `-f nodes/<name>.yaml` so you never retype `--nodes` and `--endpoints`. Configs stay usable with talosctl and Omni.

    [:octicons-arrow-right-24: talosctl-compatible commands](operations/talosctl-commands.md)

-   :material-package-variant-closed:{ .lg .middle } **Reproducible projects**

    ---

    `talm init` vendors the preset and library chart into your repo, and warns when a binary upgrade leaves them stale.

    [:octicons-arrow-right-24: Upgrading talm](operations/upgrading.md)

</div>

## From zero to a configured node

```bash
brew install talm
mkdir mycluster && cd mycluster
talm init -p cozystack -N mycluster --endpoints 192.0.2.4
talm --nodes 192.0.2.4 -e 192.0.2.4 template -t templates/controlplane.yaml -i > nodes/node1.yaml
talm apply -f nodes/node1.yaml -i
```

[The same five lines, explained :octicons-arrow-right-24:](getting-started/quickstart.md)

## Where to next

- [Initializing a project](getting-started/init.md) — `--image`, `--cluster-endpoint`, `--root`.
- [Endpoints and VIPs](configuration/endpoints-and-vips.md) — `endpoint`, `floatingIP`, `vipLink`.
- [Talos versions and output format](configuration/talos-versions.md) — which schema a render targets, and the version keys to pin.
- [Applying with side-patches](operations/side-patches.md) — the `-f` chain.
- [CLI reference](reference/index.md) — generated from the command tree.
