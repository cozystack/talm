# talm shutdown

Shutdown a node

```
talm shutdown [flags]
```

## Options

```
      --debug              debug operation from kernel logs. --wait is set to true when this flag is set
  -f, --file strings       specify config files or patches in a YAML file (can specify multiple)
      --force              if true, force a node to shutdown without a cordon/drain
  -h, --help               help for shutdown
      --timeout duration   time to wait for the operation is complete if --debug or --wait is set (default 30m0s)
      --wait               wait for the operation to complete, tracking its progress. always set to true when --debug is set (default true)
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

