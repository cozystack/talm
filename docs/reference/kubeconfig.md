# talm kubeconfig

Download the admin kubeconfig from the node

## Synopsis

Download the admin kubeconfig from the node.
If merge flag is true, config will be merged with ~/.kube/config.
Otherwise, kubeconfig will be written to PWD.

```
talm kubeconfig [flags]
```

## Options

```
  -f, --file strings                specify config files or patches in a YAML file (can specify multiple)
  -F, --force                       Force overwrite of kubeconfig if already present, force overwrite on kubeconfig merge
      --force-context-name string   Force context name for kubeconfig merge
  -h, --help                        help for kubeconfig
  -l, --login                       update system kubeconfig file, not local one
  -m, --merge                       Merge with existing kubeconfig (default true)
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

