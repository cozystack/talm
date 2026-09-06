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

## `--set` vs `--set-string`

`--set` converts a value that parses as an integer or a boolean into that type:

```bash
talm template --set port=8080       # port: 8080     (integer)
talm template --set enabled=true    # enabled: true  (boolean)
```

Use `--set-string` where the template or the Talos schema wants a string:

```bash
talm template --set-string port=8080   # port: "8080"
```

Dots only nest on the left of the `=`, so `--set a.b=x` sets `a.b` while a dotted value stays whole: `--set endpoint=10.0.0.1` renders `endpoint: "10.0.0.1"`, and IP addresses, CIDR blocks and version strings need no special handling.

For values containing characters Helm's strvals treats specially (e.g. `=`, `,` inside the value, or content that should be opaque to all parsing), use `--set-literal` — it stores the entire RHS as a verbatim string without any escape interpretation.
