# talm apply

Apply config to a Talos node

## Synopsis

Apply rendered configuration to a Talos node.

Multi-file invocation (anchor + side-patches): the FIRST -f file is the
anchor — it must carry a "# talm: nodes=[…], templates=[…]" modeline and
live under a project root (Chart.yaml + secrets.yaml). Subsequent -f
files are side-patches stacked on top of the anchor's rendered config in
the order they appear; each is merged via the same overlay mechanism the
anchor's node body uses. A single ApplyConfiguration is issued per node
with the composed result.

Examples:

  # Single node file (anchor only):
  talm apply -f nodes/cp01.yaml

  # Stacked side-patches (e.g. one-shot debug overlay):
  talm apply -f nodes/cp01.yaml -f /tmp/debug-kubelet.yaml

  # Side-patches do NOT need to live under the project root; the
  # first file anchors detection. Reversing the order is an error
  # (the orphan path has no project to anchor on).

```
talm apply [flags]
```

## Options

```
      --cert-fingerprint strings                    list of server certificate fingeprints to accept (defaults to no check)
      --debug                                       show only rendered patches
      --dry-run                                     check how the config change will be applied in dry-run mode
  -f, --file .yaml                                  node config files / patches (.yaml / `.yml`; shell completion narrows to these extensions). First -f is the modelined anchor (must live under a `talm init`'d project root); subsequent -f files are side-patches stacked onto the anchor's rendered config and may live anywhere.
      --force                                       will overwrite existing files
  -h, --help                                        help for apply
  -i, --insecure                                    apply using the insecure (encrypted with no auth) maintenance service
      --kubernetes-version string                   desired kubernetes version to run (default "1.36.2")
  -m, --mode auto, no-reboot, reboot, staged, try   apply config mode (default auto)
      --set stringArray                             set values on the command line (can specify multiple or separate values with commas: key1=val1,key2=val2). For IP / CIDR / version literals use --set-string — dots in --set values are interpreted as YAML key nesting.
      --set-file stringArray                        set values from respective files specified via the command line (can specify multiple or separate values with commas: key1=path1,key2=path2)
      --set-json stringArray                        set JSON values on the command line (can specify multiple or separate values with commas: key1=jsonval1,key2=jsonval2)
      --set-literal stringArray                     set a literal STRING value on the command line
      --set-string stringArray                      set STRING values on the command line (can specify multiple or separate values with commas: key1=val1,key2=val2). Use for IP addresses, CIDR blocks, version strings, or any literal value where dots must NOT be interpreted as YAML key nesting.
      --show-secrets-in-drift                       show secret-bearing field values verbatim in drift preview / post-apply verify output (default: redacted). Covers both the Talos bootstrap allowlist (cluster.token, cluster.ca.key, machine.token, Wireguard private keys, etc.) and values from encrypted value files (*.encrypted.yaml). Counterpart on template is --show-secrets, which governs the same values in template's stdout render.
      --skip-drift-preview                          skip the pre-apply diff of on-node vs rendered MachineConfig
      --skip-post-apply-verify                      skip the post-apply structural verification of on-node vs sent MachineConfig (default skip until the Talos-mutated field allowlist lands) (default true)
      --skip-resource-validation                    skip the pre-apply check that declared host resources (links, disks) exist on the target node
      --talos-version string                        the desired Talos version to generate config for (backwards compatibility, e.g. v0.8)
      --timeout duration                            the config will be rolled back after specified timeout (if try mode is selected) (default 1m0s)
      --values talm template                        specify values in a YAML file (can specify multiple). Must match talm template — apply re-renders from the modeline and would otherwise drop value files supplied at template time.
      --with-secrets string                         use a secrets file generated using 'gen secrets'
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

