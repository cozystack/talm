# talm manual test plan

A QA-oriented matrix for exercising `talm` end-to-end against a real Talos cluster. Designed to surface bugs that unit + contract tests miss — encoding edge cases, real-disk topology quirks, multi-node interactions, recovery flows.

The narrow apply-safety-gates checklist lives at [`apply-safety-gates-test-plan.md`](./apply-safety-gates-test-plan.md); this document covers the whole CLI surface.

## How to use this plan

1. Build the binary under test:

   ```bash
   cd ~/git/github.com/cozystack/talm && go build -o /tmp/talm-safety ./
   ```

2. Have a reachable Talos cluster (3 controlplane nodes recommended so you can exercise reset / etcd-member-removal without losing quorum). A small OCI / cloud / bare-metal v1.12.x stand is enough.

3. Work through the sections below in order. Each section lists a command, the expected outcome, and the failure modes to watch for. **Regression anchors** at the bottom of some sections call out specific behaviours to assert against — formulated as forward-looking checks the operator runs, not as a retrospective of past findings.

4. After every destructive action (reset / wipe / rotate-ca) run the sanity-check block at the end of this document.

## Conventions

- `$NODE` is the node's reachable IP. Use the same value for `--nodes` and `--endpoints` unless you're exercising a multi-node bug.
- `$TALM_REPO` is `~/git/github.com/cozystack/talm`.
- `$PROJECT` is your talm project root (a directory with `Chart.yaml` and `nodes/`).
- `--dry-run` works on `apply` and `rotate-ca`. Use it first whenever the command can mutate cluster state.

## A. Project bootstrap

### A1. `talm init` from scratch

```bash
mkdir -p /tmp/talm-init-test && cd /tmp/talm-init-test
/tmp/talm-safety init --preset cozystack --name test-cluster \
  --endpoints https://192.0.2.1:6443
```

Expected: creates `Chart.yaml`, `charts/talm/`, `templates/`, `nodes/`, `secrets.yaml` + `secrets.encrypted.yaml`, `talosconfig` + `talosconfig.encrypted`, `talm.key`, `values.yaml`. Prints the "Security Information" banner reminding the operator to back up `talm.key`.

Watch for:

- Missing files in the listing.
- `talm.key` written without the security-information banner.
- `.gitignore` not updated.

### A2. `talm init` second run without `--force`

```bash
cd /tmp/talm-init-test
/tmp/talm-safety init --preset cozystack --name test-cluster
```

Expected: error citing each conflicting file, hint mentioning both `--force` and `--update`. Exit non-zero.

### A3. `talm init --update --preset cozystack` non-tty

```bash
cd $PROJECT
/tmp/talm-safety init --update --preset cozystack < /dev/null
```

Expected: hint-bearing error pointing at `--force`. NOT a raw `reading interactive overwrite confirmation: EOF`.

### A4. `talm init --update --preset cozystack --force` non-tty

```bash
cd $PROJECT
/tmp/talm-safety init --update --preset cozystack --force < /dev/null
```

Expected: one `Overwriting <path> (--force)` line per diff; no prompt; exit zero.

### A5. Encrypt / decrypt round-trip

```bash
cd /tmp/talm-init-test
/tmp/talm-safety init --decrypt
test -f secrets.yaml && test -f talosconfig
/tmp/talm-safety init --encrypt
test -f secrets.encrypted.yaml && test -f talosconfig.encrypted
```

Expected: per-file `Decrypting X -> Y` / `Encrypting X -> Y` lines; both round-trips succeed.

### A6. Decrypt without `talm.key`

```bash
cd /tmp/talm-init-test && mv talm.key /tmp/talm.key.backup
/tmp/talm-safety init --decrypt
mv /tmp/talm.key.backup talm.key
```

Expected: clear error mentioning the missing key path.

### A7. Cleanup

```bash
rm -rf /tmp/talm-init-test
```

**Regression anchor**: A6's error must reference the missing-key path explicitly. A bare `read key file: open ...: no such file or directory` with no follow-up hint is a UX regression — the operator should see a clear recovery path (`run \`talm init\` to generate a new key, or restore your backup`).

## B. Render / template

### B1. Happy-path render

```bash
cd $PROJECT
/tmp/talm-safety template -f nodes/node0.yaml | head -10
```

Expected: rendered MachineConfig YAML starting with the project modeline. Exit zero.

### B2. Render with CLI override

```bash
/tmp/talm-safety template -f nodes/node0.yaml \
  --set clusterDomain=overridden.local | grep dnsDomain
```

Expected: `dnsDomain: overridden.local` appears in output.

### B3. Render against missing file

```bash
/tmp/talm-safety template -f nodes/_doesnotexist.yaml
```

Expected: clear error with hint about the missing path. Exit non-zero.

### B4. In-place rewrite (`-I`)

```bash
cp nodes/node0.yaml /tmp/inplace-before.yaml
/tmp/talm-safety template -I -f nodes/node0.yaml
diff /tmp/inplace-before.yaml nodes/node0.yaml
cp /tmp/inplace-before.yaml nodes/node0.yaml  # restore
```

Expected: `Updated.` on stdout. The diff shows that the rendered body replaces the previous contents — **including any user-added comments**. This is by design (`-I` is rewrite, not merge), but operators often trip on it; note in your test report.

