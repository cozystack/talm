// Copyright Cozystack Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package age

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"filippo.io/age"
	"github.com/cockroachdb/errors"
	"gopkg.in/yaml.v3"

	"github.com/cozystack/talm/pkg/secureperm"
)

// ErrLeftoverRotationBackup is returned by RotateKeys when it detects
// `*.rotation-backup` files from a previous run still on disk. Callers
// can use errors.Is to recognise the unsafe-to-rotate state and offer
// targeted recovery guidance instead of a generic failure message.
var ErrLeftoverRotationBackup = errors.New("leftover rotation backup from a previous run (either interrupted, or successful with a failed cleanup step); inspect and remove (or restore) before retrying")

// errInternalInvariant is the sentinel for the typed wrapper helpers
// (encryptYAMLMap / mergeAndEncryptYAMLMap) when their underlying
// recursive function returns a non-map for a map input. Reaching this
// branch means the recursive function violated its own postcondition;
// the wrap chain attached at the call site supplies the offending type.
var errInternalInvariant = errors.New("internal invariant violation: recursive helper returned wrong kind for top-level input")

const (
	keyFileName          = "talm.key"
	encryptedSecretsFile = "secrets.encrypted.yaml"
	plainSecretsFile     = "secrets.yaml"
	ageEncryptionPrefix  = "ENC[AGE,data:"
	ageEncryptionSuffix  = "]"
)

// GenerateKey generates a new age identity and saves it to talm.key file in age keygen format.
// Returns true if a new key was created (not loaded from existing file).
func GenerateKey(rootDir string) (*age.X25519Identity, bool, error) {
	keyFile := filepath.Join(rootDir, keyFileName)

	// Check if key already exists
	_, statErr := os.Stat(keyFile)
	if statErr == nil {
		// Key exists, load it
		identity, err := LoadKey(rootDir)
		if err != nil {
			return nil, false, errors.Wrap(err, "load existing key")
		}

		return identity, false, nil
	}

	// Generate new key
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		return nil, false, errors.Wrap(err, "generate age identity")
	}

	writeErr := secureperm.WriteFile(keyFile, []byte(formatKeyFile(identity, time.Now())))
	if writeErr != nil {
		return nil, false, errors.Wrap(writeErr, "write key file")
	}

	return identity, true, nil
}

// formatKeyFile renders the canonical age keygen layout: a creation
// timestamp comment, a public key comment, and the AGE-SECRET-KEY-1
// secret line, each terminated by a newline. Extracted from
// GenerateKey so RotateKeys can produce the same layout for the
// new identity it generates in memory.
func formatKeyFile(identity *age.X25519Identity, now time.Time) string {
	return fmt.Sprintf(
		"# created: %s\n# public key: %s\n%s\n",
		now.Format(time.RFC3339),
		identity.Recipient().String(),
		identity.String(),
	)
}

// LoadKey loads age identity from talm.key file.
// Supports both age keygen format (with comments) and plain format.
func LoadKey(rootDir string) (*age.X25519Identity, error) {
	keyFile := filepath.Join(rootDir, keyFileName)

	keyData, err := os.ReadFile(keyFile)
	if err != nil {
		return nil, errors.Wrap(err, "read key file")
	}

	// Find the secret key line (starts with AGE-SECRET-KEY)
	lines := strings.Split(string(keyData), "\n")

	var secretKeyLine string

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "AGE-SECRET-KEY-") {
			secretKeyLine = line

			break
		}
	}

	// If no AGE-SECRET-KEY found, try parsing the whole file (old format)
	if secretKeyLine == "" {
		// Try parsing the entire file content (for backward compatibility)
		trimmed := strings.TrimSpace(string(keyData))
		if strings.HasPrefix(trimmed, "AGE-SECRET-KEY-") {
			secretKeyLine = trimmed
		} else {
			//nolint:wrapcheck // errors.WithHint is the project standard for attaching operator hints; the inner errors.New supplies the wrap chain.
			return nil, errors.WithHint(
				errors.New("no AGE-SECRET-KEY found in key file"),
				"the key file must contain an AGE-SECRET-KEY-1... line, either alone (legacy plain format) or alongside age keygen comments",
			)
		}
	}

	identity, err := age.ParseX25519Identity(secretKeyLine)
	if err != nil {
		return nil, errors.Wrap(err, "parse age identity")
	}

	return identity, nil
}

