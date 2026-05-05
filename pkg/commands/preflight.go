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

	"github.com/cockroachdb/errors"
	"github.com/cosi-project/runtime/pkg/resource"
	"github.com/cosi-project/runtime/pkg/safe"

	"github.com/siderolabs/talos/pkg/machinery/client"
	machineryconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/resources/runtime"
)

// preflightVersionMismatchHint is the hint attached to the warning when the
// configured talosVersion contract is newer than the version reported by the
// node. It does not name a specific Talos version — the warning line itself
// includes the concrete numbers.
const preflightVersionMismatchHint = "the generated config may include fields the node's machinery doesn't know; " +
	"either reboot the node into a maintenance image matching templateOptions.talosVersion, " +
	"or pin --talos-version to the version reported by the node."

// applyConfigDecodeHint is the hint attached when the node's strict decoder
// rejects the applied config because of an unknown field. It points at the
// machinery contract / running Talos mismatch without naming a specific
// version.
const applyConfigDecodeHint = "the maintenance Talos parser on the node didn't recognize a field talm injected. " +
	"this usually means templateOptions.talosVersion / --talos-version is set to a contract " +
	"newer than the running Talos. reboot the node into a maintenance image matching the configured " +
	"contract, or lower --talos-version to match what's running."

// annotateApplyConfigError attaches applyConfigDecodeHint when err is a
// strict-decoder failure from the node side. Returns err unchanged otherwise.
func annotateApplyConfigError(err error) error {
	if err == nil {
		return nil
	}

	if !strings.Contains(err.Error(), "unknown keys found during decoding:") {
		return err
	}

	return errors.WithHint(err, applyConfigDecodeHint)
}

// preflightCheckTalosVersion compares the configured Talos contract against
// the version actually running on the node and prints a warning to the given
// writer if the configured contract is strictly newer than the running
// version.
//
// This is best-effort — every error path (no contract configured, COSI read
// failed, version unparseable) returns silently. The check must never block
// or alter apply behavior.
//
// The Talos version on the node is read from the
// `Versions.runtime.talos.dev/runtime/version` COSI resource, which is
// declared NonSensitive and is therefore reachable through a maintenance
// (--insecure) connection that only carries the Reader role.
func preflightCheckTalosVersion(ctx context.Context, c *client.Client, configuredVersion string, w io.Writer) {
	if configuredVersion == "" {
		return
	}

	res, err := safe.StateGet[*runtime.Version](
		ctx,
		c.COSI,
		resource.NewMetadata(runtime.NamespaceName, runtime.VersionType, "version", resource.VersionUndefined),
	)
	if err != nil {
		return
	}

	if warning := evaluateVersionMismatch(configuredVersion, res.TypedSpec().Version); warning != nil {
		_, _ = fmt.Fprintln(w, "warning:", warning.Error())
		for _, hint := range errors.GetAllHints(warning) {
			_, _ = fmt.Fprintf(w, "hint: %s\n", hint)
		}
	}
}

// evaluateVersionMismatch returns a hint-bearing warning error if the
// configured contract is strictly newer than the running version. It returns
// nil when versions agree, when the configured contract isn't newer, or when
// either side cannot be parsed (best-effort: never block on parse failure).
func evaluateVersionMismatch(configuredVersion, runningVersion string) error {
	configuredContract, err := machineryconfig.ParseContractFromVersion(configuredVersion)
	if err != nil {
		return nil
	}

	runningContract, err := machineryconfig.ParseContractFromVersion(runningVersion)
	if err != nil {
		return nil
	}

	if !configuredContract.Greater(runningContract) {
		return nil
	}

	warning := fmt.Errorf(
		"pre-flight: configured talosVersion=%s is newer than the node's running Talos %s",
		configuredContract,
		runningVersion,
	)
	return errors.WithHint(warning, preflightVersionMismatchHint)
}
