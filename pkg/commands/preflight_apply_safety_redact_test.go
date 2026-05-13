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

package commands

import (
	"strings"
	"testing"

	"github.com/cozystack/talm/pkg/applycheck"
)

// Hoisted secret-path literals avoid goconst on the test slice
// while keeping the per-case strings legible. These mirror entries
// in secretFieldPaths; the production list is the source of truth.
const (
	pathClusterSecret           = "cluster.secret"
	pathClusterToken            = "cluster.token"
	pathClusterAescbcEncryption = "cluster.aescbcEncryptionSecret"
	pathMachineToken            = "machine.token"
	pathWireguardPrivateKey     = "privateKey"
	pathClusterAcceptedCAs      = "cluster.acceptedCAs"
	pathWireguardPeers          = "peers"
)

// TestIsSecretPath_ExactMatch pins the simplest case: an exact
// path match against a top-level secret returns true. Without this
// case the implementation could regress to bracket-only matching
// and fail on the most common shape (cluster.token / cluster.secret
// are not array elements).
func TestIsSecretPath_ExactMatch(t *testing.T) {
	t.Parallel()

	for _, path := range []string{
		pathClusterSecret,
		pathClusterToken,
		pathClusterAescbcEncryption,
		pathMachineToken,
	} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()

			if !isSecretPath(path) {
				t.Errorf("isSecretPath(%q) = false, want true", path)
			}
		})
	}
}

// TestIsSecretPath_WireguardSecretPaths pins the bare-path entries
// that match Wireguard multidoc secret-bearing fields. The
// --show-secrets-in-drift flag help text advertises that Wireguard
// private and pre-shared keys are redacted by default; without
// these allowlist entries the help text would lie and rotating
// either field would leak the base64 key value to stderr.
//
// The differ's flatten step does not prefix multidoc paths with
// the doc kind (pkg/applycheck/diff.go) and treats slices as
// atomic leaves, so the emitted paths are `privateKey` (scalar
// leaf) and `peers` (whole slice). The presharedKey lives inside
// peer elements; the whole peers slice is redacted because the
// formatter does not descend into element fields.
func TestIsSecretPath_WireguardSecretPaths(t *testing.T) {
	t.Parallel()

	for _, path := range []string{
		pathWireguardPrivateKey,
		pathWireguardPeers,
	} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()

			if !isSecretPath(path) {
				t.Errorf("isSecretPath(%q) = false; Wireguard private/preshared keys are advertised as redacted by --show-secrets-in-drift", path)
			}
		})
	}
}

// TestFormatFieldChangeLine_AcceptedCAsSliceRotation_NoLeak is the
// real-differ-shape regression pin against the security-class bug
// the original allowlist had: bracket-form entries
// (`cluster.acceptedCAs[].key`) never matched the differ's actual
// output (`cluster.acceptedCAs`, slice-atomic). A rotation of the
// CA list leaked the new and old `key` bytes through
// formatSliceSetDiff. This test exercises the exact shape the
// differ emits and asserts the key bytes never appear in the
// rendered line.
func TestFormatFieldChangeLine_AcceptedCAsSliceRotation_NoLeak(t *testing.T) {
	t.Parallel()

	change := &applycheck.FieldChange{
		Path: pathClusterAcceptedCAs,
		Old: []any{
			map[string]any{"crt": "AAA", "key": "SECRET_CA_KEY_AAA"},
		},
		New: []any{
			map[string]any{"crt": "BBB", "key": "SECRET_CA_KEY_BBB"},
		},
		HasOld: true,
		HasNew: true,
	}

	got := formatFieldChangeLine(change, false)
	for _, leaked := range []string{"SECRET_CA_KEY_AAA", "SECRET_CA_KEY_BBB"} {
		if strings.Contains(got, leaked) {
			t.Errorf("cluster.acceptedCAs slice rotation must not leak key bytes; found %q in %q", leaked, got)
		}
	}

	if !strings.Contains(got, "redacted") {
		t.Errorf("cluster.acceptedCAs slice rotation must render the redaction sentinel; got %q", got)
	}
}

