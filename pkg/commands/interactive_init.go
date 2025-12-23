// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package commands

import (
	"time"

	"github.com/cozystack/talm/internal/pkg/ui/initwizard"
	"github.com/spf13/cobra"
)

var interactiveCmdFlags struct {
	interval    time.Duration
	configFiles []string
	insecure    bool
}

// interactiveCmd starts terminal TUI for interactive configuration and apply.
var interactiveCmd = &cobra.Command{
	Use:  "interactive",
	Long: `Start a terminal-based UI (TUI) similar to talos-bootstrap.`,
	Args: cobra.NoArgs,
	PreRunE: func(cmd *cobra.Command, args []string) error {
		nodesFromArgs := len(GlobalArgs.Nodes) > 0
		endpointsFromArgs := len(GlobalArgs.Endpoints) > 0
		for _, configFile := range interactiveCmdFlags.configFiles {
			if err := processModelineAndUpdateGlobals(configFile, nodesFromArgs, endpointsFromArgs, false); err != nil {
				return err
			}
		}
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		return initwizard.RunInitWizard()
	},
}

func init() {
	interactiveCmd.Flags().StringSliceVarP(&interactiveCmdFlags.configFiles,
		"file", "f", nil, "specify config files with talm modeline (can specify multiple)")
	interactiveCmd.Flags().DurationVarP(&interactiveCmdFlags.interval, "update-interval", "d", 3*time.Second, "interval between updates")
	interactiveCmd.Flags().BoolVarP(&interactiveCmdFlags.insecure, "insecure", "i", false, "use Talos insecure maintenance mode (no talosconfig required)")
	addCommand(interactiveCmd)
}
