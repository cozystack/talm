# talm wipe pv

Remove an LVM physical volume label

## Synopsis

Wipe LVM metadata from a block device.

The PV must not be part of an active volume group; remove the VG first with "talosctl wipe vg".

The argument is a full device path, e.g. /dev/sda1.

```
talm wipe pv <device> [flags]
```

## Options

```
      --cert-fingerprint strings   list of server certificate fingerprints to accept (defaults to no check, only used with --insecure flag)
  -f, --file strings               specify config files or patches in a YAML file (can specify multiple)
  -h, --help                       help for pv
  -i, --insecure                   use the insecure (encrypted with no auth) maintenance service
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

* [talm wipe](wipe.md)	 - Wipe block device or volumes

