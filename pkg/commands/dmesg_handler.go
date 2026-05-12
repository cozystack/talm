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

	"github.com/cockroachdb/errors"
	"github.com/spf13/cobra"
)

// dmesgCmdName labels the wrapped dmesg subcommand in the dispatch.
// Hoisted so a future rename touches one site.
const dmesgCmdName = "dmesg"

// wrapDmesgCommand installs a FlagErrorFunc that rewrites the
// cryptic ParseBool error from a numeric --tail value into an
// operator-friendly hint.
//
// Upstream talosctl registers --tail as a BoolVarP toggling
// tail-mode for --follow (`Dmesg(ctx, follow, tail bool)` on the
// wire). Operators' first instinct is `tail(1)`-style line count;
// `talm dmesg --tail=3` then surfaces
// `strconv.ParseBool: parsing "3": invalid syntax` with no hint
// at the actual contract.
//
// The original ParseBool error is wrapped (not replaced) so it
// stays reachable via errors.Unwrap and verbose fmt.Sprintf("%+v",
// err) rendering — debugging the underlying pflag failure remains
// possible while the operator-facing top line describes what --tail
// actually does and what to do instead.
func wrapDmesgCommand(wrappedCmd *cobra.Command) {
	wrappedCmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		// Substring detection on pflag's error format. Two tokens
		// — `--tail` plus `ParseBool` — together are specific
		// enough to avoid false positives (pflag only emits
		// ParseBool errors for bool flags). Matching on `--tail`
		// without surrounding quotes is durable to pflag's
		// rendering difference between shorthand-present and
		// shorthand-absent flags: today's `BoolVarP(..., "tail",
		// "", ...)` emits `"--tail"`, but a future upstream that
		// adds a shorthand would emit `-T, --tail` and would
		// otherwise silently bypass the cushion. pflag does not
		// export a typed error for invalid-argument failures, so
		// substring is the only available detection path.
		msg := err.Error()
		if strings.Contains(msg, "--tail") && strings.Contains(msg, "ParseBool") {
			return errors.WithHint(
				errors.Wrap(err, "talm dmesg --tail is a boolean toggle (tail-mode for --follow), not a line count"),
				"for the last N lines, pipe to tail(1): `talm dmesg --nodes <node> | tail -n N`; to stream only new messages, use `--follow --tail`",
			)
		}

		return err
	})
}
