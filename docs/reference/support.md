# talm support

Dump debug information about the cluster

## Synopsis

Generated bundle contains the following debug information:

- For each node:

	- Kernel logs.
	- All Talos internal services logs.
	- All kube-system pods logs.
	- Talos COSI resources without secrets.
	- COSI runtime state graph.
	- Processes snapshot.
	- IO pressure snapshot.
	- Mounts list.
	- PCI devices info.
	- Talos version.

- For the cluster:

	- Kubernetes nodes and kube-system pods manifests.

By default, the generated bundle is encrypted using age encryption to the list of recipients
set by the members of the 'siderolabs' GitHub organization. The encrypted bundle by default will
only be decryptable by the Sidero Labs team, but you can also specify additional recipients using the
--encryption-recipients flag, or disable encryption completely using the --no-encryption flag.
Default encryption recipients can be removed by setting --encryption-no-default-recipients flag.


```
talm support [flags]
```

## Options

```
      --cert-fingerprint strings            list of server certificate fingerprints to accept (defaults to no check, only used with --insecure flag)
      --encryption-no-default-recipients    do not encrypt to the default recipients, only to the ones provided via --encryption-recipients
      --encryption-recipients stringArray   additional age recipients (SSH or age public keys) to encrypt the support bundle to (can be specified multiple times)
  -f, --file strings                        specify config files or patches in a YAML file (can specify multiple)
  -h, --help                                help for support
  -i, --insecure                            use the insecure (encrypted with no auth) maintenance service
      --no-encryption                       do not encrypt the support bundle (output is written as-is)
  -w, --num-workers int                     number of workers per node (default 1)
  -O, --output string                       output file to write support archive to
  -v, --verbose                             verbose output
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

