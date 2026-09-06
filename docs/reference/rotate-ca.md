# talm rotate-ca

Rotate cluster CAs (Talos and Kubernetes APIs).

## Synopsis

Rotates Talos and/or Kubernetes root Certificate Authorities.

This command must be run against a SINGLE control-plane node. The specified node
will be used to coordinate the CA rotation across the entire cluster.

The command works by:
1. Auto-discovering all cluster nodes (control-plane and workers) from Kubernetes API
2. Generating new CA certificates
3. Gracefully rolling out the new CAs to all nodes
4. Updating local configs (talosconfig, secrets.yaml, kubeconfig)

IMPORTANT: You must specify exactly ONE control-plane node via --endpoints/-e or --nodes
flags, or through a single config file (-f). The node must be a control-plane node.

By default, both Talos API CA and Kubernetes API CA are rotated. Use --talos=false
or --kubernetes=false to rotate only one of them.

The command runs in dry-run mode by default. Use --dry-run=false to perform actual rotation.

```
talm rotate-ca [flags]
```

## Examples

```
  # Dry-run CA rotation (recommended first step)
  talm rotate-ca -f nodes/controlplane-1.yaml

  # Actually perform the rotation
  talm rotate-ca -f nodes/controlplane-1.yaml --dry-run=false

  # Rotate only Talos API CA
  talm rotate-ca -f nodes/controlplane-1.yaml --kubernetes=false --dry-run=false

  # Rotate only Kubernetes API CA
  talm rotate-ca -f nodes/controlplane-1.yaml --talos=false --dry-run=false
```

## Options

```
      --control-plane-nodes strings   specify IPs of control plane nodes
      --dry-run                       dry-run mode (no changes to the cluster) (default true)
  -f, --file strings                  specify config files or patches in a YAML file (can specify multiple)
  -h, --help                          help for rotate-ca
      --init-node string              specify IPs of init node
      --k8s-endpoint string           use endpoint instead of kubeconfig default
      --kubernetes                    rotate Kubernetes API CA (default true)
  -o, --output talosconfig            path to the output new talosconfig (default "talosconfig")
      --talos                         rotate Talos API CA (default true)
      --with-docs                     patch all machine configs adding the documentation for each field
      --with-examples                 patch all machine configs with the commented examples
      --worker-nodes strings          specify IPs of worker nodes
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

