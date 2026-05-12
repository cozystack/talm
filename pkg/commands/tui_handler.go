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
	"os"

	"github.com/cockroachdb/errors"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// Names of the TUI-style subcommands the wrapper refuses on
// non-tty stdin. Hoisted so the dispatch + tests share one
// canonical list.
const (
	dashboardCmdName = "dashboard"
	editCmdName      = "edit"
)

// stdinIsTTY reports whether stdin is currently attached to a
// terminal. Defined as a package-level var so tests can stub it
// (the term package's IsTerminal touches the real fd and can't be
// faked otherwise).
//
//nolint:gochecknoglobals // function-type indirection for test injection; the same pattern is used by linksDisksReader / machineConfigReader / versionReader elsewhere in the package.
var stdinIsTTY = func() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

// wrapTUICommand installs a PreRunE that refuses the wrapped
// interactive-only command when stdin is not attached to a
// terminal. Two upstream commands take this path with different
// failure mechanisms:
//
//   - `dashboard` uses gdamore/tcell directly. Without a tty it
//     panics inside `tScreen.finish` with `close of nil channel`
//     on teardown — operators running under CI / piped stdin /
//     `< /dev/null` see a Go stack trace.
//   - `edit` shells out to the kubectl external-editor helper
//     (`k8s.io/kubectl/pkg/cmd/util/editor`). Without a tty it
//     hangs allocating an editor session — operators see no
//     output and have to ^C.
//
// Both manifest as no-actionable-signal failures the operator
// can't diagnose; the refusal here surfaces a clear hint instead.
//
// The cmdLabel parameter ("dashboard" / "edit") customises the
// refusal copy so the operator's correlation between the command
// they ran and the error they got is immediate.
//
// The PreRunE chains the original PreRunE first (modeline,
// GlobalArgs sync). If stdin is a terminal, behaviour is unchanged
// from upstream.
func wrapTUICommand(wrappedCmd *cobra.Command, cmdLabel string) {
	originalPreRunE := wrappedCmd.PreRunE

	wrappedCmd.PreRunE = func(cmd *cobra.Command, args []string) error {
		// TTY check runs BEFORE chaining the original PreRunE.
		// Inverting the obvious chain order is deliberate:
		// originalPreRunE is wrapTalosCommand's closure, which
		// reads modeline files and mutates package-level
		// GlobalArgs. For a non-tty invocation we know the
		// command will be refused — there's no point parsing
		// modeline files or mutating shared state. Refuse fast,
		// touch nothing.
		if !stdinIsTTY() {
			return errors.WithHint(
				errors.Newf("talm %s requires an interactive terminal; stdin is not a tty", cmdLabel),
				"run from a regular terminal (not a CI job, piped stdin, or `< /dev/null`); for non-interactive resource inspection use `talm get` instead",
			)
		}

		if originalPreRunE != nil {
			return originalPreRunE(cmd, args)
		}

		return nil
	}
}
