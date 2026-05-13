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

package engine

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

// setValueWarningWriter is the sink for the operator-facing warning
// emitted by loadValues when a `--set` value's RHS looks like an IP,
// CIDR, or version literal — shapes that strvals.ParseInto would
// interpret as nested map keys, silently corrupting the rendered
// config. Defaulted to os.Stderr; redirected in tests via
// withCapturedSetValueWarnings.
//
//nolint:gochecknoglobals // package-level writer is the standard Go pattern for test-overridable side-channel output; an Options-borne writer would propagate through the public engine API for a single internal-use warning.
var setValueWarningWriter io.Writer = os.Stderr

// ipShapedValueRe matches a valid IPv4 literal or IPv4 CIDR. dots
// in these shapes are not separators (they belong to the address)
// but strvals.ParseInto reads them as YAML key nesting. The per-
// octet alternation rejects out-of-range numbers (256.x.y.z) so
// the heuristic does not flag arbitrary four-dotted numeric strings
// like 999.999.999.999 — which carry no operator-intent signal.
var ipShapedValueRe = regexp.MustCompile(`^(?:25[0-5]|2[0-4]\d|1?\d?\d)(?:\.(?:25[0-5]|2[0-4]\d|1?\d?\d)){3}(?:/(?:3[0-2]|[12]?\d))?$`)

// semverShapedValueRe matches semantic-version literals: either a
// `v`-prefixed two-or-three-component form (`v1.13`, `v1.13.0`) or
// a bare three-component form (`1.13.0`). A bare two-component form
// (`1.5`, `2.0`) is deliberately NOT matched — those shapes are
// indistinguishable from plain decimal values like
// `cpu=1.5` / `weight=2.0` and would generate noise. Same dot-as-
// nesting trap as the IP shape; bare two-component decimals are
// the common false-positive class.
var semverShapedValueRe = regexp.MustCompile(`^(?:v\d+\.\d+(?:\.\d+)?|\d+\.\d+\.\d+)$`)

// looksLikeAccidentalSetCoercion screens a single `key=value` pair
// for the canonical strvals footgun shapes. Returns true when the
// RHS contains dots that strvals.ParseInto would interpret as YAML
// key nesting against the operator's intent. IPv6 / MAC / colon-
// separated literals are deliberately NOT flagged: colons are not
// strvals separators, so `--set startTime=12:34:56` and
// `--set mac=00:11:22:33:44:55` do not hit the nesting trap and
// must not emit a misleading "use --set-string" warning.
func looksLikeAccidentalSetCoercion(pair string) bool {
	// Split on the first '=' only; chained values are screened
	// upstream by splitChainedSetValue. A pair without '='
	// can't have a footgun RHS to flag.
	_, value, ok := strings.Cut(pair, "=")
	if !ok || value == "" {
		return false
	}

	switch {
	case ipShapedValueRe.MatchString(value):
		return true
	case semverShapedValueRe.MatchString(value):
		return true
	}

	return false
}

// splitChainedSetValue splits a chained --set value
// (`k1=v1,k2=v2,k3=v3`) into its constituent pairs. The actual Helm
// strvals parser handles escape semantics; this helper exists only
// to screen each pair for the IP-shape footgun before strvals chews
// the string. A simple Split on ',' is enough for the canonical
// shapes the warning targets — operators with embedded commas in
// values should already be reaching for --set-literal or --set-json.
func splitChainedSetValue(value string) []string {
	return strings.Split(value, ",")
}

// emitSetValueCoercionWarning writes the operator-facing warning to
// setValueWarningWriter for the offending pair. Format matches the
// rest of the cobra error / hint conventions ("talm:" prefix, one
// line per warning).
func emitSetValueCoercionWarning(pair string) {
	fmt.Fprintf(setValueWarningWriter,
		"talm: --set %s looks like an IP / CIDR / version literal; "+
			"strvals.ParseInto interprets dots as YAML key nesting, "+
			"so the rendered value will be a nested map. Pass --set-string %s "+
			"to keep the literal verbatim.\n",
		pair, pair,
	)
}

// screenSetValuesForCoercion walks every comma-separated pair in
// every --set entry and emits a warning per footgun-shape match.
// The screen is non-fatal: parsing proceeds as before, the warning
// is the only behavioural change.
func screenSetValuesForCoercion(values []string) {
	for _, value := range values {
		for _, pair := range splitChainedSetValue(value) {
			if looksLikeAccidentalSetCoercion(pair) {
				emitSetValueCoercionWarning(pair)
			}
		}
	}
}
