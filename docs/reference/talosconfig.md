# talm talosconfig

Regenerate talosconfig with new client certificates

## Synopsis

Regenerate talosconfig from secrets.yaml with fresh client certificates.

This command:
1. Decrypts talosconfig if encrypted version exists
2. Regenerates client certificates from secrets.yaml
3. Preserves endpoints and nodes from existing config
4. Re-encrypts if encryption is used

Use this command when your client certificate has expired.

```
talm talosconfig [flags]
```

## Options

```
  -h, --help   help for talosconfig
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

