# talm edit

Edit Talos node machine configuration with the default editor.

## Synopsis

The edit command allows you to directly edit the machine configuration
of a Talos node using your preferred text editor.

It will open the editor defined by your TALOS_EDITOR,
or EDITOR environment variables, or fall back to 'vi' for Linux
or 'notepad' for Windows.

```
talm edit machineconfig [flags]
```

## Options

```
      --dry-run                             do not apply the change after editing and print the change summary instead
  -f, --file strings                        specify config files or patches in a YAML file (can specify multiple)
  -h, --help                                help for edit
  -m, --mode auto, no-reboot, staged, try   apply config mode (default auto)
      --namespace string                    resource namespace (default is to use default namespace per resource)
      --timeout duration                    the config will be rolled back after specified timeout (if try mode is selected) (default 1m0s)
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

