# Keeping charts in sync after a binary upgrade

`talm init` **vendors** its preset and library charts into the project directory — the preset templates plus a copy of the talm library chart under `charts/talm/`:

```text
mycluster/
├── Chart.yaml
├── values.yaml
├── .talm-preset.lock   # pinned preset baseline — commit it
├── templates/          # preset templates — you own and edit these
└── charts/
    └── talm/           # talm library chart — vendored from the binary
        ├── Chart.yaml
        └── templates/_helpers.tpl
```

That is what `init` vendors, not the whole project — `nodes/`, `secrets.yaml` and `talosconfig` land there too. `.talm-preset.lock` records which preset the project was built from so a later binary upgrade can detect [preset drift](#preset-drift); commit it so the baseline is shared across the team.

Render commands (`template`, `apply`, `upgrade`) read this **local** copy, never the binary's built-in charts. That makes a project self-contained and reproducible — but it also means upgrading the `talm` binary does not touch `charts/talm/`. The vendored library stays frozen at whatever version last ran `init`, so a binary upgrade can leave you rendering with stale chart logic.

Re-sync the vendored library with `talm init --update`:

```bash
talm init --update --preset <your-preset>
```

This refreshes `charts/talm/` (always) and offers to update the preset files — the templates, `Chart.yaml` and `values.yaml` — one at a time, interactively, since you may have edited them. Secrets and node files are left untouched.

To catch drift automatically, a release build compares the vendored `charts/talm/` against its own built-in copy on every config-loading command. The comparison is by **content**, not version number — re-vendoring after a binary bump that did not change the library is a no-op and raises no warning. When the content genuinely differs, talm prints a non-fatal warning to stderr (stdout and the exit code are unchanged):

```text
WARN: project's vendored charts/talm/ library differs from the copy built into talm <version> (modified: templates/_helpers.tpl); run `talm init --update --preset <preset>` to re-sync (or ignore if this is intentional)
```

The remediation needs the preset name because `talm init --update` resolves the preset from `Chart.yaml`, which an init'd project does not record — pass `--preset <your-preset>` (the one you ran `talm init` with) explicitly.

Teams that want this enforced can turn the warning into a hard error (exit 1): set `strictCharts: true` in `Chart.yaml` so the whole team and CI inherit it, or pass `--strict-charts` for a single run. Strict mode applies to every config-loading command, including read-only ones such as `talm get` — run `talm init --update --preset <preset>`, or drop the flag / unset `strictCharts`, to unblock. Strict mode also escalates a check that cannot run at all — an unreadable `charts/talm/` or a corrupted `.talm-preset.lock` — into the same hard error, where the default behaviour degrades it to a `WARN: could not check drift` line: an unverifiable baseline passing silently would defeat the enforcement. A *missing* baseline (no `charts/talm/`, no `.talm-preset.lock`) blocks under strict for the same reason — deleting the baseline must not be a quieter bypass than corrupting it — while staying silent without strict, so projects generated before baseline pinning are not nagged. The check stays silent for `dev`/source builds, whose embedded charts are a moving target the developer controls.

## Version keys a project must pin

A project created before the shipped presets carried version pins has `templateOptions.talosVersion` and `templateOptions.kubernetesVersion` empty in its `Chart.yaml`. On a talm built against Talos v1.14 or newer, that combination stops the render rather than guessing:

```text
templateOptions.kubernetesVersion is not set
```

The line carries a prefix naming the step that hit it, and there are three: `serializing original config bundle:` on a default render, `serializing patched config bundle:` on a `--full` render or an apply that goes through templates, and `serializing configuration:` on an apply of a plain patch file. Grep for the part above rather than the whole line.

A project pinned at or below `v1.13` with an empty `kubernetesVersion` renders without error, but no longer pins component images: each node then uses the Kubernetes version of its own Talos release, and the render warns on stderr.

Node files never carried those images — a default render diffs against the same bundle, so they cancel out — but the config an apply sends did, because an earlier talm substituted its own built-in version for an empty key. In the drift preview this shows up as five `-` lines for the kubelet and control-plane images, and after the apply those components follow the node's own Talos release. If that release is older than the talm that last wrote the config, it is a downgrade. If it is newer, the same mechanism quietly upgrades the cluster's Kubernetes — the more common direction after a Talos bump. Read the preview before applying, or pin the key.

Pin both keys to what the cluster actually runs: `talosVersion` no newer than the Talos on the nodes, `kubernetesVersion` the release the control plane runs. [Talos versions and output format](../configuration/talos-versions.md) explains what each key selects and why an unpinned value stopped being safe.

Both presets now pin `talosVersion: "v1.14"`, generic with `kubernetesVersion: "v1.37.0"` and cozystack with `kubernetesVersion: "v1.36.5"` and a Talos v1.14.2 install image, so their embedded hashes changed: an existing project that pins a baseline in `.talm-preset.lock` will report preset drift until it is re-synced. That is the mechanism described below, and `talm init --update --preset <preset>` re-syncs both at once — but read this before running it.

It rewrites `Chart.yaml` from the preset, keeping only the project's `name` (`version` becomes the talm binary's own), so the preset's pins replace whatever the project used. A v1.14 contract is only valid for nodes already running Talos v1.14 or newer, and a different `kubernetesVersion` moves the control plane on the next apply, while a move **down** is not something Kubernetes supports. Set both keys back to what your cluster runs rather than keeping whatever the re-sync wrote, and copy any other settings back too. Once the nodes run Talos v1.14 and the project pins it, re-render the node files with `talm template -I`, after saving any hand-written body below the modeline since `-I` rewrites it: files rendered on an earlier contract carry v1alpha1 Kubernetes fields the v1.14 render refuses. The same run offers the preset's `values.yaml` too, and on cozystack its `image` now points at the Talos v1.14.2 build: decline that change, or restore your own `image` and values afterwards, unless the nodes are meant to move to it. The command also refuses to run non-interactively without `--force`.

## Preset drift

The vendored library (`charts/talm/`) is not the whole story. `talm init` also copies the **preset** — the `templates/` that render your machine config (sysctls, etcd args, the cozystack opinions) — into the project, and those you are *expected* to edit. That makes content comparison the wrong tool: it would flag every legitimate customization. So the preset is tracked differently. At `init` (and `init --update`) time talm pins the hash of the preset **as shipped** into `.talm-preset.lock`:

```yaml
preset: cozystack
presetHash: <hash of the preset built into the binary at init time>
```

A release build then compares the binary's *current* preset hash against that pinned baseline — never against your edited `templates/` — so operator customizations are never reported as drift. When a newer binary ships changed preset defaults, the baseline no longer matches and talm warns:

```text
WARN: project's cozystack preset differs from the copy built into talm <version>; run `talm init --update --preset cozystack` to pull the new preset defaults; it asks before it overwrites each preset file whole, so decline the files you have edited and merge their changes by hand
```

`talm init --update --preset <preset>` asks `File <path> differs from template. Overwrite? [y/N]` for each preset file that differs, shows no diff, and replaces the file whole when you accept. Decline the files you have edited, compare them with the preset by hand (the preset is in `charts/<preset>/` of the talm source at the binary's version), and merge what you want. The run advances the baseline either way, which clears the warning even if you decline every file. `--force` accepts every file without asking. `--strict-charts` / `strictCharts: true` escalate this to a hard error exactly as for the library. Projects with no `.talm-preset.lock` (generated before preset pinning) stay silent — there is no baseline to compare — unless strict mode is on, which treats a missing baseline as a blocker. Commit `.talm-preset.lock` so the baseline is shared across your team.
