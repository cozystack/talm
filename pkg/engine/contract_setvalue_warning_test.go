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
	"bytes"
	"strings"
	"testing"
)

// withCapturedSetValueWarnings redirects setValueWarningWriter to a
// caller-supplied buffer for the duration of the test, restoring the
// original writer via t.Cleanup. The package-level writer defaults
// to os.Stderr, so tests must isolate it to read the warning stream.
func withCapturedSetValueWarnings(t *testing.T) *bytes.Buffer {
	t.Helper()

	buf := &bytes.Buffer{}
	prev := setValueWarningWriter
	setValueWarningWriter = buf

	t.Cleanup(func() {
		setValueWarningWriter = prev
	})

	return buf
}

// TestLoadValues_IPShapedSetValue_EmitsWarning pins the operator-
// footgun guard added for `--set <key>=<ip>`. Helm's strvals.ParseInto
// interprets dots in the RHS as YAML key nesting, so
// `--set endpoint=192.168.1.1` produces the map
// `{endpoint: {192: {168: {1: 1}}}}` — silently corrupt config the
// operator notices only when the rendered manifest is wrong. The
// warning steers them to `--set-string` BEFORE the bad render lands.
func TestLoadValues_IPShapedSetValue_EmitsWarning(t *testing.T) {
	buf := withCapturedSetValueWarnings(t)

	if _, err := loadValues(Options{Values: []string{"endpoint=192.168.1.1"}}); err != nil {
		t.Fatalf("loadValues should succeed even when the value is IP-shaped (warning, not fatal); got: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "endpoint=192.168.1.1") {
		t.Errorf("warning must echo the offending key=value so the operator can correlate; got:\n%s", out)
	}
	if !strings.Contains(out, "--set-string") {
		t.Errorf("warning must point at --set-string as the fix; got:\n%s", out)
	}
}

// TestLoadValues_CIDRShapedSetValue_EmitsWarning extends the IPv4
// case to a CIDR. The same nesting trap applies: dots in the RHS
// stay dots; the `/24` suffix is irrelevant to strvals' parser.
func TestLoadValues_CIDRShapedSetValue_EmitsWarning(t *testing.T) {
	buf := withCapturedSetValueWarnings(t)

	if _, err := loadValues(Options{Values: []string{"subnet=10.0.0.0/24"}}); err != nil {
		t.Fatalf("loadValues: %v", err)
	}

	if !strings.Contains(buf.String(), "--set-string") {
		t.Errorf("CIDR value must trigger the same --set-string warning; got:\n%s", buf.String())
	}
}

// TestLoadValues_ColonSeparatedLiteral_NoWarning pins the no-
// false-positive contract for colon-separated values. The warning
// targets the strvals dot-nesting trap; colons are not strvals
// separators, so `--set startTime=12:34:56`,
// `--set mac=00:11:22:33:44:55`, and IPv6 literals must not emit
// a warning whose copy says "dots are interpreted as YAML key
// nesting".
func TestLoadValues_ColonSeparatedLiteral_NoWarning(t *testing.T) {
	cases := []string{
		"startTime=12:34:56",
		"mac=00:11:22:33:44:55",
		"endpoint=2001:db8::1",
		"hex=ABCD:1234:EF56",
	}

	for _, v := range cases {
		t.Run(v, func(t *testing.T) {
			buf := withCapturedSetValueWarnings(t)

			if _, err := loadValues(Options{Values: []string{v}}); err != nil {
				t.Fatalf("loadValues: %v", err)
			}

			if buf.Len() > 0 {
				t.Errorf("colon-separated literal %q must not emit IP-shape warning (colons are not strvals separators); got:\n%s", v, buf.String())
			}
		})
	}
}

// TestLoadValues_SemverShapedSetValue_EmitsWarning covers the
// version-string case (`v1.13.0` or `1.13.0`). Same root cause:
// dots become nesting separators.
func TestLoadValues_SemverShapedSetValue_EmitsWarning(t *testing.T) {
	cases := []string{
		"image.tag=v1.13.0", // v-prefixed three-component
		"image.tag=v1.13",   // v-prefixed two-component
		"image.tag=1.13.0",  // bare three-component
	}

	for _, v := range cases {
		t.Run(v, func(t *testing.T) {
			buf := withCapturedSetValueWarnings(t)

			if _, err := loadValues(Options{Values: []string{v}}); err != nil {
				t.Fatalf("loadValues: %v", err)
			}

			if !strings.Contains(buf.String(), "--set-string") {
				t.Errorf("semver-shaped value must trigger the --set-string warning; got:\n%s", buf.String())
			}
		})
	}
}

// TestLoadValues_BareDecimalSetValue_NoWarning pins the negative
// contract for two-component bare decimals: `cpu=1.5`,
// `weight=2.0`, and similar plain-numeric values that share the
// "single dot, digits on both sides" shape with semver-two but
// carry no operator-intent signal toward strvals nesting. Without
// this guard the warning generates noise on every Helm chart that
// uses `--set cpu=1.5` to override resource requests.
func TestLoadValues_BareDecimalSetValue_NoWarning(t *testing.T) {
	cases := []string{
		"cpu=1.5",
		"weight=2.0",
		"ratio=0.95",
	}

	for _, v := range cases {
		t.Run(v, func(t *testing.T) {
			buf := withCapturedSetValueWarnings(t)

			if _, err := loadValues(Options{Values: []string{v}}); err != nil {
				t.Fatalf("loadValues: %v", err)
			}

			if buf.Len() > 0 {
				t.Errorf("bare decimal %q must not emit semver warning; got:\n%s", v, buf.String())
			}
		})
	}
}

// TestLoadValues_InvalidIPv4OctetSetValue_NoWarning pins that the
// IPv4 detector rejects out-of-range octets. `999.999.999.999`
// shares the four-dotted-numeric shape with a real IPv4 literal
// but is overwhelmingly likely to be a chart-internal magic
// number, not an accidental IP literal. Tightening the regex
// avoids the false positive at zero cost to legitimate IPv4
// detection.
func TestLoadValues_InvalidIPv4OctetSetValue_NoWarning(t *testing.T) {
	buf := withCapturedSetValueWarnings(t)

	if _, err := loadValues(Options{Values: []string{"magic=999.999.999.999"}}); err != nil {
		t.Fatalf("loadValues: %v", err)
	}

	if buf.Len() > 0 {
		t.Errorf("invalid-octet four-dotted-numeric must not trigger IPv4 warning; got:\n%s", buf.String())
	}
}

// TestLoadValues_IPShapedSetStringValue_NoWarning pins the negative
// contract: when the operator already uses --set-string, no warning
// fires. The guard is for the `--set` footgun specifically.
func TestLoadValues_IPShapedSetStringValue_NoWarning(t *testing.T) {
	buf := withCapturedSetValueWarnings(t)

	if _, err := loadValues(Options{StringValues: []string{"endpoint=192.168.1.1"}}); err != nil {
		t.Fatalf("loadValues: %v", err)
	}

	if buf.Len() > 0 {
		t.Errorf("--set-string must not emit the IP-shape warning; got:\n%s", buf.String())
	}
}

// TestLoadValues_PlainSetValue_NoWarning pins the no-false-positive
// contract: plain string / numeric / bool values must not trigger
// the warning. The detector matches IP / CIDR / semver shapes only.
func TestLoadValues_PlainSetValue_NoWarning(t *testing.T) {
	cases := []string{
		"clusterName=test",
		"count=5",
		"enabled=true",
		"image.repository=ghcr.io/foo/bar",
	}

	for _, v := range cases {
		t.Run(v, func(t *testing.T) {
			buf := withCapturedSetValueWarnings(t)

			if _, err := loadValues(Options{Values: []string{v}}); err != nil {
				t.Fatalf("loadValues: %v", err)
			}

			if buf.Len() > 0 {
				t.Errorf("plain value %q must not emit IP-shape warning; got:\n%s", v, buf.String())
			}
		})
	}
}

// TestLoadValues_ChainedSetValue_WarnsOnEachIPLiteral covers Helm's
// comma-chained shorthand: `--set k1=v1,k2=v2`. Each comma-segment
// is parsed independently and each must be screened independently.
func TestLoadValues_ChainedSetValue_WarnsOnEachIPLiteral(t *testing.T) {
	buf := withCapturedSetValueWarnings(t)

	if _, err := loadValues(Options{Values: []string{"a=1.2.3.4,b=plain,c=5.6.7.8"}}); err != nil {
		t.Fatalf("loadValues: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "a=1.2.3.4") {
		t.Errorf("first IP literal must be flagged; got:\n%s", out)
	}
	if !strings.Contains(out, "c=5.6.7.8") {
		t.Errorf("third IP literal must be flagged; got:\n%s", out)
	}
	if strings.Contains(out, "b=plain") {
		t.Errorf("plain pair must not be flagged; got:\n%s", out)
	}
}
