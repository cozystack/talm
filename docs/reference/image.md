# talm image

Manage container images

## Options

```
  -f, --file strings       specify config files or patches in a YAML file (can specify multiple)
  -h, --help               help for image
      --namespace string   namespace to use: "system" (etcd and kubelet images), "cri" for all Kubernetes workloads, "inmem" for in-memory containerd instance, "taloscontainers" for containers declared via ContainerConfig (default "cri")
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
* [talm image cache-cert-gen](image_cache-cert-gen.md)	 - Generate TLS certificates and CA patch required for securing image cache to Talos communication
* [talm image cache-create](image_cache-create.md)	 - Create a cache of images in OCI format into a directory
* [talm image cache-serve](image_cache-serve.md)	 - Serve an OCI image cache directory over HTTP(S) as a container registry
* [talm image integration](image_integration.md)	 - List the integration images used by k8s in Talos
* [talm image k8s-bundle](image_k8s-bundle.md)	 - List the default Kubernetes images used by Talos
* [talm image list](image_list.md)	 - List images in the machine's container runtime
* [talm image pull](image_pull.md)	 - Pull an image into the machine's container runtime
* [talm image remove](image_remove.md)	 - Remove an image from the machine's container runtime
* [talm image talos-bundle](image_talos-bundle.md)	 - List the default system images and extensions used for Talos

