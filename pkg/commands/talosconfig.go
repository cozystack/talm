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
	"fmt"
	"os"
	"path/filepath"

	"github.com/siderolabs/talos/cmd/talosctl/cmd/mgmt/gen"
	"github.com/siderolabs/talos/pkg/machinery/client/config"
	machineconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/generate"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// talosconfigCmd represents the `talosconfig` command.
var talosconfigCmd = &cobra.Command{
	Use:   "talosconfig",
	Short: "Regenerate talosconfig with new client certificates",
	Long: `Regenerate talosconfig from secrets.yaml with fresh client certificates.

This command:
1. Decrypts talosconfig if encrypted version exists
2. Regenerates client certificates from secrets.yaml
3. Preserves endpoints and nodes from existing config
4. Re-encrypts if encryption is used

Use this command when your client certificate has expired.`,
	Args:  cobra.NoArgs,
	PreRunE: func(cmd *cobra.Command, args []string) error {
		// Ensure project root is detected
		if !Config.RootDirExplicit {
			detectedRoot, err := detectRootFromCWD()
			if err == nil && detectedRoot != "" {
				Config.RootDir = detectedRoot
			}
		}
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		// First, decrypt talosconfig if encrypted version exists
		if _, err := handleTalosconfigEncryption(false); err != nil {
			// If decryption fails, continue - we may be able to regenerate
			fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
		}

		// Regenerate talosconfig from secrets.yaml
		if err := regenerateTalosconfig(); err != nil {
			return err
		}

		// Handle encryption (encrypt if needed)
		if _, err := handleTalosconfigEncryption(false); err != nil {
			return err
		}

		// Update .gitignore if needed
		if err := writeGitignoreFile(); err != nil {
			// Don't fail the command if gitignore update fails, but log warning
			fmt.Fprintf(os.Stderr, "Warning: failed to update .gitignore: %v\n", err)
		}

		return nil
	},
}

func init() {
	addCommand(talosconfigCmd)
}

// regenerateTalosconfig regenerates the talosconfig file from secrets.yaml,
// preserving endpoints and nodes from the existing config.
func regenerateTalosconfig() error {
	talosconfigFile := filepath.Join(Config.RootDir, "talosconfig")

	// Load existing talosconfig to preserve endpoints and nodes
	var oldConfig *config.Config
	var clusterName string

	if fileExists(talosconfigFile) {
		var err error
		oldConfig, err = config.Open(talosconfigFile)
		if err != nil {
			return fmt.Errorf("failed to read existing talosconfig: %w", err)
		}
		clusterName = oldConfig.Context
	}

	// Resolve secrets path
	secretsPath := ResolveSecretsPath(Config.TemplateOptions.WithSecrets)
	if !fileExists(secretsPath) {
		return fmt.Errorf("secrets.yaml not found at %s. Run 'talm init' or restore secrets.yaml", secretsPath)
	}

	// Load secrets bundle
	secretsBundle, err := secrets.LoadBundle(secretsPath)
	if err != nil {
		return fmt.Errorf("failed to load secrets bundle: %w", err)
	}

	// Build generate options
	var genOptions []generate.Option
	genOptions = append(genOptions, generate.WithSecretsBundle(secretsBundle))

	// Add version contract if configured
	if Config.TemplateOptions.TalosVersion != "" {
		versionContract, err := machineconfig.ParseContractFromVersion(Config.TemplateOptions.TalosVersion)
		if err != nil {
			return fmt.Errorf("invalid talos-version: %w", err)
		}
		genOptions = append(genOptions, generate.WithVersionContract(versionContract))
	}

	// If no cluster name from existing config, try to get it from Chart.yaml
	if clusterName == "" {
		clusterName = getClusterNameFromChart()
		if clusterName == "" {
			clusterName = "talos-default"
		}
	}

	fmt.Fprintf(os.Stderr, "Regenerating talosconfig from secrets.yaml...\n")

	// Generate new config bundle
	configBundle, err := gen.GenerateConfigBundle(
		genOptions,
		clusterName,
		"https://192.168.0.1:6443", // dummy endpoint, not used for talosconfig
		"",
		[]string{},
		[]string{},
		[]string{},
	)
	if err != nil {
		return fmt.Errorf("failed to generate config bundle: %w", err)
	}

	// Get the new talosconfig
	newConfig := configBundle.TalosConfig()

	// Preserve endpoints and nodes from old config
	if oldConfig != nil {
		// Copy all contexts from old config (preserves other contexts like "home")
		for ctxName, oldCtx := range oldConfig.Contexts {
			if newCtx, exists := newConfig.Contexts[ctxName]; exists {
				// For matching context, preserve endpoints and nodes
				newCtx.Endpoints = oldCtx.Endpoints
				newCtx.Nodes = oldCtx.Nodes
			} else {
				// For non-matching contexts, copy entire context as-is
				newConfig.Contexts[ctxName] = oldCtx
			}
		}
		// Preserve the current context setting
		newConfig.Context = oldConfig.Context

		fmt.Fprintf(os.Stderr, "Preserved endpoints and nodes from existing config\n")
	} else {
		// No old config, set default endpoint
		newConfig.Contexts[clusterName].Endpoints = []string{"127.0.0.1"}
	}

	// Marshal and write the new talosconfig
	data, err := yaml.Marshal(newConfig)
	if err != nil {
		return fmt.Errorf("failed to marshal talosconfig: %w", err)
	}

	if err := os.WriteFile(talosconfigFile, data, 0o600); err != nil {
		return fmt.Errorf("failed to write talosconfig: %w", err)
	}

	return nil
}

// getClusterNameFromChart reads the cluster name from Chart.yaml
func getClusterNameFromChart() string {
	chartYamlPath := filepath.Join(Config.RootDir, "Chart.yaml")
	data, err := os.ReadFile(chartYamlPath)
	if err != nil {
		return ""
	}

	var chartData struct {
		Name string `yaml:"name"`
	}

	if err := yaml.Unmarshal(data, &chartData); err != nil {
		return ""
	}

	return chartData.Name
}

