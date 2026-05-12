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
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/cockroachdb/errors"
)

// TestShouldRunPostUpgradeVerify_SkipMatrix pins the predicate that
// gates Phase 2C scheduling. The gate cannot produce a meaningful
// result on --insecure (no auth COSI path) or --stage (new partition
// not yet booted); both must be skipped to avoid false-positive
// blockers. The skip flag overrides everything (operator opt-out).
func TestShouldRunPostUpgradeVerify_SkipMatrix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		insecure bool
		staged   bool
		skip     bool
		want     bool
	}{
		{"default runs", false, false, false, true},
		{"--skip-post-upgrade-verify suppresses everything", false, false, true, false},
		{"--insecure skipped (no auth COSI)", true, false, false, false},
		{"--stage skipped (new partition not booted)", false, true, false, false},
		{"--insecure + --stage skipped", true, true, false, false},
		{"all-on skipped", true, true, true, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := shouldRunPostUpgradeVerify(tc.insecure, tc.staged, tc.skip)
			if got != tc.want {
				t.Errorf("shouldRunPostUpgradeVerify(insecure=%v, staged=%v, skip=%v) = %v, want %v",
					tc.insecure, tc.staged, tc.skip, got, tc.want)
			}
		})
	}
}

// TestParseTargetVersion pins the image-tag → version-literal contract.
// The walker handles ghcr / quay / factory references and silently
// surrenders on digest pins (we can't infer the version without an
// extra registry round-trip).
func TestParseTargetVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"ghcr cozystack", "ghcr.io/cozystack/cozystack/talos:v1.12.6", "v1.12.6"},
		{"ghcr siderolabs installer", "ghcr.io/siderolabs/installer:v1.13.0", "v1.13.0"},
		{"factory installer", "factory.talos.dev/installer/abcd1234:v1.13.0", "v1.13.0"},
		{"no tag", "ghcr.io/cozystack/cozystack/talos", ""},
		{"empty tag", "ghcr.io/cozystack/cozystack/talos:", ""},
		{"digest pin with slash in hex (rare)", "ghcr.io/cozystack/cozystack/talos@sha256:abc/def", ""},
		{"realistic digest pin (hex only)", "ghcr.io/cozystack/cozystack/talos@sha256:abc123def456", ""},
		{"sha512 digest", "ghcr.io/foo/bar@sha512:0123456789abcdef", ""},
		{"registry with port, no tag", "registry.local:5000/foo/installer", ""},
		{"registry with port AND tag", "registry.local:5000/foo/installer:v1.13.0", "v1.13.0"},
		{"empty input", "", ""},
		{"only colon", ":", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := parseTargetVersion(tc.in)
			if got != tc.want {
				t.Errorf("parseTargetVersion(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestVerifyPostUpgradeVersion_Match_NoError pins the silent-pass
// contract: running version's contract matches the target's contract
// → no output, no error. This is the post-upgrade equivalent of
// 'apply -dry-run says 0/0/0 unchanged'.
func TestVerifyPostUpgradeVersion_Match_NoError(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	err := verifyPostUpgradeVersion(
		context.Background(),
		stubReader("v1.13.0", true),
		"ghcr.io/siderolabs/installer:v1.13.0",
		buf,
	)
	if err != nil {
		t.Errorf("matching versions should not error, got %v", err)
	}

	if buf.Len() != 0 {
		t.Errorf("matching versions should be silent, got %q", buf.String())
	}
}

// TestVerifyPostUpgradeVersion_MinorMismatch_Blocks pins the
// silent-rollback detection: running v1.12 + target v1.13 means
// Talos auto-rolled back. The gate must surface a blocker with a
// hint pointing at the cross-vendor / missing-extension footgun.
func TestVerifyPostUpgradeVersion_MinorMismatch_Blocks(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	err := verifyPostUpgradeVersion(
		context.Background(),
		stubReader("v1.12.6", true),
		"ghcr.io/siderolabs/installer:v1.13.0",
		buf,
	)
	if err == nil {
		t.Fatal("version mismatch must block, got nil error")
	}

	msg := err.Error() + " " + buf.String()
	if !strings.Contains(msg, "v1.12.6") {
		t.Errorf("error should cite running version, got %q", msg)
	}

	if !strings.Contains(msg, "v1.13") {
		t.Errorf("error should cite target version, got %q", msg)
	}
}

// TestVerifyPostUpgradeVersion_PatchVersion_Match pins the point-
// release case: running v1.12.6 + target v1.12.7 are the same
// minor contract (both 1.12). Phase 2C considers this a success —
// point releases don't trip the rollback detection.
func TestVerifyPostUpgradeVersion_PatchVersion_Match(t *testing.T) {
	t.Parallel()

	err := verifyPostUpgradeVersion(
		context.Background(),
		stubReader("v1.12.6", true),
		"ghcr.io/cozystack/cozystack/talos:v1.12.7",
		&bytes.Buffer{},
	)
	if err != nil {
		t.Errorf("point release (same minor contract) should pass, got %v", err)
	}
}

// TestVerifyPostUpgradeVersion_UnparseableTag_Skip pins the
// best-effort contract: if the target image's tag is missing,
// digest-pinned, or unparseable, the gate cannot verify — it
// should pass silently rather than block a legitimate upgrade.
func TestVerifyPostUpgradeVersion_UnparseableTag_Skip(t *testing.T) {
	t.Parallel()

	tests := []string{
		"",
		"no-tag-image",
		"ghcr.io/foo/bar@sha256:abc/def",
		"ghcr.io/foo/bar@sha256:abc123def456",
		"ghcr.io/foo/bar:not-a-version-string",
	}

	for _, image := range tests {
		t.Run(image, func(t *testing.T) {
			t.Parallel()

			err := verifyPostUpgradeVersion(
				context.Background(),
				stubReader("v1.12.6", true),
				image,
				&bytes.Buffer{},
			)
			if err != nil {
				t.Errorf("unparseable image %q should pass silently, got %v", image, err)
			}
		})
	}
}

// TestVerifyPostUpgradeVersion_ReaderConnectionRefused_NotSilent is
// the structural guard for the three-valued versionReader contract.
// A post-upgrade reader returning ("", false, err) — the shape a real
// silent rollback or hung boot produces (connection refused / context
// deadline exceeded from COSI) — must surface as a hint-bearing
// blocker citing both hypotheses (rollback OR slow boot), NOT as a
// silent warning. The original two-valued reader signature collapsed
// every read failure to ok=false and silent-passed it, leaving the
// silent-rollback detection with a documented blind spot. The new
// three-valued signature lets the gate distinguish "by design
// unreachable" (ok=false err=nil → silent pass, see
// ReaderFails_SoftWarning_NoBlock) from "real read failure"
// (ok=false err!=nil → blocker, this test).
func TestVerifyPostUpgradeVersion_ReaderConnectionRefused_NotSilent(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	err := verifyPostUpgradeVersion(
		context.Background(),
		stubReaderErr(errors.New("connection refused")),
		"ghcr.io/siderolabs/installer:v1.13.0",
		buf,
	)
	if err == nil {
		t.Fatal("connection-refused read must surface as a blocker — it IS the rollback signal")
	}

	msg := err.Error()
	if !strings.Contains(msg, "connection refused") {
		t.Errorf("blocker should wrap the underlying read err so the operator sees the cause, got %q", msg)
	}

	if !strings.Contains(msg, "v1.13.0") {
		t.Errorf("blocker should cite the target version the upgrade was supposed to reach, got %q", msg)
	}

	hint := errors.GetAllHints(err)
	if len(hint) == 0 {
		t.Errorf("blocker must carry the two-hypothesis hint (rollback OR slow boot), got no hints on err=%v", err)
	}
}

// TestVerifyPostUpgradeVersion_ReaderFails_SoftWarning_NoBlock is
// the pin that backs the test-plan's Phase 2C "Reader failure" row:
// when the post-upgrade reader returns ok=false, the gate emits a
// soft warning AND returns nil — never a hint-bearing blocker.
// Direct guard against the test-plan claim drifting back to "wrap
// the underlying err": the versionReader signature has no err to
// wrap, and the gate is informational-by-design for reader-side
// transients. Companion to TestVerifyPostUpgradeVersion_ReaderFails_BestEffort
// (which asserts the warning text) — this one asserts the
// contractual shape (no error, no WithHint chain).
func TestVerifyPostUpgradeVersion_ReaderFails_SoftWarning_NoBlock(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	err := verifyPostUpgradeVersion(
		context.Background(),
		stubReader("", false),
		"ghcr.io/siderolabs/installer:v1.13.0",
		buf,
	)
	if err != nil {
		t.Fatalf("reader failure must NOT block: got err=%v", err)
	}

	// Soft warning visible to the operator.
	if !strings.Contains(buf.String(), "warning") {
		t.Errorf("soft warning expected in output, got %q", buf.String())
	}

	// Direct guard against the test-plan claim drifting back to
	// "Hint-bearing blocker with wrapped underlying err". If a future
	// refactor flips this branch to a blocker, both this test and
	// TestVerifyPostUpgradeVersion_ReaderFails_BestEffort must be
	// updated in lockstep with the doc — and the versionReader
	// signature must grow an error return for "wrapped err" to even
	// be meaningful.
}

// TestVerifyPostUpgradeVersion_ReaderFails_BestEffort pins the
// reader-failure path. Phase 2C is informational at its core — if
// the node is unreachable post-upgrade, the gate prints a warning
// to w and returns nil. The blocking class is reserved for actual
// detected mismatches; a transient unreachable node should not
// block the rest of the apply pipeline.
func TestVerifyPostUpgradeVersion_ReaderFails_BestEffort(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	err := verifyPostUpgradeVersion(
		context.Background(),
		stubReader("", false),
		"ghcr.io/siderolabs/installer:v1.13.0",
		buf,
	)
	if err != nil {
		t.Errorf("reader failure should pass best-effort, got %v", err)
	}

	if !strings.Contains(buf.String(), "could not read") {
		t.Errorf("expected explanatory line about read failure, got %q", buf.String())
	}
}
