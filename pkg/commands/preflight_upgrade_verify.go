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
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/cockroachdb/errors"
	machineryconfig "github.com/siderolabs/talos/pkg/machinery/config"
)

const postUpgradeVersionMismatchHint = "two hypotheses produce this symptom: " +
	"(1) Talos auto-rolled back after the new partition failed its boot readiness check — " +
	"cross-vendor upgrades (e.g. cozystack-bundled image -> vanilla siderolabs installer) " +
	"drop bundled extensions and trigger this. " +
	"(2) The node is slower than the configured reconcile window — large image pulls or cold " +
	"hardware can exceed it. Widen via --post-upgrade-reconcile-window or re-run " +
	"`talm get version` after a minute to distinguish: if the version updated, the node " +
	"was just slow; if it's still the old version, the rollback case is real. " +
	"Pass --skip-post-upgrade-verify to bypass."

// verifyPostUpgradeVersion is the Phase 2C gate: after talosctl upgrade
// returns, re-read the node's runtime.Version COSI resource and compare
// against the target version parsed from the installer image. Talos
// auto-rolls back when a new boot fails its readiness check, but the
// upgrade RPC has already acked — so success without verification is
// false advertising. See cozystack/talm#175 for the reproduction
// (cross-vendor installer image triggering an A/B rollback on a
// 3-node OCI v1.12.6 stand).
//
// Returns nil on match (or on best-effort read failure with --skip-* not
// set; the caller decides). Returns a hint-bearing blocker on
// version mismatch.
func verifyPostUpgradeVersion(
	ctx context.Context,
	read versionReader,
	targetImage string,
	reconcileWindow time.Duration,
	w io.Writer,
) error {
	target := parseTargetVersion(targetImage)
	if target == "" {
		// Best-effort: no tag, digest pin, or empty image. We can't
		// verify without a tag literal — pass silently. The blocking
		// class is reserved for actual detected mismatches.
		return nil
	}

	targetContract, err := machineryconfig.ParseContractFromVersion(target)
	if err != nil {
		// Unparseable tag (e.g. "latest", a branch name, a SHA-style
		// fork tag) — same best-effort surrender as the empty case.
		return nil //nolint:nilerr // surrender is the contract; see TestVerifyPostUpgradeVersion_UnparseableTag_Skip.
	}

	running, ok, readErr := read(ctx)
	if !ok {
		if readErr != nil {
			// Real read failure on an upgrade-verify call IS the
			// rollback signal — exactly what the gate exists to
			// catch. A node that auto-rolled back or hung mid-boot
			// looks like "connection refused" / "context deadline
			// exceeded" from the COSI client. Block with the same
			// two-hypothesis hint as a detected version mismatch so
			// the operator gets actionable guidance instead of a
			// silent warning that buries the real signal.
			//
			//nolint:wrapcheck // cockroachdb/errors.WithHint at boundary.
			return errors.WithHint(
				errors.Wrapf(readErr, "post-upgrade verify: could not read running version from the node after upgrade to %s", target),
				postUpgradeVersionMismatchHint,
			)
		}

		// ok=false err=nil — by-design unreachable (e.g. a custom
		// reader that signals "not applicable on this path" without
		// an err). cosiVersionReader does not produce this shape,
		// but the contract leaves room for future readers that need
		// to surrender silently without an error.
		_, _ = fmt.Fprintln(w, "warning: post-upgrade verification skipped, could not read running version from the node")

		return nil
	}

	runningContract, err := machineryconfig.ParseContractFromVersion(running)
	if err != nil {
		_, _ = fmt.Fprintf(w, "warning: post-upgrade verification skipped, could not parse running version %q\n", running)

		return nil //nolint:nilerr // best-effort: unparseable running version is a soft warning.
	}

	if runningContract.Major == targetContract.Major && runningContract.Minor == targetContract.Minor {
		return nil
	}

	//nolint:wrapcheck // cockroachdb/errors.WithHint at boundary.
	return errors.WithHint(
		errors.Newf("post-upgrade: requested upgrade to %s but running version is %s — either Talos auto-rolled back, or the node is still booting beyond the configured reconcile window (%s)", target, running, reconcileWindow),
		postUpgradeVersionMismatchHint,
	)
}

// parseTargetVersion lifts a Talos version literal from an installer
// image reference. Inputs:
//
//   - ghcr.io/cozystack/cozystack/talos:v1.12.6 -> v1.12.6
//   - ghcr.io/siderolabs/installer:v1.13.0 -> v1.13.0
//   - factory.talos.dev/installer/<sha256>:v1.13.0 -> v1.13.0
//   - registry.local:5000/foo/installer (no tag, port in registry) -> ""
//   - "" or no tag separator -> ""
//
// Digest-pinned references (image@sha256:...) are rejected up front:
// Phase 2C can only verify when the tag carries the version literal.
// A digest-pinned upgrade silently passes the check (the operator
// opted into a content-addressed install and can read it back via
// talos image list).
//
// Returns the tag verbatim; comparison is done via machineryconfig
// VersionContract so v1.13 and v1.13.0 are equivalent at the minor
// level (point-release upgrades pass silently).
func parseTargetVersion(image string) string {
	// Reject digest pins up front. The "@sha256:" / "@sha512:" marker
	// is the authoritative signal — searching for `/` in the
	// post-`:` substring catches some shapes but misses hex-only
	// digests like `image@sha256:abc123def456`.
	if strings.Contains(image, "@sha256:") || strings.Contains(image, "@sha512:") {
		return ""
	}

	// The tag — when present — is always the last path component's
	// suffix after `:`. Splitting on `/` first isolates that
	// component, so a registry port (`registry.local:5000`) in an
	// earlier path component cannot be mistaken for a tag separator.
	lastSlash := strings.LastIndex(image, "/")

	tail := image
	if lastSlash >= 0 {
		tail = image[lastSlash+1:]
	}

	idx := strings.LastIndex(tail, ":")
	if idx < 0 || idx == len(tail)-1 {
		return ""
	}

	return tail[idx+1:]
}
