# talm etcd

Manage etcd

## Options

```
  -f, --file strings   specify config files or patches in a YAML file (can specify multiple)
  -h, --help           help for etcd
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
* [talm etcd alarm](etcd_alarm.md)	 - Manage etcd alarms
* [talm etcd defrag](etcd_defrag.md)	 - Defragment etcd database on the node
* [talm etcd downgrade](etcd_downgrade.md)	 - Manage etcd storage system downgrades
* [talm etcd forfeit-leadership](etcd_forfeit-leadership.md)	 - Tell node to forfeit etcd cluster leadership
* [talm etcd leave](etcd_leave.md)	 - Tell nodes to leave etcd cluster
* [talm etcd members](etcd_members.md)	 - Get the list of etcd cluster members
* [talm etcd remove-member](etcd_remove-member.md)	 - Remove the node from etcd cluster
* [talm etcd snapshot](etcd_snapshot.md)	 - Stream snapshot of the etcd node to the path.
* [talm etcd status](etcd_status.md)	 - Get the status of etcd cluster member

