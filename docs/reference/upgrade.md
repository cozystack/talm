# talm upgrade

Upgrade Talos on the target node

## Synopsis

Upgrade Talos on the target node(s).

Image resolution (when -f is provided):
  - --image <ref>         takes precedence and is used as-is.
  - otherwise, talm reads `image:` from values.yaml at the
    project root and passes it as the upgrade target. Bumping
    values.yaml::image is the canonical "raise the cluster's
    Talos version" workflow — re-running `talm template` to
    refresh node files first is NOT required.

The first -f file anchors the project root (Chart.yaml +
secrets.yaml); its modeline supplies the nodes / endpoints. The
node body's machine.install.image is no longer consulted by the
upgrade flow.

Post-upgrade sync (when the upgrade succeeds):
  - talm point-patches machine.install.image in every -f node body
    to the image that was applied. Keeps the body consistent with
    the running node — without this a follow-up `talm apply` would
    merge the stale install.image over the chart-rendered new value
    and silently pin the cluster back to the pre-upgrade image on
    the next A/B boot. Comments, modeline, and unrelated keys are
    preserved (yaml.v3 node round-trip); files without an
    install.image key (orphans / side-patches) are silently skipped.
  - The patch fires after post-upgrade verify confirms the running
    version matches the target. A failed verify (auto-rollback)
    intentionally leaves the body untouched, so it still reflects
    what the node actually runs.

```
talm upgrade [flags]
```

## Options

```
      --debug                                    debug operation from kernel logs. --wait is set to true when this flag is set
      --drain                                    drain the Kubernetes node before rebooting (cordon + evict pods) (default true)
      --drain-timeout duration                   timeout for draining the Kubernetes node (default 5m0s)
  -f, --file strings                             specify config files or patches in a YAML file (can specify multiple)
  -h, --help                                     help for upgrade
  -i, --image string                             the container image to use for performing the install (default "ghcr.io/siderolabs/installer:v1.13.7")
      --legacy                                   force use of legacy upgrade method
      --namespace string                         namespace to use: "system" (etcd and kubelet images), "cri" for all Kubernetes workloads, "inmem" for in-memory containerd instance (default "system")
      --no-reboot                                do not reboot the node after upgrade (skip reboot and drain)
      --post-upgrade-reconcile-window duration   how long to wait after upgrade returns before re-reading the running version; widen for slow hardware / large image pulls (default 1m30s)
      --progress string                          output mode for upgrade progress. Values: [auto plain] (default "auto")
  -m, --reboot-mode string                       select the reboot mode during upgrade. Mode "powercycle" bypasses kexec. Values: [default force powercycle] (default "default")
      --skip-post-upgrade-verify                 skip the post-upgrade check that compares running Talos version against the target image's tag (detects silent A/B rollback after the RPC acks success)
      --timeout duration                         time to wait for the operation is complete if --debug or --wait is set (default 30m0s)
      --wait                                     wait for the operation to complete, tracking its progress. always set to true when --debug is set (default true)
```

## Options inherited from parent commands

```
      --cluster string       Cluster to connect to if a proxy endpoint is used.
      --context string       Context to be used in command
  -e, --endpoints strings    override default endpoints in Talos configuration
      --nodes strings        target the specified nodes
      --root string          root directory of the project (default ".")
      --skip-verify          skip TLS certificate verification (keeps client authentication)
      --strict-charts        fail if the project's vendored charts/talm/ or pinned preset baseline differs from the talm binary (run talm init --update --preset <preset> to re-sync)
      --talosconfig string   The path to the Talos configuration file. Defaults to 'TALOSCONFIG' env variable if set, otherwise '$HOME/.talos/config' and '/var/run/secrets/talos.dev/config' in order.
      --version              Print the version number of the application
```

## SEE ALSO

* [talm](index.md)	 - Manage Talos the GitOps Way!