// TestFormatFieldChangeLine_WireguardPeersRotation_NoLeak is the
// counterpart for the multidoc Wireguard kind. The differ emits
// `peers` (slice-atomic) on a peer-list rotation; the entries
// carry presharedKey leaves nested under each element. The whole
// peers slice must redact; presharedKey bytes must never leak.
func TestFormatFieldChangeLine_WireguardPeersRotation_NoLeak(t *testing.T) {
	t.Parallel()

	change := &applycheck.FieldChange{
		Path: pathWireguardPeers,
		Old: []any{
			map[string]any{"publicKey": "PUB1", "presharedKey": "SECRET_PSK_AAA"},
		},
		New: []any{
			map[string]any{"publicKey": "PUB1", "presharedKey": "SECRET_PSK_BBB"},
		},
		HasOld: true,
		HasNew: true,
	}

	got := formatFieldChangeLine(change, false)
	for _, leaked := range []string{"SECRET_PSK_AAA", "SECRET_PSK_BBB"} {
		if strings.Contains(got, leaked) {
			t.Errorf("peers slice rotation must not leak presharedKey bytes; found %q in %q", leaked, got)
		}
	}

	if !strings.Contains(got, "redacted") {
		t.Errorf("peers slice rotation must render the redaction sentinel; got %q", got)
	}
}

// TestIsSecretPath_BracketNormalisationStillNormalises pins the
// arrayIndexPattern regex contract independent of the current
// allowlist content. If a future allowlist entry uses the
// bracket-wildcard form (e.g. when the differ learns to descend
// into slice elements with stable identity), normalisation must
// still convert numeric `[N]` to `[]` so `foo[2].bar` matches
// `foo[].bar`. Test against a synthetic entry rather than the
// real allowlist to decouple the regex contract from allowlist
// drift.
func TestIsSecretPath_BracketNormalisationStillNormalises(t *testing.T) {
	t.Parallel()

	got := arrayIndexPattern.ReplaceAllString("cluster.acceptedCAs[42].key", "[]")
	if got != "cluster.acceptedCAs[].key" {
		t.Errorf("arrayIndexPattern must normalise [42] -> []; got %q", got)
	}
}

// TestIsSecretPath_NoFalseMatchOnPrefix pins that a non-secret path
// sharing a string prefix with a secret entry does NOT match. The
// matcher is path-segment-aware (split on dots), not raw substring;
// cluster.tokenExtras must not match cluster.token. Without this
// pin a substring-based implementation would silently redact
// operator-visible fields that happen to share a prefix.
func TestIsSecretPath_NoFalseMatchOnPrefix(t *testing.T) {
	t.Parallel()

	for _, path := range []string{
		"cluster.tokenExtras",
		"cluster.secretsManager",
		"machine.tokenSomething",
		"cluster.acceptedCAsExtras",
	} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()

			if isSecretPath(path) {
				t.Errorf("isSecretPath(%q) = true, want false (false-prefix match)", path)
			}
		})
	}
}

// TestIsSecretPath_NonSecretPath_NoMatch pins that ordinary
// operator-visible paths are not redacted. A regression here would
// hide useful information from the operator (network changes,
// install disk changes, etc.).
func TestIsSecretPath_NonSecretPath_NoMatch(t *testing.T) {
	t.Parallel()

	for _, path := range []string{
		"cluster.network.podSubnets",
		"cluster.network.serviceSubnets",
		"cluster.apiServer.certSANs",
		"machine.install.disk",
		"machine.network.hostname",
	} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()

			if isSecretPath(path) {
				t.Errorf("isSecretPath(%q) = true, want false (non-secret operator-visible path)", path)
			}
		})
	}
}

// TestRedactValue_PreservesLength pins the length-disclosure
// contract. Operators rotating a secret want a signal that a
// rotation happened — but not the value. Length carries the
// "something changed" bit without exposing the secret.
func TestRedactValue_PreservesLength(t *testing.T) {
	t.Parallel()

	got := redactValue("abcdefg")
	if !strings.Contains(got, "len=7") {
		t.Errorf("redactValue should disclose length to signal rotation; got %q", got)
	}

	if strings.Contains(got, "abcdefg") {
		t.Errorf("redactValue must NOT leak the input; got %q", got)
	}
}

// TestFormatFieldChangeLine_RedactsSecretByDefault pins the default
// redaction at the formatter level: a FieldChange whose Path is a
// known secret renders both sides as the redaction sentinel when
// showSecrets is false (the default).
func TestFormatFieldChangeLine_RedactsSecretByDefault(t *testing.T) {
	t.Parallel()

	f := &applycheck.FieldChange{
		Path:   pathMachineToken,
		Old:    "old-secret-aaaa",
		New:    "new-secret-bbbbbbbb",
		HasOld: true,
		HasNew: true,
	}

	got := formatFieldChangeLine(f, false)
	if strings.Contains(got, "old-secret-aaaa") || strings.Contains(got, "new-secret-bbbbbbbb") {
		t.Errorf("default-redacted formatter must NOT leak secret values; got %q", got)
	}

	if !strings.Contains(got, "redacted") {
		t.Errorf("default-redacted formatter must render '***redacted...' sentinel; got %q", got)
	}
}

