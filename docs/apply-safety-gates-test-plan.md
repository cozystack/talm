# Apply-time safety gates: test plan

A reference checklist for validating changes to the apply-time safety gates introduced in #172 / PR #173. Covers the contract tests that ship with the package plus the manual real-Talos validation steps that surface issues unit tests cannot.

## Build under test

```bash
cd ~/git/github.com/cozystack/talm && go build -o /tmp/talm-safety ./
```

Run all matrix cells against the binary at `/tmp/talm-safety`. Use a 3-node Talos v1.12.6 cluster for live runs (any OCI / cloud / bare-metal stand with reachable talosconfig works).

## Phase 1 — declared-resource existence

### Link references

| Case | How to trigger | Expected |
| --- | --- | --- |
| Typoed `LinkConfig.name` | Add `LinkConfig{name: eth9999}` to a node body | `[blocker] declared link "eth9999" not found …` plus available-links hint |
| Typoed bond slave | Add `BondConfig{name: bond0, links: [ghost0, ens5]}` | Blocker on `ghost0` only; `bond0` (new bond) NOT flagged |
| Typoed VLAN parent | Add `VLANConfig{name: ens5.99, parent: ghost0, vlanID: 99}` (YAML key is `parent`, NOT `link` — `vlan.go ParentLinkConfig`) | Blocker on `parent: ghost0`; `name: ens5.99` not flagged (new VLAN) |
| Typoed bridge slave | Add `BridgeConfig{name: br99, links: [ghost0]}` (YAML key is `links`, NOT `ports` — `bridge.go BridgeLinks`) | Blocker on `ghost0` only; `br99` not flagged |
| Typoed Layer2VIP link | Set `vipLink: ghost0` in values | Blocker on `link: ghost0` |
| Legacy v1.11 interface | `machine.network.interfaces[].interface: eth9999` | Blocker; same hint shape |

### Disk references

| Case | How to trigger | Expected |
| --- | --- | --- |
| Bad literal disk | Set `machine.install.disk: /dev/sdz` | Blocker, hint lists real disks (sda, sdb) — **must omit** virtual class (dm-*, drbd*, loop*) |
| Bad model selector | `diskSelector: {model: Samsumg}` | Blocker "matches zero disks", hint lists candidate disks with size |
| Impossible size | `diskSelector: {size: ">= 99TB"}` | Blocker "matches zero disks" |
| Lowercase units | `diskSelector: {size: ">= 100gb"}` | Matches as if `>= 100GB` (humanize.ParseBytes case-insensitive) |
| Mixed case + spaces | `diskSelector: {size: "<= 200000MiB"}` | Parsed correctly |
| Multiple matches | `diskSelector: {type: ssd}` on host with several SSDs | Warning (not blocker) "matches multiple disks; install picks the first match" |
| Type semantics | `type: nvme/sd/hdd/ssd` per-disk Transport+Rotational | Mirror Talos `v1alpha1_provider.go:1325-1351` mapping |
| Readonly excluded | Selector + a readonly disk on host | Readonly disk not counted as match |
| CDROM excluded | Selector + a CD drive on host | CD not counted as match |
| Virtual excluded | Selector on cozystack host with many dm/drbd/loop | dm/drbd/loop not counted; hint omits them |

### Hint length budget

| Case | Trigger | Expected |
| --- | --- | --- |
| Few candidates (≤10) | Storage host with 4 disks, bad selector | Hint lists all candidates inline; no `... and N more` suffix |
| Many candidates (>10) | Host with 25+ links (bonds, VLANs, bridges) + bad link ref | Hint shows first 10 alphabetically; tail collapsed as `... and 15 more`; total chars on the hint line stays under ~400 |
| Boundary case (exactly 11) | 11 links on the host, bad ref | First 10 inline + `... and 1 more` (the suffix fires at >10, not at >=10) |
| Empty candidate set | Selector matches zero, no real candidates either (mock) | Hint says `<none>` rather than empty trailing space |

### Opt-out

