// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cozystack/talm/pkg/generated"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/siderolabs/talos/cmd/talosctl/cmd/mgmt/gen"
	"github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/generate"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
)

var initCmdFlags struct {
	force        bool
	preset       string
	talosVersion string
	update       bool
}

// initCmd represents the `init` command.
var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize a new project and generate default values",
	Long:  ``,
	Args:  cobra.NoArgs,
	PreRunE: func(cmd *cobra.Command, args []string) error {
		if !cmd.Flags().Changed("talos-version") {
			initCmdFlags.talosVersion = Config.TemplateOptions.TalosVersion
		}
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		var (
			secretsBundle   *secrets.Bundle
			versionContract *config.VersionContract
			err             error
		)

		if initCmdFlags.update {
			return updateTalmLibraryChart()
		}
		if initCmdFlags.talosVersion != "" {
			versionContract, err = config.ParseContractFromVersion(initCmdFlags.talosVersion)
			if err != nil {
				return fmt.Errorf("invalid talos-version: %w", err)
			}
		}

		secretsBundle, err = secrets.NewBundle(secrets.NewFixedClock(time.Now()),
			versionContract,
		)
		if err != nil {
			return fmt.Errorf("failed to create secrets bundle: %w", err)
		}
		var genOptions []generate.Option //nolint:prealloc
		availablePresets, err := generated.AvailablePresets()
		if err != nil {
			return fmt.Errorf("failed to get available presets: %w", err)
		}
		if !isValidPreset(initCmdFlags.preset, availablePresets) {
			return fmt.Errorf("invalid preset: %s. Valid presets are: %v", initCmdFlags.preset, availablePresets)
		}
		if initCmdFlags.talosVersion != "" {
			var versionContract *config.VersionContract

			versionContract, err = config.ParseContractFromVersion(initCmdFlags.talosVersion)
			if err != nil {
				return fmt.Errorf("invalid talos-version: %w", err)
			}

			genOptions = append(genOptions, generate.WithVersionContract(versionContract))
		}
		genOptions = append(genOptions, generate.WithSecretsBundle(secretsBundle))

		// Write secrets.yaml only if it doesn't exist
		secretsFile := filepath.Join(Config.RootDir, "secrets.yaml")
		if _, err := os.Stat(secretsFile); os.IsNotExist(err) {
			if err = writeSecretsBundleToFile(secretsBundle); err != nil {
				return err
			}
		}

		// Clalculate cluster name from directory
		absolutePath, err := filepath.Abs(Config.RootDir)
		if err != nil {
			return err
		}
		clusterName := filepath.Base(absolutePath)

		// Generate talosconfig only if it doesn't exist
		talosconfigFile := filepath.Join(Config.RootDir, "talosconfig")
		if _, err := os.Stat(talosconfigFile); os.IsNotExist(err) {
			configBundle, err := gen.GenerateConfigBundle(genOptions, clusterName, "https://192.168.0.1:6443", "", []string{}, []string{}, []string{})
			if err != nil {
				return err
			}
			configBundle.TalosConfig().Contexts[clusterName].Endpoints = []string{"127.0.0.1"}

			data, err := yaml.Marshal(configBundle.TalosConfig())
			if err != nil {
				return fmt.Errorf("failed to marshal config: %+v", err)
			}

			if err = writeToDestination(data, talosconfigFile, 0o644); err != nil {
				return err
			}
		}

		// Create or update .gitignore file
		if err = writeGitignoreFile(); err != nil {
			return err
		}

		nodesDir := filepath.Join(Config.RootDir, "nodes")
		if err := os.MkdirAll(nodesDir, os.ModePerm); err != nil {
			return fmt.Errorf("failed to create nodes directory: %w", err)
		}

		presetFiles, err := generated.PresetFiles()
		if err != nil {
			return fmt.Errorf("failed to get preset files: %w", err)
		}

		for path, content := range presetFiles {
			parts := strings.SplitN(path, "/", 2)
			chartName := parts[0]
			// Write preset files
			if chartName == initCmdFlags.preset {
				file := filepath.Join(Config.RootDir, filepath.Join(parts[1:]...))
				if parts[len(parts)-1] == "Chart.yaml" {
					writeToDestination([]byte(fmt.Sprintf(content, clusterName, Config.InitOptions.Version)), file, 0o644)
				} else {
					err = writeToDestination([]byte(content), file, 0o644)
				}
				if err != nil {
					return err
				}
			}
			// Write library chart
			if chartName == "talm" {
				file := filepath.Join(Config.RootDir, filepath.Join("charts", path))
				if parts[len(parts)-1] == "Chart.yaml" {
					writeToDestination([]byte(fmt.Sprintf(content, "talm", Config.InitOptions.Version)), file, 0o644)
				} else {
					err = writeToDestination([]byte(content), file, 0o644)
				}
				if err != nil {
					return err
				}
			}
		}

		return nil

	},
}

func writeSecretsBundleToFile(bundle *secrets.Bundle) error {
	bundleBytes, err := yaml.Marshal(bundle)
	if err != nil {
		return err
	}

	secretsFile := filepath.Join(Config.RootDir, "secrets.yaml")
	if err = validateFileExists(secretsFile); err != nil {
		return err
	}

	return writeToDestination(bundleBytes, secretsFile, 0o644)
}