### B5. Render with stale chart preset

When the local `charts/talm/` is older than the talm binary's embedded preset, `talm template` succeeds against the local preset — it does NOT auto-bump. The operator must run `init --update`. Confirm by inspecting `talm version` against `Chart.yaml`.

**Regression anchor**: `template -I` is rewrite, not merge — verify by adding a `# my comment` line above the modeline in `nodes/node0.yaml`, running B4, and confirming the comment is GONE in the new body. If the comment survives, a behaviour change shipped (could be either an intentional new `--preserve-comments` flag or an undocumented merge mode — neither should appear silently).

## C. Apply (auth path)

The apply-safety gates are detailed in [`apply-safety-gates-test-plan.md`](./apply-safety-gates-test-plan.md). This section is the minimal smoke-test for the apply pipe itself.

### C1. Dry-run apply

```bash
/tmp/talm-safety apply --dry-run -f nodes/node0.yaml
```

Expected: drift-preview section, then `Dry run summary:` and the diff the apply would produce. Exit zero.

### C2. Real apply, no-reboot mode

```bash
/tmp/talm-safety apply --mode=no-reboot \
  --skip-post-apply-verify=false -f nodes/node0.yaml
```

Expected: drift preview, `Applied configuration without a reboot`. Phase 2B is silent on a clean apply.

### C3. Multi-file apply

```bash
/tmp/talm-safety apply --dry-run \
  -f nodes/node0.yaml -f nodes/node1.yaml -f nodes/node2.yaml
```

Expected: each node renders / diffs independently; per-node gate output sections.

### C4. Stage mode

```bash
/tmp/talm-safety apply --mode=staged --skip-post-apply-verify=false \
  -f nodes/node0.yaml
```

