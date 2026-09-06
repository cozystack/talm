# Templates and values

You're free to edit template files in the project's `templates/` directory — the one [`talm init`](../operations/upgrading.md) writes alongside the vendored library chart.

All the [Helm](https://helm.sh/docs/chart_template_guide/functions_and_pipelines/) and [Sprig](https://masterminds.github.io/sprig/) functions are supported, including lookup for talos resources!

Lookup function example:

=== "Template"

    ```helm
    {{ lookup "nodeaddresses" "network" "default" }}
    ```

=== "Equivalent talosctl call"

    ```bash
    talosctl get nodeaddresses --namespace=network default
    ```

Querying disks map example:

```helm
{{ range .Disks }}{{ if .system_disk }}{{ .device_name }}{{ end }}{{ end }}
```

This returns the system disk device name.

## `--set` vs `--set-string` for IP / version literals

Helm's `--set` parser interprets dots in the right-hand side as YAML key nesting:

```bash
talm template --set endpoint=10.0.0.1   # parsed as: endpoint: {10: {0: {0: 1}}}
```

For IP addresses, CIDR blocks, or version strings, use `--set-string` to keep the value verbatim:

```bash
talm template --set-string endpoint=10.0.0.1   # parsed as: endpoint: "10.0.0.1"
```

`talm` warns on stderr when it detects an IP-, CIDR-, or version-shaped value in `--set` and points at `--set-string` as the fix. The warning is non-fatal — rendering proceeds with the (likely-broken) nested map so existing automation does not break. For values containing characters Helm's strvals treats specially (e.g. `=`, `,` inside the value, or content that should be opaque to all parsing), use `--set-literal` — it stores the entire RHS as a verbatim string without any escape interpretation.
