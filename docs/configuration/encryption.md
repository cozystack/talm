# Encryption

Talm provides built-in encryption support using [age](https://age-encryption.org/) encryption. Sensitive files are encrypted with their values stored in SOPS format (`ENC[AGE,data:...]`), while YAML keys remain unencrypted for better readability.

## Encrypting files

To encrypt all sensitive files (secrets.yaml, talosconfig, kubeconfig):

```bash
talm init --encrypt
# or
talm init -e
```

This command will:
- Generate `talm.key` if it doesn't exist
- Encrypt `secrets.yaml` → `secrets.encrypted.yaml`
- Encrypt `talosconfig` → `talosconfig.encrypted`
- Encrypt `kubeconfig` → `kubeconfig.encrypted` (if exists)
- Encrypt `values-secret.yaml` → `values-secret.encrypted.yaml` (if exists)
- Update `.gitignore` with sensitive files

## Decrypting files

To decrypt all encrypted files:

```bash
talm init --decrypt
# or
talm init -d
```

This command will:
- Decrypt `secrets.encrypted.yaml` → `secrets.yaml`
- Decrypt `talosconfig.encrypted` → `talosconfig`
- Decrypt `kubeconfig.encrypted` → `kubeconfig` (if exists)
- Decrypt `values-secret.encrypted.yaml` → `values-secret.yaml` (if exists)
- Update `.gitignore` with sensitive files

## Encrypted user values

Beyond Talos' own PKI/tokens, you can store **arbitrary secret values that chart templates consume** (a registry password, a KMS plugin's secret-id, etc.) encrypted at rest with the same `talm.key`:

1. Author the secrets in plaintext `values-secret.yaml` (git-ignored), e.g.:

   ```yaml
   registryPassword: hunter2
   ```

2. Encrypt them with `talm init --encrypt` → produces the committable `values-secret.encrypted.yaml` (per-value `ENC[AGE,...]` envelopes; keys stay readable).

3. Reference the **encrypted** file from `Chart.yaml`:

   ```yaml
   templateOptions:
     valueFiles:
       - values-secret.encrypted.yaml
   ```

`talm template` and `talm apply` decrypt it **in memory** via `talm.key` — the plaintext never has to be present at render time. Both commands honor the full value-source set (`--values`, `--set`, `--set-string`, `--set-file`, `--set-json`, `--set-literal`) plus `templateOptions.*`, so a value renders identically whether you preview with `template` or push with `apply`.

Secret values are kept out of committed and printed output:

- `talm template -I` **omits** secret-bearing fields from the rendered `nodes/*.yaml`; the real value is re-injected only at `apply` (which re-renders from the encrypted file).
- `talm template` (stdout) and `talm apply` (drift preview) **redact** them to `***` by default. Reveal verbatim with `talm template --show-secrets` / [`talm apply --show-secrets-in-drift`](../operations/safety-gates.md) (debugging only).

!!! danger "Put the encrypted file in Chart.yaml, not only on the command line"

    Reference the encrypted file from `Chart.yaml templateOptions.valueFiles` (as shown above), NOT only via `template --values`. The node-file modeline does not persist value files, so `apply` only re-reads what is in `Chart.yaml` (plus its own `--values`). If an encrypted file is passed solely to `template -I`, the omitted secret is absent from the node file AND never re-rendered at apply — silently lost from the applied config. `template -I` prints a warning when it omits secrets from a file that is not in `Chart.yaml`.

!!! warning "Encrypt only high-entropy values"

    Sealing matches by exact value across the whole rendered config, so do not encrypt low-entropy values that collide with ordinary config strings (e.g. a bare port, or a password literally set to `controlplane`) — that unrelated field would be sealed too. Prefer high-entropy secrets. Secret values must be strings (quote them in `values-secret.yaml`); the encryption only covers string leaves.

## Key management

The `talm.key` file is generated in age keygen format and contains:
- Creation timestamp
- Public key (for sharing)
- Private key (keep secure!)

!!! danger "Back up talm.key"

    Always backup your `talm.key` file! Without it, you won't be able to decrypt your encrypted secrets. The key file is automatically added to `.gitignore` to prevent accidental commits.

Encrypted files (`*.encrypted.yaml`, `*.encrypted`) can be safely committed to Git, while plain files (`secrets.yaml`, `talosconfig`, `kubeconfig`, `talm.key`) are ignored.
