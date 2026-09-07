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

// setFlagCommands are the commands that expose the --set family. The help
// contracts below hold for every one of them, so a flag reworded on one
// command and not the other is a failure.
//
//nolint:gochecknoglobals // immutable lookup table read by the help contract tests.
var setFlagCommands = map[string]func(name string) string{
	"template": func(name string) string { return templateCmd.Flags().Lookup(name).Usage },
	"apply":    func(name string) string { return applyCmd.Flags().Lookup(name).Usage },
}

// TestSetHelpText_PointsAtSetString pins the operator-facing UX: an
// operator deciding between the two flags reads `--help`, so the --set
// description has to name --set-string as the alternative.
func TestSetHelpText_PointsAtSetString(t *testing.T) {
	t.Parallel()

	for name, usage := range setFlagCommands {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := usage("set"); !strings.Contains(got, "--set-string") {
				t.Errorf("talm %s --set Usage must name --set-string as the alternative; got:\n%s", name, got)
			}
		})
	}
}

// TestSetStringHelpText_ExplainsTypeConversion pins the reason to reach
// for --set-string. Integers and booleans are the only values --set
// converts, so "set STRING values" alone leaves an operator with no way
// to tell when it matters.
func TestSetStringHelpText_ExplainsTypeConversion(t *testing.T) {
	t.Parallel()

	for name, usage := range setFlagCommands {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := usage("set-string"); !strings.Contains(strings.ToLower(got), "convert") {
				t.Errorf("talm %s --set-string Usage must say that nothing is type-converted; got:\n%s", name, got)
			}
		})
	}
}

// TestSetHelpText_DoesNotClaimDotNesting keeps a long-standing false
// claim from coming back. The help text used to tell operators that a
// dot in a --set value nested it into a map, so that
// `--set endpoint=10.0.0.1` supposedly rendered
// {endpoint: {10: {0: {0: 1}}}} and IP literals needed --set-string.
// strvals stops key scanning at '=', so dots only nest on the left of
// it and a dotted value stays whole — see
// TestSetValueDotsNestOnlyInTheKey in pkg/engine, which pins the
// parser behaviour this text describes.
func TestSetHelpText_DoesNotClaimDotNesting(t *testing.T) {
	t.Parallel()

	for name, usage := range setFlagCommands {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			for _, flag := range []string{"set", "set-string"} {
				if got := strings.ToLower(usage(flag)); strings.Contains(got, "nesting") || strings.Contains(got, "nested") {
					t.Errorf("talm %s --%s Usage must not describe dots in a value as key nesting; got:\n%s", name, flag, got)
				}
			}
		})
	}
}
