# Applying with side-patches

`talm apply -f` accepts a chain of files. The FIRST `-f` is the **anchor** — it must carry a `# talm: nodes=[…], templates=[…]` modeline and live under a `talm init`'d project (Chart.yaml + secrets.yaml). Any subsequent `-f` files are **side-patches**: they are merged in order on top of the anchor's rendered config, and a single `ApplyConfiguration` is issued per node carrying the composed result.

```bash
# Single node file (anchor only):
talm apply -f nodes/node1.yaml

# Side-patch stacked on top — useful for one-shot overlays
# (debug kubelet flags, temporary cert SANs, mode=staged drills):
talm apply -f nodes/node1.yaml -f /tmp/debug-kubelet.yaml
```

Side-patches do not need to live under the project root; the first file anchors detection. Reversing the order is an error — the first file must be the rooted anchor.

Applying several nodes at once is a separate operation, not a longer `-f` chain — `talm apply -f n1.yaml -f n2.yaml -f n3.yaml` does not run three applies. Invoke `talm apply` once per node file:

```bash
for n in nodes/*.yaml; do talm apply -f "$n"; done
```

That misuse is rejected loudly: if any of the subsequent `-f` files carries its own `# talm: …` modeline, the apply errors out before any RPC fires with a hint pointing at the shell loop above. Stripping the modeline turns a file into a legitimate side-patch.

Side-patches with a non-empty body are restricted to **single-node anchors**. The same body cannot be distinguished from per-node fields (hostname, address, VIP) versus cluster-wide knobs (NTP servers, KubeProxy mode) by static inspection, and stamping per-node fields across N machines is the original foot-gun the per-node-body guard was designed to prevent. If your anchor's `nodes=[…]` lists more than one target and your side-patch is non-empty, talm rejects the apply early with a hint pointing at the per-file shell loop. For cluster-wide overlays on multi-node anchors, fold the overlay into `values.yaml` or templates rather than passing it as a side-patch; for per-node overrides, generate per-node files via `talm template -I` and feed them into the per-file shell loop.
