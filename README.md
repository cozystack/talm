# Talm

Manage Talos the GitOps Way!

Talm is just like Helm, but for Talos Linux

## Features

While developing Talm, we aimed to achieve the following goals:

- **Automatic Discovery**: In a bare-metal environment, each server may vary slightly in aspects such as disks and network interfaces. Talm enables discovery of node information, which is then used to generate patches.

- **Ease of Customization**: You can customize templates to create your unique configuration based on your environment. The templates use the standard Go templates syntax, enhanced with widely-known Helm templating logic.

- **GitOps Friendly**: The patches generated do not contain sensitive data, allowing them to be stored in Git in an unencrypted, open format. For scenarios requiring complete configurations, the `--full` option allows the obtain a complete config that can be used for matchbox and other solutions.

- **Simplicity of Use**: You no longer need to pass connection options for each specific server; they are saved along with the templating results into a separate file. Per-node configuration sits in a single modeline-annotated `nodes/<name>.yaml`; the first `-f` file is the anchor and any subsequent `-f` files stack onto its rendered config as side-patches (see [Applying with side-patches](https://talm.cozystack.io/operations/side-patches/)).

- **Compatibility with talosctl**: We strive to maintain compatibility with the upstream project in patches and configurations. The configurations you obtain can be used with the official tools like talosctl and Omni.


## Installation

### Homebrew
For macOS and Linux users, the recommended way to install talm is with Homebrew.


```bash
brew install talm
```

### Binary

Download binary from Github [releases page](https://github.com/cozystack/talm/releases/latest)

Or use simple script to install it:
```bash
curl -sSL https://github.com/cozystack/talm/raw/refs/heads/main/hack/install.sh | sh -s
```

### Windows

Windows is supported. Download the `talm-windows-*.zip` archive from the [releases page](https://github.com/cozystack/talm/releases/latest) and extract `talm.exe`. On Windows, template paths passed to the `-t` / `--template` flag accept either `\` or `/` separators, so `-t templates\controlplane.yaml` and `-t templates/controlplane.yaml` are equivalent. Other path flags (`--talosconfig`, `-f` / `--file`) are delegated to the underlying OS file loader and follow standard Windows path rules.

## Documentation

Full documentation lives at **[talm.cozystack.io](https://talm.cozystack.io/)**.

- [Quickstart](https://talm.cozystack.io/getting-started/quickstart/) — from an empty directory to a configured node.
- [Initializing a project](https://talm.cozystack.io/getting-started/init/) — `--image`, `--cluster-endpoint`, `--root`.
- [Node files](https://talm.cozystack.io/configuration/node-files/) — the modeline, discovery comments, per-node overrides.
- [Endpoints and VIPs](https://talm.cozystack.io/configuration/endpoints-and-vips/) — `endpoint`, `floatingIP`, `vipLink`.
- [Talos versions and output format](https://talm.cozystack.io/configuration/talos-versions/) — which schema a render targets, and the version keys to pin.
- [Templates and values](https://talm.cozystack.io/configuration/templates/) — `lookup`, `--set` vs `--set-string`.
- [Encryption](https://talm.cozystack.io/configuration/encryption/) — age-encrypted secrets and user values.
- [Applying with side-patches](https://talm.cozystack.io/operations/side-patches/) — the `-f` chain.
- [Apply-time safety gates](https://talm.cozystack.io/operations/safety-gates/) — what `apply` and `upgrade` check.
- [talosctl-compatible commands](https://talm.cozystack.io/operations/talosctl-commands/) — what talm wraps and what it does not.
- [Upgrading talm](https://talm.cozystack.io/operations/upgrading/) — keeping vendored charts in sync.
- [CLI reference](https://talm.cozystack.io/reference/) — commands and flags.

The site is built from `docs/` in this repository; the CLI reference under `docs/reference/` is generated from the command tree.

## License

Apache-2.0, except for two files ported from [siderolabs/talos](https://github.com/siderolabs/talos), which is MPL-2.0: `pkg/engine/talos_helpers.go` and `pkg/commands/talos_client.go` carry Talos code that v1.14 stopped exporting, so they stay under MPL-2.0 and say so in their headers. MPL-2.0 section 3.3 covers distributing the combined work under Apache-2.0.
