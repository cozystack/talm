// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package commands

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/cozystack/talm/pkg/engine"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/siderolabs/talos/cmd/talosctl/pkg/talos/helpers"
	machineapi "github.com/siderolabs/talos/pkg/machinery/api/machine"
	"github.com/siderolabs/talos/pkg/machinery/client"
	"github.com/siderolabs/talos/pkg/machinery/constants"
)

var applyCmdFlags struct {
	helpers.Mode
	certFingerprints  []string
	insecure          bool
	configFiles       []string // -f/--files
	talosVersion      string
	withSecrets       string
	debug             bool
	kubernetesVersion string
	dryRun            bool
	preserve          bool
	stage             bool
	force             bool
	configTryTimeout  time.Duration
	nodesFromArgs     bool
	endpointsFromArgs bool
}

var applyCmd = &cobra.Command{
	Use:   "apply",
	Short: "Apply config to a Talos node",
	Long:  ``,
	Args:  cobra.NoArgs,
	PreRunE: func(cmd *cobra.Command, args []string) error {
		if !cmd.Flags().Changed("talos-version") {
			applyCmdFlags.talosVersion = Config.TemplateOptions.TalosVersion
		}
		if !cmd.Flags().Changed("with-secrets") {
			applyCmdFlags.withSecrets = Config.TemplateOptions.WithSecrets
		}
		if !cmd.Flags().Changed("kubernetes-version") {
			applyCmdFlags.kubernetesVersion = Config.TemplateOptions.KubernetesVersion
		}
		if !cmd.Flags().Changed("debug") {
			applyCmdFlags.debug = Config.TemplateOptions.Debug
		}
		if !cmd.Flags().Changed("preserve") {
			applyCmdFlags.preserve = Config.UpgradeOptions.Preserve
		}
		if !cmd.Flags().Changed("stage") {
			applyCmdFlags.stage = Config.UpgradeOptions.Stage
		}
		if !cmd.Flags().Changed("force") {
			applyCmdFlags.force = Config.UpgradeOptions.Force
		}
		applyCmdFlags.nodesFromArgs = len(GlobalArgs.Nodes) > 0
		applyCmdFlags.endpointsFromArgs = len(GlobalArgs.Endpoints) > 0
		// Set dummy endpoint to avoid errors on building clinet
		if len(GlobalArgs.Endpoints) == 0 {
			GlobalArgs.Endpoints = append(GlobalArgs.Endpoints, "127.0.0.1")
		}

		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		return WithClientNoNodes(apply(args))
	},
}

func apply(args []string) func(ctx context.Context, c *client.Client) error {
	return func(ctx context.Context, c *client.Client) error {
		for _, configFile := range applyCmdFlags.configFiles {
			if err := processModelineAndUpdateGlobals(configFile, applyCmdFlags.nodesFromArgs, applyCmdFlags.endpointsFromArgs, true); err != nil {
				return err
			}

			opts := engine.Options{
				TalosVersion:      applyCmdFlags.talosVersion,
				WithSecrets:       applyCmdFlags.withSecrets,
				KubernetesVersion: applyCmdFlags.kubernetesVersion,
				Debug:             applyCmdFlags.debug,
			}

			patches := []string{"@" + configFile}
			configBundle, machineType, err := engine.FullConfigProcess(ctx, opts, patches)
			if err != nil {
				return fmt.Errorf("full config processing error: %s", err)
			}

			result, err := engine.SerializeConfiguration(configBundle, machineType)
			if err != nil {
				return fmt.Errorf("error serializing configuration: %s", err)
			}

			withClient := func(f func(ctx context.Context, c *client.Client) error) error {
				if applyCmdFlags.insecure {
					return WithClientMaintenance(applyCmdFlags.certFingerprints, f)
				}

				return WithClientNoNodes(func(ctx context.Context, cli *client.Client) error {
					if len(GlobalArgs.Nodes) < 1 {
						configContext := cli.GetConfigContext()
						if configContext == nil {
							return errors.New("failed to resolve config context")
						}

						GlobalArgs.Nodes = configContext.Nodes
					}

					ctx = client.WithNodes(ctx, GlobalArgs.Nodes...)

					return f(ctx, cli)
				})
			}

			err = withClient(func(ctx context.Context, c *client.Client) error {
				fmt.Printf("- talm: file=%s, nodes=%s, endpoints=%s\n", configFile, GlobalArgs.Nodes, GlobalArgs.Endpoints)

				resp, err := c.ApplyConfiguration(ctx, &machineapi.ApplyConfigurationRequest{
					Data:           result,
					Mode:           applyCmdFlags.Mode.Mode,
					DryRun:         applyCmdFlags.dryRun,
					TryModeTimeout: durationpb.New(applyCmdFlags.configTryTimeout),
				})
				if err != nil {
					return fmt.Errorf("error applying new configuration: %s", err)
				}

				helpers.PrintApplyResults(resp)

				return nil
			})
			if err != nil {
				return err
			}

			// Reset args
			if !applyCmdFlags.nodesFromArgs {
				GlobalArgs.Nodes = []string{}
			}
			if !applyCmdFlags.endpointsFromArgs {
				GlobalArgs.Endpoints = []string{}
			}
		}
		return nil
	}
}

// readFirstLine reads and returns the first line of the file specified by the filename.
// It returns an error if opening or reading the file fails.
func readFirstLine(filename string) (string, error) {
	// Open the file
	file, err := os.Open(filename)
	if err != nil {
		return "", fmt.Errorf("error opening file: %v", err)
	}
	defer file.Close() // Ensure the file is closed after reading

	// Create a scanner to read the file
	scanner := bufio.NewScanner(file)

	// Read the first line
	if scanner.Scan() {
		return scanner.Text(), nil
	}

	// Check for errors during scanning
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("error reading file: %v", err)
	}

	// If no lines in the file, return an empty string
	return "", nil
}

func init() {
	applyCmd.Flags().BoolVarP(&applyCmdFlags.insecure, "insecure", "i", false, "apply using the insecure (encrypted with no auth) maintenance service")
	applyCmd.Flags().StringSliceVarP(&applyCmdFlags.configFiles, "file", "f", nil, "specify config files or patches in a YAML file (can specify multiple)")
	applyCmd.Flags().StringVar(&applyCmdFlags.talosVersion, "talos-version", "", "the desired Talos version to generate config for (backwards compatibility, e.g. v0.8)")
	applyCmd.Flags().StringVar(&applyCmdFlags.withSecrets, "with-secrets", "", "use a secrets file generated using 'gen secrets'")
	applyCmd.Flags().StringVar(&applyCmdFlags.kubernetesVersion, "kubernetes-version", constants.DefaultKubernetesVersion, "desired kubernetes version to run")
	applyCmd.Flags().BoolVarP(&applyCmdFlags.debug, "debug", "", false, "show only rendered patches")
	applyCmd.Flags().BoolVar(&applyCmdFlags.dryRun, "dry-run", false, "check how the config change will be applied in dry-run mode")
	applyCmd.Flags().DurationVar(&applyCmdFlags.configTryTimeout, "timeout", constants.ConfigTryTimeout, "the config will be rolled back after specified timeout (if try mode is selected)")
	applyCmd.Flags().StringSliceVar(&applyCmdFlags.certFingerprints, "cert-fingerprint", nil, "list of server certificate fingeprints to accept (defaults to no check)")
	applyCmd.Flags().BoolVar(&applyCmdFlags.force, "force", false, "will overwrite existing files")
	helpers.AddModeFlags(&applyCmdFlags.Mode, applyCmd)

	addCommand(applyCmd)
}
