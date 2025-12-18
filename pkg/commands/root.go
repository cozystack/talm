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
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	RootDirExplicit bool // true if --root was explicitly set
	GlobalOptions struct {
		Talosconfig string `yaml:"talosconfig"`
		Kubeconfig  string `yaml:"kubeconfig"`
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

// DetectProjectRoot automatically detects the project root directory by looking
// for Chart.yaml and secrets.yaml files in the current directory and parent directories.
// Returns the absolute path to the project root, or empty string if not found.
func DetectProjectRoot(startDir string) (string, error) {
	absStartDir, err := filepath.Abs(startDir)
	if err != nil {
		return "", fmt.Errorf("failed to get absolute path: %w", err)
	}

	currentDir := absStartDir
	for {
		chartYaml := filepath.Join(currentDir, "Chart.yaml")
		secretsYaml := filepath.Join(currentDir, "secrets.yaml")

		chartExists := false
		secretsExists := false

		if _, err := os.Stat(chartYaml); err == nil {
			chartExists = true
		}
		if _, err := os.Stat(secretsYaml); err == nil {
			secretsExists = true
		}

		if chartExists && secretsExists {
			return currentDir, nil
		}

		parentDir := filepath.Dir(currentDir)
		if parentDir == currentDir {
			// Reached filesystem root
			break
		}
		currentDir = parentDir
	}

	return "", nil
}

// DetectProjectRootForFile detects the project root for a given file path.
// It finds the directory containing the file, then searches up for Chart.yaml and secrets.yaml.
func DetectProjectRootForFile(filePath string) (string, error) {
	absFilePath, err := filepath.Abs(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to get absolute path: %w", err)
	}

	// Get directory containing the file
	fileDir := filepath.Dir(absFilePath)
	return DetectProjectRoot(fileDir)
}

// ValidateAndDetectRootsForFiles validates that all files belong to the same project root.
// Returns the common root directory and an error if files belong to different roots.
func ValidateAndDetectRootsForFiles(filePaths []string) (string, error) {
	if len(filePaths) == 0 {
		return "", nil
	}

	var commonRoot string
	roots := make(map[string]bool)

	for _, filePath := range filePaths {
		fileRoot, err := DetectProjectRootForFile(filePath)
		if err != nil {
			return "", fmt.Errorf("failed to detect root for file %s: %w", filePath, err)
		}
		if fileRoot == "" {
			return "", fmt.Errorf("failed to detect project root for file %s (Chart.yaml and secrets.yaml not found)", filePath)
		}

		roots[fileRoot] = true
		if commonRoot == "" {
			commonRoot = fileRoot
		} else if commonRoot != fileRoot {
			return "", fmt.Errorf("files belong to different project roots: %s and %s", commonRoot, fileRoot)
		}
	}

	return commonRoot, nil
}

// DetectRootForTemplate detects the project root for a template file path.
// Similar to ValidateAndDetectRootsForFiles but for a single template file.
func DetectRootForTemplate(templatePath string) (string, error) {
	return DetectProjectRootForFile(templatePath)
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
