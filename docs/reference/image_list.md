# talm image list

List images in the machine's container runtime

```
talm image list [flags]
```

## Options

```
  -f, --file strings   specify config files or patches in a YAML file (can specify multiple)
  -h, --help           help for list
```

## Options inherited from parent commands

```
      --cluster string       Cluster to connect to if a proxy endpoint is used.
      --context string       Context to be used in command
  -e, --endpoints strings    override default endpoints in Talos configuration
      --namespace string     namespace to use: "system" (etcd and kubelet images), "cri" for all Kubernetes workloads, "inmem" for in-memory containerd instance, "taloscontainers" for containers declared via ContainerConfig (default "cri")
      --nodes strings        target the specified nodes
      --root string          root directory of the project (default ".")
      --skip-verify          skip TLS certificate verification (keeps client authentication)
      --strict-charts        fail if the project's vendored charts/talm/ or pinned preset baseline differs from the talm binary (run talm init --update --preset <preset> to re-sync)
      --talosconfig string   The path to the Talos configuration file. Defaults to 'TALOSCONFIG' env variable if set, otherwise '$HOME/.talos/config' and '/var/run/secrets/talos.dev/config' in order.
      --version              Print the version number of the application
```

## SEE ALSO

* [talm image](image.md)	 - Manage container images

