# talm completion

Generate the autocompletion script for the specified shell

## Synopsis

Generate the autocompletion script for talm for the specified shell.
See each sub-command's help for details on how to use the generated script.


## Options

```
  -h, --help   help for completion
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
* [talm completion bash](completion_bash.md)	 - Generate the autocompletion script for bash
* [talm completion fish](completion_fish.md)	 - Generate the autocompletion script for fish
* [talm completion powershell](completion_powershell.md)	 - Generate the autocompletion script for powershell
* [talm completion zsh](completion_zsh.md)	 - Generate the autocompletion script for zsh