// GetPublicKey returns the public key from an identity.
func GetPublicKey(identity *age.X25519Identity) string {
	return identity.Recipient().String()
}

// GetPublicKeyFromFile extracts the public key from talm.key file.
func GetPublicKeyFromFile(rootDir string) (string, error) {
	keyFile := filepath.Join(rootDir, keyFileName)

	keyData, err := os.ReadFile(keyFile)
	if err != nil {
		return "", errors.Wrap(err, "read key file")
	}

	// Find the public key line (starts with # public key:)
	lines := strings.SplitSeq(string(keyData), "\n")
	for line := range lines {
		line = strings.TrimSpace(line)
		if after, ok := strings.CutPrefix(line, "# public key: "); ok {
			return after, nil
		}
	}

	// Fallback: load identity and get public key
	identity, err := LoadKey(rootDir)
	if err != nil {
		return "", errors.Wrap(err, "load key")
	}

	return identity.Recipient().String(), nil
}

// EncryptSecretsFile encrypts secrets.yaml values and saves to secrets.encrypted.yaml.
// Uses incremental encryption: only encrypts values that have changed.
func EncryptSecretsFile(rootDir string) error {
	return encryptYAMLPair(rootDir, plainSecretsFile, encryptedSecretsFile)
}

// DecryptSecretsFile decrypts secrets.encrypted.yaml and saves to secrets.yaml.
func DecryptSecretsFile(rootDir string) error {
	return decryptYAMLPair(rootDir, encryptedSecretsFile, plainSecretsFile)
}

// encryptYAMLValues recursively encrypts string values in YAML structure.
func encryptYAMLValues(data any, recipient *age.X25519Recipient) (any, error) {
	switch v := data.(type) {
	case map[string]any:
		result := make(map[string]any)

		for key, value := range v {
			encryptedValue, err := encryptYAMLValues(value, recipient)
			if err != nil {
				return nil, err
			}

			result[key] = encryptedValue
		}

		return result, nil
	case []any:
		result := make([]any, len(v))

		for i, item := range v {
			encryptedItem, err := encryptYAMLValues(item, recipient)
			if err != nil {
				return nil, err
			}

			result[i] = encryptedItem
		}

		return result, nil
	case string:
		// Encrypt string value
		encrypted, err := encryptString(v, recipient)
		if err != nil {
			return nil, err
		}

		return ageEncryptionPrefix + encrypted + ageEncryptionSuffix, nil
	default:
		return v, nil
	}
}

// decryptYAMLValues recursively decrypts string values in YAML structure.
func decryptYAMLValues(data any, identity *age.X25519Identity) (any, error) {
	switch v := data.(type) {
	case map[string]any:
		result := make(map[string]any)

		for key, value := range v {
			decryptedValue, err := decryptYAMLValues(value, identity)
			if err != nil {
				return nil, err
			}

			result[key] = decryptedValue
		}

		return result, nil
	case []any:
		result := make([]any, len(v))

		for i, item := range v {
			decryptedItem, err := decryptYAMLValues(item, identity)
			if err != nil {
				return nil, err
			}

			result[i] = decryptedItem
		}

		return result, nil
	case string:
		// Check if it's an encrypted value in SOPS format: ENC[AGE,data:...]
		if strings.HasPrefix(v, ageEncryptionPrefix) && strings.HasSuffix(v, ageEncryptionSuffix) {
			// Extract the encrypted data between ENC[AGE,data: and ]
			encrypted := strings.TrimPrefix(v, ageEncryptionPrefix)
			encrypted = strings.TrimSuffix(encrypted, ageEncryptionSuffix)

			decrypted, err := decryptString(encrypted, identity)
			if err != nil {
				return nil, err
			}

			return decrypted, nil
		}

		return v, nil
	default:
		return v, nil
	}
}