Expected: Phase 2B auto-skipped (staged config doesn't change ActiveID); output ends with `Staged configuration to be applied after the next reboot`.

### C5. Drift preview redacts secret-bearing fields by default

```bash
# Rotate machine.token by editing secrets.yaml (or any allowlisted path) then:
/tmp/talm-safety apply --dry-run -f nodes/node0.yaml
```

Expected: the drift preview line for `machine.token` reads `machine.token: ***redacted (len=N)*** -> ***redacted (len=M)***`. The literal `old-token-value` / `new-token-value` strings MUST NOT appear in stderr. Non-secret paths (e.g. `machine.network.hostname` if it changed) render verbatim.

Regression anchor: rotating any field in the allowlist (`cluster.{secret,token,aescbcEncryptionSecret,secretboxEncryptionSecret}`, `cluster.{ca,aggregatorCA,serviceAccount,etcd.ca}.key`, `cluster.acceptedCAs[].key`, `machine.{token,ca.key}`, `machine.acceptedCAs[].key`) MUST redact. A regression that silently leaks a secret value into stderr is a security-class bug — verify the substring with `grep -F` against the captured output.

### C6. Drift preview shows secrets with explicit opt-in

```bash
/tmp/talm-safety apply --dry-run --show-secrets-in-drift -f nodes/node0.yaml
```

Expected: same drift preview as C5, but the secret paths render verbatim — no `***redacted***` sentinel. Operator-explicit bypass for debugging.

Regression anchor: `--show-secrets-in-drift` is operator opt-in, never default. Verify by running `talm apply --help` and confirming the flag default is `false`.

### C7. Phase 1 walker rejects malformed net-addr fields before the RPC

When a rendered MachineConfig carries a malformed value in any of the new walker-covered fields, Phase 1 must block before the apply RPC fires:

- `StaticHostConfig.name` not a parseable IP literal (the `name` field on this kind is the IP the hostnames map to — Talos's schema does not have a separate `address` field).
- `NetworkRuleConfig.ingress[i].subnet` or `.except` not a parseable CIDR.
- `WireguardConfig.peers[i].endpoint` not a parseable host:port.

Hand-craft a chart that emits a bad value (e.g. `name: 999.999.0.1` on a `StaticHostConfig`, or `ingress: [{subnet: notacidr}]` on a `NetworkRuleConfig`, or `endpoint: notavalid:endpoint` on a Wireguard peer) and run `apply --dry-run`. Expected: Phase 1 emits a blocker citing the offending field path (`doc[N].name`, `doc[N].ingress[i].subnet` or `.except`, `doc[N].peers[i].endpoint`); exit non-zero before any RPC. Valid values (IPv4, IPv6, IPv6:port via `[host]:port`) pass through.

Regression anchor: empty / omitted endpoint on a Wireguard peer is NOT a finding — peers without endpoints are listener-only remote peers. Verify a chart with `endpoint: ""` passes Phase 1.

## D. Apply (insecure / maintenance path)

### D1. Apply with chart that uses discovery

```bash
/tmp/talm-safety apply -i --dry-run -f nodes/node0.yaml
```

Expected: render fails because `lookup "disks"` / `lookup "links"` require auth. Hint mentions reachability.

### D2. Drift-preview degrade on insecure path (when render succeeds)

When a chart renders fully offline (no `lookup`), `talm apply -i` runs through to the gates. Phase 2A prints `drift verification unavailable on maintenance connection` and proceeds; Phase 2B same.

**Regression anchor**: D2's offline-renderable behaviour is also covered by unit-level mocking — see `pkg/commands/preflight_apply_safety_test.go` for the in-process equivalent. Surface that file's tests in the manual suite when D2 is impractical to exercise live.

### D3. Per-node prefix on the maintenance-connection warning

On a multi-node insecure apply where every node hits the `ok=false` (maintenance) path, each per-node emission of the warning must carry the node identifier prefix so the operator can correlate which line came from which node:

```bash
/tmp/talm-safety apply -i \
  --nodes 192.0.2.10,192.0.2.11,192.0.2.12 \
  --endpoints 192.0.2.10,192.0.2.11,192.0.2.12 \
  -f nodes/node0.yaml
```

Expected (per node): `node 192.0.2.10: talm: drift verification unavailable on maintenance connection`. The single-node case (empty `nodeID`, the implicit path) MUST still emit the bare `talm: drift verification unavailable on maintenance connection` line — no `node : ` garbage prefix.

Regression anchor: a refactor that always-prefixes (`node : talm: ...` on single-node) is a UX regression. The `nodePrefix("")` helper must collapse to empty for the bare-line single-node case.

## E. Upgrade

### E1. Stage an upgrade to the same image

```bash
/tmp/talm-safety upgrade --stage -f nodes/node0.yaml
```

Note: `--stage --wait` (the default) actually triggers a reboot to apply the staged upgrade. Plan for a 1-2 minute outage of the node under test. The cluster should stay healthy if you have 3+ control plane nodes and other nodes hold quorum.

Expected: events stream from BOOTING through `post check passed`. Node returns to running state.

### E2. Upgrade with bad image

```bash
/tmp/talm-safety upgrade --image ghcr.io/cozystack/cozystack/talos:doesnotexist \
  --stage -f nodes/node0.yaml
```

Expected: `error validating installer image ... not found`. Talos itself catches this; talm passes through the error.

### E3. Configurable post-upgrade reconcile window

```bash
# Help-text surface — confirms the flag is registered with the 90s default.
/tmp/talm-safety upgrade --help | grep -A1 post-upgrade-reconcile-window

# Custom widened window (slow hardware / large image pulls).
/tmp/talm-safety upgrade --post-upgrade-reconcile-window=180s \
  --image ghcr.io/siderolabs/installer:v1.13.0 \
  --stage -f nodes/node0.yaml

# Rejection of non-positive values.
/tmp/talm-safety upgrade --post-upgrade-reconcile-window=0s \
  -f nodes/node0.yaml
```

Expected for the help line: flag listed with `default 1m30s`. Expected for the 180s run: stderr emits `post-upgrade verify: waiting 3m0s for the node to finish booting...` — Go's `time.Duration.String()` renders `180 * time.Second` as `3m0s` deterministically, not `180s`. Expected for the `0s` rejection: error with a hint mentioning "positive duration".

Regression anchor: the version-mismatch hint emitted on a Phase 2C blocker MUST NOT contain the literal string `90s` — operators passing a custom window would see contradictory advice. The hint should reference "the configured reconcile window (`--post-upgrade-reconcile-window`)" instead.

## F. CA rotation

### F1. Rotate CA dry-run

```bash
/tmp/talm-safety rotate-ca --dry-run --nodes $NODE --endpoints $NODE
```

Expected: every per-step line ends with `(dry-run)`; final line mentions `re-run with \`--dry-run=false\` to apply the changes`. Possibly trailing `failed to create new client with rotated Talos CA` — harmless under dry-run.

### F2. Real CA rotation

```bash
/tmp/talm-safety rotate-ca --dry-run=false --nodes $NODE --endpoints $NODE
```

Expected: `CA rotation completed successfully!`. `secrets.yaml`, `secrets.encrypted.yaml`, `talosconfig`, `kubeconfig` updated on disk.

### F3. Apply after rotation

```bash
/tmp/talm-safety apply --dry-run -f nodes/node0.yaml
```

Expected: works against the rotated CA. No `tls: certificate required` errors.

## G. META partition

### G1. Read / list

```bash
/tmp/talm-safety get metakey --nodes $NODE --endpoints $NODE
```

Expected: table of META keys with their values.

### G2. Write a test key

```bash
/tmp/talm-safety meta write 0x0a "test-value" --nodes $NODE --endpoints $NODE
/tmp/talm-safety get metakey 0x0a --nodes $NODE --endpoints $NODE
```

Expected: written; reads back the value.

### G3. Delete the test key

```bash
/tmp/talm-safety meta delete 0x0a --nodes $NODE --endpoints $NODE
/tmp/talm-safety get metakey 0x0a --nodes $NODE --endpoints $NODE
```

Expected: delete succeeds; read returns `NotFound`.

## H. Reset and recovery

### H1. Bootstrap on running cluster

```bash
/tmp/talm-safety bootstrap --nodes $NODE --endpoints $NODE
```

Expected: refuses with `etcd data directory is not empty`.

### H2. Reset a control-plane node (talm safe default — preserves META)

⚠️ Destructive. Run only against a cluster you can afford to lose one node from. The talm default populates `--system-labels-to-wipe=STATE,EPHEMERAL` automatically when neither `--wipe-mode` nor `--system-labels-to-wipe` was passed, so META survives and the node self-recovers on the next boot. Upstream `talosctl reset` defaults to `--wipe-mode=all`, which destroys META; that path is exposed in talm as the explicit `--wipe-mode=all` opt-in (see H2a).

```bash
/tmp/talm-safety reset --graceful=true --reboot \
  --nodes $NODE --endpoints $OTHER_NODE
```

Expected: etcd member departs (`talm etcd members` from another node shows 2 members), node reboots, `post check passed`. After the reboot the node returns to etcd as a fresh member with placeholder hostname `talos-XXXXX` within ~90s; the next `talm apply` refreshes the hostname.

Regression anchors:

- `talm reset --help` must show the talm-divergence note on both `--wipe-mode` ("preserves META") and `--system-labels-to-wipe` ("STATE,EPHEMERAL"). Without the help text, the default flip is invisible to operators reading the CLI surface.
- The reset request must succeed without the operator having to type `--system-labels-to-wipe` manually. If the node comes back in maintenance mode requiring fresh apply, the wrapper did not apply the safe default and META was wiped — that is a regression.

### H2a. Reset with explicit destructive opt-in (`--wipe-mode=all` or `--wipe-mode=system-disk`)

⚠️ Highly destructive — META wiped, node CANNOT self-recover and requires fresh apply against `--insecure` maintenance mode. Run only on a cluster where the multi-day re-bootstrap cost is acceptable.

Two opt-out values land in the same destructive server-side branch: `--wipe-mode=all` (full system disk + user disks) and `--wipe-mode=system-disk` (system disk only). Both bypass the safety override and wipe META. `--wipe-mode=user-disks` is safe — it doesn't touch system partitions.

```bash
# Equivalent destructive paths:
/tmp/talm-safety reset --wipe-mode=all --graceful=true --reboot \
  --nodes $NODE --endpoints $OTHER_NODE
/tmp/talm-safety reset --wipe-mode=system-disk --graceful=true --reboot \
  --nodes $NODE --endpoints $OTHER_NODE
```

Expected: same as H2 up to the reboot; after the reboot the node comes up in maintenance mode (no machine config). `talm get hostnames -i --nodes $NODE` succeeds via the insecure path but the node is not yet a cluster member.

Regression anchor: when EITHER of these commands is run the wrapper MUST NOT silently add `--system-labels-to-wipe=STATE,EPHEMERAL` (which would override the operator's stated intent and quietly turn a destructive reset into a selective one). Verify via `talm reset --wipe-mode=all --help` or by observing that the reset request actually destroys META.

### H2b. Reset with operator-specified narrower scope (`--system-labels-to-wipe=STATE` only)

```bash
/tmp/talm-safety reset --system-labels-to-wipe=STATE --graceful=true --reboot \
  --nodes $NODE --endpoints $OTHER_NODE
```

Expected: only STATE wiped, EPHEMERAL kept (containerd image cache survives the reset), node returns. The operator's explicit narrower list must be honored byte-for-byte; the wrapper MUST NOT silently expand to `STATE,EPHEMERAL`.

Regression anchor: after the node returns, `talm dmesg --nodes $NODE | grep -i ephemeral` should show no fresh-format markers for the EPHEMERAL partition. If the wrapper silently expanded the operator's list, EPHEMERAL would have been wiped too.

### H2c. Reset with `--graceful=false` (ungraceful, preserves safe default)

```bash
/tmp/talm-safety reset --graceful=false --reboot \
  --nodes $NODE --endpoints $OTHER_NODE
```

Expected: ungraceful reset (no etcd leave), but the wrapper's safe default still fires (STATE+EPHEMERAL labels populated by talm because no wipe flag was passed). Node reboots; etcd cluster recovers via remaining quorum; rejoining member appears within ~120s.

Regression anchor: the default-flip MUST be independent of `--graceful`. A change that conditions the flip on `--graceful=true` is a regression — operators on ungraceful reset are the ones who most need the safe default.

### H2d. Reset triggered from modeline-bearing project root

```bash
cd $PROJECT  # directory with nodes/$NODE.yaml carrying the modeline
/tmp/talm-safety reset --reboot --graceful=true
```

Expected: same outcome as H2 — modeline supplies `--nodes` / `--endpoints` from `nodes/$NODE.yaml`, no wipe flags on the CLI, wrapper applies the safe default, META preserved.

Regression anchor: the default-flip is gated on `Changed("wipe-mode") && Changed("system-labels-to-wipe")` only — it is independent of where in the PreRunE chain it runs. A refactor that reorders the dispatch chain must keep this path working (modeline-supplied `--nodes` / `--endpoints` plus no operator-supplied wipe flags must still produce the safe default).

### H3. Etcd quorum after reset

```bash
/tmp/talm-safety etcd members --nodes $OTHER_NODE --endpoints $OTHER_NODE
```

Expected during the reset: 2 of 3 members. After Talos brings the reset node back from META (typical Linux/Talos auto-rejoin path): 3 members; the reset node may carry a placeholder hostname (`talos-XXXXX`) until the next apply.

### H4. Rejoin after reset

```bash
/tmp/talm-safety apply --dry-run -f nodes/node-resetted.yaml
```

Expected: `0 addition, 0 removal, 0 update, N unchanged` when META preserved the full config; otherwise drift will reflect the missing state (re-apply to fix).

### H5. Insecure path on a freshly-wiped node

```bash
/tmp/talm-safety apply -i -f nodes/node-fresh.yaml
```

Expected: render error from `lookup "disks"` requiring auth, OR drift-preview degrade line if the chart is offline-renderable.

## I-pre. Cluster-wide diagnostics & helpers

### I0-1. Read-only command sweep

A non-destructive bake to confirm every wrapper returns within ~5s on a healthy 3-node cluster. Useful after every major refactor in `pkg/commands/talosctl_wrapper.go`.

```bash
NODE=$NODE
for cmd in version "get machineconfig -o yaml" containers processes \
           "health --server=false" "interfaces" "disks" "etcd members" \
           "list /system/state" memory mounts stats service cgroups \
           "dmesg --tail" netstat routes "usage /var/log" \
           "logs kubelet" "logs etcd" "events --tail=3" \
           "image list" "etcd status" "etcd alarm list"; do
  timeout 8 /tmp/talm-safety $cmd --nodes $NODE --endpoints $NODE 2>&1 | head -1
done
```

Expected: every command prints either a header row (table) or an error from the node side. None should hang past the timeout.

### I0-2. Concurrent dry-run apply

```bash
for i in 1 2 3; do
  /tmp/talm-safety apply --dry-run -f nodes/node0.yaml 2>&1 | grep -E "^talm:" &
done
wait
```

Expected: 3 independent renders, all complete, no race-condition diagnostics.

### I0-3. CLI nodes/endpoints override modeline

```bash
/tmp/talm-safety apply --dry-run \
  --nodes $OTHER_NODE --endpoints $OTHER_NODE \
  -f nodes/node0.yaml | grep "^- talm"
```

Expected: log line shows `nodes=[$OTHER_NODE]` not the modeline value. The CLI takes precedence.

### I0-4. Reboot a node (no config change)

```bash
/tmp/talm-safety reboot --nodes $NODE --endpoints $NODE
```

⚠️ Destructive timing — the node will be unreachable for ~30-60s. Cluster keeps quorum if at least one other controlplane is healthy.

Expected: returns once events check completes; etcd member list shows the node back in.

### I0-5. Wipe a non-system disk

```bash
/tmp/talm-safety wipe disk <devname> --nodes $NODE --endpoints $NODE
```

Expected: refuses with `FailedPrecondition: blockdevice "<dev>" is in use by disk "..."` if it's mounted / part of LVM / part of DRBD. Wipe succeeds only on truly idle block devices. The error itself is the regression pin: a wipe that DIDN'T refuse would risk destroying the cluster's persistent state.

## I. Shell completion

### I1. Generate completion for each shell

```bash
for sh in bash zsh fish powershell; do
  /tmp/talm-safety completion $sh > /tmp/talm-completion.$sh
  case $sh in
    bash|zsh) bash -n /tmp/talm-completion.$sh && echo "$sh OK" ;;
    *) echo "$sh: $(wc -l < /tmp/talm-completion.$sh) lines" ;;
  esac
done
```

Expected: every shell prints a script that parses (for bash/zsh syntax-check confirms). Non-zero output sizes.

## J-pre. Set / values / overlay variations

### J0-1. `--set` vs `--set-string` for IP-shaped values

```bash
/tmp/talm-safety template -f nodes/node0.yaml --set floatingIP=0700
```

Expected: with the post-#163 chart, fails fast with `talm: floatingIP "0700" is not a valid IPv4 / IPv6 literal`. Pre-#163 chart silently renders an invalid VIP.

**Operator footgun**: `--set floatingIP=198.51.100.1` *may* be parsed as the float `198.51 × 100.1` by Helm's loose type-coercion. For IPs use `--set-string floatingIP="198.51.100.1"` or put it in `values.yaml`.

### J0-2. `--set-literal` keeps dotted keys intact

```bash
/tmp/talm-safety template -f nodes/node0.yaml \
  --set-literal "label.with.dots=raw"
```

Expected: key `label.with.dots` (single literal) appears in values rather than nested `label → with → dots`.

### J0-3. `--set-file` reads file content as string

```bash
echo "from-file" > /tmp/_v.txt
/tmp/talm-safety template -f nodes/node0.yaml --set-file someKey=/tmp/_v.txt
rm /tmp/_v.txt
```

Expected: file content available as `.Values.someKey` during render.

### J0-4. `--values` external overlay

```bash
echo "clusterDomain: overlay.local" > /tmp/_overlay.yaml
/tmp/talm-safety template -f nodes/node0.yaml --values /tmp/_overlay.yaml
rm /tmp/_overlay.yaml
```

Expected: `dnsDomain: overlay.local` in rendered config.

## J. Read-only diagnostics (safe everywhere)

| Command | Expected |
| --- | --- |
| `talm version` | Client + Server tags + Go versions |
| `talm get links` | LinkStatus rows per node |
| `talm get disks` | Disk rows; check `transport`, `bus_path`, `rotational`, `cdrom`, `readonly` |
| `talm get metakey` | META keys |
| `talm get machineconfig` | Active MachineConfig (auth only — Sensitive) |
| `talm containers` | Talos system containers + Kubernetes pods |
| `talm processes` | PID list with CPU/RES mem |
| `talm health` | Cluster health summary |
| `talm interfaces` | Host network interfaces |
| `talm disks` | Block devices via talosctl wrapper |
| `talm etcd members` | etcd member list (auth only) |

Each command should return promptly (sub-second) for read-only paths.

## K. Cross-version upgrade

### K1. Preflight version-mismatch warning

```bash
# Bump talosVersion in Chart.yaml to one minor ahead of running.
sed -i 's|talosVersion: "v1.12"|talosVersion: "v1.13"|' Chart.yaml
/tmp/talm-safety apply --dry-run -f nodes/node0.yaml | head -5
```

Expected: `warning: pre-flight: configured talosVersion=v1.13 is newer than the node's running Talos v1.12.x` plus a hint about rebooting into a matching maintenance image or lowering the contract. Drift preview still runs.

### K1-pre. Phase 2C version-verify catches silent rollback

⚠️ Same destructive setup as K2, but the gate now does the work automatically. **Heads-up**: the target image lives in the node body (`nodes/<name>.yaml`'s `machine.install.image`), not in `values.yaml` — talm's upgrade wrapper reads it from the rendered config patch, not the chart values overlay (see #176).

Run an intentionally-bad cross-vendor upgrade and expect a hint-bearing blocker:

```bash
sed -i 's|cozystack/cozystack/talos:v1.12.6|siderolabs/installer:v1.13.0|' nodes/node0.yaml
talm upgrade -f nodes/node0.yaml
```

Expected: talosctl upgrade RPC returns success → "post-upgrade verify: waiting 1m30s for the node to finish booting..." → 90s reconcile window → `verifyPostUpgradeVersion` reads `runtime.Version` → detects mismatch → blocker:

```
post-upgrade: requested upgrade to v1.13.0 but running version is
v1.12.6 — either Talos auto-rolled back, or the node is still
booting beyond the 90s window
hint: two hypotheses produce this symptom: (1) Talos auto-rolled
back after the new partition failed its boot readiness check —
cross-vendor upgrades (e.g. cozystack-bundled image -> vanilla
siderolabs installer) drop bundled extensions and trigger this.
(2) The node is slower than the 90s reconcile window — large
image pulls or cold hardware can exceed it. Re-run `talm get
version` after a minute to distinguish: if the version updated,
the node was just slow; if it's still the old version, the
rollback case is real. Pass --skip-post-upgrade-verify to bypass.
```

`talm upgrade` exits non-zero — the operator sees the failure instead of a false "success".

Phase 2C is **skipped** for the following upgrade flows (each documented in the code):

- `--skip-post-upgrade-verify` (operator opt-out)
- `--insecure` (auth-only COSI path is unreachable)
- `--stage` (new partition not yet booted; runtime.Version would always report the old version — guaranteed false positive)

### K2-pre. Manual fallback for `--skip-post-upgrade-verify`

K1-pre exercises the automated Phase 2C gate. If the operator disables it (`--skip-post-upgrade-verify`) — or in flows that the gate doesn't cover (`--insecure`, `--stage`, no target image) — the equivalent manual check is:

```bash
target="v1.13.0"
talm upgrade --skip-post-upgrade-verify -f nodes/node0.yaml
running=$(talm get version --nodes $NODE --endpoints $NODE \
  -o jsonpath='{.spec.version}')
test "$running" = "$target" || echo "SILENT ROLLBACK / SLOW BOOT — running $running, expected $target"
```

This is the post-merge equivalent of what Phase 2C does automatically. Keep the script around — it's still relevant for the `--insecure` flow which the gate skips by design.

### K2. Stage-upgrade to a new minor

```bash
# In values.yaml, point image at the new installer:
sed -i 's|installer:v1.12.6|installer:v1.13.0|' values.yaml
/tmp/talm-safety upgrade --stage -f nodes/node0.yaml
```

Expected: events stream from `installAndReboot` through `post check passed`. Node returns running v1.13.x (`talm version --nodes $NODE`). Etcd member count unchanged (`talm etcd members`).

### K3. Per-node sequential upgrade (safe)

```bash
for n in node0 node1 node2; do
  /tmp/talm-safety upgrade --stage -f nodes/$n.yaml
  /tmp/talm-safety etcd members --nodes $OTHER --endpoints $OTHER \
    | grep -c "^[0-9]"  # quorum must be >= 2 at all times
done
```

Expected: each node returns to etcd within 60s; quorum never drops below 2/3 (one node down at a time).

### K4. Phase 2A drift preview against new-version node

After K2, with the chart still on v1.13 contract:

```bash
/tmp/talm-safety apply --dry-run -f nodes/node0.yaml
```

Expected: no version-mismatch warning (chart contract matches running). Drift preview shows the per-version diff if any (e.g. a new field machinery v1.13 injects).

### K5. Phase 2B on a heterogeneous cluster (mid-rollout)

Between K2-step-1 (node0 upgraded) and K2-step-2 (node1 still on old version), Phase 1 still resolves `lookup "links"` (non-Sensitive COSI resource works on both versions). Phase 2A diffs against the specific node, so the cross-version state is per-node, not cluster-wide. Phase 2B (if enabled) compares against the bytes sent; expect cert-hash false-positives until the allowlist lands (see open question in #172).

## L. Extended diagnostics + service control

### L1. `inspect dependencies` returns a DOT graph

```bash
/tmp/talm-safety inspect dependencies --nodes $NODE --endpoints $NODE | head
```

Expected: starts with `digraph {`. Useful for visualizing Talos controller deps. Pipe through `dot -Tpng` to render.

### L2. `pcap` short capture on loopback

```bash
timeout 8 /tmp/talm-safety pcap --nodes $NODE --endpoints $NODE \
  --interface lo --duration 2s > /tmp/_cap.pcap
file /tmp/_cap.pcap && rm /tmp/_cap.pcap
```

Expected: binary pcap stream to stdout. `file` reports "pcap capture file".

### L3. `time` against NTP

```bash
/tmp/talm-safety time --nodes $NODE --endpoints $NODE
```

Expected: table with `NTP-SERVER`, `NODE-TIME`, `NTP-SERVER-TIME`.

### L4. `etcd defrag`

```bash
/tmp/talm-safety etcd defrag --nodes $NODE --endpoints $NODE
```

Expected: silent return (no output), exit 0. DB is defragmented.

### L5. `etcd alarm list`

```bash
/tmp/talm-safety etcd alarm list --nodes $NODE --endpoints $NODE
```

Expected: empty output on a healthy cluster. Any output indicates an alarm to investigate (NOSPACE / CORRUPT).

### L6. `etcd forfeit-leadership` on a non-leader

```bash
/tmp/talm-safety etcd forfeit-leadership --nodes $NON_LEADER --endpoints $NODE
```

Expected: silent no-op. Leader unchanged.

### L7. `service kubelet restart`

```bash
/tmp/talm-safety service kubelet restart --nodes $NODE --endpoints $NODE
```

Expected: `Service "kubelet" restarted`. Pod replays after ~10s.

### L8. `service kubelet stop` (refused)

```bash
/tmp/talm-safety service kubelet stop --nodes $NODE --endpoints $NODE
```

Expected: `kubelet doesn't support stop operation via API`. Talos intentionally blocks stop on essential services.

### L9. `shutdown` (destructive)

⚠️ Powers off the node. Recovery requires `tofu apply` (or manual provider-side reboot). Use only against a node whose recovery path you control.

```bash
/tmp/talm-safety shutdown --nodes $TARGET --endpoints $OTHER
```

Expected: events stream through `teardownLifecycle` → `stopEverything` → `events check condition met`. Etcd member remains in the list until TTL expires (~10 min) or the next membership reconciliation.

### L10. `get rd` lists registered resource types

```bash
/tmp/talm-safety get rd --nodes $NODE --endpoints $NODE | wc -l
```

Expected: 100+ resource types. Baseline for `get <type>` calls.

### L11. `get -o jsonpath`

```bash
/tmp/talm-safety get hostname --nodes $NODE --endpoints $NODE \
  -o jsonpath='{.spec.hostname}'
```

Expected: the node's hostname as a bare string. Useful for scripted extraction.

### L12. `logs --tail N`

```bash
/tmp/talm-safety logs kubelet --tail 3 --nodes $NODE --endpoints $NODE
```

Expected: last 3 lines of kubelet log.

## M. Negative / boundary cases

### M1. Malformed modeline

```bash
echo "# talm: nodes=this-is-not-json-array" > /tmp/_bad.yaml
echo "machine: {type: controlplane}" >> /tmp/_bad.yaml
/tmp/talm-safety apply --dry-run -f /tmp/_bad.yaml
rm /tmp/_bad.yaml
```

Expected: `error parsing JSON array for key nodes` with a hint about the expected syntax.

### M2. Malformed patch (string-where-map)

```bash
cat > /tmp/_bad.yaml << 'EOF'
# talm: nodes=["192.0.2.4"], endpoints=["192.0.2.4"], templates=["templates/controlplane.yaml"]
machine:
  type: controlplane
  install: not-a-map-but-a-string
EOF
/tmp/talm-safety apply --dry-run -f /tmp/_bad.yaml
rm /tmp/_bad.yaml
```

Expected: `yaml: construct errors: cannot construct !!str ... into v1alpha1.InstallConfig` plus a hint about patch shape.

### M3. Bad `--cert-fingerprint`

```bash
/tmp/talm-safety apply --insecure --cert-fingerprint deadbeef \
  -f nodes/node0.yaml
```

Expected: TLS handshake error `leaf peer certificate doesn't match the provided fingerprints: [deadbeef]`.

### M4. `--talosconfig` pointing at missing file

```bash
/tmp/talm-safety apply --dry-run --talosconfig /tmp/nonexistent \
  -f nodes/node0.yaml
```

Expected: `talos config file is empty`. Apply does not proceed.

### M5. `TALOSCONFIG` env var

```bash
TALOSCONFIG=$PWD/talosconfig /tmp/talm-safety apply --dry-run \
  --talosconfig "" -f nodes/node0.yaml
```

Expected: same as native `--talosconfig $PWD/talosconfig`. Phase 2A drift preview runs normally.

### M6. Secret redaction false-positive guard (intentional rotation)

When an operator deliberately rotates a secret (e.g. `cluster.token` via `talm init --update`), the drift preview must render both sides as `***redacted (len=N)***` — same shape as C5. The control case lives here: confirm a "rotation" of a non-secret-shaped path adjacent to the allowlist (`cluster.tokenExtras`, `cluster.acceptedCAsExtras`, or a synthetic test path like `machine.network.hostname`) renders verbatim.

Expected: paths matching `cluster.token` → redacted; paths matching `cluster.tokenExtras` → verbatim. The path-segment-aware matcher must not false-positive on string-prefix overlap.

Regression anchor: a future regression to substring matching (`strings.HasPrefix(path, "cluster.token")`) would silently redact `cluster.tokenExtras` and other operator-visible fields that share a prefix. Verify by inspecting a chart with both shapes side-by-side.

### M7. Net-addr walker boundary cases

Walk the net-addr walker (C7) on the full boundary set:

- `StaticHostConfig.name: 2001:db8::1` — valid IPv6, passes.
- `StaticHostConfig.name: 192.0.2.999` — IPv4 with octet >255, blocks.
- `StaticHostConfig` with no `name` field — passes (Talos rejects at RPC with a clearer message about required fields).
- `NetworkRuleConfig.ingress: [{subnet: 192.0.2.0/24}, {subnet: 2001:db8::/32}]` — mixed IPv4 + IPv6 CIDRs, both pass.
- `NetworkRuleConfig.ingress: [{subnet: 192.0.2.0/24}, {subnet: notacidr}]` — one blocker per malformed entry; the count must equal exactly one.
- `NetworkRuleConfig.ingress: [{subnet: 192.0.2.0/24, except: notacidr}]` — `except` validated alongside `subnet`; malformed `except` blocks even when `subnet` is valid.
- `WireguardConfig.peers[].endpoint: "[2001:db8::1]:51820"` — bracketed IPv6:port, passes.
- `WireguardConfig.peers[].endpoint: ""` — listener-only peer, passes.
- `WireguardConfig.peers[].endpoint: example.invalid:51820` — hostname:port, blocks (hostnames must already be resolved in the rendered config).

Expected: per-entry findings count exactly, valid forms produce zero findings, and the per-finding `Reason` cites the field path with the bracket-normalised index (`peers[1].endpoint`, not `peers[].endpoint`).

Regression anchor: the no-overlap unit test `TestMultidocNetAddrHandlers_NoOverlapWithRefHandlers` pins the dispatch-map disjointness contract. A future entry that lands in BOTH `multidocHandlers` and `multidocNetAddrHandlers` produces double findings — verify via the unit suite before manual smokes.

## Sanity-check block

Run after every destructive section (E, F, H, and anything that touches `--mode=reboot` / `--mode=staged` / `apply -I`):

```bash
cd $PROJECT
for n in node0 node1 node2; do
  echo "=== $n ==="
  /tmp/talm-safety apply --dry-run -f nodes/$n.yaml | grep -E "^talm:"
done
/tmp/talm-safety etcd members --nodes $NODE --endpoints $NODE
/tmp/talm-safety health --nodes $NODE --endpoints $NODE
```

Expected: each node reports drift preview (typically `0/0/0 unchanged` after a clean run), etcd shows 3 members, health passes.

## Adversarial extras

These don't ship as part of the regular run but are worth re-running after refactors that touch the walker / differ / preflight hooks.

### Walker robustness

```bash
echo "" > /tmp/empty.yaml
/tmp/talm-safety apply --dry-run -f /tmp/empty.yaml
# Expected: modeline-prefix error.

cat > /tmp/modeline-only.yaml <<EOF
# talm: nodes=["$NODE"], endpoints=["$NODE"], templates=["templates/controlplane.yaml"]
EOF
/tmp/talm-safety apply --dry-run -f /tmp/modeline-only.yaml
# Expected: drift preview, possibly significant `-` removals.
```

### Schema confusion

Try every BondConfig / VLANConfig / BridgeConfig YAML field permutation that operators paste from old docs / unofficial gists. The walker must catch typos in:

- `BridgeConfig.links[]` (legacy docs say `ports[]` — Phase 1 must NOT treat `ports[]` as valid).
- `VLANConfig.parent` (legacy docs say `link` — same rule).

### Encoding edge cases

```bash
printf '\xef\xbb\xbfmachine:\r\n  install:\r\n    disk: /dev/sda\r\n' > /tmp/bom.yaml
/tmp/talm-safety apply --dry-run -f /tmp/bom.yaml
# Expected: no panic. Walker decodes the doc.
```

### Mode-gating

Walk every `--mode` value with `--skip-post-apply-verify=false`:

- `auto`, `no-reboot` → Phase 2B runs.
- `reboot`, `staged`, `try` → Phase 2B auto-skipped (each for a different documented reason).
- `--dry-run` always skips Phase 2B.

## Cleanup at end of session

```bash
# Restore any `_test-*.yaml` files left in nodes/
ls $PROJECT/nodes/_test-* 2>/dev/null && rm $PROJECT/nodes/_test-*
# Verify project tree is clean
cd $PROJECT && git status --short
```

The project should report no `_test-*.yaml` orphans and only the intentional changes (e.g. `Chart.yaml` and `charts/talm/*` updates from `init --update`).
