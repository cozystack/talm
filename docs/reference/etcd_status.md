# talm etcd status

Get the status of etcd cluster member

## Synopsis

Returns the status of etcd member on the node, use multiple nodes to get status of all members.

```
talm etcd status [flags]
```

## Options

```
  -f, --file strings   specify config files or patches in a YAML file (can specify multiple)
  -h, --help           help for status
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

* [talm etcd](etcd.md)	 - Manage etcd