// decryptYAMLValuesString decrypts a single encrypted string value (helper for mergeAndEncryptYAMLValues).
func decryptYAMLValuesString(encrypted string, identity *age.X25519Identity) (string, error) {
	if strings.HasPrefix(encrypted, ageEncryptionPrefix) && strings.HasSuffix(encrypted, ageEncryptionSuffix) {
		encryptedData := strings.TrimPrefix(encrypted, ageEncryptionPrefix)
		encryptedData = strings.TrimSuffix(encryptedData, ageEncryptionSuffix)

		return decryptString(encryptedData, identity)
	}

	return encrypted, nil
}

// mergeAndEncryptYAMLValues merges plain and encrypted YAML, encrypting only changed values.
// This ensures idempotency: unchanged values keep their encrypted form.
func mergeAndEncryptYAMLValues(plain, encrypted any, identity *age.X25519Identity) (any, error) {
	switch plainVal := plain.(type) {
	case map[string]any:
		encryptedMap, ok := encrypted.(map[string]any)
		if !ok {
			// Type mismatch, encrypt everything
			return encryptYAMLValues(plain, identity.Recipient())
		}

		result := make(map[string]any)
		// Copy all keys from plain (to handle new keys)
		for key, plainValue := range plainVal {
			if encryptedValue, exists := encryptedMap[key]; exists {
				// Key exists in both, recursively merge
				merged, err := mergeAndEncryptYAMLValues(plainValue, encryptedValue, identity)
				if err != nil {
					return nil, err
				}

				result[key] = merged
			} else {
				// New key, encrypt it
				encryptedValue, err := encryptYAMLValues(plainValue, identity.Recipient())
				if err != nil {
					return nil, err
				}

				result[key] = encryptedValue
			}
		}

		return result, nil

	case []any:
		encryptedSlice, ok := encrypted.([]any)
		if !ok || len(plainVal) != len(encryptedSlice) {
			// Type or length mismatch, encrypt everything
			return encryptYAMLValues(plain, identity.Recipient())
		}

		result := make([]any, len(plainVal))
		for i, plainItem := range plainVal {
			merged, err := mergeAndEncryptYAMLValues(plainItem, encryptedSlice[i], identity)
			if err != nil {
				return nil, err
			}

			result[i] = merged
		}

		return result, nil

	case string:
		encryptedStr, ok := encrypted.(string)
		if !ok {
			// Type mismatch, encrypt
			return encryptYAMLValues(plain, identity.Recipient())
		}

		// Check if encrypted value is already encrypted
		if strings.HasPrefix(encryptedStr, ageEncryptionPrefix) && strings.HasSuffix(encryptedStr, ageEncryptionSuffix) {
			// Decrypt existing value to compare
			decrypted, err := decryptYAMLValuesString(encryptedStr, identity)
			if err == nil && decrypted == plainVal {
				// Values are the same, keep existing encrypted value (idempotent)
				return encryptedStr, nil
			}
		}
		// Encrypt the new value (if decryption fails, values differ, or both are plain)
		return encryptYAMLValues(plain, identity.Recipient())

	default:
		// For other types, compare directly
		if plain == encrypted {
			// Values are the same, if encrypted is already encrypted, keep it
			if encryptedStr, ok := encrypted.(string); ok {
				if strings.HasPrefix(encryptedStr, ageEncryptionPrefix) && strings.HasSuffix(encryptedStr, ageEncryptionSuffix) {
					return encrypted, nil
				}
			}
		}
		// Encrypt the value
		return encryptYAMLValues(plain, identity.Recipient())
	}
}