func updateTalmLibraryChart() error {
	talmChartDir := filepath.Join(Config.RootDir, "charts/talm")

	if err := os.RemoveAll(talmChartDir); err != nil {
		return fmt.Errorf("failed to remove existing talm chart directory: %w", err)
	}

	presetFiles, err := generated.PresetFiles()
	if err != nil {
		return fmt.Errorf("failed to get preset files: %w", err)
	}

	content, exists := presetFiles["talm/Chart.yaml"]
	if !exists {
		return fmt.Errorf("talm chart preset not found")
	}

	file := filepath.Join(talmChartDir, "Chart.yaml")
	err = writeToDestination([]byte(fmt.Sprintf(content, "talm", Config.InitOptions.Version)), file, 0o644)
	if err != nil {
		return err
	}

	// Remove the existing talm chart directory
	if err := os.RemoveAll(talmChartDir); err != nil {
		return fmt.Errorf("failed to remove existing talm chart directory: %w", err)
	}

	for path, content := range presetFiles {
		parts := strings.SplitN(path, "/", 2)
		chartName := parts[0]
		// Write library chart
		if chartName == "talm" {
			file := filepath.Join(Config.RootDir, filepath.Join("charts", path))
			if parts[len(parts)-1] == "Chart.yaml" {
				writeToDestination([]byte(fmt.Sprintf(content, "talm", Config.InitOptions.Version)), file, 0o644)
			} else {
				err = writeToDestination([]byte(content), file, 0o644)
			}
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func init() {
	initCmd.Flags().StringVar(&initCmdFlags.talosVersion, "talos-version", "", "the desired Talos version to generate config for (backwards compatibility, e.g. v0.8)")
	initCmd.Flags().StringVarP(&initCmdFlags.preset, "preset", "p", "", "specify preset to generate files")
	initCmd.Flags().BoolVar(&initCmdFlags.force, "force", false, "will overwrite existing files")
	initCmd.Flags().BoolVarP(&initCmdFlags.update, "update", "u", false, "update Talm library chart")

	addCommand(initCmd)
	initCmd.MarkFlagRequired("preset")
}

func isValidPreset(preset string, availablePresets []string) bool {
	for _, validPreset := range availablePresets {
		if preset == validPreset {
			return true
		}
	}
	return false
}

func validateFileExists(file string) error {
	if !initCmdFlags.force {
		if _, err := os.Stat(file); err == nil {
			return fmt.Errorf("file %q already exists, use --force to overwrite, and --update to update Talm library chart only", file)
		}
	}

	return nil
}

func writeGitignoreFile() error {
	gitignoreContent := `# Sensitive files
secrets.yaml
talosconfig
`
	gitignoreFile := filepath.Join(Config.RootDir, ".gitignore")
	
	// If .gitignore exists, read it and append if needed
	if _, err := os.Stat(gitignoreFile); err == nil {
		existingContent, err := os.ReadFile(gitignoreFile)
		if err != nil {
			return fmt.Errorf("failed to read existing .gitignore: %w", err)
		}
		
		// Check if secrets.yaml or talosconfig are already in .gitignore
		existingStr := string(existingContent)
		hasSecrets := strings.Contains(existingStr, "secrets.yaml")
		hasTalosconfig := strings.Contains(existingStr, "talosconfig")
		
		// Only update if missing entries
		if hasSecrets && hasTalosconfig {
			return nil // Already has both entries
		}
		
		// Append missing entries
		if !hasSecrets {
			if !strings.HasSuffix(existingStr, "\n") {
				existingStr += "\n"
			}
			existingStr += "secrets.yaml\n"
		}
		if !hasTalosconfig {
			if !strings.HasSuffix(existingStr, "\n") {
				existingStr += "\n"
			}
			existingStr += "talosconfig\n"
		}
		
		// Write without validation (allow overwrite for .gitignore)
		parentDir := filepath.Dir(gitignoreFile)
		if err := os.MkdirAll(parentDir, os.ModePerm); err != nil {
			return fmt.Errorf("failed to create output dir: %w", err)
		}
		err = os.WriteFile(gitignoreFile, []byte(existingStr), 0o644)
		fmt.Fprintf(os.Stderr, "Updated %s\n", gitignoreFile)
		return err
	}
	
	// Create new .gitignore without validation
	parentDir := filepath.Dir(gitignoreFile)
	if err := os.MkdirAll(parentDir, os.ModePerm); err != nil {
		return fmt.Errorf("failed to create output dir: %w", err)
	}
	err := os.WriteFile(gitignoreFile, []byte(gitignoreContent), 0o644)
	fmt.Fprintf(os.Stderr, "Created %s\n", gitignoreFile)
	return err
}

func writeToDestination(data []byte, destination string, permissions os.FileMode) error {
	if err := validateFileExists(destination); err != nil {
		return err
	}

	parentDir := filepath.Dir(destination)

	// Create dir path, ignoring "already exists" messages
	if err := os.MkdirAll(parentDir, os.ModePerm); err != nil {
		return fmt.Errorf("failed to create output dir: %w", err)
	}

	err := os.WriteFile(destination, data, permissions)

	fmt.Fprintf(os.Stderr, "Created %s\n", destination)

	return err
}
