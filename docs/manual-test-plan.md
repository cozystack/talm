# talm manual test plan

A QA-oriented matrix for exercising `talm` end-to-end against a real Talos cluster. Designed to surface bugs that unit + contract tests miss — encoding edge cases, real-disk topology quirks, multi-node interactions, recovery flows.

The apply-time safety gate matrix (Phase 1 / 2A / 2B / 2C, per-case triggers + expectations) is folded into Section C-safety below.

## How to use this plan

1. Build the binary under test and put it on `PATH`:

   ```bash
   cd ~/git/github.com/cozystack/talm
   go install ./   # places `talm` in $GOBIN (or $GOPATH/bin), which should be on PATH
   # OR, if you prefer a local build:
   #   go build -o talm ./ && export PATH=$PWD:$PATH
   ```

   The commands in this plan assume `talm` is invokable by bare name. Adjust paths accordingly if you keep the binary elsewhere.

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
talm init --preset cozystack --name test-cluster \
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
talm init --preset cozystack --name test-cluster
```

Expected: error citing each conflicting file, hint mentioning both `--force` and `--update`. Exit non-zero.

### A3. `talm init --update --preset cozystack` non-tty

```bash
cd $PROJECT
talm init --update --preset cozystack < /dev/null
```

Expected: hint-bearing error pointing at `--force`. NOT a raw `reading interactive overwrite confirmation: EOF`.

### A4. `talm init --update --preset cozystack --force` non-tty

```bash
cd $PROJECT
talm init --update --preset cozystack --force < /dev/null
```

Expected: one `Overwriting <path> (--force)` line per diff; no prompt; exit zero.

### A5. Encrypt / decrypt round-trip

```bash
cd /tmp/talm-init-test
talm init --decrypt
test -f secrets.yaml && test -f talosconfig
talm init --encrypt
test -f secrets.encrypted.yaml && test -f talosconfig.encrypted
```

Expected: per-file `Decrypting X -> Y` / `Encrypting X -> Y` lines; both round-trips succeed.

### A6. Decrypt without `talm.key`

```bash
cd /tmp/talm-init-test && mv talm.key /tmp/talm.key.backup
talm init --decrypt
mv /tmp/talm.key.backup talm.key
```

Expected: clear error mentioning the missing key path.

### A7. Cleanup

```bash
rm -rf /tmp/talm-init-test
```

### A6a. `init --decrypt` without `talm.key` surfaces recovery hint

```bash
mkdir -p /tmp/talm-decrypt-test && cd /tmp/talm-decrypt-test
talm init --preset cozystack --name test --endpoints 192.0.2.1
mv talm.key /tmp/talm.key.backup
talm init --decrypt
mv /tmp/talm.key.backup talm.key  # restore for next run
rm -rf /tmp/talm-decrypt-test
```

Expected: error `failed to decrypt secrets: load key: read key file: open <path>/talm.key: no such file or directory` followed by hint `talm.key is required to decrypt secrets.encrypted.yaml. Restore your backed-up key, or re-run \`talm init\` to regenerate (this writes new secrets — the old secrets.encrypted.yaml will not be decryptable without the original key).`

Regression anchor: the hint must name BOTH recovery paths (restore from backup, re-run init to regenerate) AND include the warning that regeneration writes new secrets making the old encrypted secrets undecryptable. A regression that drops either path or the warning silently invites operators to "just run init again" without understanding the data-loss tradeoff.

### A9. `init --cluster-endpoint` populates values.yaml::endpoint

```bash
mkdir -p /tmp/talm-cluster-ep-test && cd /tmp/talm-cluster-ep-test
talm init --preset cozystack --name test \
  --endpoints 10.0.0.1,10.0.0.2,10.0.0.3 \
  --cluster-endpoint https://vip.example.test:6443
grep "^endpoint:" values.yaml
grep -A 3 "endpoints:" talosconfig | head -5
rm -rf /tmp/talm-cluster-ep-test
```

