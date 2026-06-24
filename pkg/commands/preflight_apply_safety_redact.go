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
	"fmt"
	"regexp"
	"slices"
)

// secretFieldPaths is the operator-visible allowlist of paths the
// drift preview redacts by default. Inclusion criteria:
//
//  1. Cluster-private bootstrap material — CA keys, encryption
//     secrets, bootstrap tokens — whose disclosure to a CI log or
//     screen-share is an incident.
//  2. Operator-managed credential material — Wireguard private /
//     pre-shared keys.
//  3. The path has a stable form in v1alpha1.
//
// Out of scope: exhaustive sweep over every Sensitive-marked field
// in the Talos v1alpha1 schema. The list grows when an operator
// reports a new leak; each addition should cite the symptom.
//
// Bracket-normalisation lets array-indexed paths
// (cluster.acceptedCAs[2].key) match the wildcard form
// (cluster.acceptedCAs[].key) so an operator-visible diff with
// concrete indices is redacted.
//
//nolint:gochecknoglobals // static allowlist of secret field paths.
var secretFieldPaths = []string{
	// v1alpha1 MachineConfig (cluster.* / machine.*) bootstrap material.
	// Scalar leaves: differ emits these paths directly.
	"cluster.secret",
	"cluster.token",
	"cluster.aescbcEncryptionSecret",
	"cluster.secretboxEncryptionSecret",
	"cluster.ca.key",
	"cluster.aggregatorCA.key",
	"cluster.serviceAccount.key",
	"cluster.etcd.ca.key",
	"machine.token",
	"machine.ca.key",

	// Slice-of-maps fields that carry secrets nested under each
	// element. The differ's flatten step (pkg/applycheck/diff.go)
	// treats slices as atomic leaves, so an `acceptedCAs[2].key`
	// rotation surfaces at the formatter as a FieldChange whose
	// Path is the parent slice (`cluster.acceptedCAs`), value is
	// the whole `[]any` of maps. The whole slice is redacted —
	// element-level granularity is sacrificed for correctness: the
	// formatter cannot today render `{crt: visible, key: redacted}`
	// per element because the secret check fires above bothSlices,
	// not inside the renderer. If/when the differ recurses into
	// slice elements with stable identity (e.g.
	// `cluster.acceptedCAs[crt=foo].key`), the bracket forms can
	// be added alongside these parent entries.
	"cluster.acceptedCAs",
	"machine.acceptedCAs",

	// v1alpha1 multidoc kinds. Paths are bare (no doc-kind prefix)
	// because the differ does not prepend the doc kind to inner
	// paths. `privateKey` matches WireguardConfig.privateKey
	// directly (scalar leaf). `peers` matches WireguardConfig.peers
	// as a parent slice (same shape as cluster.acceptedCAs — the
	// whole peers slice is redacted because the differ won't
	// descend into element fields to find the presharedKey leaf).
	"privateKey",
	"peers",
}

// arrayIndexPattern matches `[N]` segments (one or more digits) so
// isSecretPath can normalise paths like `cluster.acceptedCAs[2].key`
// down to `cluster.acceptedCAs[].key` before comparing against the
// allowlist.
var arrayIndexPattern = regexp.MustCompile(`\[\d+\]`)

// isSecretPath reports whether the leaf-field path falls inside the
// drift-preview redaction allowlist. The matcher is exact-equality
// after bracket normalisation, NOT a prefix match: `cluster.token`
// matches only `cluster.token`, not `cluster.tokenExtras` or
// `cluster.token.subkey`.
//
// Numeric array indices are normalised to `[]` before comparison
// so an operator-visible diff with concrete indices
// (`cluster.acceptedCAs[2].key`) matches the allowlist entry
// (`cluster.acceptedCAs[].key`). The normalisation is the only
// transformation; nested-field paths under a secret entry are not
// auto-included — an allowlist entry must name the leaf exactly.
func isSecretPath(path string) bool {
	normalised := arrayIndexPattern.ReplaceAllString(path, "[]")

	return slices.Contains(secretFieldPaths, normalised)
}

// secretRedactor decides whether a drift-preview field value must be masked.
// It combines two independent sources:
//
//   - the static allowlist of Talos bootstrap field PATHS (isSecretPath), and
//   - the dynamic set of user secret VALUES collected from encrypted value
//     files, so a secret authored in values-secret.encrypted.yaml is masked
//     wherever it surfaces in the diff, at any path.
//
// show (from --show-secrets-in-drift) disables both. A zero secretRedactor
// (show=false, nil userSecrets) reproduces the historical path-only behaviour,
// so callers with no encrypted value files in scope need no special casing.
type secretRedactor struct {
	show        bool
	userSecrets map[string]struct{}
}

// redactPath reports whether the whole field line must be masked because its
// path is an allowlisted Talos bootstrap secret.
func (r secretRedactor) redactPath(path string) bool {
	return !r.show && isSecretPath(path)
}

// redactSide reports whether one side of a field change must be masked because
// its value — or, for a slice/map, any string nested within it — equals a
// known user secret. Absent sides and the --show-secrets-in-drift bypass are
// never redacted.
func (r secretRedactor) redactSide(has bool, value any) bool {
	if r.show || !has || len(r.userSecrets) == 0 {
		return false
	}

	return containsUserSecretValue(value, r.userSecrets)
}

// containsUserSecretValue reports whether value, or any string nested in a
// slice/map it contains, is in the secret set. Composite shapes are scanned
// because the differ flattens a slice-of-maps to a single FieldChange whose
// value is the whole []any — a secret nested in a machine.pods[] element would
// otherwise leak through the non-redacted slice path.
func containsUserSecretValue(value any, secrets map[string]struct{}) bool {
	switch typed := value.(type) {
	case string:
		_, ok := secrets[typed]

		return ok
	case []any:
		for _, item := range typed {
			if containsUserSecretValue(item, secrets) {
				return true
			}
		}

		return false
	case map[string]any:
		for _, item := range typed {
			if containsUserSecretValue(item, secrets) {
				return true
			}
		}

		return false
	default:
		return false
	}
}

// redactValue renders the redaction sentinel for a secret-bearing
// value. Length disclosure is intentional: operators rotating a
// secret want a signal that the rotation actually happened on the
// node (different lengths = the value changed); without the length
// disclosure two `***redacted***` sides look identical regardless
// of whether the value rotated.
//
// Empty / absent values stay distinct: an empty-string secret
// reads as `***redacted (len=0)***`, which is still distinguishable
// from `(absent)` rendered by formatFieldValue.
func redactValue(s string) string {
	return fmt.Sprintf("***redacted (len=%d)***", len(s))
}
