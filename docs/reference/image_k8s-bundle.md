# talm image k8s-bundle

List the default Kubernetes images used by Talos

```
talm image k8s-bundle [flags]
```

## Options

```
      --coredns-version semver                 CoreDNS semantic version (default v1.14.4)
      --etcd-version semver                    ETCD semantic version (default v3.6.12)
  -f, --file strings                           specify config files or patches in a YAML file (can specify multiple)
      --flannel-version semver                 Flannel CNI semantic version (default 0.28.7)
  -h, --help                                   help for k8s-bundle
      --k8s-version semver                     Kubernetes semantic version (default v1.36.2)
      --kube-network-policies-version semver   kube-network-policies semantic version (default v1.1.0)
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

