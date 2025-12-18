// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package commands

import (
	"context"
	"fmt"
	"os"

	"github.com/cozystack/talm/pkg/engine"
	"github.com/siderolabs/talos/pkg/machinery/config/configloader"
	"github.com/spf13/cobra"
)

// wrapUpgradeCommand adds special handling for upgrade command: extract image from config and set --image flag
func wrapUpgradeCommand(wrappedCmd *cobra.Command, originalRunE func(*cobra.Command, []string) error) {
	wrappedCmd.RunE = func(cmd *cobra.Command, args []string) error {
		// Get config files from --file flag
		var filesToProcess []string
		if fileFlag := cmd.Flags().Lookup("file"); fileFlag != nil {
			if fileFlagValue, err := cmd.Flags().GetStringSlice("file"); err == nil {
				filesToProcess = fileFlagValue
			}
		}

		// If config files are provided and --image flag is not set, extract image from config
		if len(filesToProcess) > 0 && !cmd.Flags().Changed("image") {
			// Process first config file to extract image
			configFile := filesToProcess[0]

			// Process modeline to update GlobalArgs
			nodesFromArgs := len(GlobalArgs.Nodes) > 0
			endpointsFromArgs := len(GlobalArgs.Endpoints) > 0
			if err := processModelineAndUpdateGlobals(configFile, nodesFromArgs, endpointsFromArgs, true); err != nil {
				return fmt.Errorf("failed to process modeline: %w", err)
			}

			// Get talos-version, with-secrets, kubernetes-version from flags or config
			talosVersion := Config.TemplateOptions.TalosVersion
			if cmd.Flags().Changed("talos-version") {
				if val, err := cmd.Flags().GetString("talos-version"); err == nil {
					talosVersion = val
				}
			}

			withSecrets := Config.TemplateOptions.WithSecrets
			if cmd.Flags().Changed("with-secrets") {
				if val, err := cmd.Flags().GetString("with-secrets"); err == nil {
					withSecrets = val
				}
			}

			kubernetesVersion := Config.TemplateOptions.KubernetesVersion
			if cmd.Flags().Changed("kubernetes-version") {
				if val, err := cmd.Flags().GetString("kubernetes-version"); err == nil {
					kubernetesVersion = val
				}
			}

			// Process config to extract image
			ctx := context.Background()
			eopts := engine.Options{
				TalosVersion:      talosVersion,
				WithSecrets:       withSecrets,
				KubernetesVersion: kubernetesVersion,
			}

			patches := []string{"@" + configFile}
			configBundle, machineType, err := engine.FullConfigProcess(ctx, eopts, patches)
			if err != nil {
				return fmt.Errorf("full config processing error: %s", err)
			}

			result, err := engine.SerializeConfiguration(configBundle, machineType)
			if err != nil {
				return fmt.Errorf("error serializing configuration: %s", err)
			}

			config, err := configloader.NewFromBytes(result)
			if err != nil {
				return fmt.Errorf("error loading config: %w", err)
			}

			image := config.Machine().Install().Image()
			if image == "" {
				return fmt.Errorf("error getting image from config")
			}

			// Set --image flag with extracted image
			if err := cmd.Flags().Set("image", image); err != nil {
				// Flag might not exist, ignore error
				fmt.Fprintf(os.Stderr, "Warning: failed to set --image flag: %v\n", err)
			} else {
				fmt.Fprintf(os.Stderr, "Using image from config: %s\n", image)
			}
		}

		// Execute original command
		if originalRunE != nil {
			return originalRunE(cmd, args)
		} else if wrappedCmd.Run != nil {
			wrappedCmd.Run(cmd, args)
			return nil
		}
		return nil
	}
}