| Case | Trigger | Expected |
| --- | --- | --- |
| `--skip-resource-validation` | Pass with bad selector + bad link | No Phase 1 output; render proceeds |

## Phase 2A — pre-apply drift preview

### Diff classification

| Case | Trigger | Expected |
| --- | --- | --- |
| Identical desired/on-node | First-run apply after the same render | `0 addition, 0 removal, 0 update, N unchanged.` |
| Removed doc | Apply config that drops a previously-emitted doc (e.g. dropping a LinkConfig that was on-node) | `- LinkConfig{name: …}` line |
| Added doc | Apply config that adds a fresh doc | `+ LinkConfig{name: …}` line |
| Updated leaf | Change one nested field (e.g. `clusterDomain`) | `~ MachineConfig` plus `cluster.network.dnsDomain: cozy.local -> cozy.example` |
| Identical inputs include Equal entries | Verified via Diff API; OpEqual entries returned, FilterChanged drops them | — |
| Distinguish absent vs null | YAML `extraField: null` added to one side | FieldChange.HasOld=false / HasNew=true; formatter renders `(absent) -> <nil>` |
| Stable ordering | Re-run on same inputs | Identical output bytes |

### Path / mode interactions

| Case | Trigger | Expected |
| --- | --- | --- |
| Dry-run shows preview | `talm apply --dry-run -f node.yaml` | Phase 2A runs; this is the "show me what would change" contract |
| `--mode=no-reboot` | `talm apply --mode=no-reboot -f node.yaml` | Phase 2A runs |
| `--mode=auto` | `talm apply --mode=auto -f node.yaml` | Phase 2A runs |
| `--mode=reboot` | `talm apply --mode=reboot -f node.yaml` | Phase 2A runs (preview is read-only and shows what the reboot will activate) |
| `--mode=staged` | `talm apply --mode=staged -f node.yaml` | Phase 2A runs (operator still wants to see what got staged) |
| `--mode=try` | `talm apply --mode=try -f node.yaml` | Phase 2A runs (mirrors --mode=auto from the preview's perspective) |
| Insecure path | `talm apply -i -f node.yaml` (where chart can render offline) | `talm: drift verification unavailable on maintenance connection`; no block |
| `--skip-drift-preview` | Pass with any change | Preview suppressed entirely |

### Output pretty-print

| Case | Trigger | Expected |
| --- | --- | --- |
| Scalar field change | Change `clusterDomain` in values | `cluster.network.dnsDomain: cozy.local -> cozy.example` inline |
| Map field change | Add a key to `machine.nodeLabels` | YAML flow mapping `{role: control-plane, tier: primary}`, NOT `map[role:control-plane tier:primary]` |
| Multi-element slice add-or-remove | Add a SAN to `cluster.apiServer.certSANs` | `cluster.apiServer.certSANs: added [192.0.2.6]` — set-diff form, NOT a full-slice dump |
| Duplicate-cleanup on slice | Remove a duplicate entry from `certSANs` | `cluster.apiServer.certSANs: removed [127.0.0.1]` — multiset semantics surface a single removal |
| Both add and remove | Replace one SAN with another | `cluster.apiServer.certSANs: removed [old.example], added [new.example]` |
| Reorder-only change | Same elements in a different order | `cluster.apiServer.certSANs: reordered (3 element(s))` — explicit signal, NOT silent OpUpdate |
| Slice appearing from absent | Field added that wasn't there before | `(absent) -> [a, b, c]` — flow-style list, NOT `[a b c]` |

## Phase 2B — post-apply state verification

Default off until the Talos-mutated-field allowlist lands. Enable explicitly with `--skip-post-apply-verify=false`.

| Case | Trigger | Expected |
| --- | --- | --- |
| Clean apply | Apply config matching on-node, `--skip-post-apply-verify=false` | Silent success (no output, no error) |
| Mode=staged | `--mode=staged --skip-post-apply-verify=false` | Phase 2B skipped (staged store doesn't change ActiveID) |
| Mode=try | `--mode=try --skip-post-apply-verify=false` | Phase 2B skipped (rollback timer races verify) |
| Mode=reboot | `--mode=reboot --skip-post-apply-verify=false` | Phase 2B skipped (reboot kills the COSI connection mid-verify) |
| Mode=auto | `--mode=auto --skip-post-apply-verify=false` | Phase 2B skipped — Talos promotes AUTO to REBOOT internally when the change requires it, so the verify would race the reboot (same shape as the explicit REBOOT skip). Acceptable cost: AUTO applies that don't reboot also lose their verify; pass `--mode=no-reboot` to opt back in |
| Mode=no-reboot | Real apply with verify enabled | Phase 2B runs (the only mode where the verify is guaranteed to reach a stable post-apply ActiveID) |
| Dry-run | `--dry-run --skip-post-apply-verify=false` | Phase 2B skipped (no real apply) |
| Reader error | Simulated COSI hiccup on auth path | Hint-bearing blocker `post-apply: re-reading on-node MachineConfig`, exit non-zero (the gate is here to catch silent rollbacks — error is not swallowed) |
| Insecure path | `talm apply -i --skip-post-apply-verify=false` | `drift verification unavailable on maintenance connection` line; no block |

## Phase 2C — post-upgrade version verify

On by default for `talm upgrade`. The gate fires after talosctl upgrade returns success, waits 90s for the node to finish booting, reads `runtime.Version` COSI, and compares the running version's contract against the contract parsed from the target image tag. Catches the silent A/B rollback case where Talos rolls back to the previous partition (cross-vendor image, missing extensions, failed boot readiness check) yet talosctl's RPC already acked.

| Case | Trigger | Expected |
| --- | --- | --- |
| Same-minor upgrade | `talm upgrade -f node.yaml` to a same-minor image (e.g. v1.12.6 -> v1.12.7) | Silent success; contracts match at minor level |
| Cross-minor mismatch | upgrade to `siderolabs/installer:v1.13.0` on a node that rolls back to v1.12 | Hint-bearing blocker citing both versions + two-hypothesis hint (rollback OR slow boot) |
| `--skip-post-upgrade-verify` | Pass with any image | Phase 2C suppressed entirely |
| `--insecure` upgrade | Maintenance path — auth-only COSI unreachable | Phase 2C skipped entirely (hard early-return in `shouldRunPostUpgradeVerify(insecure=true, …)`). Distinct from the Phase 2A/2B insecure path, which still calls the COSI reader and degrades gracefully with a "drift verification unavailable on maintenance connection" line — Phase 2C drops the call site itself because there is no graceful degradation path for "verify the version after upgrade" without auth |
| `--stage` upgrade | New partition not yet activated until reboot — `runtime.Version` would always be the OLD value | Phase 2C skipped via `shouldRunPostUpgradeVerify(staged=true, …)`; guaranteed false positive without skip |
| Digest-pinned image | `--image foo/bar@sha256:abc...` | Phase 2C surrenders silently (no tag to parse the target version from) |
| Image with no tag | `--image foo/bar` | Phase 2C surrenders silently |
| Slow boot beyond 90s | Cold OCI instance or large image pull | Same blocker as cross-minor mismatch BUT hint instructs the operator to re-run `talm get version` after a minute to distinguish a slow boot from a real rollback |
| Real read failure (connection refused, RPC error) | Reader returns `("", false, err)` | Hint-bearing blocker — a node that auto-rolled back or hung mid-boot looks like "connection refused" from the COSI client, so the read failure IS the rollback signal. Same two-hypothesis hint as a detected mismatch (rollback OR slow boot). The blocker wraps the underlying err so the operator sees the cause |
| By-design unreachable | Reader returns `("", false, nil)` (cosiVersionReader does not produce this; reserved for future custom readers that need to surrender silently) | Soft warning line `post-upgrade verification skipped, could not read running version from the node`, no block. Distinguishable from the real-read-failure case via the err — three-valued contract makes the contract explicit |
| Zero target nodes | `--nodes` empty and talosconfig context has no nodes either | Explanatory "skipped, no target nodes resolved" line (no silent no-op) |
| Reconcile wait line | Any non-skipped run | "post-upgrade verify: waiting 1m30s for the node to finish booting..." printed up front so the operator's terminal isn't a mystery hang |

## Real-Talos validation

Before requesting human review, exercise the gates against a live Talos node.

### Setup

```bash
cd /path/to/your/talm/values/tree
```

Use a 3-node Talos cluster (replace placeholders below with your own node IPs — examples use RFC 5737 documentation ranges). The vendored talm library in `charts/talm/` may need `talm init --update --preset cozystack` to pick up new helpers; pass `--force` for non-interactive refresh (CI, scripted), or run under a tty for the interactive prompt.

### Sanity check

```bash
/tmp/talm-safety template -f nodes/node0.yaml > /tmp/rendered.yaml
test -s /tmp/rendered.yaml || echo "render failed"
```

### Phase 1 (auth path)

```bash
# Clean run — should silently pass:
/tmp/talm-safety apply --dry-run -f nodes/node0.yaml

# Inject a bad link ref (cp + edit a temp file inside the talm project):
cp nodes/node0.yaml nodes/_test-bad.yaml
echo -e "---\napiVersion: v1alpha1\nkind: LinkConfig\nname: eth9999" >> nodes/_test-bad.yaml
/tmp/talm-safety apply --dry-run -f nodes/_test-bad.yaml  # expect [blocker]
rm nodes/_test-bad.yaml
```

### Phase 2A (drift preview)

```bash
# Dry-run against a clean cluster — should report 0/0/0 unchanged:
/tmp/talm-safety apply --dry-run -f nodes/node0.yaml

# Force a leaf change via values.yaml (back up then revert):
sed -i.bak 's/^clusterDomain: .*/clusterDomain: cozy.example/' values.yaml
/tmp/talm-safety apply --dry-run -f nodes/node0.yaml | grep -E "^  [+\-~=]|^      "
mv values.yaml.bak values.yaml
```

### Phase 2B (real apply with verify enabled)

```bash
/tmp/talm-safety apply --mode=no-reboot --skip-post-apply-verify=false -f nodes/node0.yaml
# Expected: drift preview + 'Applied configuration without a reboot' + silent post-apply verify
```

### Multi-node + mix

```bash
/tmp/talm-safety apply --dry-run -f nodes/node0.yaml -f nodes/node1.yaml -f nodes/node2.yaml
# Each node renders its own preview; per-node independence.
```

### Insecure path

`talm apply -i` exercises the maintenance connection. The cozystack reference chart uses live discovery (`lookup "disks"`), which fails on insecure (no auth for COSI). The render errors before the gate runs — that's existing talm behaviour, not a regression.

## Implementation health

Run as part of every push:

```bash
go test ./...
go test -race ./pkg/applycheck/... ./pkg/commands/...
golangci-lint run ./...
GOOS=windows golangci-lint run ./...
go vet ./...
```

## Known limitations / follow-ups

- **Talos-mutated-field allowlist** (open in #172): Phase 2B reports cert hashes / timestamps as divergence today; the verify is off by default until an allowlist lands.
- **`talm upgrade` has no pre-upgrade gates** (Phase 2C runs *after*, not before): the upgrade flow wraps `talosctl upgrade` and doesn't route through `buildApplyClosure` / `applyOneFileDirectPatchMode`, so Phase 1 / Phase 2A do not run. Phase 2C (post-upgrade version verify) was added precisely to catch the silent-rollback class without that refactor. Full pre-upgrade gates would require reproducing the gate calls in `upgrade_handler.go` or refactoring the apply flow.
- **Phase 1/2 on `--insecure`**: the safety gates can't run before the chart renders, and the chart's `lookup` calls need an authenticated COSI connection. Insecure path = effectively no gates today.
