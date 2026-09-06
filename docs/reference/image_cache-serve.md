# talm image cache-serve

Serve an OCI image cache directory over HTTP(S) as a container registry

## Synopsis

Serve an OCI image cache directory over HTTP(S) as a container registry

```
talm image cache-serve [flags]
```

## Options

```
      --address string            address to serve the registry on (default "127.0.0.1:3172")
  -f, --file strings              specify config files or patches in a YAML file (can specify multiple)
  -h, --help                      help for cache-serve
      --image-cache-path string   directory to save the image cache in flat format
      --mirror strings            list of registry mirrors to add to the Talos config patch (default [docker.io,ghcr.io,registry.k8s.io])
      --tls-cert-file string      TLS certificate file to use for serving
      --tls-key-file string       TLS key file to use for serving
```

## Options inherited from parent commands

```
      --cluster string       Cluster to connect to if a proxy endpoint is used.
      --context string       Context to be used in command
  -e, --endpoints strings    override default endpoints in Talos configuration
      --namespace string     namespace to use: "system" (etcd and kubelet images), "cri" for all Kubernetes workloads, "inmem" for in-memory containerd instance (default "cri")
      --nodes strings        target the specified nodes
      --root string          root directory of the project (default ".")
      --skip-verify          skip TLS certificate verification (keeps client authentication)
      --strict-charts        fail if the project's vendored charts/talm/ or pinned preset baseline differs from the talm binary (run talm init --update --preset <preset> to re-sync)
      --talosconfig string   The path to the Talos configuration file. Defaults to 'TALOSCONFIG' env variable if set, otherwise '$HOME/.talos/config' and '/var/run/secrets/talos.dev/config' in order.
      --version              Print the version number of the application
```

## SEE ALSO

* [talm image](image.md)	 - Manage container images

