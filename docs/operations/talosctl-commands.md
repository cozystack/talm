# talosctl-compatible commands

Talm offers a similar set of commands to those provided by talosctl. However, you can specify the `--file` option for them, so the node and endpoint addresses come from the node file instead of being retyped on every invocation.

For example, to run a dashboard for three nodes:

```bash
talm dashboard -f nodes/node1.yaml -f nodes/node2.yaml -f nodes/node3.yaml
```

!!! warning "`-f` means something different here than it does for `apply`"

    On a wrapped talosctl command each `-f` names another **target**: the example above opens one dashboard covering three nodes. On [`talm apply`](side-patches.md) the first `-f` is the anchor and every later one is a side-patch merged onto it, so passing three node files there applies one node, not three.

## Commands talm does not wrap

Every talosctl command is re-exported except these, which talm either replaces or does not need:

| Command | Why |
| --- | --- |
| `apply-config` | talm has its own [`apply`](safety-gates.md) |
| `config` | talm manages project configuration through `Chart.yaml` and `values.yaml` |
| `talosconfig` | talm has its own `talosconfig` command |
| `patch` | superseded by templates and [side-patches](side-patches.md) |
| `upgrade-k8s` | out of scope; talm configures machines, not the Kubernetes control plane |
| `dmesg` | retired upstream — use `talm logs kernel --tail=N` |

## `talm reset` — META-preserving default

`talm reset` diverges from upstream `talosctl reset` on one default. Upstream defaults to `--wipe-mode=all`, which wipes the Talos META partition along with STATE and EPHEMERAL — the node cannot self-recover and comes up in maintenance mode requiring a full re-apply. Talm instead populates `--system-labels-to-wipe=STATE,EPHEMERAL` when neither `--wipe-mode` nor `--system-labels-to-wipe` was passed, which preserves META so the node rejoins the cluster from its META-stored bootstrap config on the next boot.

Explicit operator intent is honored unchanged. In the examples below `$NODE` is the node being reset and `$OTHER_NODE` is a surviving control-plane node to talk to, since the node under reset stops answering partway through:

```bash
# talm default — preserves META, node self-recovers.
talm reset --reboot --graceful=true --nodes $NODE --endpoints $OTHER_NODE

# Explicit destructive opt-in (upstream's default).
talm reset --wipe-mode=all --reboot --nodes $NODE --endpoints $OTHER_NODE

# Operator-specified narrower scope is honored byte-for-byte.
talm reset --system-labels-to-wipe=STATE --reboot --nodes $NODE --endpoints $OTHER_NODE
```
