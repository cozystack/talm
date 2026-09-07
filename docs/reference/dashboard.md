# talm dashboard

Cluster dashboard with node overview, logs and real-time metrics

## Synopsis

Provide a text-based UI to navigate node overview, logs and real-time metrics.

Keyboard shortcuts:

 - h, <Left> - switch one node to the left
 - l, <Right> - switch one node to the right
 - j, <Down> - scroll logs/process list down
 - k, <Up> - scroll logs/process list up
 - <C-d> - scroll logs/process list half page down
 - <C-u> - scroll logs/process list half page up
 - <C-f> - scroll logs/process list one page down
 - <C-b> - scroll logs/process list one page up


```
talm dashboard [flags]
```

## Options

```
  -f, --file strings               specify config files or patches in a YAML file (can specify multiple)
  -h, --help                       help for dashboard
  -d, --update-interval duration   interval between updates (default 3s)
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

