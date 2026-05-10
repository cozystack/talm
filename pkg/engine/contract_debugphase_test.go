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

// Contract: debugPhase tolerates empty patch entries in its input
// slice. Templates that conditionally emit nothing legitimately
// produce "" in the slice; the original implementation indexed
// patch[0] without a length guard and panicked at runtime, which
// happened ONLY under --debug — the worst possible time to crash.

package engine

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/siderolabs/talos/pkg/machinery/config/machine"
)

// TestContract_DebugPhase_TolerantOfEmptyPatch runs debugPhase as a
// subprocess (it calls os.Exit(0) at the end so cannot be exercised
// in-process) and asserts:
//
//   - exit code is 0 (graceful exit, not a panic)
//   - no "runtime error" or "index out of range" in stderr
//   - the surviving non-empty patches are still printed
//
// Re-entry uses the os.Args[0]+TEST_DEBUG_PHASE_HELPER pattern from
// Go's own stdlib tests (e.g. os/exec_test.go).
func TestContract_DebugPhase_TolerantOfEmptyPatch(t *testing.T) {
	if os.Getenv("TEST_DEBUG_PHASE_HELPER") == "1" {
		// Child process: invoke debugPhase with a slice that contains
		// an empty patch sandwiched between two non-empty ones. The
		// pre-fix implementation would panic on patch[0] for the
		// empty entry. With the fix, the empty entry is skipped and
		// the surrounding entries are printed.
		debugPhase(
			Options{},
			[]string{"machine:\n  type: worker", "", "machine:\n  type: controlplane"},
			"test-cluster",
			"https://example.com:6443",
			machine.TypeWorker,
		)
		return // unreachable, debugPhase calls os.Exit(0)
	}

	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestContract_DebugPhase_TolerantOfEmptyPatch$")
	cmd.Env = append(os.Environ(), "TEST_DEBUG_PHASE_HELPER=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("debugPhase subprocess failed: %v\noutput:\n%s", err, out)
	}
	output := string(out)
	if strings.Contains(output, "runtime error") || strings.Contains(output, "index out of range") {
		t.Errorf("debugPhase panicked on empty patch:\n%s", output)
	}
	// The non-empty patches must still appear in output (one as
	// machine.type=worker, the other as type=controlplane). Order is
	// preserved.
	if !strings.Contains(output, "type: worker") {
		t.Errorf("expected first non-empty patch in output:\n%s", output)
	}
	if !strings.Contains(output, "type: controlplane") {
		t.Errorf("expected last non-empty patch in output:\n%s", output)
	}
}

// TestContract_DebugPhase_HandlesAllEmpty verifies the all-empty
// slice — the loop simply skips every entry, debugPhase prints the
// header then exits 0. Pinning so a refactor that errors on
// "no patches printed" surfaces here.
func TestContract_DebugPhase_HandlesAllEmpty(t *testing.T) {
	if os.Getenv("TEST_DEBUG_PHASE_HELPER_ALL_EMPTY") == "1" {
		debugPhase(
			Options{},
			[]string{"", "", ""},
			"test-cluster",
			"https://example.com:6443",
			machine.TypeWorker,
		)
		return
	}

	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestContract_DebugPhase_HandlesAllEmpty$")
	cmd.Env = append(os.Environ(), "TEST_DEBUG_PHASE_HELPER_ALL_EMPTY=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("debugPhase subprocess failed on all-empty input: %v\noutput:\n%s", err, out)
	}
	if strings.Contains(string(out), "runtime error") {
		t.Errorf("debugPhase panicked on all-empty input:\n%s", out)
	}
}
