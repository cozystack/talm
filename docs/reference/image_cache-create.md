# talm image cache-create

Create a cache of images in OCI format into a directory

## Synopsis

Create a cache of images in OCI format into a directory

```
talm image cache-create [flags]
```

## Examples

```
talosctl images cache-create --images=ghcr.io/siderolabs/kubelet:v1.36.2 --image-cache-path=/tmp/talos-image-cache

Alternatively, stdin can be piped to the command:
talosctl images default | talosctl images cache-create --image-cache-path=/tmp/talos-image-cache --images=-

```

## Options

```
      --cosign-signatures               pull and cache cosign signatures for images (default true)
  -f, --file strings                    specify config files or patches in a YAML file (can specify multiple)
      --force                           force overwrite of existing image cache
  -h, --help                            help for cache-create
      --image-cache-path string         directory to save the image cache in OCI format
      --image-layer-cache-path string   directory to save the image layer cache
      --images strings                  images to cache
      --insecure                        allow insecure registries
      --layout string                   Specifies the cache layout format: "oci" for an OCI image layout directory, or "flat" for a registry-like flat file structure (default "oci")
      --platform strings                platform(s) to cache (e.g. linux/amd64,linux/arm64), or "all" to cache every platform in the image index (default [linux/amd64])
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

