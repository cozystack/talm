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
	"os"
	"path/filepath"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/cozystack/talm/pkg/modeline"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"

	"github.com/siderolabs/talos/cmd/talosctl/pkg/talos/global"
	"github.com/siderolabs/talos/pkg/machinery/client"
)

// GlobalArgs is the common arguments for the root command.
//
//nolint:gochecknoglobals // cobra CLI architecture: persistent flags bind to package-level state shared across all subcommands; refactoring out the global would require threading state through every command's RunE.
var GlobalArgs global.Args

// Config is the package-level configuration populated from Chart.yaml and
// CLI persistent flags. Mirrors GlobalArgs for project-root-relative path
// resolution across every subcommand.
//
//nolint:gochecknoglobals // cobra CLI architecture: persistent flags bind to package-level config; mirrors GlobalArgs and is read by every subcommand for project-root-relative path resolution.
var Config struct {
	RootDir         string
	RootDirExplicit bool // true if --root was explicitly set
	GlobalOptions   struct {
		Talosconfig string `yaml:"talosconfig"`
		Kubeconfig  string `yaml:"kubeconfig"`
	} `yaml:"globalOptions"`
	TemplateOptions struct {
		Offline           bool     `yaml:"offline"`
		ValueFiles        []string `yaml:"valueFiles"`
		Values            []string `yaml:"values"`
		StringValues      []string `yaml:"stringValues"`
		FileValues        []string `yaml:"fileValues"`
		JsonValues        []string `yaml:"jsonValues"` //nolint:revive // public field name kept for backwards compatibility with existing consumers in template.go and pkg/engine
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

// WithClientNoNodes wraps common code to initialize Talos client and provide cancellable context.
//
// WithClientNoNodes doesn't set any node information on the request context.
func WithClientNoNodes(action func(context.Context, *client.Client) error, dialOptions ...grpc.DialOption) error {
	//nolint:wrapcheck // thin pass-through to talos global.Args; error already carries Talos context
	return GlobalArgs.WithClientNoNodes(action, dialOptions...)
}

// WithClient builds upon WithClientNoNodes to provide set of nodes on request context based on config & flags.
func WithClient(action func(context.Context, *client.Client) error, dialOptions ...grpc.DialOption) error {
	return WithClientNoNodes(
		func(ctx context.Context, cli *client.Client) error {
			if len(GlobalArgs.Nodes) < 1 {
				configContext := cli.GetConfigContext()
				if configContext == nil {
					return errors.WithHint(
						errors.New("failed to resolve config context"),
						"verify ~/.talos/config or pass --talosconfig pointing at a valid file",
					)
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
	//nolint:wrapcheck // thin pass-through to talos global.Args; error already carries Talos context
	return GlobalArgs.WithClientMaintenance(enforceFingerprints, action)
}

// WithClientSkipVerify wraps common code to initialize Talos client with TLS verification disabled
// but with client certificate authentication preserved.
// This is useful when connecting to nodes via IP addresses not listed in the server certificate's SANs.
func WithClientSkipVerify(action func(context.Context, *client.Client) error) error {
	//nolint:wrapcheck // thin pass-through to talos global.Args; error already carries Talos context
	return GlobalArgs.WithClientSkipVerify(action)
}

// WithClientAuto automatically selects the appropriate client wrapper based on GlobalArgs.SkipVerify.
// If SkipVerify is true, uses WithClientSkipVerify, otherwise uses WithClientNoNodes.
func WithClientAuto(action func(context.Context, *client.Client) error) error {
	if GlobalArgs.SkipVerify {
		return WithClientSkipVerify(action)
	}

	return WithClientNoNodes(action)
}

// Commands is a list of commands published by the package.
//
//nolint:gochecknoglobals // command registry: each subcommand's init() registers itself via addCommand(); main.go iterates the slice to attach all commands to the root cobra command.
var Commands []*cobra.Command

func addCommand(cmd *cobra.Command) {
	Commands = append(Commands, cmd)
}

// DetectProjectRoot automatically detects the project root directory by looking
// for Chart.yaml and secrets.yaml (or secrets.encrypted.yaml) files in the current directory and parent directories.
// Returns the absolute path to the project root, or empty string if not found.
func DetectProjectRoot(startDir string) (string, error) {
	absStartDir, err := filepath.Abs(startDir)
	if err != nil {
		return "", errors.Wrap(err, "failed to get absolute path")
	}

	currentDir := absStartDir
	for {
		chartYaml := filepath.Join(currentDir, chartYamlName)
		secretsYaml := filepath.Join(currentDir, "secrets.yaml")
		secretsEncryptedYaml := filepath.Join(currentDir, "secrets.encrypted.yaml")

		chartExists := false
		secretsExists := false

		if _, err := os.Stat(chartYaml); err == nil {
			chartExists = true
		}

		if _, err := os.Stat(secretsYaml); err == nil {
			secretsExists = true
		}

		if _, err := os.Stat(secretsEncryptedYaml); err == nil {
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
		return "", errors.Wrap(err, "failed to get absolute path")
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
			return "", errors.Wrapf(err, "failed to detect root for file %s", filePath)
		}

		if fileRoot == "" {
			//nolint:wrapcheck // cockroachdb/errors.WithHint at boundary.
			return "", errors.WithHint(
				errors.Newf("failed to detect project root for file %s (Chart.yaml and secrets.yaml not found)", filePath),
				"run `talm init` at the project root, or move the file under an existing talm project",
			)
		}

		roots[fileRoot] = true
		if commonRoot == "" {
			commonRoot = fileRoot
		} else if commonRoot != fileRoot {
			//nolint:wrapcheck // cockroachdb/errors.WithHint at boundary.
			return "", errors.WithHint(
				errors.Newf("files belong to different project roots: %s and %s", commonRoot, fileRoot),
				"run the command separately for each project, or pass files from a single project root",
			)
		}
	}

	return commonRoot, nil
}

// DetectRootForTemplate detects the project root for a template file path.
// Similar to ValidateAndDetectRootsForFiles but for a single template file.
func DetectRootForTemplate(templatePath string) (string, error) {
	return DetectProjectRootForFile(templatePath)
}

func processModelineAndUpdateGlobals(configFile string, nodesFromArgs, endpointsFromArgs, overwrite bool) ([]string, error) {
	// FindAndParseModeline accepts operator-authored comment / blank
	// lines before the modeline (#178). Every workflow that consumes
	// node files — apply, upgrade, template -I, completion, wrapped
	// talosctl subcommands — must agree on file shape; the strict
	// "first line must be modeline" rule of the old ReadAndParseModeline
	// would silently break the apply / upgrade / talosctl path against
	// files that template -I just produced.
	_, modelineConfig, err := modeline.FindAndParseModeline(configFile)
	if err != nil {
		// Don't print the error here — cobra surfaces the wrapped
		// return through stderr at the command level. Printing here
		// AND returning the wrap caused the same message to appear
		// twice with a misleading "Warning:" prefix on the first copy.
		return nil, errors.Wrapf(err, "parsing modeline in %s", configFile)
	}

	templates := updateGlobalsFromModeline(modelineConfig, nodesFromArgs, endpointsFromArgs, overwrite)

	if len(GlobalArgs.Nodes) < 1 {
		//nolint:wrapcheck // cockroachdb/errors.WithHint at boundary.
		return nil, errors.WithHint(
			errors.New("nodes are not set for the command"),
			"use --nodes flag or configuration file to set the nodes to run the command against",
		)
	}

	return templates, nil
}

// updateGlobalsFromModeline merges modeline-supplied nodes/endpoints into
// GlobalArgs and returns the templates list. Split out of
// processModelineAndUpdateGlobals to flatten the surrounding nestif and
// keep modeline-merge logic isolated from validation.
func updateGlobalsFromModeline(modelineConfig *modeline.Config, nodesFromArgs, endpointsFromArgs, overwrite bool) []string {
	if modelineConfig == nil {
		return nil
	}

	if !nodesFromArgs && len(modelineConfig.Nodes) > 0 {
		if overwrite {
			GlobalArgs.Nodes = modelineConfig.Nodes
		} else {
			GlobalArgs.Nodes = append(GlobalArgs.Nodes, modelineConfig.Nodes...)
		}
	}

	if !endpointsFromArgs && len(modelineConfig.Endpoints) > 0 {
		if overwrite {
			GlobalArgs.Endpoints = modelineConfig.Endpoints
		} else {
			GlobalArgs.Endpoints = append(GlobalArgs.Endpoints, modelineConfig.Endpoints...)
		}
	}

	return modelineConfig.Templates
}
