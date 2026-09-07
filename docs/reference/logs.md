# talm logs

Retrieve logs for a service

```
talm logs <service name> [flags]
```

## Options

```
  -f, --file strings   specify config files or patches in a YAML file (can specify multiple)
  -F, --follow         specify if the logs should be streamed
  -h, --help           help for logs
  -k, --kubernetes     use the k8s.io containerd namespace
      --tail int32     lines of log file to display (default is to show from the beginning) (default -1)
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

