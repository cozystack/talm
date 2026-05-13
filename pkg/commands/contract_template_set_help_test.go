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
)

// TestTemplateFlag_SetHelpText_MentionsSetString pins the operator-
// facing UX: `talm template --help` must surface the --set-string
// escape hatch directly in the --set flag description. Operators
// hitting the strvals dot-nesting footgun (e.g.
// `--set endpoint=10.0.0.1` parsed as nested map) check --help
// before CHANGELOG. The Usage string must name --set-string so the
// hint lands in front of them.
func TestTemplateFlag_SetHelpText_MentionsSetString(t *testing.T) {
	flag := templateCmd.Flags().Lookup("set")
	if flag == nil {
		t.Fatal("expected templateCmd to register a --set flag, got nil")
	}

	if !strings.Contains(flag.Usage, "--set-string") {
		t.Errorf("--set Usage must point at --set-string as the escape hatch for IP / version literals; got:\n%s", flag.Usage)
	}
}

// TestTemplateFlag_SetStringHelpText_MentionsLiteralStability pins
// the sibling contract: --set-string Usage must describe its niche
// (IP / CIDR / version literals — values that must not be type-
// coerced or dot-nested). Without this, the help text reads as
// "set STRING values" with no clue when to prefer it over --set.
func TestTemplateFlag_SetStringHelpText_MentionsLiteralStability(t *testing.T) {
	flag := templateCmd.Flags().Lookup("set-string")
	if flag == nil {
		t.Fatal("expected templateCmd to register a --set-string flag, got nil")
	}

	// The Usage must surface the literal-stability niche. The
	// word "literal" is the canonical anchor; the sibling words
	// (IP / CIDR / version) all describe shapes that need it.
	usage := strings.ToLower(flag.Usage)
	if !strings.Contains(usage, "literal") {
		t.Errorf("--set-string Usage must name the literal-stability use case; got:\n%s", flag.Usage)
	}
}

// TestTemplateFlag_SetStringHelpText_DoesNotOverpromiseHostnames pins
// the alignment between the --set-string Usage copy and the actual
// detector in pkg/engine/setvalue_warn.go. The screener matches
// IPv4 / IPv4 CIDR / semver shapes only — hostnames like
// `foo.example.com` are NOT flagged, so the Usage must NOT advertise
// hostname coverage. A previous revision did, which mislead operators
// into thinking `--set host=foo.example.com` would be screened.
func TestTemplateFlag_SetStringHelpText_DoesNotOverpromiseHostnames(t *testing.T) {
	flag := templateCmd.Flags().Lookup("set-string")
	if flag == nil {
		t.Fatal("expected templateCmd to register a --set-string flag, got nil")
	}

	if strings.Contains(strings.ToLower(flag.Usage), "hostname") {
		t.Errorf("--set-string Usage must NOT claim hostname coverage — the screener does not detect hostname-shaped values; got:\n%s", flag.Usage)
	}
}