// TestFormatFieldChangeLine_ShowsSecretsWhenFlagSet pins the
// opt-out: passing showSecrets=true bypasses redaction so debugging
// workflows can inspect the actual values. This is operator-explicit
// (via --show-secrets-in-drift) so the leak is intentional.
func TestFormatFieldChangeLine_ShowsSecretsWhenFlagSet(t *testing.T) {
	t.Parallel()

	f := &applycheck.FieldChange{
		Path:   pathMachineToken,
		Old:    "old-secret-aaaa",
		New:    "new-secret-bbbbbbbb",
		HasOld: true,
		HasNew: true,
	}

	got := formatFieldChangeLine(f, true)
	if !strings.Contains(got, "old-secret-aaaa") {
		t.Errorf("showSecrets=true must render raw old value; got %q", got)
	}

	if !strings.Contains(got, "new-secret-bbbbbbbb") {
		t.Errorf("showSecrets=true must render raw new value; got %q", got)
	}

	if strings.Contains(got, "redacted") {
		t.Errorf("showSecrets=true must NOT apply the redaction sentinel; got %q", got)
	}
}

// TestFormatFieldChangeLine_NonSecretPathsUnchanged pins the control:
// non-secret paths render verbatim regardless of the showSecrets
// flag. A regression here would silently redact operator-visible
// information.
func TestFormatFieldChangeLine_NonSecretPathsUnchanged(t *testing.T) {
	t.Parallel()

	f := &applycheck.FieldChange{
		Path:   "machine.install.disk",
		Old:    "/dev/sda",
		New:    "/dev/sdb",
		HasOld: true,
		HasNew: true,
	}

	for _, showSecrets := range []bool{false, true} {
		got := formatFieldChangeLine(f, showSecrets)
		if !strings.Contains(got, "/dev/sda") || !strings.Contains(got, "/dev/sdb") {
			t.Errorf("non-secret path must render verbatim regardless of showSecrets=%v; got %q", showSecrets, got)
		}

		if strings.Contains(got, "redacted") {
			t.Errorf("non-secret path must NOT trigger redaction; got %q", got)
		}
	}
}

// TestFormatFieldChangeLine_SecretWithNonStringValue_StillRedacted
// pins the non-string branch of formatSecretFieldValue. If a future
// schema drift puts a number/bool on a secret-bearing path, the
// redactor must still emit the length tell so a rotation surfaces
// as "different sentinel" instead of two identical ***redacted***
// strings that hide the change.
func TestFormatFieldChangeLine_SecretWithNonStringValue_StillRedacted(t *testing.T) {
	t.Parallel()

	change := &applycheck.FieldChange{
		Path:   pathMachineToken,
		Old:    42,
		New:    1234,
		HasOld: true,
		HasNew: true,
	}

	got := formatFieldChangeLine(change, false)
	if !strings.Contains(got, "redacted") {
		t.Errorf("non-string secret-path value must still render the redaction sentinel; got %q", got)
	}

	if strings.Contains(got, " 42 ") || strings.Contains(got, " 1234 ") {
		t.Errorf("non-string secret values must NOT appear verbatim in the rendered line; got %q", got)
	}

	// Different inputs must produce different sentinels (rotation
	// signal). 42 and 1234 render as "42" (len 2) and "1234" (len 4)
	// via fmt.Sprintf("%v", ...), so the redacted sides must
	// disclose different lengths.
	if !strings.Contains(got, "len=2") || !strings.Contains(got, "len=4") {
		t.Errorf("non-string secret values of different lengths must produce different len=N sentinels; got %q", got)
	}
}

