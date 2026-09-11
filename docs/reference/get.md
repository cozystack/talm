# talm get

Get a specific resource or list of resources (use 'talosctl get rd' to see all available resource types).

## Synopsis

Similar to 'kubectl get', 'talosctl get' returns a set of resources from the OS.
To get a list of all available resource definitions, issue 'talosctl get rd'

```
talm get <type> [<id>] [flags]
```

## Options

```
      --cert-fingerprint strings   list of server certificate fingerprints to accept (defaults to no check, only used with --insecure flag)
  -f, --file strings               specify config files or patches in a YAML file (can specify multiple)
  -h, --help                       help for get
  -i, --insecure                   use the insecure (encrypted with no auth) maintenance service
      --namespace string           resource namespace (default is to use default namespace per resource)
  -o, --output string              output mode (json, table, yaml, jsonpath) (default "table")
  -w, --watch                      watch resource changes
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

