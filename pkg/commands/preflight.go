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
	"github.com/cosi-project/runtime/pkg/resource"
	"github.com/cosi-project/runtime/pkg/safe"
	"github.com/siderolabs/talos/pkg/machinery/client"
	machineryconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/resources/runtime"
)

// preflightCOSIReadTimeout caps the COSI read latency so a slow or
// unresponsive node cannot turn a best-effort informational check into a
// blocker for `apply`. Two seconds is comfortably above any expected
// roundtrip on a healthy node and short enough to be unnoticeable when the
// read actually fails.
const preflightCOSIReadTimeout = 2 * time.Second

// preflightVersionMismatchHint is the hint attached to the warning when the
// configured talosVersion contract is newer than the version reported by the
// node. It does not name a specific Talos version — the warning line itself
// includes the concrete numbers.
const preflightVersionMismatchHint = "the generated config may include fields the node's machinery doesn't know; " +
	"either reboot the node into a maintenance image matching templateOptions.talosVersion / --talos-version, " +
	"or lower templateOptions.talosVersion / --talos-version to match the running Talos."

// applyConfigDecodeHint is the hint attached when the node's strict decoder
// rejects the applied config because of an unknown field. It points at the
// machinery contract / running Talos mismatch without naming a specific
// version.
const applyConfigDecodeHint = "the maintenance Talos parser on the node didn't recognize a field talm injected. " +
	"this usually means templateOptions.talosVersion / --talos-version is set to a contract " +
	"newer than the running Talos. reboot the node into a maintenance image matching the configured " +
	"contract, or lower templateOptions.talosVersion / --talos-version to match what's running."

// annotateApplyConfigError attaches applyConfigDecodeHint when err is a
// strict-decoder failure from the node side. Returns err unchanged otherwise.
func annotateApplyConfigError(err error) error {
	if err == nil {
		return nil
	}

	if !strings.Contains(err.Error(), "unknown keys found during decoding:") {
		return err
	}

	//nolint:wrapcheck // cockroachdb/errors.WithHint at boundary.
	return errors.WithHint(err, applyConfigDecodeHint)
}

// versionReader fetches the running Talos version from a node. Three-valued
// return mirrors the linksDisksReader / machineConfigReader contract:
//
//   - (version, true,  nil) — success
//   - ("",      false, err) — real read failure (network, RPC, COSI server)
//   - ("",      false, nil) — by-design unreachable (auth-disallowed, etc.)
//
// Callers decide what each combination means. preflightCheckTalosVersion's
// pre-apply warning silently surrenders on any !ok regardless of err
// (best-effort warning). verifyPostUpgradeVersion treats err!=nil as a real
// read failure that IS the rollback signal — exactly what the gate exists to
// catch — and surfaces it as a hint-bearing blocker.
type versionReader func(ctx context.Context) (version string, ok bool, err error)

// cosiVersionReader returns a versionReader that reads the Talos version from
// the node's COSI `Versions.runtime.talos.dev/runtime/version` resource. The
// resource is declared NonSensitive in Talos and is therefore reachable
// through a maintenance (--insecure) connection that only carries the Reader
// role.
//
// Any read failure (RPC error, NotFound, PermissionDenied, multi-node proxy
// error, connection refused) flows through as (false, wrapped err). Callers
// that need to distinguish a real failure from a no-result case can inspect
// the err.
func cosiVersionReader(c *client.Client) versionReader {
	return func(ctx context.Context) (string, bool, error) {
		ctx, cancel := context.WithTimeout(ctx, preflightCOSIReadTimeout)
		defer cancel()

		res, err := safe.StateGet[*runtime.Version](
			ctx,
			c.COSI,
			resource.NewMetadata(runtime.NamespaceName, runtime.VersionType, "version", resource.VersionUndefined),
		)
		if err != nil {
			return "", false, errors.Wrap(err, "reading runtime.Version COSI resource")
		}

		return res.TypedSpec().Version, true, nil
	}
}

// preflightCheckTalosVersion compares the configured Talos contract against
// the version reported by `read` and prints a warning + hint to `w` if the
// configured contract is strictly newer than the running version.
//
// Best-effort: any read or parse failure returns silently and never blocks
// apply. An empty configuredVersion is treated as TalosVersionCurrent (the
// nil-pointer contract that machinery uses by default), which is the most
// aggressive contract — this is the documented reproduction case for the
// "unknown keys found during decoding" error.
func preflightCheckTalosVersion(ctx context.Context, read versionReader, configuredVersion string, w io.Writer) {
	// Pre-apply warning is best-effort: silent surrender on any !ok
	// regardless of err. The err is captured but intentionally
	// discarded — if reading the runtime.Version fails, the apply call
	// that follows will surface the underlying connection issue with a
	// clearer message than this gate could.
	runningVersion, ok, _ := read(ctx)
	if !ok {
		return
	}

	warning := evaluateVersionMismatch(configuredVersion, runningVersion)
	if warning == nil {
		return
	}

	_, _ = fmt.Fprintln(w, "warning:", warning.Error())
	for _, hint := range errors.GetAllHints(warning) {
		_, _ = fmt.Fprintf(w, "hint: %s\n", hint)
	}
}

// evaluateVersionMismatch returns a hint-bearing warning error if the
// configured contract is strictly newer than the running version. It returns
// nil when versions agree, when the configured contract isn't newer, or when
// the running version cannot be parsed (best-effort: never block on parse
// failure).
//
// An empty configuredVersion is treated as machinery's TalosVersionCurrent
// (nil pointer), which compares as strictly greater than every concrete
// version. This matches what generate.NewInput does when no
// WithVersionContract option is supplied.
func evaluateVersionMismatch(configuredVersion, runningVersion string) error {
	var configuredContract *machineryconfig.VersionContract

	if configuredVersion != "" {
		var err error

		configuredContract, err = machineryconfig.ParseContractFromVersion(configuredVersion)
		if err != nil {
			return nil //nolint:nilerr // best-effort: never block apply on parse failure
		}
	}

	runningContract, err := machineryconfig.ParseContractFromVersion(runningVersion)
	if err != nil {
		return nil //nolint:nilerr // best-effort: never block apply on parse failure
	}

	if !configuredContract.Greater(runningContract) {
		return nil
	}

	warning := errors.Newf(
		"pre-flight: configured talosVersion=%s is newer than the node's running Talos %s",
		configuredContract,
		runningVersion,
	)

	//nolint:wrapcheck // cockroachdb/errors.WithHint at boundary.
	return errors.WithHint(warning, preflightVersionMismatchHint)
}
