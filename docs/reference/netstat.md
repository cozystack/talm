# talm netstat

Show network connections and sockets

## Synopsis

Show network connections and sockets.

You can pass an optional argument to view a specific pod's connections.
To do this, format the argument as "namespace/pod".
Note that only pods with a pod network namespace are allowed.
If you don't pass an argument, the command will show host connections.

```
talm netstat [flags]
```

## Options

```
  -a, --all            display all sockets states (default: connected)
  -x, --extend         show detailed socket information
  -f, --file strings   specify config files or patches in a YAML file (can specify multiple)
  -h, --help           help for netstat
  -4, --ipv4           display only ipv4 sockets
  -6, --ipv6           display only ipv6 sockets
  -l, --listening      display listening server sockets
  -k, --pods           show sockets used by Kubernetes pods
  -p, --programs       show process using socket
  -w, --raw            display only RAW sockets
  -t, --tcp            display only TCP sockets
  -o, --timers         display timers
  -u, --udp            display only UDP sockets
  -U, --udplite        display only UDPLite sockets
  -v, --verbose        display sockets of all supported transport protocols
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

