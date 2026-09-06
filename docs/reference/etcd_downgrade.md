# talm etcd downgrade

Manage etcd storage system downgrades

## Options

```
  -f, --file strings   specify config files or patches in a YAML file (can specify multiple)
  -h, --help           help for downgrade
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
* [talm etcd downgrade cancel](etcd_downgrade_cancel.md)	 - Cancel etcd storage system downgrade.
* [talm etcd downgrade enable](etcd_downgrade_enable.md)	 - Enable etcd storage system downgrade to the specified version.
* [talm etcd downgrade validate](etcd_downgrade_validate.md)	 - Validate if the etcd storage system can be downgraded to the specified version.

