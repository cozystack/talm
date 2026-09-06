# talm cgroups

Retrieve cgroups usage information

## Synopsis

The cgroups command fetches control group v2 (cgroupv2) usage details from the machine.
Several presets are available to focus on specific cgroup subsystems:

* cpu
* cpuset
* io
* memory
* process
* swap

You can specify the preset using the --preset flag.

Alternatively, a custom schema can be provided using the --schema-file flag.
To see schema examples, refer to https://github.com/siderolabs/talos/tree/main/cmd/talosctl/cmd/talos/cgroupsprinter/schemas.


```
talm cgroups [flags]
```

## Options

```
  -f, --file strings         specify config files or patches in a YAML file (can specify multiple)
  -h, --help                 help for cgroups
      --preset string        preset name (one of: [cpu cpuset io memory process psi swap])
      --schema-file string   path to the columns schema file
      --skip-cri-resolve     do not resolve cgroup names via a request to CRI
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

