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
	if _, err := os.Stat(keyFile); err == nil {
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

	if err := secureperm.WriteFile(keyFile, []byte(formatKeyFile(identity, time.Now()))); err != nil {
		return nil, false, errors.Wrap(err, "write key file")
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
	secretsFile := filepath.Join(rootDir, plainSecretsFile)
	encryptedFile := filepath.Join(rootDir, encryptedSecretsFile)

	// Load plain secrets
	secretsData, err := os.ReadFile(secretsFile)
	if err != nil {
		return errors.Wrap(err, "read secrets file")
	}

	// Load or generate key
	var identity *age.X25519Identity
	keyFile := filepath.Join(rootDir, keyFileName)
	if _, err := os.Stat(keyFile); os.IsNotExist(err) {
		var keyCreated bool
		identity, keyCreated, err = GenerateKey(rootDir)
		if err != nil {
			return errors.Wrap(err, "generate key")
		}
		_ = keyCreated // Not used in this context
	} else {
		identity, err = LoadKey(rootDir)
		if err != nil {
			return errors.Wrap(err, "load key")
		}
	}

	// Parse YAML
	var secrets map[string]any
	if err := yaml.Unmarshal(secretsData, &secrets); err != nil {
		return errors.Wrap(err, "parse secrets YAML")
	}

	// If encrypted file exists, load it and merge (preserve unchanged encrypted values)
	var encryptedSecrets map[string]any
	if _, err := os.Stat(encryptedFile); err == nil {
		encryptedData, err := os.ReadFile(encryptedFile)
		if err == nil {
			if err := yaml.Unmarshal(encryptedData, &encryptedSecrets); err == nil {
				// Merge: encrypt only changed values, preserve unchanged encrypted values
				merged, err := mergeAndEncryptYAMLValues(secrets, encryptedSecrets, identity)
				if err != nil {
					return errors.Wrap(err, "merge and encrypt")
				}
				encryptedSecrets = merged.(map[string]any)
			} else {
				// If parsing fails, encrypt everything
				encrypted, err := encryptYAMLValues(secrets, identity.Recipient())
				if err != nil {
					return errors.Wrap(err, "encrypt secrets")
				}
				encryptedSecrets = encrypted.(map[string]any)
			}
		} else {
			// If reading fails, encrypt everything
			encrypted, err := encryptYAMLValues(secrets, identity.Recipient())
			if err != nil {
				return errors.Wrap(err, "encrypt secrets")
			}
			encryptedSecrets = encrypted.(map[string]any)
		}
	} else {
		// No encrypted file exists, encrypt everything
		encrypted, err := encryptYAMLValues(secrets, identity.Recipient())
		if err != nil {
			return errors.Wrap(err, "encrypt secrets")
		}
		encryptedSecrets = encrypted.(map[string]any)
	}

	// Marshal encrypted YAML
	encryptedData, err := yaml.Marshal(encryptedSecrets)
	if err != nil {
		return errors.Wrap(err, "marshal encrypted secrets")
	}

	// Write encrypted file via secureperm.WriteFile so it lands at
	// mode 0o600 (defense-in-depth — age encryption is the security
	// layer, but world-readable secrets material on shared
	// workstations invites mistakes). RotateKeys uses the same
	// helper for the same file, keeping the on-disk mode invariant
	// across every code path that writes secrets.encrypted.yaml.
	if err := secureperm.WriteFile(encryptedFile, encryptedData); err != nil {
		return errors.Wrap(err, "write encrypted file")
	}

	return nil
}

// DecryptSecretsFile decrypts secrets.encrypted.yaml and saves to secrets.yaml.
func DecryptSecretsFile(rootDir string) error {
	encryptedFile := filepath.Join(rootDir, encryptedSecretsFile)
	secretsFile := filepath.Join(rootDir, plainSecretsFile)

	// Load encrypted secrets
	encryptedData, err := os.ReadFile(encryptedFile)
	if err != nil {
		return errors.Wrap(err, "read encrypted file")
	}

	// Load key
	identity, err := LoadKey(rootDir)
	if err != nil {
		return errors.Wrap(err, "load key")
	}

	// Parse YAML
	var encryptedSecrets map[string]any
	if err := yaml.Unmarshal(encryptedData, &encryptedSecrets); err != nil {
		return errors.Wrap(err, "parse encrypted YAML")
	}

	// Decrypt values
	decryptedSecrets, err := decryptYAMLValues(encryptedSecrets, identity)
	if err != nil {
		return errors.Wrap(err, "decrypt secrets")
	}

	// Marshal decrypted YAML
	decryptedData, err := yaml.Marshal(decryptedSecrets)
	if err != nil {
		return errors.Wrap(err, "marshal decrypted secrets")
	}

	// Write decrypted file with secure permissions
	if err := secureperm.WriteFile(secretsFile, decryptedData); err != nil {
		return errors.Wrap(err, "write decrypted file")
	}

	return nil
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

	if _, err := writer.Write([]byte(plaintext)); err != nil {
		return "", errors.Wrap(err, "write plaintext")
	}

	if err := writer.Close(); err != nil {
		return "", errors.Wrap(err, "close encrypt writer")
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
		if _, err := os.Stat(p); err == nil {
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
	if err := yaml.Unmarshal(encryptedData, &encryptedSecrets); err != nil {
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
	if err := os.Rename(keyFile, keyBackup); err != nil {
		return errors.Wrap(err, "back up key file before rotation")
	}
	if err := os.Rename(encryptedFile, encryptedBackup); err != nil {
		// Roll back the key rename so the project is untouched.
		// Capture the rollback error too — if it fails the
		// operator is left with keyBackup but no keyFile, and the
		// caller-facing error must say so explicitly (otherwise
		// a Phase 0 refusal on retry would be the first sign of
		// the partial state).
		if rbErr := os.Rename(keyBackup, keyFile); rbErr != nil {
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
	if err := secureperm.WriteFile(keyFile, []byte(formatKeyFile(newIdentity, time.Now()))); err != nil {
		return restore("write new key", err)
	}
	if err := secureperm.WriteFile(encryptedFile, encryptedDataNew); err != nil {
		return restore("write new encrypted file", err)
	}

	// Phase 5: rotation committed — remove backups. If removal
	// fails we return an error so the caller (and a future
	// RotateKeys run) sees an unambiguous "rotation succeeded but
	// backups linger" signal. The new pair on disk is correct and
	// usable; the leftover backups must be removed manually before
	// the next rotation can run (Phase 0 will refuse otherwise).
	var cleanupErrs []string
	if err := os.Remove(keyBackup); err != nil {
		cleanupErrs = append(cleanupErrs, fmt.Sprintf("%q: %v", keyBackup, err))
	}
	if err := os.Remove(encryptedBackup); err != nil {
		cleanupErrs = append(cleanupErrs, fmt.Sprintf("%q: %v", encryptedBackup, err))
	}
	if len(cleanupErrs) > 0 {
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
	plainFilePath := filepath.Join(rootDir, plainFile)
	encryptedFilePath := filepath.Join(rootDir, encryptedFile)

	// Load plain file
	plainData, err := os.ReadFile(plainFilePath)
	if err != nil {
		return errors.Wrap(err, "read plain file")
	}

	// Load or generate key
	var identity *age.X25519Identity
	keyFile := filepath.Join(rootDir, keyFileName)
	if _, err := os.Stat(keyFile); os.IsNotExist(err) {
		var keyCreated bool
		identity, keyCreated, err = GenerateKey(rootDir)
		if err != nil {
			return errors.Wrap(err, "generate key")
		}
		_ = keyCreated // Not used in this context
	} else {
		identity, err = LoadKey(rootDir)
		if err != nil {
			return errors.Wrap(err, "load key")
		}
	}

	// Parse YAML
	var yamlData map[string]any
	if err := yaml.Unmarshal(plainData, &yamlData); err != nil {
		return errors.Wrap(err, "parse YAML")
	}

	// If encrypted file exists, load it and merge (preserve unchanged encrypted values)
	var encryptedYAML map[string]any
	if _, err := os.Stat(encryptedFilePath); err == nil {
		encryptedData, err := os.ReadFile(encryptedFilePath)
		if err == nil {
			if err := yaml.Unmarshal(encryptedData, &encryptedYAML); err == nil {
				// Merge: encrypt only changed values, preserve unchanged encrypted values
				merged, err := mergeAndEncryptYAMLValues(yamlData, encryptedYAML, identity)
				if err != nil {
					return errors.Wrap(err, "merge and encrypt")
				}
				encryptedYAML = merged.(map[string]any)
			} else {
				// If parsing fails, encrypt everything
				encrypted, err := encryptYAMLValues(yamlData, identity.Recipient())
				if err != nil {
					return errors.Wrap(err, "encrypt YAML values")
				}
				encryptedYAML = encrypted.(map[string]any)
			}
		} else {
			// If reading fails, encrypt everything
			encrypted, err := encryptYAMLValues(yamlData, identity.Recipient())
			if err != nil {
				return errors.Wrap(err, "encrypt YAML values")
			}
			encryptedYAML = encrypted.(map[string]any)
		}
	} else {
		// No encrypted file exists, encrypt everything
		encrypted, err := encryptYAMLValues(yamlData, identity.Recipient())
		if err != nil {
			return errors.Wrap(err, "encrypt YAML values")
		}
		encryptedYAML = encrypted.(map[string]any)
	}

	// Marshal encrypted YAML
	encryptedData, err := yaml.Marshal(encryptedYAML)
	if err != nil {
		return errors.Wrap(err, "marshal encrypted YAML")
	}

	// Write encrypted file via secureperm.WriteFile (mode 0o600).
	// Same defense-in-depth rationale as EncryptSecretsFile and
	// RotateKeys — every code path that writes encrypted secrets
	// material agrees on the same on-disk permission.
	if err := secureperm.WriteFile(encryptedFilePath, encryptedData); err != nil {
		return errors.Wrap(err, "write encrypted file")
	}

	return nil
}

// DecryptYAMLFile decrypts an encrypted YAML file's values and saves to plain file.
func DecryptYAMLFile(rootDir, encryptedFile, plainFile string) error {
	encryptedFilePath := filepath.Join(rootDir, encryptedFile)
	plainFilePath := filepath.Join(rootDir, plainFile)

	// Load encrypted file
	encryptedData, err := os.ReadFile(encryptedFilePath)
	if err != nil {
		return errors.Wrap(err, "read encrypted file")
	}

	// Load key
	identity, err := LoadKey(rootDir)
	if err != nil {
		return errors.Wrap(err, "load key")
	}

	// Parse YAML
	var encryptedYAML map[string]any
	if err := yaml.Unmarshal(encryptedData, &encryptedYAML); err != nil {
		return errors.Wrap(err, "parse encrypted YAML")
	}

	// Decrypt values
	decryptedYAML, err := decryptYAMLValues(encryptedYAML, identity)
	if err != nil {
		return errors.Wrap(err, "decrypt YAML values")
	}

	// Marshal decrypted YAML
	decryptedData, err := yaml.Marshal(decryptedYAML)
	if err != nil {
		return errors.Wrap(err, "marshal decrypted YAML")
	}

	// Write decrypted file with secure permissions
	if err := secureperm.WriteFile(plainFilePath, decryptedData); err != nil {
		return errors.Wrap(err, "write decrypted file")
	}

	return nil
}
