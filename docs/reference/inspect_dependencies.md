# talm inspect dependencies

Inspect controller-resource dependencies as graphviz graph.

## Synopsis

Inspect controller-resource dependencies as graphviz graph.

Pipe the output of the command through the "dot" program (part of graphviz package)
to render the graph:

    talosctl inspect dependencies | dot -Tpng > graph.png


```
talm inspect dependencies [flags]
```

## Options

```
  -f, --file strings     specify config files or patches in a YAML file (can specify multiple)
  -h, --help             help for dependencies
      --with-resources   display live resource information with dependencies
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

* [talm inspect](inspect.md)	 - Inspect internals of Talos

