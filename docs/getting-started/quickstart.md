# Quickstart

From an empty directory to a configured Talos node. The example uses a single control-plane node at `192.0.2.4`; substitute your own address.

!!! note "All commands run from the project root"

    `talm init` creates the project directory and everything below assumes you are inside it. Node paths like `nodes/node1.yaml` are relative to that root.

## 1. Create a project

```bash
mkdir newcluster
cd newcluster
talm init -p cozystack -N myawesomecluster --endpoints 192.0.2.4
```

Passing a single `--endpoints` value lets init derive `values.yaml::endpoint` as `https://192.0.2.4:6443`, so there is nothing to edit by hand. With more than one endpoint it does not derive anything — see [Initializing a project](init.md) for `--cluster-endpoint` and the rest of the init flags.

!!! note

    `talm init` refuses to run inside an existing talm project. If that is deliberate, see [Re-initializing inside an existing project](init.md#re-initializing-inside-an-existing-project).

`values.yaml` should now read:

```yaml
# values.yaml (single-node example)
endpoint: "https://192.0.2.4:6443"
floatingIP: ""
```

[Endpoints and VIPs](../configuration/endpoints-and-vips.md) covers what to put here for a VIP or a load-balanced control plane.

## 2. Gather node information

Boot the Talos node and, while it is in maintenance mode, render its config:

```bash
talm --nodes 192.0.2.4 -e 192.0.2.4 template -t templates/controlplane.yaml -i > nodes/node1.yaml
```

This writes `nodes/node1.yaml` with a `# talm:` modeline recording the node and endpoint addresses, the discovered interfaces and disks as comments, and the rendered machine config. Every later command reads those addresses back out of the file, so you never retype them. See [Node files](../configuration/node-files.md) for the file's anatomy and how to add per-node overrides.

## 3. Apply the config

```bash
talm apply -f nodes/node1.yaml -i
```

`-i` (`--insecure`) is the maintenance-mode connection used for a node that has no config yet. Drop it once the node is configured.

Before anything is sent, `apply` checks that every link and disk your config names actually exists on the node and prints a diff of what is about to change — see [Apply-time safety gates](../operations/safety-gates.md).

## 4. Day-to-day commands

Preview what an apply would change, without applying it:

```bash
talm apply -f nodes/node1.yaml --dry-run
```

Upgrade the node:

```bash
talm upgrade -f nodes/node1.yaml
```

`talm upgrade` resolves the target installer image from `values.yaml::image` (the cluster-wide knob). To pick the new version, bump `values.yaml::image` and re-run `talm upgrade -f nodes/<name>.yaml`; there is no need to re-template the node files first. Pass `--image <ref>` to override per-invocation (e.g. for an experimental installer build); the flag wins over the `values.yaml` lookup.

Re-template a node file in place after editing templates or values (this overwrites it):

```bash
talm template -f nodes/node1.yaml -I
```

## Next steps

- [Node files](../configuration/node-files.md) — per-node overrides below the modeline.
- [Templates and values](../configuration/templates.md) — editing templates, `lookup`, `--set-string`.
- [Encryption](../configuration/encryption.md) — keeping secrets out of Git.
- [CLI reference](../reference/index.md) — generated from the command tree.
