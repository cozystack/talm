// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package commands

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/cozystack/talm/pkg/engine"
	"github.com/siderolabs/gen/maps"
	"github.com/siderolabs/gen/xslices"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/peer"

	"github.com/siderolabs/talos/cmd/talosctl/cmd/common"
	"github.com/siderolabs/talos/cmd/talosctl/pkg/talos/action"
	"github.com/siderolabs/talos/cmd/talosctl/pkg/talos/helpers"
	"github.com/siderolabs/talos/pkg/cli"
	"github.com/siderolabs/talos/pkg/machinery/api/machine"
	"github.com/siderolabs/talos/pkg/machinery/client"
	"github.com/siderolabs/talos/pkg/machinery/config/configloader"
	"github.com/siderolabs/talos/pkg/machinery/constants"
)

var upgradeCmdFlags struct {
	trackableActionCmdFlags
	rebootMode        string
	preserve          bool
	stage             bool
	force             bool
	insecure          bool
	configFiles       []string // -f/--files
	talosVersion      string
	withSecrets       string
	kubernetesVersion string
}

var upgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "Upgrade Talos on the target node",
	Long:  ``,
	Args:  cobra.NoArgs,
	PreRunE: func(cmd *cobra.Command, args []string) error {
		if !cmd.Flags().Changed("talos-version") {
			upgradeCmdFlags.talosVersion = Config.TemplateOptions.TalosVersion
		}
		if !cmd.Flags().Changed("with-secrets") {
			upgradeCmdFlags.withSecrets = Config.TemplateOptions.WithSecrets
		}
		if !cmd.Flags().Changed("kubernetes-version") {
			upgradeCmdFlags.kubernetesVersion = Config.TemplateOptions.KubernetesVersion
		}
		if !cmd.Flags().Changed("preserve") {
			upgradeCmdFlags.preserve = Config.UpgradeOptions.Preserve
		}
		if !cmd.Flags().Changed("stage") {
			upgradeCmdFlags.stage = Config.UpgradeOptions.Stage
		}
		if !cmd.Flags().Changed("force") {
			upgradeCmdFlags.force = Config.UpgradeOptions.Force
		}
		return nil
	},

	RunE: func(cmd *cobra.Command, args []string) error {
		if upgradeCmdFlags.debug {
			upgradeCmdFlags.wait = true
		}

		if upgradeCmdFlags.wait && upgradeCmdFlags.insecure {
			return fmt.Errorf("cannot use --wait and --insecure together")
		}
		if upgradeCmdFlags.insecure {
			return WithClientMaintenance(nil, upgrade(args))
		}

		return WithClient(upgrade(args))
	},
}

func upgrade(args []string) func(ctx context.Context, c *client.Client) error {
	return func(ctx context.Context, c *client.Client) error {
		nodesFromArgs := len(GlobalArgs.Nodes) > 0
		endpointsFromArgs := len(GlobalArgs.Endpoints) > 0
		rebootModeStr := strings.ToUpper(upgradeCmdFlags.rebootMode)

		rebootMode, rebootModeOk := machine.UpgradeRequest_RebootMode_value[rebootModeStr]
		if !rebootModeOk {
			return fmt.Errorf("invalid reboot mode: %s", upgradeCmdFlags.rebootMode)
		}

		for _, configFile := range upgradeCmdFlags.configFiles {
			if err := processModelineAndUpdateGlobals(configFile, nodesFromArgs, endpointsFromArgs, true); err != nil {
				return err
			}

			eopts := engine.Options{
				TalosVersion:      upgradeCmdFlags.talosVersion,
				WithSecrets:       upgradeCmdFlags.withSecrets,
				KubernetesVersion: upgradeCmdFlags.kubernetesVersion,
			}

			patches := []string{"@" + configFile}
			configBundle, err := engine.FullConfigProcess(ctx, eopts, patches)
			if err != nil {
				return fmt.Errorf("full config processing error: %s", err)
			}

			machineType := configBundle.ControlPlaneCfg.Machine().Type()
			result, err := engine.SerializeConfiguration(configBundle, machineType)
			if err != nil {
				return fmt.Errorf("error serializing configuration: %s", err)
			}

			config, err := configloader.NewFromBytes(result)
			if err != nil {
				return err
			}

			image := config.Machine().Install().Image()
			if image == "" {
				return fmt.Errorf("error getting image from config")
			}

			opts := []client.UpgradeOption{
				client.WithUpgradeImage(image),
				client.WithUpgradeRebootMode(machine.UpgradeRequest_RebootMode(rebootMode)),
				client.WithUpgradePreserve(upgradeCmdFlags.preserve),
				client.WithUpgradeStage(upgradeCmdFlags.stage),
				client.WithUpgradeForce(upgradeCmdFlags.force),
			}

			if !upgradeCmdFlags.wait {
				return runUpgradeNoWait(opts)
			}

			common.SuppressErrors = true

			fmt.Printf("- talm: file=%s, nodes=%s, endpoints=%s, image=%s\n", configFile, GlobalArgs.Nodes, GlobalArgs.Endpoints, image)

			err = action.NewTracker(
				&GlobalArgs,
				action.MachineReadyEventFn,
				func(ctx context.Context, c *client.Client) (string, error) {
					return upgradeGetActorID(ctx, c, opts)
				},
				action.WithPostCheck(action.BootIDChangedPostCheckFn),
				action.WithDebug(upgradeCmdFlags.debug),
				action.WithTimeout(upgradeCmdFlags.timeout),
			).Run()
			if err != nil {
				return err
			}
		}
		return nil
	}
	return nil
}

