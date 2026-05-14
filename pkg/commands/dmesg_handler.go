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
	"github.com/cockroachdb/errors"
	"github.com/spf13/cobra"
)

// dmesgCmdName labels the talm stub that replaces the wrapped
// upstream dmesg command. Hoisted so a future rename touches one
// site.
const dmesgCmdName = "dmesg"

// dmesgCmd is a hidden migration stub for the retired `talm dmesg`
// surface.
//
// Upstream Talos is retiring the `dmesg` command in favor of
// `talm logs kernel`, which supports `--tail=N` as a line count
// directly (the semantics operators reach for; the original
// `dmesg --tail` was a boolean toggle for tail-mode under
// --follow, a frequent source of `strconv.ParseBool` confusion).
// The upstream maintainer's response on siderolabs/talos#13333
// pointed at `logs kernel` as the replacement.
//
// Removing the command proactively (rather than waiting for
// upstream's removal) means operators with scripts that call
// `talm dmesg ...` get an immediate, actionable migration hint
// instead of a silently-deprecated command that may disappear
// on the next Talos bump. The stub is hidden from `talm --help`
// so the command does not surface to new operators.
// DisableFlagParsing lets operator-supplied flags pass through
// to RunE without pflag erroring first — the migration hint
// runs regardless of what arguments accompanied the call.
//
// Side effect of DisableFlagParsing: cobra does NOT intercept
// `--help` either — `talm dmesg --help` reaches RunE and
// surfaces the same migration hint instead of cobra's help
// renderer. Intentional: every invocation of the retired
// command should reach the same nudge. A future maintainer
// who flips DisableFlagParsing to false would silently
// re-introduce the original pflag failure shape on `--tail=N`
// for any operator who didn't migrate yet.
//
//nolint:gochecknoglobals // cobra command registration requires a package-level value
var dmesgCmd = &cobra.Command{
	Use:                dmesgCmdName,
	Short:              "[removed] use `talm logs kernel` instead",
	Hidden:             true,
	DisableFlagParsing: true,
	RunE: func(_ *cobra.Command, _ []string) error {
		return errors.WithHint(
			errors.New("talm dmesg has been removed"),
			"use `talm logs kernel --tail=N --nodes <node>` for the last N kernel-log lines, or `talm logs kernel --follow --nodes <node>` to stream new messages. Upstream Talos is retiring the `dmesg` command (see siderolabs/talos#13333) and talm drops it ahead of time to avoid a silent break on the next Talos bump.",
		)
	},
}

func init() {
	addCommand(dmesgCmd)
}
