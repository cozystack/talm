// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package commands

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cozystack/talm/pkg/modeline"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"

	"github.com/siderolabs/talos/cmd/talosctl/pkg/talos/global"
	"github.com/siderolabs/talos/pkg/machinery/client"
)

var kubernetesFlag bool

// GlobalArgs is the common arguments for the root command.
var GlobalArgs global.Args

var Config struct {
	RootDir       string
	GlobalOptions struct {
		Talosconfig string `yaml:"talosconfig"`
	} `yaml:"globalOptions"`
	TemplateOptions struct {
		Offline           bool     `yaml:"offline"`
		ValueFiles        []string `yaml:"valueFiles"`
		Values            []string `yaml:"values"`
		StringValues      []string `yaml:"stringValues"`
		FileValues        []string `yaml:"fileValues"`
		JsonValues        []string `yaml:"jsonValues"`
		LiteralValues     []string `yaml:"literalValues"`
		TalosVersion      string   `yaml:"talosVersion"`
		WithSecrets       string   `yaml:"withSecrets"`
		KubernetesVersion string   `yaml:"kubernetesVersion"`
		Full              bool     `yaml:"full"`
		Debug             bool     `yaml:"debug"`
	} `yaml:"templateOptions"`
	ApplyOptions struct {
		DryRun           bool   `yaml:"preserve"`
		Timeout          string `yaml:"timeout"`
		TimeoutDuration  time.Duration
		CertFingerprints []string `yaml:"certFingerprints"`
	} `yaml:"applyOptions"`
	UpgradeOptions struct {
		Preserve bool `yaml:"preserve"`
		Stage    bool `yaml:"stage"`
		Force    bool `yaml:"force"`
	} `yaml:"upgradeOptions"`
	InitOptions struct {
		Version string
	}
}

const pathAutoCompleteLimit = 500

// WithClientNoNodes wraps common code to initialize Talos client and provide cancellable context.
//
// WithClientNoNodes doesn't set any node information on the request context.
func WithClientNoNodes(action func(context.Context, *client.Client) error, dialOptions ...grpc.DialOption) error {
	return GlobalArgs.WithClientNoNodes(action, dialOptions...)
}

// WithClient builds upon WithClientNoNodes to provide set of nodes on request context based on config & flags.
func WithClient(action func(context.Context, *client.Client) error, dialOptions ...grpc.DialOption) error {
	return WithClientNoNodes(
		func(ctx context.Context, cli *client.Client) error {
			if len(GlobalArgs.Nodes) < 1 {
				configContext := cli.GetConfigContext()
				if configContext == nil {
					return errors.New("failed to resolve config context")
				}

				GlobalArgs.Nodes = configContext.Nodes
			}

			ctx = client.WithNodes(ctx, GlobalArgs.Nodes...)

			return action(ctx, cli)
		},
		dialOptions...,
	)

}

// WithClientMaintenance wraps common code to initialize Talos client in maintenance (insecure mode).
func WithClientMaintenance(enforceFingerprints []string, action func(context.Context, *client.Client) error) error {
	return GlobalArgs.WithClientMaintenance(enforceFingerprints, action)
}

// Commands is a list of commands published by the package.
var Commands []*cobra.Command

func addCommand(cmd *cobra.Command) {
	Commands = append(Commands, cmd)
}

func processModelineAndUpdateGlobals(configFile string, nodesFromArgs bool, endpointsFromArgs bool, owerwrite bool) error {
	modelineConfig, err := modeline.ReadAndParseModeline(configFile)
	if err != nil {
		fmt.Printf("Warning: modeline parsing failed: %v\n", err)
		return err
	}

	// Update global settings if modeline was successfully parsed
	if modelineConfig != nil {
		if !nodesFromArgs && len(modelineConfig.Nodes) > 0 {
			if owerwrite {
				GlobalArgs.Nodes = modelineConfig.Nodes
			} else {
				GlobalArgs.Nodes = append(GlobalArgs.Nodes, modelineConfig.Nodes...)
			}
		}
		if !endpointsFromArgs && len(modelineConfig.Endpoints) > 0 {
			if owerwrite {
				GlobalArgs.Endpoints = modelineConfig.Endpoints
			} else {
				GlobalArgs.Endpoints = append(GlobalArgs.Endpoints, modelineConfig.Endpoints...)
			}
		}
	}

	if len(GlobalArgs.Nodes) < 1 {
		return errors.New("nodes are not set for the command: please use `--nodes` flag or configuration file to set the nodes to run the command against")
	}

	return nil
}
