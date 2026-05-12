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

	"github.com/cockroachdb/errors"
)

// TestResolveOverwritePolicy pins the (force, isTTY) -> policy
// matrix that drives askUserOverwrite. The whole point is to make
// the non-tty case fail loudly rather than silently leave the
// project on a stale preset, AND to honor --force as the documented
// scripted-refresh escape hatch.
func TestResolveOverwritePolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		force bool
		isTTY bool
		want  overwritePolicy
	}{
		{"force + tty -> force", true, true, overwritePolicyForce},
		{"force + non-tty -> force", true, false, overwritePolicyForce},
		{"no force + tty -> ask", false, true, overwritePolicyAsk},
		{"no force + non-tty -> non-interactive", false, false, overwritePolicyNonInteractive},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := resolveOverwritePolicy(tc.force, tc.isTTY)
			if got != tc.want {
				t.Errorf("resolveOverwritePolicy(force=%v, tty=%v) = %v, want %v", tc.force, tc.isTTY, got, tc.want)
			}
		})
	}
}

// TestAskUserOverwrite_ForcePolicy pins the contract: under
// overwritePolicyForce the prompt is bypassed entirely and the
// answer is unconditionally yes. The operator opted into this by
// passing --force; no stdin read happens.
func TestAskUserOverwrite_ForcePolicy(t *testing.T) {
	t.Parallel()

	ok, err := askUserOverwrite("/tmp/anything", overwritePolicyForce)
	if err != nil {
		t.Fatalf("force policy should not error, got %v", err)
	}

	if !ok {
		t.Errorf("force policy must return true, got false")
	}
}

// TestAskUserOverwrite_NonInteractivePolicy pins the contract: when
// running non-interactively without --force, the call returns a
// hint-bearing error pointing operators at --force. The previous
// behaviour was a raw stdin EOF wrapped in a less actionable message.
func TestAskUserOverwrite_NonInteractivePolicy(t *testing.T) {
	t.Parallel()

	ok, err := askUserOverwrite("/tmp/anything", overwritePolicyNonInteractive)
	if err == nil {
		t.Fatal("non-interactive policy must error, got nil")
	}

	if ok {
		t.Errorf("non-interactive policy must return false, got true")
	}

	hints := errors.GetAllHints(err)
	if len(hints) == 0 {
		t.Errorf("expected a hint pointing at --force, got none")
	}

	hintsStr := strings.Join(hints, " ")
	if !strings.Contains(hintsStr, "--force") {
		t.Errorf("hint should mention --force, got %q", hintsStr)
	}
}

// TestAskUserOverwrite_AskPolicy_YesResponse pins the interactive
// path: under the ask policy, stdin is consulted; a `y` answer
// returns true, anything else returns false. stdinReader is swapped
// for a strings.Reader so the test doesn't need a real tty.
func TestAskUserOverwrite_AskPolicy_YesResponse(t *testing.T) {
	originalReader := stdinReader
	t.Cleanup(func() { stdinReader = originalReader })

	stdinReader = strings.NewReader("y\n")

	ok, err := askUserOverwrite("/tmp/anything", overwritePolicyAsk)
	if err != nil {
		t.Fatalf("ask policy returned error: %v", err)
	}

	if !ok {
		t.Errorf("answer 'y' must return true, got false")
	}
}

// TestAskUserOverwrite_AskPolicy_NoResponse pins the inverse: every
// answer that isn't 'y' / 'yes' returns false (skip). Default-no
// is the conservative behaviour the previous prompt promised via
// the `[y/N]` suffix.
func TestAskUserOverwrite_AskPolicy_NoResponse(t *testing.T) {
	originalReader := stdinReader
	t.Cleanup(func() { stdinReader = originalReader })

	for _, response := range []string{"\n", "n\n", "no\n", "garbage\n"} {
		stdinReader = strings.NewReader(response)

		ok, err := askUserOverwrite("/tmp/anything", overwritePolicyAsk)
		if err != nil {
			t.Errorf("response %q returned unexpected error: %v", response, err)
		}

		if ok {
			t.Errorf("response %q must return false, got true", response)
		}
	}
}
