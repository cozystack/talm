# Initializing a project

`talm init` creates a project directory, vendors the preset and library charts into it, and writes the files every later command reads. This page covers the flags that shape that first run.

```bash
talm init -p cozystack -N myawesomecluster
```

!!! warning "`--image` and `--cluster-endpoint` apply on the first `init` only"

    Both flags write into `values.yaml` at creation time. On an existing project they are not re-applied — edit `values.yaml` directly instead.

## Re-initializing inside an existing project

`talm init` refuses to run when the current directory is inside an existing talm project (it would otherwise walk up and partially overwrite the parent). To create a project under the current directory anyway — e.g. a sub-project nested inside another talm project — pass `--root .` explicitly. To re-initialise the parent itself, run `talm init` from the parent directory.

## Pinning the installer image

To pin a specific Talos installer image at init time (e.g. a [Talos Factory](https://factory.talos.dev/) image with extensions), pass `--image`:

```bash
talm init -p cozystack -N myawesomecluster --image factory.talos.dev/installer/<sha256>:<version>
```

`--image` rewrites the top-level `image:` field in the preset's `values.yaml` before write. The `cozystack` preset declares `image:`; the `generic` preset does not, so `--image --preset generic` is rejected up front.

## Setting the control-plane URL

To set the Kubernetes control-plane URL at init time, pass `--cluster-endpoint`:

```bash
talm init -p cozystack -N myawesomecluster --cluster-endpoint https://vip.example.test:6443
```

`--cluster-endpoint` writes the URL into `values.yaml::endpoint`, which the chart renders into `cluster.controlPlane.endpoint` of every node's MachineConfig (the URL kubelet and kube-proxy dial).

## `--endpoints` vs `--cluster-endpoint`

`--endpoints` and `--cluster-endpoint` address different concepts: `--endpoints` (plural, list) populates the `talosconfig` context for the talosctl client; `--cluster-endpoint` (singular, full URL) populates the Kubernetes control-plane address inside the chart. When `--endpoints` is given a single value, init auto-derives `values.yaml::endpoint` as `https://<that>:6443` — the single-target case is unambiguous. Multi-endpoint inputs never auto-derive (picking one node would silently couple cluster availability to it); the operator must pass `--cluster-endpoint` explicitly or fill `values.yaml::endpoint` later. The init flow prints a hint at the end when the field is left empty.

## Filling the endpoint by hand

Edit `values.yaml` to set your cluster's control-plane endpoint if neither flag set it. This is the URL every node's kubelet and kube-proxy will dial. The chart leaves it empty by default so a missed override fails loudly instead of silently embedding a placeholder.

See [Endpoints and VIPs](../configuration/endpoints-and-vips.md) for how `endpoint` interacts with `floatingIP` and `vipLink`.
