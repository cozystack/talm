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

// Contract: the drift preview must redact user secret VALUES (from encrypted
// value files), not only the static Talos-bootstrap path allowlist. Otherwise
// `talm apply --dry-run` — the common pre-apply command — would print a secret
// authored in values-secret.encrypted.yaml verbatim, even though `talm
// template` redacts the same value.

package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cozystack/talm/pkg/applycheck"
)

// TestContract_DriftRedaction_UserSecretAtArbitraryPath pins that a user
// secret landing at a NON-allowlisted path is redacted by default and revealed
// only with --show-secrets-in-drift (modelled by secretRedactor.show).
func TestContract_DriftRedaction_UserSecretAtArbitraryPath(t *testing.T) {
	change := &applycheck.FieldChange{
		Path:   "machine.registries.config.r\\.example.auth.password",
		HasNew: true,
		New:    "hunter2",
	}

	redacted := formatFieldChangeLine(change, secretRedactor{userSecrets: secretSetOf("hunter2")})
	if strings.Contains(redacted, "hunter2") {
		t.Errorf("user secret at a non-allowlisted path must be redacted by default:\n%s", redacted)
	}
	if !strings.Contains(redacted, "redacted") {
		t.Errorf("redacted line must carry the redaction sentinel:\n%s", redacted)
	}

	shown := formatFieldChangeLine(change, secretRedactor{show: true})
	if !strings.Contains(shown, "hunter2") {
		t.Errorf("--show-secrets-in-drift must reveal the value:\n%s", shown)
	}
}

// TestContract_DriftRedaction_UserSecretNestedInSlice pins that a secret nested
// inside a slice element (the machine.pods[] shape, which the differ flattens
// to one slice-valued FieldChange) is redacted whole rather than dumped
// element-by-element through the slice set-diff path.
func TestContract_DriftRedaction_UserSecretNestedInSlice(t *testing.T) {
	change := &applycheck.FieldChange{
		Path:   "machine.pods",
		HasNew: true,
		New: []any{
			map[string]any{
				"spec": map[string]any{
					"containers": []any{
						map[string]any{"env": []any{map[string]any{"name": "SECRET_ID", "value": "vault-secret"}}},
					},
				},
			},
		},
	}

	redacted := formatFieldChangeLine(change, secretRedactor{userSecrets: secretSetOf("vault-secret")})
	if strings.Contains(redacted, "vault-secret") {
		t.Errorf("a secret nested in a slice element must be redacted, not dumped:\n%s", redacted)
	}
}

// TestContract_DriftRedaction_NoUserSecretsIsPathOnly pins backward
// compatibility: with no user secret set, redaction falls back to the static
// path allowlist exactly as before (a non-allowlisted, non-secret value is
// printed verbatim).
func TestContract_DriftRedaction_NoUserSecretsIsPathOnly(t *testing.T) {
	change := &applycheck.FieldChange{
		Path:   "machine.network.hostname",
		HasNew: true,
		New:    "node0",
	}

	got := formatFieldChangeLine(change, secretRedactor{})
	if !strings.Contains(got, "node0") {
		t.Errorf("ordinary field must remain visible when no user secrets are in scope:\n%s", got)
	}
}

// TestContract_DriftRedaction_ValueCollisionIsRedacted pins the known sharp
// edge of value-based sealing (also applies to template's omit/redact): a
// secret value that coincides with an ordinary string elsewhere in the config
// causes that ordinary field to be redacted too. This is documented behaviour,
// not a bug — operators must not encrypt values that collide with structural
// config strings. Pinned so any future change to the matching granularity is a
// conscious decision.
func TestContract_DriftRedaction_ValueCollisionIsRedacted(t *testing.T) {
	// "controlplane" is a legitimate machine.type value; if an operator
	// (unwisely) encrypts a secret whose plaintext is also "controlplane",
	// the machine.type field is redacted by the value match.
	change := &applycheck.FieldChange{
		Path:   "machine.type",
		HasNew: true,
		New:    "controlplane",
	}

	got := formatFieldChangeLine(change, secretRedactor{userSecrets: secretSetOf("controlplane")})
	if strings.Contains(got, "controlplane") {
		t.Errorf("value-based redaction is exact-match across the whole config (documented collision); got:\n%s", got)
	}
}

