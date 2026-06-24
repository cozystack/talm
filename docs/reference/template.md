# talm template

Render templates locally and display the output

## Synopsis

Render Talos configuration templates locally.

Multi-file invocation note: unlike `talm apply` (where the first -f
anchors and later -f files are stacked side-patches),
`talm template -f a.yaml -f b.yaml -f c.yaml` renders each file
independently driven by its own modeline. This is intentional —
template's per-file render mirrors the "regenerate the artifact
beside the source" workflow, while apply's chain models "compose a
single MachineConfig and apply it once".

```
talm template [flags]
```

## Options

```
      --debug                       show only rendered patches
  -f, --file .yaml                  node config files for in-place update (.yaml / `.yml`; shell completion narrows to these extensions). Each file's modeline drives the per-file render.
      --full                        show full resulting config, not only patch
  -h, --help                        help for template
  -I, --in-place                    re-template and update generated files in place (overwrite them)
  -i, --insecure                    template using the insecure (encrypted with no auth) maintenance service
      --kubernetes-version string   desired kubernetes version to run (default "1.37.0")
      --offline                     disable gathering information and lookup functions
      --set stringArray             set values on the command line (can specify multiple or separate values with commas: key1=val1,key2=val2). Values that parse as an integer or a boolean are converted to that type; use --set-string to keep them as strings.
      --set-file stringArray        set values from respective files specified via the command line (can specify multiple or separate values with commas: key1=path1,key2=path2)
      --set-json stringArray        set JSON values on the command line (can specify multiple or separate values with commas: key1=jsonval1,key2=jsonval2)
      --set-literal stringArray     set a literal STRING value on the command line
      --set-string stringArray      set STRING values on the command line (can specify multiple or separate values with commas: key1=val1,key2=val2). Nothing is type-converted, so the value reaches the template exactly as typed.
      --show-secrets                print values from encrypted value files (*.encrypted.yaml) verbatim in stdout output (default: redacted to ***; never affects -I, which always omits them). Counterpart on apply is --show-secrets-in-drift, which governs the same values in apply's drift preview.
      --talos-version string        the desired Talos version to generate config for (backwards compatibility, e.g. v0.8)
  -t, --template strings            specify templates to render manifest from (can specify multiple)
      --values strings              specify values in a YAML file (can specify multiple)
      --with-secrets string         use a secrets file generated using 'gen secrets'
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