// encryptString encrypts a string using age.
func encryptString(plaintext string, recipient *age.X25519Recipient) (string, error) {
	var buf bytes.Buffer

	writer, err := age.Encrypt(&buf, recipient)
	if err != nil {
		return "", errors.Wrap(err, "create encrypt writer")
	}

	_, writeErr := writer.Write([]byte(plaintext))
	if writeErr != nil {
		return "", errors.Wrap(writeErr, "write plaintext")
	}

	closeErr := writer.Close()
	if closeErr != nil {
		return "", errors.Wrap(closeErr, "close encrypt writer")
	}

	// Encode to base64 for safe YAML storage
	return base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

// decryptString decrypts a base64-encoded age-encrypted string.
func decryptString(encryptedBase64 string, identity *age.X25519Identity) (string, error) {
	encrypted, err := base64.StdEncoding.DecodeString(encryptedBase64)
	if err != nil {
		return "", errors.Wrap(err, "decode base64")
	}

	reader, err := age.Decrypt(bytes.NewReader(encrypted), identity)
	if err != nil {
		return "", errors.Wrap(err, "create decrypt reader")
	}

	decrypted, err := io.ReadAll(reader)
	if err != nil {
		return "", errors.Wrap(err, "read decrypted data")
	}

	return string(decrypted), nil
}

// RotateKeys rotates encryption keys in secrets.encrypted.yaml
// RotateKeys atomically rotates the age key encrypting
// secrets.encrypted.yaml. The old key is replaced with a freshly
// generated identity, and the secrets file is re-encrypted under
// the new key.
//
// Atomicity strategy: every disk-mutating step uses os.Rename or
// secureperm.WriteFile (which is itself atomic temp+rename). The
// previous key+encrypted pair is renamed aside into
// `*.rotation-backup` files BEFORE the new files are committed; if
// any later step fails the originals are restored.
//
// The function returns nil only after the new pair is committed
// AND both backup files have been removed. If either commit or
// cleanup fails the function returns a non-nil error, so the only
// state in which `*.rotation-backup` files outlive the call is
// when the call ITSELF returned an error. Operators who find
// leftover `*.rotation-backup` files in that state should:
//
//   - inspect both `talm.key` and the backup; if `talm.key` exists
//     and is newer than the backup, rotation succeeded and only
//     cleanup failed — remove the `*.rotation-backup` files;
//   - otherwise rotation was interrupted before commit — rename
//     the backups back into place to recover the original state.
//
// Both new files are written via secureperm.WriteFile so they end
// up at mode 0o600 (defense-in-depth — age encryption is the
// security layer, but world-readable secrets material on shared
// workstations invites mistakes).
func RotateKeys(rootDir string) error {
	keyFile := filepath.Join(rootDir, keyFileName)
	encryptedFile := filepath.Join(rootDir, encryptedSecretsFile)
	keyBackup := keyFile + ".rotation-backup"
	encryptedBackup := encryptedFile + ".rotation-backup"

	// Refuse to start if leftover rotation backups exist — the
	// operator must inspect them and decide what to keep before
	// another rotation runs, otherwise this run would silently
	// overwrite the recovery state. Both interrupted and
	// successful-with-failed-cleanup states leave these files
	// behind; the docstring above explains how to distinguish.
	for _, p := range []string{keyBackup, encryptedBackup} {
		_, statErr := os.Stat(p)
		if statErr == nil {
			return errors.Wrapf(ErrLeftoverRotationBackup, "leftover rotation-backup at %q", p)
		}
	}

	// Phase 1: read and decrypt with old key, all in memory.
	oldIdentity, err := LoadKey(rootDir)
	if err != nil {
		return errors.Wrap(err, "load old key")
	}

	encryptedData, err := os.ReadFile(encryptedFile)
	if err != nil {
		return errors.Wrap(err, "read encrypted file")
	}

	var encryptedSecrets map[string]any

	err = yaml.Unmarshal(encryptedData, &encryptedSecrets)
	if err != nil {
		return errors.Wrap(err, "parse encrypted YAML")
	}

	decryptedSecrets, err := decryptYAMLValues(encryptedSecrets, oldIdentity)
	if err != nil {
		return errors.Wrap(err, "decrypt with old key")
	}

	// Phase 2: generate new identity and encrypt new ciphertext —
	// still all in memory. No disk mutation yet.
	newIdentity, err := age.GenerateX25519Identity()
	if err != nil {
		return errors.Wrap(err, "generate new identity")
	}

	encryptedSecretsNew, err := encryptYAMLValues(decryptedSecrets, newIdentity.Recipient())
	if err != nil {
		return errors.Wrap(err, "encrypt with new key")
	}

	encryptedDataNew, err := yaml.Marshal(encryptedSecretsNew)
	if err != nil {
		return errors.Wrap(err, "marshal new encrypted secrets")
	}

	// Phase 3: move originals aside as backups. Atomic rename, so
	// either both originals exist as `*.rotation-backup` after this
	// block or neither move took effect (for the second rename: we
	// undo the first if it errors).
	err = os.Rename(keyFile, keyBackup)
	if err != nil {
		return errors.Wrap(err, "back up key file before rotation")
	}

	err = os.Rename(encryptedFile, encryptedBackup)
	if err != nil {
		// Roll back the key rename so the project is untouched.
		// Capture the rollback error too — if it fails the
		// operator is left with keyBackup but no keyFile, and the
		// caller-facing error must say so explicitly (otherwise
		// a Phase 0 refusal on retry would be the first sign of
		// the partial state).
		rbErr := os.Rename(keyBackup, keyFile)
		if rbErr != nil {
			//nolint:wrapcheck // errors.WithHintf wraps an already-wrapped chain (errors.Wrapf below); cockroachdb hint helpers are the project standard for operator-facing recovery instructions.
			return errors.WithHintf(
				errors.Wrapf(errors.WithSecondaryError(err, rbErr),
					"back up encrypted file before rotation; AND rollback of key-file rename failed"),
				"manual recovery: rename %q -> %q",
				keyBackup, keyFile,
			)
		}

		return errors.Wrap(err, "back up encrypted file before rotation (key file rename rolled back)")
	}

	// restore is a recovery helper used when any later step fails:
	// rename the backups back into place and return the original
	// error wrapped with a recovery note. We never silently swallow
	// the restore's own error — if recovery fails the operator
	// needs to know.
	restore := func(stage string, cause error) error {
		_ = os.Remove(keyFile)       // best-effort: remove half-written new key if any
		_ = os.Remove(encryptedFile) // ditto for encrypted file

		errKey := os.Rename(keyBackup, keyFile)
		errEnc := os.Rename(encryptedBackup, encryptedFile)

		if errKey != nil || errEnc != nil {
			combined := cause
			if errKey != nil {
				combined = errors.WithSecondaryError(combined, errors.Wrap(errKey, "restore key from backup"))
			}

			if errEnc != nil {
				combined = errors.WithSecondaryError(combined, errors.Wrap(errEnc, "restore encrypted file from backup"))
			}

			return errors.WithHintf(
				errors.Wrapf(combined, "rotation failed at %s; AND restore from backup partially failed", stage),
				"manual recovery: rename %q -> %q and %q -> %q",
				keyBackup, keyFile, encryptedBackup, encryptedFile,
			)
		}

		return errors.Wrapf(cause, "rotation failed at %s (originals restored)", stage)
	}

	// Phase 4: write new key, then new encrypted file. Both via
	// secureperm.WriteFile (atomic + 0o600). On any failure the
	// `restore` closure puts the originals back.
	err = secureperm.WriteFile(keyFile, []byte(formatKeyFile(newIdentity, time.Now())))
	if err != nil {
		return restore("write new key", err)
	}

	err = secureperm.WriteFile(encryptedFile, encryptedDataNew)
	if err != nil {
		return restore("write new encrypted file", err)
	}

	// Phase 5: rotation committed — remove backups. If removal
	// fails we return an error so the caller (and a future
	// RotateKeys run) sees an unambiguous "rotation succeeded but
	// backups linger" signal. The new pair on disk is correct and
	// usable; the leftover backups must be removed manually before
	// the next rotation can run (Phase 0 will refuse otherwise).
	var cleanupErrs []string

	err = os.Remove(keyBackup)
	if err != nil {
		cleanupErrs = append(cleanupErrs, fmt.Sprintf("%q: %v", keyBackup, err))
	}

	err = os.Remove(encryptedBackup)
	if err != nil {
		cleanupErrs = append(cleanupErrs, fmt.Sprintf("%q: %v", encryptedBackup, err))
	}

	if len(cleanupErrs) > 0 {
		//nolint:wrapcheck // errors.WithHint wraps the errors.Newf below; cockroachdb hint helpers are the project standard for operator-facing recovery instructions.
		return errors.WithHint(
			errors.Newf("rotation committed (new key and encrypted file are on disk) but cleanup of backup files failed: %s",
				strings.Join(cleanupErrs, "; ")),
			"remove these files manually before the next rotation",
		)
	}

	return nil
}

// EncryptYAMLFile encrypts a YAML file's values (keeping keys unencrypted) and saves to encrypted file.
// Uses incremental encryption: only encrypts values that have changed.
func EncryptYAMLFile(rootDir, plainFile, encryptedFile string) error {
	return encryptYAMLPair(rootDir, plainFile, encryptedFile)
}

// encryptYAMLPair is the shared implementation for EncryptSecretsFile
// and EncryptYAMLFile. Both flows take a plain-YAML path and an
// encrypted-YAML destination under rootDir, load (or generate) the
// project's age key, and write the destination at mode 0o600
// (defense-in-depth — age encryption is the security layer, but
// world-readable secrets material on shared workstations invites
// mistakes). The function does incremental encryption: an existing
// destination is loaded and used to keep ciphertext byte-stable for
// values whose plaintext did not change. If the existing destination
// cannot be read or parsed, the function falls back to a full
// encryption — the project still ends up in a consistent state.
func encryptYAMLPair(rootDir, plainFile, encryptedFile string) error {
	plainFilePath := filepath.Join(rootDir, plainFile)
	encryptedFilePath := filepath.Join(rootDir, encryptedFile)

	plainData, err := os.ReadFile(plainFilePath)
	if err != nil {
		return errors.Wrap(err, "read plain file")
	}

	identity, err := loadOrGenerateIdentity(rootDir)
	if err != nil {
		return err
	}

	var plain map[string]any

	err = yaml.Unmarshal(plainData, &plain)
	if err != nil {
		return errors.Wrap(err, "parse plain YAML")
	}

	encryptedMap, err := incrementalEncryptMap(plain, encryptedFilePath, identity)
	if err != nil {
		return err
	}

	encryptedData, err := yaml.Marshal(encryptedMap)
	if err != nil {
		return errors.Wrap(err, "marshal encrypted YAML")
	}

	err = secureperm.WriteFile(encryptedFilePath, encryptedData)
	if err != nil {
		return errors.Wrap(err, "write encrypted file")
	}

	return nil
}

// loadOrGenerateIdentity loads the project's age identity from
// `<rootDir>/talm.key` or creates one if the file does not exist. The
// load-or-create semantics are what `talm init` and the encrypt
// helpers rely on across init/apply/talosconfig flows.
func loadOrGenerateIdentity(rootDir string) (*age.X25519Identity, error) {
	keyFile := filepath.Join(rootDir, keyFileName)

	_, statErr := os.Stat(keyFile)
	if os.IsNotExist(statErr) {
		identity, _, err := GenerateKey(rootDir)
		if err != nil {
			return nil, errors.Wrap(err, "generate key")
		}

		return identity, nil
	}

	identity, err := LoadKey(rootDir)
	if err != nil {
		return nil, errors.Wrap(err, "load key")
	}

	return identity, nil
}

// incrementalEncryptMap returns the encrypted map[string]any for plain
// using existing ciphertext at encryptedFilePath when available. The
// fall-through to full encryption mirrors the behaviour of the old
// inline blocks in EncryptSecretsFile / EncryptYAMLFile: a missing,
// unreadable, or unparseable destination drops to encryptYAMLMap on
// the full plain tree, so the project recovers from a corrupted
// destination on the next encrypt round.
func incrementalEncryptMap(plain map[string]any, encryptedFilePath string, identity *age.X25519Identity) (map[string]any, error) {
	_, statErr := os.Stat(encryptedFilePath)
	if statErr != nil {
		return encryptYAMLMap(plain, identity.Recipient())
	}

	encryptedData, err := os.ReadFile(encryptedFilePath)
	if err != nil {
		return encryptYAMLMap(plain, identity.Recipient())
	}

	var existing map[string]any

	err = yaml.Unmarshal(encryptedData, &existing)
	if err != nil {
		return encryptYAMLMap(plain, identity.Recipient())
	}

	return mergeAndEncryptYAMLMap(plain, existing, identity)
}

// encryptYAMLMap is a typed wrapper around encryptYAMLValues that
// returns map[string]any directly. The wrapper exists so callers can
// avoid the unchecked any -> map[string]any assertion the linter
// rightly flags. encryptYAMLValues always returns the same kind it
// received at the top level, so a non-map result on a map input would
// indicate a bug rather than a runtime input shape.
func encryptYAMLMap(plain map[string]any, recipient *age.X25519Recipient) (map[string]any, error) {
	encrypted, err := encryptYAMLValues(plain, recipient)
	if err != nil {
		return nil, errors.Wrap(err, "encrypt YAML values")
	}

	out, ok := encrypted.(map[string]any)
	if !ok {
		return nil, errors.Wrapf(errInternalInvariant, "encryptYAMLValues returned %T", encrypted)
	}

	return out, nil
}

// mergeAndEncryptYAMLMap is a typed wrapper around
// mergeAndEncryptYAMLValues. Same rationale as encryptYAMLMap — both
// helpers exist to confine the any -> map[string]any boundary to one
// site that emits an explicit invariant-violation error rather than
// panicking.
func mergeAndEncryptYAMLMap(plain, encrypted map[string]any, identity *age.X25519Identity) (map[string]any, error) {
	merged, err := mergeAndEncryptYAMLValues(plain, encrypted, identity)
	if err != nil {
		return nil, errors.Wrap(err, "merge and encrypt")
	}

	out, ok := merged.(map[string]any)
	if !ok {
		return nil, errors.Wrapf(errInternalInvariant, "mergeAndEncryptYAMLValues returned %T", merged)
	}

	return out, nil
}

// DecryptYAMLFile decrypts an encrypted YAML file's values and saves to plain file.
func DecryptYAMLFile(rootDir, encryptedFile, plainFile string) error {
	return decryptYAMLPair(rootDir, encryptedFile, plainFile)
}

// decryptYAMLPair is the shared implementation for DecryptSecretsFile
// and DecryptYAMLFile. Both flows take an encrypted-YAML path and a
// plain-YAML destination under rootDir, load the project's age key,
// and write the destination with secure permissions.
func decryptYAMLPair(rootDir, encryptedFile, plainFile string) error {
	encryptedFilePath := filepath.Join(rootDir, encryptedFile)
	plainFilePath := filepath.Join(rootDir, plainFile)

	encryptedData, err := os.ReadFile(encryptedFilePath)
	if err != nil {
		return errors.Wrap(err, "read encrypted file")
	}

	identity, err := LoadKey(rootDir)
	if err != nil {
		return errors.Wrap(err, "load key")
	}

	var encryptedYAML map[string]any

	err = yaml.Unmarshal(encryptedData, &encryptedYAML)
	if err != nil {
		return errors.Wrap(err, "parse encrypted YAML")
	}

	decryptedYAML, err := decryptYAMLValues(encryptedYAML, identity)
	if err != nil {
		return errors.Wrap(err, "decrypt YAML values")
	}

	decryptedData, err := yaml.Marshal(decryptedYAML)
	if err != nil {
		return errors.Wrap(err, "marshal decrypted YAML")
	}

	err = secureperm.WriteFile(plainFilePath, decryptedData)
	if err != nil {
		return errors.Wrap(err, "write decrypted file")
	}

	return nil
}
