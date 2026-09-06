# talm image cache-cert-gen

Generate TLS certificates and CA patch required for securing image cache to Talos communication

## Synopsis

Generate TLS certificates and CA patch required for securing image cache to Talos communication

```
talm image cache-cert-gen [flags]
```

## Options

```
      --advertised-address ipSlice   The addresses to advertise. (default [])
      --advertised-name strings      The DNS names to advertise.
  -f, --file strings                 specify config files or patches in a YAML file (can specify multiple)
  -h, --help                         help for cache-cert-gen
      --tls-ca-file string           TLS certificate authority file (default "ca.crt")
      --tls-cert-file string         TLS certificate file to use for serving (default "tls.crt")
      --tls-key-file string          TLS key file to use for serving (default "tls.key")
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