// TestFormatFieldChangeLine_SecretNilValue_StillRedacts pins the
// corner case where HasOld=true / HasNew=true but the value is
// literally nil (e.g. a YAML `field: null` round-trip rather than
// an absent field). formatSecretFieldValue's non-string branch
// renders via redactValue(fmt.Sprintf("%v", value)) which produces
// "***redacted (len=5)***" for nil (since %v of nil is "<nil>",
// 5 chars). The differ does not produce this shape today, but
// the corner is reachable if a future YAML decode round-trip
// emits null-valued secret fields — pin that the value never
// leaks even on this path.
func TestFormatFieldChangeLine_SecretNilValue_StillRedacts(t *testing.T) {
	t.Parallel()

	change := &applycheck.FieldChange{
		Path:   pathMachineToken,
		Old:    nil,
		New:    "new-secret-value",
		HasOld: true,
		HasNew: true,
	}

	got := formatFieldChangeLine(change, false)
	if !strings.Contains(got, "redacted") {
		t.Errorf("HasOld=true with nil value must still render the redaction sentinel; got %q", got)
	}

	if strings.Contains(got, "new-secret-value") {
		t.Errorf("HasNew=true with secret string must redact, not leak; got %q", got)
	}
}

// TestFormatFieldChangeLine_SecretAbsentSide_DistinguishesAddFromRotate
// pins the absent-side branch of formatSecretFieldValue. A CA-list
// addition (HasOld=false, HasNew=true) must render as
// `(absent) -> ***redacted (len=N)***` so the operator can still
// distinguish "this secret was just added" from "this secret was
// rotated". The branch is the only thing keeping add-vs-rotate
// signal alive on a secret path; without this pin a future
// "tighten redaction" refactor could regress to
// `***redacted (len=0)*** -> ***redacted (len=N)***` and silently
// kill the distinction.
func TestFormatFieldChangeLine_SecretAbsentSide_DistinguishesAddFromRotate(t *testing.T) {
	t.Parallel()

	addition := &applycheck.FieldChange{
		Path:   pathMachineToken,
		Old:    nil,
		New:    "new-token-value",
		HasOld: false,
		HasNew: true,
	}

	got := formatFieldChangeLine(addition, false)
	if !strings.Contains(got, "(absent)") {
		t.Errorf("addition (HasOld=false) must render LEFT side as `(absent)` so add-vs-rotate stays distinguishable; got %q", got)
	}

	if strings.Contains(got, "redacted (len=0)") {
		t.Errorf("addition LEFT side must NOT collapse to `***redacted (len=0)***` (operator can't tell add from rotate-to-empty); got %q", got)
	}

	if !strings.Contains(got, "redacted") {
		t.Errorf("addition RIGHT side must still redact the new value; got %q", got)
	}

	removal := &applycheck.FieldChange{
		Path:   pathMachineToken,
		Old:    "old-token-value",
		New:    nil,
		HasOld: true,
		HasNew: false,
	}

	got = formatFieldChangeLine(removal, false)
	if !strings.Contains(got, "(absent)") {
		t.Errorf("removal (HasNew=false) must render RIGHT side as `(absent)`; got %q", got)
	}

	if strings.Contains(got, "old-token-value") {
		t.Errorf("removal LEFT side must redact, not leak the old value; got %q", got)
	}
}

// TestFormatFieldChangeLine_SliceSecretPath_NoLeak pins the
// secret-check-before-bothSlices ordering at the formatter. The
// allowlist names parent slice paths (cluster.acceptedCAs,
// machine.acceptedCAs, peers) because the differ flattens slices
// atomically — those entries are NOT speculative. This test uses
// a scalar allowlist entry (machine.token) wrapped in a
// FieldChange whose Old/New are slices to prove the ordering
// works in the abstract: even if a hypothetical future scenario
// puts a scalar-allowlisted path on slice-valued Old/New, the
// secret check still fires before bothSlices and the contents
// never leak through formatSliceSetDiff. The real-shape
// CA / Wireguard slice-rotation regression pins are in
// TestFormatFieldChangeLine_AcceptedCAsSliceRotation_NoLeak and
// TestFormatFieldChangeLine_WireguardPeersRotation_NoLeak.
func TestFormatFieldChangeLine_SliceSecretPath_NoLeak(t *testing.T) {
	t.Parallel()

	// Reuse a real allowlist entry; the test pins behaviour at
	// the formatter, not the allowlist content.
	change := &applycheck.FieldChange{
		Path:   pathMachineToken,
		Old:    []any{"secret-aaa", "secret-bbb"},
		New:    []any{"secret-ccc"},
		HasOld: true,
		HasNew: true,
	}

	got := formatFieldChangeLine(change, false)
	for _, leaked := range []string{"secret-aaa", "secret-bbb", "secret-ccc"} {
		if strings.Contains(got, leaked) {
			t.Errorf("slice-shaped secret path must not leak element %q; got %q", leaked, got)
		}
	}

	if !strings.Contains(got, "redacted") {
		t.Errorf("slice-shaped secret path must render the redaction sentinel; got %q", got)
	}
}
