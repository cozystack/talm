# talm reboot

Reboot a node

```
talm reboot [flags]
```

## Options

```
      --debug                    debug operation from kernel logs. --wait is set to true when this flag is set
      --drain                    drain the Kubernetes node before rebooting (cordon + evict pods)
      --drain-timeout duration   timeout for draining the Kubernetes node (default 5m0s)
  -f, --file strings             specify config files or patches in a YAML file (can specify multiple)
  -h, --help                     help for reboot
  -m, --mode string              select the reboot mode during upgrade. Mode "powercycle" bypasses kexec. Values: [default force powercycle] (default "default")
      --progress string          output mode for upgrade progress. Values: [auto plain] (default "auto")
      --timeout duration         time to wait for the operation is complete if --debug or --wait is set (default 30m0s)
      --wait                     wait for the operation to complete, tracking its progress. always set to true when --debug is set (default true)
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

