# Installation

## Homebrew
For macOS and Linux users, the recommended way to install talm is with Homebrew.

```bash
brew install talm
```

## Binary

Download binary from Github [releases page](https://github.com/cozystack/talm/releases/latest)

Or use simple script to install it:
```bash
curl -sSL https://github.com/cozystack/talm/raw/refs/heads/main/hack/install.sh | sh -s
```

## Windows

Windows is supported. Download the `talm-windows-*.zip` archive from the [releases page](https://github.com/cozystack/talm/releases/latest) and extract `talm.exe`. On Windows, template paths passed to the `-t` / `--template` flag accept either `\` or `/` separators, so `-t templates\controlplane.yaml` and `-t templates/controlplane.yaml` are equivalent. Other path flags (`--talosconfig`, `-f` / `--file`) are delegated to the underlying OS file loader and follow standard Windows path rules.
