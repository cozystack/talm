# talm reset

Reset a node

```
talm reset [flags]
```

## Options

```
      --debug                                    debug operation from kernel logs. --wait is set to true when this flag is set
  -f, --file strings                             specify config files or patches in a YAML file (can specify multiple)
      --graceful                                 if true, attempt to cordon/drain node and leave etcd (if applicable) (default true)
  -h, --help                                     help for reset
      --reboot                                   if true, reboot the node after resetting instead of shutting down
      --system-labels-to-wipe strings            wipe selected system disk partitions by label, keeping others intact (talm default when no wipe flag is set: STATE,EPHEMERAL)
      --timeout duration                         time to wait for the operation is complete if --debug or --wait is set (default 30m0s)
      --user-disks-to-wipe strings               if set, wipes defined devices in the list
      --wait                                     wait for the operation to complete, tracking its progress. always set to true when --debug is set (default true)
      --wipe-mode all, system-disk, user-disks   disk reset mode (talm default: --system-labels-to-wipe=STATE,EPHEMERAL preserves META so the node self-recovers; pass --wipe-mode=all or --wipe-mode=system-disk explicitly for upstream's destructive behaviour — both destroy META) (default all)
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

