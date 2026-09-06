# talm init

Initialize a new project and generate default values

```
talm init [flags]
```

## Options

```
      --cluster-endpoint string   Kubernetes control-plane URL written to values.yaml::endpoint (e.g. https://10.0.0.1:6443 or https://vip.example.test:6443). Takes precedence over the single-endpoint auto-derive heuristic; required for multi-control-plane setups where the operator picks a VIP or load balancer.
  -d, --decrypt                   decrypt all encrypted files (does not require preset)
  -e, --encrypt                   encrypt all sensitive files (secrets.yaml, talosconfig, kubeconfig, values-secret.yaml)
      --endpoints strings         override default endpoints in Talos configuration
      --force                     overwrite existing files; on --update also auto-accepts every preset-template diff without the interactive prompt
  -h, --help                      help for init
      --image string              override the Talos installer image written to the preset's values.yaml (e.g. factory.talos.dev/installer/<sha256>:<version>)
  -N, --name string               cluster name (not required with --encrypt, --decrypt, or --update)
  -p, --preset string             preset for file generation (not required with --encrypt, --decrypt, or --update)
      --talos-version string      the desired Talos version to generate config for (backwards compatibility, e.g. v0.8)
  -u, --update                    update Talm library chart
```

## Options inherited from parent commands

```
      --cluster string       Cluster to connect to if a proxy endpoint is used.
      --context string       Context to be used in command
      --nodes strings        target the specified nodes
      --root string          root directory of the project (default ".")
      --skip-verify          skip TLS certificate verification (keeps client authentication)
      --strict-charts        fail if the project's vendored charts/talm/ or pinned preset baseline differs from the talm binary (run talm init --update --preset <preset> to re-sync)
      --talosconfig string   The path to the Talos configuration file. Defaults to 'TALOSCONFIG' env variable if set, otherwise '$HOME/.talos/config' and '/var/run/secrets/talos.dev/config' in order.
      --version              Print the version number of the application
```

## SEE ALSO

* [talm](index.md)	 - Manage Talos the GitOps Way!

