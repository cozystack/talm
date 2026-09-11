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

## `talm meta` — `--insecure` kept reachable

Talos v1.14 registers `meta`'s `--insecure` on that command's local flag set instead of its persistent one. A local flag on a command that only hosts subcommands reaches nothing: not the subcommands, and not the command itself, which takes no arguments. So `talosctl meta write --insecure` stopped parsing on v1.14, and so did `talosctl meta --insecure write`.

That is the only way to write a META key over the maintenance service, which is what you do before a node has a machine config, so talm re-publishes such flags as persistent on its wrapper. `talm meta write --insecure` and the `-i` shorthand keep working, and `--cert-fingerprint` travels with them.

Reported upstream as [siderolabs/talos#14346](https://github.com/siderolabs/talos/issues/14346) and fixed by [siderolabs/talos#14347](https://github.com/siderolabs/talos/pull/14347), which is merged to `main` but not backported to the v1.14 branch. Once the fix ships in a Talos release talm depends on, the wrapper step becomes redundant and is dropped.
## `talm apply` — `--mode=reboot` is gone on Talos v1.14

Upstream stopped registering `reboot` among the values of the apply mode flag in v1.14, so `talm apply --mode=reboot` fails with `invalid argument "reboot" for "-m, --mode" flag`. Use `--mode=auto`, which the node promotes to a reboot when the change requires one. Shell completion reads the accepted values back from the flag, so it no longer suggests `reboot` either.

## `talm upgrade` — `--insecure` is gone on Talos v1.14

Upstream stopped registering `--insecure` on `upgrade` in v1.14 (it had been deprecated before that), so `talm upgrade --insecure` fails with `unknown flag: --insecure`. Upgrading a node that has no valid configuration is done by booting a maintenance image and applying a fresh config. The post-upgrade version verify consequently has no maintenance case left to skip.

## `talm reset` — `--insecure` is gone on Talos v1.14

Upstream dropped `--insecure` from `reset` in v1.14, so `talm reset --insecure` fails with `unknown flag: --insecure`. Resetting a node that has no valid configuration is done from the maintenance side instead: boot the node into a maintenance image and apply a fresh config, rather than resetting over an unauthenticated connection.
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