// TestContract_ShowSecretsFlags_DistinctButCrossReferenced pins the two
// reveal-secrets toggles. They are deliberately spelled differently because
// they govern different output surfaces: template's --show-secrets reveals the
// stdout render; apply's --show-secrets-in-drift reveals the drift preview.
// Both usages must cross-reference the other so an operator alternating
// between the commands is not surprised. Pinned so a rename or a dropped
// cross-reference is a conscious decision, not a silent leak vector.
func TestContract_ShowSecretsFlags_DistinctButCrossReferenced(t *testing.T) {
	tmpl := templateCmd.Flags().Lookup("show-secrets")
	if tmpl == nil {
		t.Fatal("template must register --show-secrets")
	}
	if !strings.Contains(tmpl.Usage, "--show-secrets-in-drift") {
		t.Errorf("template --show-secrets usage must point at apply's counterpart; got:\n%s", tmpl.Usage)
	}

	app := applyCmd.Flags().Lookup("show-secrets-in-drift")
	if app == nil {
		t.Fatal("apply must register --show-secrets-in-drift")
	}
	if !strings.Contains(app.Usage, "--show-secrets") {
		t.Errorf("apply --show-secrets-in-drift usage must point at template's counterpart; got:\n%s", app.Usage)
	}
	if !strings.Contains(app.Usage, "encrypted value files") && !strings.Contains(app.Usage, ".encrypted.yaml") {
		t.Errorf("apply --show-secrets-in-drift usage must document that it now also covers user encrypted values; got:\n%s", app.Usage)
	}
}

// TestContract_BuildDriftRedactor pins how apply assembles the drift
// redactor. --show-secrets-in-drift short-circuits to a reveal-everything
// policy that needs no decryption. On the template-rendering path the redactor
// carries the user secret set decrypted from the encrypted value files. On the
// direct-patch path (rendersUserValues=false) it does NOT decrypt — that path
// renders none of those values, so collecting them would be pure overhead and
// would wrongly block an apply that has no talm.key.
func TestContract_BuildDriftRedactor(t *testing.T) {
	restore := snapshotApplyValueState()
	defer restore()

	origShow := applyCmdFlags.showSecretsInDrift
	defer func() { applyCmdFlags.showSecretsInDrift = origShow }()

	dir := t.TempDir()
	enc := encryptedValuesFile(t, dir, map[string]any{"registryPassword": "hunter2"})

	Config.RootDir = dir
	Config.TemplateOptions.ValueFiles = []string{enc}
	applyCmdFlags.valueFiles = nil

	t.Run("show bypasses decryption", func(t *testing.T) {
		applyCmdFlags.showSecretsInDrift = true

		redactor, err := buildDriftRedactor(true)
		if err != nil {
			t.Fatalf("buildDriftRedactor: %v", err)
		}
		if !redactor.show {
			t.Error("--show-secrets-in-drift must produce a reveal-everything redactor")
		}
		if len(redactor.userSecrets) != 0 {
			t.Errorf("show path must not decrypt; got %v", redactor.userSecrets)
		}
	})

	t.Run("template path collects the user secret set", func(t *testing.T) {
		applyCmdFlags.showSecretsInDrift = false

		redactor, err := buildDriftRedactor(true)
		if err != nil {
			t.Fatalf("buildDriftRedactor: %v", err)
		}
		if redactor.show {
			t.Error("default must not reveal secrets")
		}
		if _, ok := redactor.userSecrets["hunter2"]; !ok {
			t.Errorf("template-path redactor must carry decrypted user secrets; got %v", redactor.userSecrets)
		}
	})

	t.Run("direct-patch path skips decryption and never blocks on a missing key", func(t *testing.T) {
		applyCmdFlags.showSecretsInDrift = false

		// Remove talm.key: the direct-patch path must not need it (it
		// renders no user values), so the redactor build must still succeed.
		if err := os.Remove(filepath.Join(dir, "talm.key")); err != nil {
			t.Fatalf("remove talm.key: %v", err)
		}

		redactor, err := buildDriftRedactor(false)
		if err != nil {
			t.Fatalf("direct-patch redactor must not fail without talm.key: %v", err)
		}
		if len(redactor.userSecrets) != 0 {
			t.Errorf("direct-patch path must not collect user secrets; got %v", redactor.userSecrets)
		}
	})
}

func secretSetOf(values ...string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, v := range values {
		out[v] = struct{}{}
	}

	return out
}