Expected: `values.yaml` has `endpoint: "https://vip.example.test:6443"` (operator's VIP, explicit), `talosconfig` has all three `10.0.0.1`/`10.0.0.2`/`10.0.0.3` under the context's endpoints array. The two flags address different concepts — `--cluster-endpoint` is the kube-apiserver URL, `--endpoints` is the talosctl-client list — and both round-trip independently.

Regression anchor: removing `--cluster-endpoint` and passing only `--endpoints 10.0.0.1,10.0.0.2,10.0.0.3` MUST leave `values.yaml::endpoint` empty (the multi-endpoint case never auto-derives — picking one node would silently couple cluster availability to it). The init flow MUST then print a hint at the end pointing the operator at `values.yaml::endpoint` with examples for VIP / LB shapes.

### A10. `init --endpoints` with single entry auto-derives values.yaml::endpoint

```bash
mkdir -p /tmp/talm-single-ep-test && cd /tmp/talm-single-ep-test
talm init --preset cozystack --name test --endpoints 192.0.2.10
grep "^endpoint:" values.yaml
rm -rf /tmp/talm-single-ep-test
```

Expected: `endpoint: "https://192.0.2.10:6443"` — the single-endpoint case is unambiguously "this is also the cluster URL", so init derives the canonical `https://<host>:6443` form. No hint printed at end of init for this case.

Regression anchor: this auto-derive ONLY fires when `len(--endpoints) == 1`. Multi-endpoint inputs MUST leave the field empty (see A9).

### A11. `init --cluster-endpoint` rejects malformed URL before any files land on disk

```bash
mkdir -p /tmp/talm-bad-ep-test && cd /tmp/talm-bad-ep-test
talm init --preset cozystack --name test \
  --endpoints 192.0.2.10 \
  --cluster-endpoint "not-a-url"
ls -la  # should be empty
rm -rf /tmp/talm-bad-ep-test
```

Expected: error `cluster-endpoint "not-a-url" is missing scheme, host, or port` with hint pointing at the canonical form. Directory remains empty — NO `talosconfig`, NO `.gitignore`, NO `secrets.yaml` written. Validation happens in PreRunE before any file writes so a malformed flag never produces a half-initialised project tree.

Regression anchor: a regression that defers validation to RunE (i.e. after the secret-bundle generation) would leave `talosconfig` and `.gitignore` behind — verify by checking `ls -la` after the failing command finds an empty directory.

### A8. `init --update` without `--preset` and without preset dep in Chart.yaml

```bash
mkdir -p /tmp/talm-update-test && cd /tmp/talm-update-test
cat > Chart.yaml <<EOF
apiVersion: v2
name: empty-project
version: 0.1.0
dependencies: []
EOF
talm init --update
```

Expected: single-wrapped error with hint. Inner cause `preset not found in Chart.yaml dependencies` is surfaced verbatim with hint `add a preset chart (e.g. cozystack) to Chart.yaml dependencies or pass --preset`. No outer rewrap line like `preset is required: use --preset flag or ensure Chart.yaml has a preset dependency` — the rewrap was double-messaging the same condition before the fix.

Regression anchor: `errors.GetAllHints(err)` (or `2>&1 | grep "^hint:"`) MUST still surface the inner `add a preset chart` hint after the unwrap. A regression that drops the hint along with the rewrap is a UX regression.

**Regression anchor**: A6's error must reference the missing-key path explicitly. A bare `read key file: open ...: no such file or directory` with no follow-up hint is a UX regression — the operator should see a clear recovery path (`run \`talm init\` to generate a new key, or restore your backup`).

## B. Render / template

### B1. Happy-path render

```bash
cd $PROJECT
talm template -f nodes/node0.yaml | head -10
```

Expected: rendered MachineConfig YAML starting with the project modeline. Exit zero.

### B2. Render with CLI override

```bash
talm template -f nodes/node0.yaml \
  --set clusterDomain=overridden.local | grep dnsDomain
```

Expected: `dnsDomain: overridden.local` appears in output.

### B3. Render against missing file

```bash
talm template -f nodes/_doesnotexist.yaml
```

Expected: clear error with hint about the missing path. Exit non-zero.

### B4. In-place rewrite (`-I`)

```bash
cp nodes/node0.yaml /tmp/inplace-before.yaml
talm template -I -f nodes/node0.yaml
diff /tmp/inplace-before.yaml nodes/node0.yaml
cp /tmp/inplace-before.yaml nodes/node0.yaml  # restore
```

Expected: `Updated.` on stdout. The rendered body replaces the previous body of the file — but **operator-authored comments above the modeline are preserved verbatim**. Comments embedded in the YAML body still get overwritten, since `-I` re-renders the body and Helm has no way to round-trip user-edits made there.

Regression anchor: write `nodes/node0.yaml` as `# Operator note A\n# Operator note B\n# talm: ...\n<body>`. After `template -I -f nodes/node0.yaml`, the first two lines (`# Operator note A`, `# Operator note B`) MUST still be there, followed by the modeline, the talm-rendered warning header, then the body. Re-run idempotent: a second `template -I` keeps the same prefix structure — leading comments don't drift, multiply, or disappear.

### B5. Render with stale chart preset

When the local `charts/talm/` is older than the talm binary's embedded preset, `talm template` succeeds against the local preset — it does NOT auto-bump. The operator must run `init --update`. Confirm by inspecting `talm version` against `Chart.yaml`.

**Regression anchor**: `template -I` is rewrite, not merge — verify by adding a `# my comment` line above the modeline in `nodes/node0.yaml`, running B4, and confirming the comment is GONE in the new body. If the comment survives, a behaviour change shipped (could be either an intentional new `--preserve-comments` flag or an undocumented merge mode — neither should appear silently).

### B6. Scope-filter symmetry across v1.11 and v1.12 renders

Pin both schema renders dropping kernel-managed scopes (`host` / `link` / `nowhere`) from the COSI addresses table. On a node where 169.254/16 link-local addresses live on the default-gateway-bearing interface (Talos always sets `scope=link` for that range), assert:

```bash
# Render the legacy v1.11 path first — explicit talosVersion pin:
sed -i.bak 's/^talosVersion: "v1.12"/talosVersion: "v1.11"/' Chart.yaml
talm template -f nodes/node0.yaml | grep -E "address: 169\.254" && echo "FAIL: link-scoped leaked into v1.11" || echo "OK v1.11"
mv Chart.yaml.bak Chart.yaml

# Then render the v1.12 path (default):
talm template -f nodes/node0.yaml | grep -E "address: 169\.254" && echo "FAIL: link-scoped leaked into v1.12" || echo "OK v1.12"
```

Expected: both renders print "OK" — no `address: 169.254.…` lines anywhere in the rendered output. The two helpers (`talm.discovered.default_addresses_by_gateway` for v1.11 and `talm.discovered.addresses_by_link` for v1.12) share the same `$skipScopes := list "host" "link" "nowhere"` filter so a link-local address on the default-gateway link is dropped from both paths.

Regression anchor: a regression that re-introduces the v1.11-only `host`-scope filter would let link-local addresses leak into the legacy `machine.network.interfaces[].addresses` block on a future COSI schema bump that emitted them on the default-gateway-bearing link. Pinned by `TestContract_NetworkLegacy_DefaultAddressesFilterLinkScope` with a `linkScopedAddressOnDefaultGatewayLookup` fixture, but the live render is still useful as a sanity check against real cluster discovery output.

## C. Apply (auth path)

This section is the smoke-test for the apply pipe itself; the per-gate matrix lives in **Section C-safety** below.

### C1. Dry-run apply

```bash
talm apply --dry-run -f nodes/node0.yaml
```

Expected: drift-preview section, then `Dry run summary:` and the diff the apply would produce. Exit zero.

### C2. Real apply, no-reboot mode

```bash
talm apply --mode=no-reboot \
  --skip-post-apply-verify=false -f nodes/node0.yaml
```

Expected: drift preview, `Applied configuration without a reboot`. Phase 2B is silent on a clean apply.

### C3. Multi-file apply

```bash
talm apply --dry-run \
  -f nodes/node0.yaml -f nodes/node1.yaml -f nodes/node2.yaml
```

Expected: each node renders / diffs independently; per-node gate output sections.

### C4. Stage mode

```bash
talm apply --mode=staged --skip-post-apply-verify=false \
  -f nodes/node0.yaml
```

Expected: Phase 2B auto-skipped (staged config doesn't change ActiveID); output ends with `Staged configuration to be applied after the next reboot`.

### C5. Drift preview redacts secret-bearing fields by default

```bash
# Rotate machine.token by editing secrets.yaml (or any allowlisted path) then:
talm apply --dry-run -f nodes/node0.yaml
```

Expected: the drift preview line for `machine.token` reads `machine.token: ***redacted (len=N)*** -> ***redacted (len=M)***`. The literal `old-token-value` / `new-token-value` strings MUST NOT appear in stderr. Non-secret paths (e.g. `machine.network.hostname` if it changed) render verbatim.

Regression anchor: rotating any field in the allowlist (`cluster.{secret,token,aescbcEncryptionSecret,secretboxEncryptionSecret}`, `cluster.{ca,aggregatorCA,serviceAccount,etcd.ca}.key`, `cluster.acceptedCAs[].key`, `machine.{token,ca.key}`, `machine.acceptedCAs[].key`) MUST redact. A regression that silently leaks a secret value into stderr is a security-class bug — verify the substring with `grep -F` against the captured output.

### C6. Drift preview shows secrets with explicit opt-in

```bash
talm apply --dry-run --show-secrets-in-drift -f nodes/node0.yaml
```

Expected: same drift preview as C5, but the secret paths render verbatim — no `***redacted***` sentinel. Operator-explicit bypass for debugging.

Regression anchor: `--show-secrets-in-drift` is operator opt-in, never default. Verify by running `talm apply --help` and confirming the flag default is `false`.

### C6a. Apply chain — first `-f` anchors project root, later `-f` files stack as side-patches

```bash
cat > /tmp/side-ntp.yaml <<'EOF'
machine:
  time:
    servers:
      - 2.example.test
EOF
talm apply --dry-run \
  -f nodes/node0.yaml -f /tmp/side-ntp.yaml
```

Expected: progress line includes `side-patches=[/tmp/side-ntp.yaml]` alongside `nodes=[…]` and `endpoints=[…]`. Drift preview shows both the anchor's rendered config diff AND the side-patch's `machine.time.servers: added [2.example.test]` mutation. The side-patch is stacked via `engine.MergeFileAsPatch`; last writer wins on overlapping keys.

Regression anchor: `/tmp/side-ntp.yaml` lives outside the project root. The first `-f` file (`nodes/node0.yaml`) is the SOLE anchor for root detection; later files don't get root-validated. Verify by reversing order — `apply --dry-run -f /tmp/side-ntp.yaml -f nodes/node0.yaml` should fail because the first file has no project root.

### C6b. Non-modelined anchor with side-patches rejected with reorder hint

```bash
cat > nodes/orphan.yaml <<'EOF'
machine: {}
EOF
talm apply --dry-run -f nodes/orphan.yaml -f /tmp/side-ntp.yaml
```

Expected: error `first -f file nodes/orphan.yaml lacks a modeline; side-patches require a modelined anchor` with hint that the first `-f` file must carry `# talm: nodes=[…], templates=[…]` so talm knows what to render before stacking. Note: the orphan file lives inside the project tree because the first `-f` file must have a Chart.yaml + secrets.yaml ancestor for root detection — see C6b-pre below for the root-detection failure mode.

### C6b-pre. Anchor outside any project root is rejected at root detection

```bash
cat > /tmp/orphan.yaml <<'EOF'
machine: {}
EOF
talm apply --dry-run -f /tmp/orphan.yaml
```

Expected: error `failed to detect project root for first file /tmp/orphan.yaml (Chart.yaml and secrets.yaml not found)` with hint `the first -f file anchors the project root; place it inside a talm init'd project, or reorder the -f chain so a rooted file comes first`. This gate fires BEFORE the modeline-vs-orphan dispatch, so any orphan placed outside a project root can never reach the direct-patch path.

### C6c. Orphan file inside project root dispatches to direct-patch when context provides nodes

```bash
# Build a talosconfig with explicit nodes field
cp talosconfig /tmp/talosconfig-with-nodes
# (edit to add nodes: [...] under contexts.<active>.nodes — YAML or yq)
talm apply --dry-run \
  --talosconfig /tmp/talosconfig-with-nodes \
  -f nodes/orphan.yaml
```

Expected: progress line `- talm: file=nodes/orphan.yaml, nodes=[…], endpoints=[…]` — targets resolved from the talosconfig context, no early "no nodes available" rejection. The dispatch reaches the direct-patch path (chart-render-with-orphan-as-patch). Note: the orphan must live inside the project tree to clear root detection (see C6b-pre).

### C6d. Orphan file with no source for nodes fails late with three-way hint

```bash
# Same talosconfig but WITHOUT a nodes: field in the active context
talm apply --dry-run \
  --talosconfig /tmp/talosconfig-no-nodes \
  -f nodes/orphan.yaml
```

Expected: `no nodes to target for nodes/orphan.yaml` with hint `the file lacks a # talm: nodes=[…] modeline, no --nodes flag was passed, and the active talosconfig context carries no nodes; pass --nodes explicitly or supply a modelined node file`. The hint must name all three sources so the operator knows which to populate.

Regression anchor: the "no nodes" check fires AFTER the talosconfig context fallback runs, not before. A regression that re-introduces the early `len(GlobalArgs.Nodes) == 0` gate would block C6c silently.

### C6e. Progress lines use comma-joined bracketed lists

Inspect any apply progress line from C1, C3, C6a, C6c. Expect `nodes=[n1,n2,n3]` and `endpoints=[e1,e2]` — comma-joined, bracket-framed, no `[n1 n2 n3]` Go slice format artifact.

Regression anchor: the format uses `strings.Join(slice, ",")` with surrounding brackets in the format string; `%s` or `%v` directly on a `[]string` produces the space-separated `[n1 n2 n3]` artifact and is a regression. Verify with a multi-node modeline (`# talm: nodes=["1.2.3.4","5.6.7.8"], ...`) → progress line shows `nodes=[1.2.3.4,5.6.7.8]`.

### C6f. Multi-IP modeline parses regardless of whitespace inside the JSON array

```bash
# Anchor with hand-written multi-IP modeline (note spaces after commas):
cat > nodes/multi.yaml <<'EOF'
# talm: nodes=["1.2.3.4", "5.6.7.8"], endpoints=["1.2.3.4", "5.6.7.8"], templates=["templates/controlplane.yaml"]
machine: {}
EOF
talm apply --dry-run -f nodes/multi.yaml

# Side-patch slot that accidentally carries a modeline (rejection path):
cat > /tmp/side-modelined.yaml <<'EOF'
# talm: nodes=["1.2.3.4", "5.6.7.8"], endpoints=["1.2.3.4", "5.6.7.8"]
machine:
  kernel:
    modules:
    - name: dm_thin_pool
EOF
talm apply --dry-run -f nodes/node0.yaml -f /tmp/side-modelined.yaml
```

Expected:

- First invocation: progress line includes `nodes=[1.2.3.4,5.6.7.8]` (two nodes, parsed from the multi-element array). The whitespace after each comma inside `nodes=[…]` and `endpoints=[…]` is tolerated.
- Second invocation: rejection error `side-patch /tmp/side-modelined.yaml carries its own modeline; the apply chain treats only the first -f file as the anchor and stacks subsequent files as side-patches` with the per-file-loop / strip-modeline hint. The error is the high-level "side-patches must not have modelines" gate from `rejectModelinedSidePatches`, NOT a low-level `error parsing JSON array for key nodes` — the parser successfully reads the multi-IP form and the rejection happens on the (correctly identified) modelined-side-patch shape.

Regression anchor: the modeline parser splits on commas at JSON-array depth 0 only. A regression that re-introduces a literal comma-plus-space split would silently truncate multi-element arrays — the anchor case above would fail with `unexpected end of JSON input` on `["1.2.3.4"` (array cut at the first comma). Verify the depth tracking by adding a comma inside a string literal: `# talm: nodes=["a,b"]` must parse to `Nodes=["a,b"]`, not split into two tokens.

### C7. Phase 1 walker rejects malformed net-addr fields before the RPC

When a rendered MachineConfig carries a malformed value in any of the new walker-covered fields, Phase 1 must block before the apply RPC fires:

- `StaticHostConfig.name` not a parseable IP literal (the `name` field on this kind is the IP the hostnames map to — Talos's schema does not have a separate `address` field).
- `NetworkRuleConfig.ingress[i].subnet` or `.except` not a parseable CIDR.
- `WireguardConfig.peers[i].endpoint` not a parseable host:port.

Hand-craft a chart that emits a bad value (e.g. `name: 999.999.0.1` on a `StaticHostConfig`, or `ingress: [{subnet: notacidr}]` on a `NetworkRuleConfig`, or `endpoint: notavalid:endpoint` on a Wireguard peer) and run `apply --dry-run`. Expected: Phase 1 emits a blocker citing the offending field path (`doc[N].name`, `doc[N].ingress[i].subnet` or `.except`, `doc[N].peers[i].endpoint`); exit non-zero before any RPC. Valid values (IPv4, IPv6, IPv6:port via `[host]:port`) pass through.

Regression anchor: empty / omitted endpoint on a Wireguard peer is NOT a finding — peers without endpoints are listener-only remote peers. Verify a chart with `endpoint: ""` passes Phase 1.

## C-safety. Apply-time safety gates (detailed matrix)

The Section C entries above smoke the apply pipe end-to-end. This section is the per-gate matrix that backs them: each Phase (1 / 2A / 2B / 2C) has a case-by-case trigger + expected-output table, used to validate changes to `pkg/applycheck/`, `pkg/commands/preflight_*.go`, and the upgrade-verify hooks in `pkg/commands/upgrade_handler.go`. Run after every refactor in those packages; cross-reference with the contract tests under `pkg/applycheck/` and `pkg/commands/preflight_*_test.go`.

### Phase 1 — declared-resource existence

#### Link references

| Case | How to trigger | Expected |
| --- | --- | --- |
| Typoed `LinkConfig.name` | Add `LinkConfig{name: eth9999}` to a node body | `[blocker] declared link "eth9999" not found …` plus available-links hint |
| Typoed bond slave | Add `BondConfig{name: bond0, links: [ghost0, ens5]}` | Blocker on `ghost0` only; `bond0` (new bond) NOT flagged |
| Typoed VLAN parent | Add `VLANConfig{name: ens5.99, parent: ghost0, vlanID: 99}` (YAML key is `parent`, NOT `link` — `vlan.go ParentLinkConfig`) | Blocker on `parent: ghost0`; `name: ens5.99` not flagged (new VLAN) |
| Typoed bridge slave | Add `BridgeConfig{name: br99, links: [ghost0]}` (YAML key is `links`, NOT `ports` — `bridge.go BridgeLinks`) | Blocker on `ghost0` only; `br99` not flagged |
| Typoed Layer2VIP link | Set `vipLink: ghost0` in values | Blocker on `link: ghost0` |
| Legacy v1.11 interface | `machine.network.interfaces[].interface: eth9999` | Blocker; same hint shape |

#### Disk references

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

#### Hint length budget

| Case | Trigger | Expected |
| --- | --- | --- |
| Few candidates (≤10) | Storage host with 4 disks, bad selector | Hint lists all candidates inline; no `... and N more` suffix |
| Many candidates (>10) | Host with 25+ links (bonds, VLANs, bridges) + bad link ref | Hint shows first 10 alphabetically; tail collapsed as `... and 15 more`; total chars on the hint line stays under ~400 |
| Boundary case (exactly 11) | 11 links on the host, bad ref | First 10 inline + `... and 1 more` (the suffix fires at >10, not at >=10) |
| Empty candidate set | Selector matches zero, no real candidates either (mock) | Hint says `<none>` rather than empty trailing space |

#### Net-addr field references

The Phase 1 walker validates the syntactic shape of net-addr fields in three v1alpha1 multidoc kinds. Pure syntactic — no host snapshot — runs alongside the Ref-based walker via `multidocNetAddrHandlers`. Field names match the actual Talos `network` schema (see `siderolabs/talos/pkg/machinery/config/types/network/`).

| Case | How to trigger | Expected |
| --- | --- | --- |
| Bad `StaticHostConfig.name` | `StaticHostConfig{name: 999.999.0.1, hostnames: [foo]}` — the `name` field carries the IP literal in this kind | Blocker "StaticHostConfig.name is not a valid IP literal" with hint listing IPv4/IPv6 examples |
| Valid IPv4 / IPv6 | `name: 192.0.2.10` / `name: 2001:db8::1` | No finding |
| Missing `name` | Omit the field | No finding (Talos rejects at RPC with a clearer required-field message) |
| Hostname-shaped name | `name: example.invalid` | Blocker — `name` is required to be an IP literal, not a DNS name |
| Bad `NetworkRuleConfig.ingress[i].subnet` | `ingress: [{subnet: notacidr}]` | Per-entry blocker citing `ingress[i].subnet` |
| Bad `NetworkRuleConfig.ingress[i].except` | `ingress: [{subnet: 192.0.2.0/24, except: notacidr}]` | Blocker on `except` even when `subnet` is valid |
| Bare IP without /N | `ingress: [{subnet: 192.0.2.10}]` | Blocker — schema is CIDR-shaped, not IP-shaped |
| Valid CIDR mix | IPv4 + IPv6 CIDRs across `ingress[].subnet` | No findings |
| Bad `WireguardConfig.peers[].endpoint` | One peer `endpoint: notavalid:endpoint` | Per-peer blocker citing `peers[i].endpoint` |
| Valid IPv4:port | `endpoint: 192.0.2.10:51820` | No finding |
| Valid bracketed IPv6:port | `endpoint: "[2001:db8::1]:51820"` | No finding |
| Empty `endpoint` | `endpoint: ""` | No finding (peer is listener-only — this side does not initiate) |
| Missing `endpoint` field | Omit the field | No finding |
| Unknown multidoc kind | A new kind not in the dispatch map | No finding (Talos extensions / future kinds do not break the gate) |
| Real-schema pin | `TestWalkNetAddrFindings_RealSchema_StaticHostConfig` / `..._NetworkRuleConfig` feed the actual schema shape (`name` carrying the IP, `ingress[].subnet/except` for CIDRs) — the walker fires on what Talos emits, not on a hand-crafted YAML the schema doesn't produce |
| No-overlap pin | Adding a kind to both `multidocHandlers` AND `multidocNetAddrHandlers` | `TestMultidocNetAddrHandlers_NoOverlapWithRefHandlers` fails — double-walking would produce duplicate findings |

#### Opt-out

| Case | Trigger | Expected |
| --- | --- | --- |
| `--skip-resource-validation` | Pass with bad selector + bad link | No Phase 1 output; render proceeds |

### Phase 2A — pre-apply drift preview

#### Diff classification

| Case | Trigger | Expected |
| --- | --- | --- |
| Identical desired/on-node | First-run apply after the same render | `0 addition, 0 removal, 0 update, N unchanged.` |
| Removed doc | Apply config that drops a previously-emitted doc (e.g. dropping a LinkConfig that was on-node) | `- LinkConfig{name: …}` line |
| Added doc | Apply config that adds a fresh doc | `+ LinkConfig{name: …}` line |
| Updated leaf | Change one nested field (e.g. `clusterDomain`) | `~ MachineConfig` plus `cluster.network.dnsDomain: cozy.local -> cozy.example` |
| Identical inputs include Equal entries | Verified via Diff API; OpEqual entries returned, FilterChanged drops them | — |
| Distinguish absent vs null | YAML `extraField: null` added to one side | FieldChange.HasOld=false / HasNew=true; formatter renders `(absent) -> <nil>` |
| Stable ordering | Re-run on same inputs | Identical output bytes |

#### Path / mode interactions

| Case | Trigger | Expected |
| --- | --- | --- |
| Dry-run shows preview | `talm apply --dry-run -f node.yaml` | Phase 2A runs; this is the "show me what would change" contract |
| `--mode=no-reboot` | `talm apply --mode=no-reboot -f node.yaml` | Phase 2A runs |
| `--mode=auto` | `talm apply --mode=auto -f node.yaml` | Phase 2A runs |
| `--mode=reboot` | `talm apply --mode=reboot -f node.yaml` | Phase 2A runs (preview is read-only and shows what the reboot will activate) |
| `--mode=staged` | `talm apply --mode=staged -f node.yaml` | Phase 2A runs (operator still wants to see what got staged) |
| `--mode=try` | `talm apply --mode=try -f node.yaml` | Phase 2A runs (mirrors --mode=auto from the preview's perspective) |
| Insecure path | `talm apply -i -f node.yaml` (where chart can render offline) | `talm: drift verification unavailable on maintenance connection`; no block |
| Insecure path, multi-node | `talm apply -i --nodes a,b -f node.yaml` (each iteration through `openClientPerNodeMaintenance`) | Per-node-prefixed line `node a: talm: drift verification unavailable …` and `node b: talm: …` — disambiguation cohort over the maintenance-warning emission |
| Insecure path, single node | `talm apply -i -f node.yaml` with single `--nodes` | Still gets `node X: talm: …` prefix because `cosiPreflightContext` falls back to `GlobalArgs.Nodes[0]` when there is no outgoing-context metadata |
| Insecure path, empty nodeID | Unusual call shape with `GlobalArgs.Nodes` somehow empty | Bare `talm: drift verification unavailable …` line — never `node : …` garbage prefix |
| `--skip-drift-preview` | Pass with any change | Preview suppressed entirely |

#### Secret-bearing field redaction

The drift preview redacts allowlisted paths by default. The opt-out is operator-explicit: `--show-secrets-in-drift`. Allowlist lives in `secretFieldPaths` (`pkg/commands/preflight_apply_safety_redact.go`).

| Case | Trigger | Expected |
| --- | --- | --- |
| Cluster secret rotation | Change `cluster.token` via `secrets.yaml` rotation | `cluster.token: ***redacted (len=N)*** -> ***redacted (len=M)***` — value never appears in stderr |
| Machine token rotation | Change `machine.token` | Same shape; `machine.token: ***redacted (len=N)*** -> …` |
| Array-indexed secret | Change `cluster.acceptedCAs[2].key` | Bracket-normalised match against `cluster.acceptedCAs[].key`; redacted |
| Wireguard private key | Rotate `WireguardConfig.privateKey` | `privateKey: ***redacted (len=N)*** -> …` — bare path because the differ's flatten step does not prefix multidoc fields with the doc kind |
| Wireguard pre-shared key | Rotate `peers[2].presharedKey` | Bracket-normalised; redacted |
| Non-secret path | Change `machine.network.hostname` | Verbatim — operator-visible information is not redacted |
| False-prefix guard | A non-secret path sharing a prefix (`cluster.tokenExtras`) | Verbatim — `isSecretPath` is path-segment exact, not substring prefix |
| `--show-secrets-in-drift` | Pass with any secret rotation | Verbatim both sides on the secret line; sentinel never appears |
| Non-string secret value | Hypothetical schema drift puts an int on a secret-bearing path | `***redacted (len=N)***` where N is the `%v` length; rotation signal survives non-string types (caveat: maps render with non-deterministic key order — disclaimed in the godoc) |
| Slice-shaped secret path | Hypothetical future allowlist entry naming an array | Redacted via the secret check that runs BEFORE `bothSlices` — elements never leak through `formatSliceSetDiff` |

#### Output pretty-print

| Case | Trigger | Expected |
| --- | --- | --- |
| Scalar field change | Change `clusterDomain` in values | `cluster.network.dnsDomain: cozy.local -> cozy.example` inline |
| Map field change | Add a key to `machine.nodeLabels` | YAML flow mapping `{role: control-plane, tier: primary}`, NOT `map[role:control-plane tier:primary]` |
| Multi-element slice add-or-remove | Add a SAN to `cluster.apiServer.certSANs` | `cluster.apiServer.certSANs: added [192.0.2.6]` — set-diff form, NOT a full-slice dump |
| Duplicate-cleanup on slice | Remove a duplicate entry from `certSANs` | `cluster.apiServer.certSANs: removed [127.0.0.1]` — multiset semantics surface a single removal |
| Both add and remove | Replace one SAN with another | `cluster.apiServer.certSANs: removed [old.example], added [new.example]` |
| Reorder-only change | Same elements in a different order | `cluster.apiServer.certSANs: reordered (3 element(s))` — explicit signal, NOT silent OpUpdate |
| Slice appearing from absent | Field added that wasn't there before | `(absent) -> [a, b, c]` — flow-style list, NOT `[a b c]` |

### Phase 2B — post-apply state verification

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

### Phase 2C — post-upgrade version verify

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
| Configurable reconcile window | `talm upgrade --post-upgrade-reconcile-window=180s …` | "post-upgrade verify: waiting 3m0s for the node to finish booting..." — Go's `time.Duration.String()` renders 180s deterministically as `3m0s`. Hint copy references "the configured reconcile window (`--post-upgrade-reconcile-window`)" instead of the hardcoded "90s reconcile window" wording |
| Window default | `talm upgrade --help` | Flag listed with `default 1m30s`; the const `defaultPostUpgradeReconcileWindow` preserves the previous hardcoded 90s for byte-identical back-compat |
| Window non-positive | `talm upgrade --post-upgrade-reconcile-window=0s …` | Fail-fast error with hint mentioning "positive duration" — validation runs at the TOP of `wrapUpgradeCommand` RunE so the talosctl upgrade RPC never fires. Same shape for `-30s` (negative). Pinned by `TestWrapUpgradeCommand_BadReconcileWindow_FailsFastBeforeOriginalRunE` which asserts the sentinel `originalRunE` stays uninvoked |

### Real-Talos validation

Before requesting human review, exercise the gates against a live Talos node.

#### Sanity check

```bash
cd $PROJECT
talm template -f nodes/node0.yaml > /tmp/rendered.yaml
test -s /tmp/rendered.yaml || echo "render failed"
```

#### Phase 1 (auth path)

```bash
# Clean run — should silently pass:
talm apply --dry-run -f nodes/node0.yaml

# Inject a bad link ref (cp + edit a temp file inside the talm project):
cp nodes/node0.yaml nodes/_test-bad.yaml
echo -e "---\napiVersion: v1alpha1\nkind: LinkConfig\nname: eth9999" >> nodes/_test-bad.yaml
talm apply --dry-run -f nodes/_test-bad.yaml  # expect [blocker]
rm nodes/_test-bad.yaml
```

#### Phase 2A (drift preview)

```bash
# Dry-run against a clean cluster — should report 0/0/0 unchanged:
talm apply --dry-run -f nodes/node0.yaml

# Force a leaf change via values.yaml (back up then revert):
sed -i.bak 's/^clusterDomain: .*/clusterDomain: cozy.example/' values.yaml
talm apply --dry-run -f nodes/node0.yaml | grep -E "^  [+\-~=]|^      "
mv values.yaml.bak values.yaml
```

#### Phase 2B (real apply with verify enabled)

```bash
talm apply --mode=no-reboot --skip-post-apply-verify=false -f nodes/node0.yaml
# Expected: drift preview + 'Applied configuration without a reboot' + silent post-apply verify
```

#### Multi-node + mix

```bash
talm apply --dry-run -f nodes/node0.yaml -f nodes/node1.yaml -f nodes/node2.yaml
# Each node renders its own preview; per-node independence.
```

#### Insecure path

`talm apply -i` exercises the maintenance connection. The cozystack reference chart uses live discovery (`lookup "disks"`), which fails on insecure (no auth for COSI). The render errors before the gate runs — that's existing talm behaviour, not a regression.

### Implementation health

Run as part of every push:

```bash
go test ./...
go test -race ./pkg/applycheck/... ./pkg/commands/...
golangci-lint run ./...
GOOS=windows golangci-lint run ./...
go vet ./...
```

### Known limitations / follow-ups

- **Talos-mutated-field allowlist**: Phase 2B reports cert hashes / timestamps as divergence today; the verify is off by default until an allowlist lands.
- **`talm upgrade` has no pre-upgrade gates** (Phase 2C runs *after*, not before): the upgrade flow wraps `talosctl upgrade` and doesn't route through `buildApplyClosure` / `applyOneFileDirectPatchMode`, so Phase 1 / Phase 2A do not run. Phase 2C (post-upgrade version verify) was added precisely to catch the silent-rollback class without that refactor. Full pre-upgrade gates would require reproducing the gate calls in `upgrade_handler.go` or refactoring the apply flow.
- **Phase 1/2 on `--insecure`**: the safety gates can't run before the chart renders, and the chart's `lookup` calls need an authenticated COSI connection. Insecure path = effectively no gates today.

## D. Apply (insecure / maintenance path)

### D1. Apply with chart that uses discovery

```bash
talm apply -i --dry-run -f nodes/node0.yaml
```

Expected: render fails because `lookup "disks"` / `lookup "links"` require auth. Hint mentions reachability.

### D2. Drift-preview degrade on insecure path (when render succeeds)

When a chart renders fully offline (no `lookup`), `talm apply -i` runs through to the gates. Phase 2A prints `drift verification unavailable on maintenance connection` and proceeds; Phase 2B same.

**Regression anchor**: D2's offline-renderable behaviour is also covered by unit-level mocking — see `pkg/commands/preflight_apply_safety_test.go` for the in-process equivalent. Surface that file's tests in the manual suite when D2 is impractical to exercise live.

### D3. Per-node prefix on the maintenance-connection warning

On a multi-node insecure apply where every node hits the `ok=false` (maintenance) path, each per-node emission of the warning must carry the node identifier prefix so the operator can correlate which line came from which node:

```bash
talm apply -i \
  --nodes 192.0.2.10,192.0.2.11,192.0.2.12 \
  --endpoints 192.0.2.10,192.0.2.11,192.0.2.12 \
  -f nodes/node0.yaml
```

Expected (per node): `node 192.0.2.10: talm: drift verification unavailable on maintenance connection`. The single-node case (empty `nodeID`, the implicit path) MUST still emit the bare `talm: drift verification unavailable on maintenance connection` line — no `node : ` garbage prefix.

Regression anchor: a refactor that always-prefixes (`node : talm: ...` on single-node) is a UX regression. The `nodePrefix("")` helper must collapse to empty for the bare-line single-node case.

## E. Upgrade

### E1. Stage an upgrade to the same image

```bash
talm upgrade --stage -f nodes/node0.yaml
```

Note: `--stage --wait` (the default) actually triggers a reboot to apply the staged upgrade. Plan for a 1-2 minute outage of the node under test. The cluster should stay healthy if you have 3+ control plane nodes and other nodes hold quorum.

Expected: events stream from BOOTING through `post check passed`. Node returns to running state.

### E2. Upgrade with bad image

```bash
talm upgrade --image ghcr.io/cozystack/cozystack/talos:doesnotexist \
  --stage -f nodes/node0.yaml
```

Expected: `error validating installer image ... not found`. Talos itself catches this; talm passes through the error.

### E3. Configurable post-upgrade reconcile window

```bash
# Help-text surface — confirms the flag is registered with the 90s default.
talm upgrade --help | grep -A1 post-upgrade-reconcile-window

# Custom widened window (slow hardware / large image pulls).
talm upgrade --post-upgrade-reconcile-window=180s \
  --image ghcr.io/siderolabs/installer:v1.13.0 \
  --stage -f nodes/node0.yaml

# Rejection of non-positive values.
talm upgrade --post-upgrade-reconcile-window=0s \
  -f nodes/node0.yaml
```

Expected for the help line: flag listed with `default 1m30s`. Expected for the 180s run: stderr emits `post-upgrade verify: waiting 3m0s for the node to finish booting...` — Go's `time.Duration.String()` renders `180 * time.Second` as `3m0s` deterministically, not `180s`. Expected for the `0s` rejection: error with a hint mentioning "positive duration".

Regression anchor: the version-mismatch hint emitted on a Phase 2C blocker MUST NOT contain the literal string `90s` — operators passing a custom window would see contradictory advice. The hint should reference "the configured reconcile window (`--post-upgrade-reconcile-window`)" instead.

## F. CA rotation

### F1. Rotate CA dry-run

```bash
talm rotate-ca --dry-run --nodes $NODE --endpoints $NODE
```

Expected: every per-step line ends with `(dry-run)`; final line mentions `re-run with \`--dry-run=false\` to apply the changes`. Possibly trailing `failed to create new client with rotated Talos CA` — harmless under dry-run.

### F2. Real CA rotation

```bash
talm rotate-ca --dry-run=false --nodes $NODE --endpoints $NODE
```

Expected: `CA rotation completed successfully!`. `secrets.yaml`, `secrets.encrypted.yaml`, `talosconfig`, `kubeconfig` updated on disk.

### F3. Apply after rotation

```bash
talm apply --dry-run -f nodes/node0.yaml
```

Expected: works against the rotated CA. No `tls: certificate required` errors.

## G. META partition

### G1. Read / list

```bash
talm get metakey --nodes $NODE --endpoints $NODE
```

Expected: table of META keys with their values.

### G2. Write a test key

```bash
talm meta write 0x0a "test-value" --nodes $NODE --endpoints $NODE
talm get metakey 0x0a --nodes $NODE --endpoints $NODE
```

Expected: written; reads back the value.

### G3. Delete the test key

```bash
talm meta delete 0x0a --nodes $NODE --endpoints $NODE
talm get metakey 0x0a --nodes $NODE --endpoints $NODE
```

Expected: delete succeeds; read returns `NotFound`.

## H. Reset and recovery

### H1. Bootstrap on running cluster

```bash
talm bootstrap --nodes $NODE --endpoints $NODE
```

Expected: refuses with `etcd data directory is not empty`.

### H2. Reset a control-plane node (talm safe default — preserves META)

⚠️ Destructive. Run only against a cluster you can afford to lose one node from. The talm default populates `--system-labels-to-wipe=STATE,EPHEMERAL` automatically when neither `--wipe-mode` nor `--system-labels-to-wipe` was passed, so META survives and the node self-recovers on the next boot. Upstream `talosctl reset` defaults to `--wipe-mode=all`, which destroys META; that path is exposed in talm as the explicit `--wipe-mode=all` opt-in (see H2a).

```bash
talm reset --graceful=true --reboot \
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
talm reset --wipe-mode=all --graceful=true --reboot \
  --nodes $NODE --endpoints $OTHER_NODE
talm reset --wipe-mode=system-disk --graceful=true --reboot \
  --nodes $NODE --endpoints $OTHER_NODE
```

Expected: same as H2 up to the reboot; after the reboot the node comes up in maintenance mode (no machine config). `talm get hostnames -i --nodes $NODE` succeeds via the insecure path but the node is not yet a cluster member.

Regression anchor: when EITHER of these commands is run the wrapper MUST NOT silently add `--system-labels-to-wipe=STATE,EPHEMERAL` (which would override the operator's stated intent and quietly turn a destructive reset into a selective one). Verify via `talm reset --wipe-mode=all --help` or by observing that the reset request actually destroys META.

### H2b. Reset with operator-specified narrower scope (`--system-labels-to-wipe=STATE` only)

```bash
talm reset --system-labels-to-wipe=STATE --graceful=true --reboot \
  --nodes $NODE --endpoints $OTHER_NODE
```

Expected: only STATE wiped, EPHEMERAL kept (containerd image cache survives the reset), node returns. The operator's explicit narrower list must be honored byte-for-byte; the wrapper MUST NOT silently expand to `STATE,EPHEMERAL`.

Regression anchor: after the node returns, `talm logs kernel --nodes $NODE | grep -i ephemeral` should show no fresh-format markers for the EPHEMERAL partition. If the wrapper silently expanded the operator's list, EPHEMERAL would have been wiped too.

### H2c. Reset with `--graceful=false` (ungraceful, preserves safe default)

```bash
talm reset --graceful=false --reboot \
  --nodes $NODE --endpoints $OTHER_NODE
```

Expected: ungraceful reset (no etcd leave), but the wrapper's safe default still fires (STATE+EPHEMERAL labels populated by talm because no wipe flag was passed). Node reboots; etcd cluster recovers via remaining quorum; rejoining member appears within ~120s.

Regression anchor: the default-flip MUST be independent of `--graceful`. A change that conditions the flip on `--graceful=true` is a regression — operators on ungraceful reset are the ones who most need the safe default.

### H2d. Reset triggered from modeline-bearing project root

```bash
cd $PROJECT  # directory with nodes/$NODE.yaml carrying the modeline
talm reset --reboot --graceful=true
```

Expected: same outcome as H2 — modeline supplies `--nodes` / `--endpoints` from `nodes/$NODE.yaml`, no wipe flags on the CLI, wrapper applies the safe default, META preserved.

Regression anchor: the default-flip is gated on `Changed("wipe-mode") && Changed("system-labels-to-wipe")` only — it is independent of where in the PreRunE chain it runs. A refactor that reorders the dispatch chain must keep this path working (modeline-supplied `--nodes` / `--endpoints` plus no operator-supplied wipe flags must still produce the safe default).

### H3. Etcd quorum after reset

```bash
talm etcd members --nodes $OTHER_NODE --endpoints $OTHER_NODE
```

Expected during the reset: 2 of 3 members. After Talos brings the reset node back from META (typical Linux/Talos auto-rejoin path): 3 members; the reset node may carry a placeholder hostname (`talos-XXXXX`) until the next apply.

### H4. Rejoin after reset

```bash
talm apply --dry-run -f nodes/node-resetted.yaml
```

Expected: `0 addition, 0 removal, 0 update, N unchanged` when META preserved the full config; otherwise drift will reflect the missing state (re-apply to fix).

### H5. Insecure path on a freshly-wiped node

```bash
talm apply -i -f nodes/node-fresh.yaml
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
           "logs kernel --tail 3" netstat routes "usage /var/log" \
           "logs kubelet" "logs etcd" "events --tail=3" \
           "image list" "etcd status" "etcd alarm list"; do
  timeout 8 talm $cmd --nodes $NODE --endpoints $NODE 2>&1 | head -1
done
```

Expected: every command prints either a header row (table) or an error from the node side. None should hang past the timeout. The `logs kernel` entry replaces the retired `dmesg` command; `logs kernel --tail=N` is the supported way to read the last N kernel-log lines.

### I0-1a. `talm dmesg` is retired — migration stub surfaces a hint

```bash
talm dmesg --tail 3
talm dmesg --nodes $NODE
```

Expected: both invocations exit non-zero with `talm dmesg has been removed` and a hint pointing at `talm logs kernel --tail=N --nodes <node>`. The stub is hidden from `talm --help` (operators don't see a retired command as available). The migration hint surfaces regardless of cwd (the stub skips Chart.yaml loading, mirroring `init` and `completion`).

Regression anchor: a regression that re-enables the upstream `dmesg` wrap (removing it from `excludedCommands` in `pkg/commands/talosctl_wrapper.go`) would either collide with the talm-owned stub at cobra registration, or — if the stub is also dropped — leave operators with the original cryptic `strconv.ParseBool` failure on `--tail=N`. Both shapes are documented elsewhere; this anchor pins the proactive removal + migration-hint contract.

### I0-2. Concurrent dry-run apply

```bash
for i in 1 2 3; do
  talm apply --dry-run -f nodes/node0.yaml 2>&1 | grep -E "^talm:" &
done
wait
```

Expected: 3 independent renders, all complete, no race-condition diagnostics.

### I0-3. CLI nodes/endpoints override modeline

```bash
talm apply --dry-run \
  --nodes $OTHER_NODE --endpoints $OTHER_NODE \
  -f nodes/node0.yaml | grep "^- talm"
```

Expected: log line shows `nodes=[$OTHER_NODE]` not the modeline value. The CLI takes precedence.

### I0-4. Reboot a node (no config change)

```bash
talm reboot --nodes $NODE --endpoints $NODE
```

⚠️ Destructive timing — the node will be unreachable for ~30-60s. Cluster keeps quorum if at least one other controlplane is healthy.

Expected: returns once events check completes; etcd member list shows the node back in.

### I0-5. Wipe a non-system disk

```bash
talm wipe disk <devname> --nodes $NODE --endpoints $NODE
```

Expected: refuses with `FailedPrecondition: blockdevice "<dev>" is in use by disk "..."` if it's mounted / part of LVM / part of DRBD. Wipe succeeds only on truly idle block devices. The error itself is the regression pin: a wipe that DIDN'T refuse would risk destroying the cluster's persistent state.

## I. Shell completion

### I1a. Per-flag and positional completion

Install bash completion:

```bash
talm completion bash > /tmp/talm-completion.bash
source /tmp/talm-completion.bash
```

Then exercise each target. Each expectation below is a forward-looking check the operator runs interactively (or via `__complete <subcommand> <flag-value-or-positional> ""` for scripted assertions).

- `talm init --preset <TAB>` → curated list including `cozystack`. Sourced from `pkg/generated/presets.go::AvailablePresets()`; no generic file completion.
- `talm apply --mode <TAB>` → `auto`, `no-reboot`, `reboot`, `staged`, `try` (the apply-mode enum).
- `talm apply --file <TAB>` → only `nodes/*.yaml` files that carry a valid `# talm: …` modeline. Non-modelined yaml files in the project tree do NOT surface. Same for `talm template --file <TAB>` and `talm upgrade --file <TAB>`.
- `talm template --values <TAB>` / `--with-secrets <TAB>` → file completion narrowed to `.yaml` / `.yml` extensions (`ShellCompDirectiveFilterFileExt`).
- `talm --nodes <TAB>` / `talm --endpoints <TAB>` → union of nodes / endpoints declared across every context in the active talosconfig.

Regression anchor: positional completion (`talm apply <TAB>` without a flag) MUST NOT surface anything — apply / template / upgrade declare `cobra.NoArgs` so the positional slot is empty. A regression that wires `ValidArgsFunction` to a populated completer would silently re-introduce a dead-completion code path.

### I1. Generate completion for each shell

```bash
for sh in bash zsh fish powershell; do
  talm completion $sh > /tmp/talm-completion.$sh
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
talm template -f nodes/node0.yaml --set floatingIP=0700
```

Expected: with an IPv4-validating chart, fails fast with `talm: floatingIP "0700" is not a valid IPv4 / IPv6 literal`. A chart without the validation silently renders an invalid VIP.

**Operator footgun**: `--set floatingIP=198.51.100.1` *may* be parsed as the float `198.51 × 100.1` by Helm's loose type-coercion. For IPs use `--set-string floatingIP="198.51.100.1"` or put it in `values.yaml`.

`--set` emits a non-fatal warning to stderr when the RHS matches an IP-shaped or semver-shaped literal:

```bash
talm template -f nodes/node0.yaml --set endpoint=10.0.0.1 2>&1 | grep "^talm:"
# Expected: warning recommending --set-string for IP / version literals.
talm template -f nodes/node0.yaml --set-string endpoint=10.0.0.1 2>&1 | grep "^talm:"
# Expected: no warning emitted — operator opt-in to verbatim string.
```

Regression anchors:

- Warning fires for IPv4 (`10.0.0.1`, `192.0.2.1/24`) and v-prefixed semver (`v1.2.3`, `v1.2`). Bare two-component decimals (`1.5`, `2.0`) and colon-separated literals (`00:11:22:33:44:55`) do NOT trigger — those aren't strvals-coerced footguns.
- Warning is non-fatal — command still proceeds. A regression that turns it into a hard error would block legitimate `--set` flows.

### J0-2. `--set-literal` keeps dotted keys intact

```bash
talm template -f nodes/node0.yaml \
  --set-literal "label.with.dots=raw"
```

Expected: key `label.with.dots` (single literal) appears in values rather than nested `label → with → dots`.

### J0-3. `--set-file` reads file content as string

```bash
echo "from-file" > /tmp/_v.txt
talm template -f nodes/node0.yaml --set-file someKey=/tmp/_v.txt
rm /tmp/_v.txt
```

Expected: file content available as `.Values.someKey` during render.

### J0-4. `--values` external overlay

```bash
echo "clusterDomain: overlay.local" > /tmp/_overlay.yaml
talm template -f nodes/node0.yaml --values /tmp/_overlay.yaml
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
talm apply --dry-run -f nodes/node0.yaml | head -5
```

Expected: `warning: pre-flight: configured talosVersion=v1.13 is newer than the node's running Talos v1.12.x` plus a hint about rebooting into a matching maintenance image or lowering the contract. Drift preview still runs.

### K1-pre. Phase 2C version-verify catches silent rollback

⚠️ Same destructive setup as K2, but the gate now does the work automatically. **Heads-up**: the upgrade wrapper resolves the target image from `values.yaml::image` (rendering the chart against current values), not from the rendered config that lives in `nodes/<name>.yaml`. Bump the image in `values.yaml` to trigger an intentionally-bad cross-vendor upgrade.

Run an intentionally-bad cross-vendor upgrade and expect a hint-bearing blocker:

```bash
sed -i 's|cozystack/cozystack/talos:v1.12.6|siderolabs/installer:v1.13.0|' values.yaml
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
talm upgrade --stage -f nodes/node0.yaml
```

Expected: events stream from `installAndReboot` through `post check passed`. Node returns running v1.13.x (`talm version --nodes $NODE`). Etcd member count unchanged (`talm etcd members`).

### K3. Per-node sequential upgrade (safe)

```bash
for n in node0 node1 node2; do
  talm upgrade --stage -f nodes/$n.yaml
  talm etcd members --nodes $OTHER --endpoints $OTHER \
    | grep -c "^[0-9]"  # quorum must be >= 2 at all times
done
```

Expected: each node returns to etcd within 60s; quorum never drops below 2/3 (one node down at a time).

### K4. Phase 2A drift preview against new-version node

After K2, with the chart still on v1.13 contract:

```bash
talm apply --dry-run -f nodes/node0.yaml
```

Expected: no version-mismatch warning (chart contract matches running). Drift preview shows the per-version diff if any (e.g. a new field machinery v1.13 injects).

### K5. Phase 2B on a heterogeneous cluster (mid-rollout)

Between K2-step-1 (node0 upgraded) and K2-step-2 (node1 still on old version), Phase 1 still resolves `lookup "links"` (non-Sensitive COSI resource works on both versions). Phase 2A diffs against the specific node, so the cross-version state is per-node, not cluster-wide. Phase 2B (if enabled) compares against the bytes sent; expect cert-hash false-positives until the Talos-mutated-field allowlist lands.

## L. Extended diagnostics + service control

### L1. `inspect dependencies` returns a DOT graph

```bash
talm inspect dependencies --nodes $NODE --endpoints $NODE | head
```

Expected: starts with `digraph {`. Useful for visualizing Talos controller deps. Pipe through `dot -Tpng` to render.

### L2. `pcap` short capture on loopback

```bash
timeout 8 talm pcap --nodes $NODE --endpoints $NODE \
  --interface lo --duration 2s > /tmp/_cap.pcap
file /tmp/_cap.pcap && rm /tmp/_cap.pcap
```

Expected: binary pcap stream to stdout. `file` reports "pcap capture file".

### L3. `time` against NTP

```bash
talm time --nodes $NODE --endpoints $NODE
```

Expected: table with `NTP-SERVER`, `NODE-TIME`, `NTP-SERVER-TIME`.

### L4. `etcd defrag`

```bash
talm etcd defrag --nodes $NODE --endpoints $NODE
```

Expected: silent return (no output), exit 0. DB is defragmented.

### L5. `etcd alarm list`

```bash
talm etcd alarm list --nodes $NODE --endpoints $NODE
```

Expected: empty output on a healthy cluster. Any output indicates an alarm to investigate (NOSPACE / CORRUPT).

### L6. `etcd forfeit-leadership` on a non-leader

```bash
talm etcd forfeit-leadership --nodes $NON_LEADER --endpoints $NODE
```

Expected: silent no-op. Leader unchanged.

### L7. `service kubelet restart`

```bash
talm service kubelet restart --nodes $NODE --endpoints $NODE
```

Expected: `Service "kubelet" restarted`. Pod replays after ~10s.

### L8. `service kubelet stop` (refused)

```bash
talm service kubelet stop --nodes $NODE --endpoints $NODE
```

Expected: `kubelet doesn't support stop operation via API`. Talos intentionally blocks stop on essential services.

### L9. `shutdown` (destructive)

⚠️ Powers off the node. Recovery requires `tofu apply` (or manual provider-side reboot). Use only against a node whose recovery path you control.

```bash
talm shutdown --nodes $TARGET --endpoints $OTHER
```

Expected: events stream through `teardownLifecycle` → `stopEverything` → `events check condition met`. Etcd member remains in the list until TTL expires (~10 min) or the next membership reconciliation.

### L10. `get rd` lists registered resource types

```bash
talm get rd --nodes $NODE --endpoints $NODE | wc -l
```

Expected: 100+ resource types. Baseline for `get <type>` calls.

### L11. `get -o jsonpath`

```bash
talm get hostname --nodes $NODE --endpoints $NODE \
  -o jsonpath='{.spec.hostname}'
```

Expected: the node's hostname as a bare string. Useful for scripted extraction.

### L12. `logs --tail N`

```bash
talm logs kubelet --tail 3 --nodes $NODE --endpoints $NODE
```

Expected: last 3 lines of kubelet log.

## M. Negative / boundary cases

### M1. Malformed modeline

```bash
echo "# talm: nodes=this-is-not-json-array" > /tmp/_bad.yaml
echo "machine: {type: controlplane}" >> /tmp/_bad.yaml
talm apply --dry-run -f /tmp/_bad.yaml
rm /tmp/_bad.yaml
```

Expected: `error parsing JSON array for key nodes` with a hint about the expected syntax. The error surfaces the modeline parse step — the file is NOT silently routed to direct-patch mode where the malformed modeline would be invisible.

### M1a. Orphan file (no modeline) inside project root — classified as orphan, not malformed

```bash
echo "# operator note above plain YAML" > nodes/_orphan-commented.yaml
echo "machine: {}" >> nodes/_orphan-commented.yaml
talm apply --dry-run --nodes $NODE --endpoints $NODE -f nodes/_orphan-commented.yaml
rm nodes/_orphan-commented.yaml
```

Expected: dispatch into direct-patch mode — progress line `- talm: file=nodes/_orphan-commented.yaml, nodes=[$NODE], endpoints=[$NODE]` followed by drift preview. NO "parsing modeline" error, NO "non-comment line found before modeline" error. The classifier (`FindAndParseModeline`) must distinguish "no `# talm:` line anywhere" (orphan, → `ErrModelineNotFound`) from "modeline present but parse failed" (malformed).

Note: the orphan must live inside the project tree (e.g. under `nodes/`) so root detection finds the project's Chart.yaml / secrets.yaml. An orphan placed at `/tmp/` is rejected at root detection before the modeline classifier runs — see C6b-pre for that path.

### M1b. Misplaced modeline (modeline below YAML) — rejected with a specific hint

```bash
cat > nodes/_misplaced.yaml <<'EOF'
machine:
  type: controlplane
# talm: nodes=["1.2.3.4"], endpoints=["1.2.3.4"], templates=["templates/controlplane.yaml"]
EOF
talm apply --dry-run -f nodes/_misplaced.yaml
rm nodes/_misplaced.yaml
```

Expected: error `modeline found below non-comment content: first non-comment line was "machine:"` with hint `the # talm: … modeline must precede any YAML content; only #-prefixed comments and blank lines may sit above it`. Distinct from both M1 (malformed value) and M1a (orphan).

Regression anchor 1: the classifier lookahead must scan the entire file for a `# talm:` line before deciding orphan vs misplaced. A regression that returns `ErrModelineNotFound` on first non-comment encounter (without lookahead) would silently route misplaced-modeline files into direct-patch mode, hiding the operator error.

Regression anchor 2: the lookahead is column-0-strict. An indented `# talm:` line inside a YAML body (e.g. `  # talm: see docs for nodes/templates wiring`) is operator-authored documentation, NOT a misplaced modeline; it MUST NOT trip the misplaced-modeline error. Verify by writing a node file with `  # talm: comment` indented under a YAML key and confirming the file routes to orphan / direct-patch mode rather than failing on misplaced-modeline.

### M2. Malformed patch (string-where-map)

```bash
cat > /tmp/_bad.yaml << 'EOF'
# talm: nodes=["192.0.2.4"], endpoints=["192.0.2.4"], templates=["templates/controlplane.yaml"]
machine:
  type: controlplane
  install: not-a-map-but-a-string
EOF
talm apply --dry-run -f /tmp/_bad.yaml
rm /tmp/_bad.yaml
```

Expected: `yaml: construct errors: cannot construct !!str ... into v1alpha1.InstallConfig` plus a hint about patch shape.

### M3. Bad `--cert-fingerprint`

```bash
talm apply --insecure --cert-fingerprint deadbeef \
  -f nodes/node0.yaml
```

Expected: TLS handshake error `leaf peer certificate doesn't match the provided fingerprints: [deadbeef]`.

### M4. `--talosconfig` pointing at missing file

```bash
talm apply --dry-run --talosconfig /tmp/nonexistent \
  -f nodes/node0.yaml
```

Expected: `talos config file is empty`. Apply does not proceed.

### M5. `TALOSCONFIG` env var

```bash
TALOSCONFIG=$PWD/talosconfig talm apply --dry-run \
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
  talm apply --dry-run -f nodes/$n.yaml | grep -E "^talm:"
done
talm etcd members --nodes $NODE --endpoints $NODE
talm health --nodes $NODE --endpoints $NODE
```

Expected: each node reports drift preview (typically `0/0/0 unchanged` after a clean run), etcd shows 3 members, health passes.

## Adversarial extras

These don't ship as part of the regular run but are worth re-running after refactors that touch the walker / differ / preflight hooks.

### Walker robustness

```bash
echo "" > /tmp/empty.yaml
talm apply --dry-run -f /tmp/empty.yaml
# Expected: modeline-prefix error.

cat > /tmp/modeline-only.yaml <<EOF
# talm: nodes=["$NODE"], endpoints=["$NODE"], templates=["templates/controlplane.yaml"]
EOF
talm apply --dry-run -f /tmp/modeline-only.yaml
# Expected: drift preview, possibly significant `-` removals.
```

### Schema confusion

Try every BondConfig / VLANConfig / BridgeConfig YAML field permutation that operators paste from old docs / unofficial gists. The walker must catch typos in:

- `BridgeConfig.links[]` (legacy docs say `ports[]` — Phase 1 must NOT treat `ports[]` as valid).
- `VLANConfig.parent` (legacy docs say `link` — same rule).

### Encoding edge cases

```bash
printf '\xef\xbb\xbfmachine:\r\n  install:\r\n    disk: /dev/sda\r\n' > /tmp/bom.yaml
talm apply --dry-run -f /tmp/bom.yaml
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