func runUpgradeNoWait(opts []client.UpgradeOption) error {
	upgradeFn := func(ctx context.Context, c *client.Client) error {
		if err := helpers.ClientVersionCheck(ctx, c); err != nil {
			return err
		}

		var remotePeer peer.Peer

		opts = append(opts, client.WithUpgradeGRPCCallOptions(grpc.Peer(&remotePeer)))

		// TODO: See if we can validate version and prevent starting upgrades to an unknown version
		resp, err := c.UpgradeWithOptions(ctx, opts...)
		if err != nil {
			if resp == nil {
				return fmt.Errorf("error performing upgrade: %s", err)
			}

			cli.Warning("%s", err)
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "NODE\tACK\tSTARTED")

		defaultNode := client.AddrFromPeer(&remotePeer)

		for _, msg := range resp.Messages {
			node := defaultNode

			if msg.Metadata != nil {
				node = msg.Metadata.Hostname
			}

			fmt.Fprintf(w, "%s\t%s\t%s\t\n", node, msg.Ack, time.Now())
		}

		return w.Flush()
	}

	if upgradeCmdFlags.insecure {
		return WithClientMaintenance(nil, upgradeFn)
	}

	return WithClient(upgradeFn)
}

func upgradeGetActorID(ctx context.Context, c *client.Client, opts []client.UpgradeOption) (string, error) {
	resp, err := c.UpgradeWithOptions(ctx, opts...)
	if err != nil {
		return "", err
	}

	if len(resp.GetMessages()) == 0 {
		return "", fmt.Errorf("no messages returned from action run")
	}

	return resp.GetMessages()[0].GetActorId(), nil
}

func init() {
	rebootModes := maps.Keys(machine.UpgradeRequest_RebootMode_value)
	sort.Slice(rebootModes, func(i, j int) bool {
		return machine.UpgradeRequest_RebootMode_value[rebootModes[i]] < machine.UpgradeRequest_RebootMode_value[rebootModes[j]]
	})

	rebootModes = xslices.Map(rebootModes, strings.ToLower)

	upgradeCmd.Flags().StringVarP(&upgradeCmdFlags.rebootMode, "reboot-mode", "m", strings.ToLower(machine.UpgradeRequest_DEFAULT.String()),
		fmt.Sprintf("select the reboot mode during upgrade. Mode %q bypasses kexec. Valid values are: %q.",
			strings.ToLower(machine.UpgradeRequest_POWERCYCLE.String()),
			rebootModes))
	upgradeCmd.Flags().BoolVarP(&upgradeCmdFlags.preserve, "preserve", "p", false, "preserve data")
	upgradeCmd.Flags().BoolVarP(&upgradeCmdFlags.stage, "stage", "", false, "stage the upgrade to perform it after a reboot")
	upgradeCmd.Flags().BoolVarP(&upgradeCmdFlags.force, "force", "", false, "force the upgrade (skip checks on etcd health and members, might lead to data loss)")
	upgradeCmdFlags.addTrackActionFlags(upgradeCmd)

	upgradeCmd.Flags().BoolVarP(&upgradeCmdFlags.insecure, "insecure", "i", false, "apply using the insecure (encrypted with no auth) maintenance service")
	upgradeCmd.Flags().StringSliceVarP(&upgradeCmdFlags.configFiles, "file", "f", nil, "specify config files or patches in a YAML file (can specify multiple)")
	upgradeCmd.Flags().StringVar(&upgradeCmdFlags.talosVersion, "talos-version", "", "the desired Talos version to generate config for (backwards compatibility, e.g. v0.8)")
	upgradeCmd.Flags().StringVar(&upgradeCmdFlags.withSecrets, "with-secrets", "", "use a secrets file generated using 'gen secrets'")
	upgradeCmd.Flags().StringVar(&upgradeCmdFlags.kubernetesVersion, "kubernetes-version", constants.DefaultKubernetesVersion, "desired kubernetes version to run")

	addCommand(upgradeCmd)
}
