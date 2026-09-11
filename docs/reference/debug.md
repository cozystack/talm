# talm debug

Run a debug container from an image archive or reference

```
talm debug [<image-tar-path|image ref>] [flags]
```

## Examples

```
  # Run a debug container from a local tar archive (image will be loaded into Talos from the archive)
    talosctl debug ./debug-tools.tar --args /bin/sh

  # Run a debug container from an image reference (Talos will pull the image if not present)
    talosctl debug docker.io/library/alpine:latest --args /bin/sh
```

## Options

```
      --args strings       arguments to pass to the container
  -f, --file strings       specify config files or patches in a YAML file (can specify multiple)
  -h, --help               help for debug
      --namespace system   namespace to use: system (CRI containerd) or `inmem` for in-memory containerd instance (default "inmem")
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

