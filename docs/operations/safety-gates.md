# Apply-time safety gates

`talm apply` and `talm upgrade` run additional gates around each operation. Each is opt-out through its own flag; the flags do not suppress each other.

| Gate | Flag | Gate runs by default |
| --- | --- | --- |
| Declared-resource existence | `--skip-resource-validation` | on |
| Pre-apply drift preview | `--skip-drift-preview` | on |
| Post-apply state verification | `--skip-post-apply-verify` | **off** |
| Post-upgrade version verify | `--skip-post-upgrade-verify` | on |

## 1. Declared-resource existence

Before sending the config to the node, the gate walks the rendered MachineConfig, extracts every reference to a host-side resource (network links from v1.12 multi-doc — `LinkConfig.name`, `BondConfig.links[]`, `VLANConfig.parent`, `BridgeConfig.links[]`, `VRFConfig.links[]`, `Layer2VIPConfig.link`, `HCloudVIPConfig.link`, `DHCPv4Config.name` / `DHCPv6Config.name` / `EthernetConfig.name`, and from v1.14 `BGPInstanceConfig.advertise[]` and `BGPInstanceConfig.neighbors[].link`; v1.11 legacy `machine.network.interfaces[].interface`; install disk via `machine.install.disk` literal or `machine.install.diskSelector`; `UserVolumeConfig.provisioning.diskSelector`), and verifies each against the node's COSI `LinkStatus`/`Disk` snapshots. A reference that doesn't resolve fails the apply with a `[blocker]` line listing the available names so the typo or migration miss is fixable from the values without re-running discovery.

Disk selectors must match at least one (non-readonly, non-CDROM, non-virtual) disk — zero matches block, multiple matches warn (install picks the first).

### Virtual links this apply creates

Virtual-link-creator documents are intentionally NOT validated against existing links — their `.name` describes a new virtual link the apply is creating, not a reference to a pre-existing host resource. They also register that name as a link this apply brings into existence, so a `VLANConfig.parent`, `BondConfig.links[]`, `Layer2VIPConfig.link` or `BGPInstanceConfig.advertise[]` pointing at one of them resolves rather than blocking. That is what makes a `network.extraLinks` bond usable on first apply, before the node carries it.

The set is talm's own, chosen per document by what its `.name` means: `BondConfig`, `BridgeConfig`, `VLANConfig`, `DummyLinkConfig`, `LinkAliasConfig`, `WireguardConfig`, `VRFConfig`, and `VethConfig` — the last registering both its `.name` and its `.peer.name`, since a veth pair creates both ends. `LinkConfig` is deliberately not among them: its `.name` addresses an existing NIC, so it is validated rather than recorded.

A `LinkAliasConfig` name ending in `%d` is registered as a pattern rather than a literal, because Talos expands it into one sequential alias per matched link (`net0`, `net1`, …) — so a reference to `net0` resolves while the literal `net%d`, which never exists on the node, is not treated as a link.

`WireguardConfig` is walked twice, by design: the net-addr walker checks its `peers[].endpoint`, and the link walker records the link it creates. That is safe because the link side emits only a created-link entry, which seeds the known-links set and produces no finding of its own — so the two walkers cannot report the same mistake twice.

### Address and CIDR checks

The gate also runs a syntactic net-addr walker against `StaticHostConfig.name` (must parse as an IP literal — the `name` field on this kind doubles as the IP the hostnames map to), `NetworkRuleConfig.ingress[].subnet` and `.except` (per-entry CIDR), and `WireguardConfig.peers[].endpoint` (host:port; empty / absent endpoint is a listener-only peer, NOT a finding).

### Limits and escape hatch

`machine.disks[].device` (extra-disk partitioning) is not covered — a reference there is neither validated nor blocked.

With `network.preserveExisting` the rendered config carries the running `machine.network.interfaces` block instead of typed per-link documents, so the gate validates the legacy `interface` fields from that block.

Pass `--skip-resource-validation` for recovery into a maintenance image with mismatched hardware or pre-staging values for hardware that isn't installed yet.

## 2. Pre-apply drift preview

Reads the node's current MachineConfig via COSI and prints a `+`/`-`/`~`/`=` diff of what's about to change, keyed by `(kind, name)`. Informational only — never blocks. The `-` lines are the most useful: they surface stale documents from a previous apply that the new render no longer emits (e.g. an `eth1` LinkConfig lingering after a migration to `eth0`). Reading the current config requires the auth path — `MachineConfig` is a Sensitive COSI resource and is unreachable on the `--insecure` maintenance connection; the gate prints `drift verification unavailable on maintenance connection` (per-node-prefixed on multi-node insecure apply) and proceeds in that case. Secret-bearing field values (`cluster.token`, `cluster.{ca,aggregatorCA,serviceAccount,etcd.ca}.key`, `machine.token` / `machine.ca.key`, the `cluster.acceptedCAs` / `machine.acceptedCAs` slices, `WireguardConfig.privateKey`, the `peers` slice carrying `presharedKey`s) are redacted by default — both sides render as `***redacted (len=N)***` so a rotation surfaces as different-length sentinels without leaking the value. In addition to that static path allowlist, any value originating from an encrypted user value file (`*.encrypted.yaml` referenced via `templateOptions.valueFiles`) is redacted **by value** wherever it surfaces in the diff (at any path, including nested in a slice) — symmetric with how `talm template` redacts the same values. Pass `--show-secrets-in-drift` to see the raw values verbatim (debugging only — disables both the path-based and value-based redaction for the run). **`--dry-run` runs this gate** — the diff is read-only and "show me what would change" is exactly the dry-run contract.

## 3. Post-apply state verification

After `ApplyConfiguration` returns success, re-reads the on-node MachineConfig and structurally compares it against the bytes that were sent. Divergence blocks the apply chain with a per-document diff, primarily catching silent doc drops (Talos parser ignored an unknown field) and controller reverts. Disabled by default because Talos mutates a handful of leaf fields post-apply (cert hashes, timestamps) that would surface as false-positive divergence without an allowlist. The verify runs only on `--mode=no-reboot`. `--mode=staged`, `--mode=try` and `--mode=auto` all skip the gate — each for a documented reason: staged stores rather than activates; try auto-rolls back; auto is promoted by Talos to REBOOT internally when the change requires it, so the verify would race the reboot that a rebooting apply dispatches, which kills the COSI connection mid-verify. (Talos v1.14 dropped the separate `--mode=reboot` spelling; `--mode=auto` is what reaches that path now.) `--dry-run` skips it too.

## 4. Post-upgrade version verify

After `talm upgrade` reports success, waits the configured reconcile window (default 90s; tune via `--post-upgrade-reconcile-window` for slow hardware / large image pulls) for the node to finish booting, then reads `runtime.Version` COSI and compares the running version's `(Major, Minor)` contract against the contract parsed from the target image tag. Point releases share a minor contract; cross-minor mismatch surfaces as a hint-bearing blocker. Catches the silent A/B rollback case where the upgrade RPC acks success but Talos rolled back to the previous partition (cross-vendor image, missing extensions, failed boot readiness check, slow boot exceeding the configured window). Best-effort surrender on digest-pinned images and unparseable tags.

## Skip flags and the maintenance path

The skip flags don't suppress each other — pass them independently. On the `--insecure` (maintenance) path the gates are functionally unreachable for charts that drive discovery via `lookup` — those COSI lookups require an authenticated connection and the render itself errors before any gate runs. Charts that render fully offline (no `lookup` calls) reach the gates on `--insecure` as well, with the Phase 2 hooks degrading gracefully because the `MachineConfig` resource is Sensitive.
