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
	"strings"
	"time"

	"github.com/cozystack/talm/pkg/age"
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
	encrypt      bool
	decrypt      bool
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
		// Preset is not required when using --encrypt or --decrypt flags
		if initCmdFlags.encrypt || initCmdFlags.decrypt {
			return nil
		}
		if initCmdFlags.preset == "" {
			return fmt.Errorf("preset is required (use --preset or -p flag)")
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
		// Validate preset only if not using --encrypt or --decrypt
		if !initCmdFlags.encrypt && !initCmdFlags.decrypt {
			availablePresets, err := generated.AvailablePresets()
			if err != nil {
				return fmt.Errorf("failed to get available presets: %w", err)
			}
			if !isValidPreset(initCmdFlags.preset, availablePresets) {
				return fmt.Errorf("invalid preset: %s. Valid presets are: %v", initCmdFlags.preset, availablePresets)
			}
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

		// Handle age encryption logic
		secretsFile := filepath.Join(Config.RootDir, "secrets.yaml")
		encryptedSecretsFile := filepath.Join(Config.RootDir, "secrets.encrypted.yaml")
		keyFile := filepath.Join(Config.RootDir, "talm.key")

		secretsFileExists := fileExists(secretsFile)
		encryptedSecretsFileExists := fileExists(encryptedSecretsFile)
		keyFileExists := fileExists(keyFile)
		keyWasCreated := false // Track if key was created during this init

		// Check for invalid state: encrypted file exists but secrets.yaml and key don't
		if encryptedSecretsFileExists && !secretsFileExists && !keyFileExists {
			return fmt.Errorf("secrets.encrypted.yaml exists but secrets.yaml and talm.key are missing. Cannot decrypt without key")
		}

		// Handle --encrypt flag (early return, doesn't need preset)
		if initCmdFlags.encrypt {
			// Ensure key exists before encryption
			keyFile := filepath.Join(Config.RootDir, "talm.key")
			keyFileExists := fileExists(keyFile)
			if !keyFileExists {
				_, keyCreated, err := age.GenerateKey(Config.RootDir)
				if err != nil {
					return fmt.Errorf("failed to generate key: %w", err)
				}
				if keyCreated {
					fmt.Fprintf(os.Stderr, "Generated new encryption key: talm.key\n")
					printSecretsWarning()
				}
			}

			// Encrypt all sensitive files
			secretsFile := filepath.Join(Config.RootDir, "secrets.yaml")
			talosconfigFile := filepath.Join(Config.RootDir, "talosconfig")
			kubeconfigPath := Config.GlobalOptions.Kubeconfig
			if kubeconfigPath == "" {
				kubeconfigPath = "kubeconfig"
			}
			kubeconfigFile := filepath.Join(Config.RootDir, kubeconfigPath)

			encryptedCount := 0

			// Encrypt secrets.yaml
			if fileExists(secretsFile) {
				fmt.Fprintf(os.Stderr, "Encrypting secrets.yaml -> secrets.encrypted.yaml\n")
				if err := age.EncryptSecretsFile(Config.RootDir); err != nil {
					return fmt.Errorf("failed to encrypt secrets: %w", err)
				}
				encryptedCount++
			} else {
				fmt.Fprintf(os.Stderr, "Skipping secrets.yaml (file not found)\n")
			}

			// Encrypt talosconfig
			if fileExists(talosconfigFile) {
				fmt.Fprintf(os.Stderr, "Encrypting talosconfig -> talosconfig.encrypted\n")
				if err := age.EncryptYAMLFile(Config.RootDir, "talosconfig", "talosconfig.encrypted"); err != nil {
					return fmt.Errorf("failed to encrypt talosconfig: %w", err)
				}
				encryptedCount++
			} else {
				fmt.Fprintf(os.Stderr, "Skipping talosconfig (file not found)\n")
			}

			// Encrypt kubeconfig
			if fileExists(kubeconfigFile) {
				fmt.Fprintf(os.Stderr, "Encrypting %s -> %s.encrypted\n", kubeconfigPath, kubeconfigPath)
				if err := age.EncryptYAMLFile(Config.RootDir, kubeconfigPath, kubeconfigPath+".encrypted"); err != nil {
					return fmt.Errorf("failed to encrypt kubeconfig: %w", err)
				}
				encryptedCount++
			} else {
				fmt.Fprintf(os.Stderr, "Skipping %s (file not found)\n", kubeconfigPath)
			}

			// Update .gitignore file
			if err := writeGitignoreFile(); err != nil {
				return fmt.Errorf("failed to update .gitignore: %w", err)
			}

			if encryptedCount > 0 {
				fmt.Fprintf(os.Stderr, "Encryption completed successfully. %d file(s) encrypted.\n", encryptedCount)
			} else {
				fmt.Fprintf(os.Stderr, "No files to encrypt.\n")
			}
			return nil
		}

		// Handle --decrypt flag (early return, doesn't need preset)
		if initCmdFlags.decrypt {
			// Decrypt all encrypted files
			encryptedSecretsFile := filepath.Join(Config.RootDir, "secrets.encrypted.yaml")
			encryptedTalosconfigFile := filepath.Join(Config.RootDir, "talosconfig.encrypted")
			kubeconfigPath := Config.GlobalOptions.Kubeconfig
			if kubeconfigPath == "" {
				kubeconfigPath = "kubeconfig"
			}
			encryptedKubeconfigFile := filepath.Join(Config.RootDir, kubeconfigPath+".encrypted")

			decryptedCount := 0

			// Decrypt secrets.encrypted.yaml
			if fileExists(encryptedSecretsFile) {
				fmt.Fprintf(os.Stderr, "Decrypting secrets.encrypted.yaml -> secrets.yaml\n")
				if err := age.DecryptSecretsFile(Config.RootDir); err != nil {
					return fmt.Errorf("failed to decrypt secrets: %w", err)
				}
				decryptedCount++
			} else {
				fmt.Fprintf(os.Stderr, "Skipping secrets.encrypted.yaml (file not found)\n")
			}

			// Decrypt talosconfig.encrypted
			if fileExists(encryptedTalosconfigFile) {
				fmt.Fprintf(os.Stderr, "Decrypting talosconfig.encrypted -> talosconfig\n")
				if err := age.DecryptYAMLFile(Config.RootDir, "talosconfig.encrypted", "talosconfig"); err != nil {
					return fmt.Errorf("failed to decrypt talosconfig: %w", err)
				}
				decryptedCount++
			} else {
				fmt.Fprintf(os.Stderr, "Skipping talosconfig.encrypted (file not found)\n")
			}

			// Decrypt kubeconfig.encrypted
			if fileExists(encryptedKubeconfigFile) {
				fmt.Fprintf(os.Stderr, "Decrypting %s.encrypted -> %s\n", kubeconfigPath, kubeconfigPath)
				if err := age.DecryptYAMLFile(Config.RootDir, kubeconfigPath+".encrypted", kubeconfigPath); err != nil {
					return fmt.Errorf("failed to decrypt kubeconfig: %w", err)
				}
				decryptedCount++
			} else {
				fmt.Fprintf(os.Stderr, "Skipping %s.encrypted (file not found)\n", kubeconfigPath)
			}

			// Update .gitignore file
			if err := writeGitignoreFile(); err != nil {
				return fmt.Errorf("failed to update .gitignore: %w", err)
			}

			if decryptedCount > 0 {
				fmt.Fprintf(os.Stderr, "Decryption completed successfully. %d file(s) decrypted.\n", decryptedCount)
			} else {
				fmt.Fprintf(os.Stderr, "No files to decrypt.\n")
			}
			return nil
		}

		// Preset is required for normal init (not --encrypt or --decrypt)
		if initCmdFlags.preset == "" {
			return fmt.Errorf("preset is required (use --preset or -p flag)")
		}

		// If encrypted file exists, decrypt it
		if encryptedSecretsFileExists && !secretsFileExists {
			if err := age.DecryptSecretsFile(Config.RootDir); err != nil {
				return fmt.Errorf("failed to decrypt secrets: %w", err)
			}
		}

		// Write secrets.yaml only if it doesn't exist
		if !secretsFileExists {
			if err = writeSecretsBundleToFile(secretsBundle); err != nil {
				return err
			}
			secretsFileExists = true // Update flag after creation
		}

		// If secrets.yaml exists but encrypted file doesn't, encrypt it
		if secretsFileExists && !encryptedSecretsFileExists {
			// Generate key if it doesn't exist
			if !keyFileExists {
				_, keyCreated, err := age.GenerateKey(Config.RootDir)
				if err != nil {
					return fmt.Errorf("failed to generate key: %w", err)
				}
				keyFileExists = true // Update flag after creation
				keyWasCreated = keyCreated
			}

			// Encrypt secrets
			if err := age.EncryptSecretsFile(Config.RootDir); err != nil {
				return fmt.Errorf("failed to encrypt secrets: %w", err)
			}
		}

		// Clalculate cluster name from directory
		absolutePath, err := filepath.Abs(Config.RootDir)
		if err != nil {
			return err
		}
		clusterName := filepath.Base(absolutePath)

		// Handle talosconfig encryption logic
		talosconfigFile := filepath.Join(Config.RootDir, "talosconfig")
		encryptedTalosconfigFile := filepath.Join(Config.RootDir, "talosconfig.encrypted")
		talosconfigFileExists := fileExists(talosconfigFile)
		encryptedTalosconfigFileExists := fileExists(encryptedTalosconfigFile)

		// If encrypted file exists, decrypt it (don't require key - will generate if needed)
		if encryptedTalosconfigFileExists && !talosconfigFileExists {
			_, err := handleTalosconfigEncryption(false)
			if err != nil {
				// If decryption fails (e.g., no key), continue to generate
			}
			talosconfigFileExists = fileExists(talosconfigFile)
		}

		// Generate talosconfig only if it doesn't exist
		if !talosconfigFileExists {
			configBundle, err := gen.GenerateConfigBundle(genOptions, clusterName, "https://192.168.0.1:6443", "", []string{}, []string{}, []string{})
			if err != nil {
				return err
			}
			configBundle.TalosConfig().Contexts[clusterName].Endpoints = []string{"127.0.0.1"}

			data, err := yaml.Marshal(configBundle.TalosConfig())
			if err != nil {
				return fmt.Errorf("failed to marshal config: %+v", err)
			}

			if err = writeToDestination(data, talosconfigFile, 0o600); err != nil {
				return err
			}
			talosconfigFileExists = true
		}

		// Encrypt talosconfig if needed
		talosKeyCreated, err := handleTalosconfigEncryption(false)
		if err != nil {
			return err
		}
		if talosKeyCreated {
			keyWasCreated = true
		}

		// Handle kubeconfig encryption logic (check if kubeconfig exists from Chart.yaml)
		kubeconfigPath := Config.GlobalOptions.Kubeconfig
		if kubeconfigPath == "" {
			kubeconfigPath = "kubeconfig"
		}
		kubeconfigFile := filepath.Join(Config.RootDir, kubeconfigPath)
		encryptedKubeconfigFile := filepath.Join(Config.RootDir, kubeconfigPath+".encrypted")
		kubeconfigFileExists := fileExists(kubeconfigFile)
		encryptedKubeconfigFileExists := fileExists(encryptedKubeconfigFile)

		// If encrypted file exists, decrypt it
		if encryptedKubeconfigFileExists && !kubeconfigFileExists {
			if err := age.DecryptYAMLFile(Config.RootDir, kubeconfigPath+".encrypted", kubeconfigPath); err != nil {
				return fmt.Errorf("failed to decrypt kubeconfig: %w", err)
			}
			kubeconfigFileExists = true
		}

		// If kubeconfig exists but encrypted file doesn't, encrypt it
		if kubeconfigFileExists && !encryptedKubeconfigFileExists {
			// Ensure key exists
			if !keyFileExists {
				_, keyCreated, err := age.GenerateKey(Config.RootDir)
				if err != nil {
					return fmt.Errorf("failed to generate key: %w", err)
				}
				keyFileExists = true // Update flag after creation
				keyWasCreated = keyCreated
			}

			// Encrypt kubeconfig
			if err := age.EncryptYAMLFile(Config.RootDir, kubeconfigPath, kubeconfigPath+".encrypted"); err != nil {
				return fmt.Errorf("failed to encrypt kubeconfig: %w", err)
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

		// Print warning about secrets and key backup (only once, at the end, if key was created)
		if keyWasCreated {
			printSecretsWarning()
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

	return writeToDestination(bundleBytes, secretsFile, 0o600)
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
	initCmd.Flags().StringVarP(&initCmdFlags.preset, "preset", "p", "", "specify preset to generate files (not required with --encrypt or --decrypt)")
	initCmd.Flags().BoolVar(&initCmdFlags.force, "force", false, "will overwrite existing files")
	initCmd.Flags().BoolVarP(&initCmdFlags.update, "update", "u", false, "update Talm library chart")
	// Override persistent -e flag for init command to use for encrypt
	// Remove the persistent endpoints flag from init command and add our own -e flag
	initCmd.Flags().StringSliceVarP(&GlobalArgs.Endpoints, "endpoints", "", []string{}, "override default endpoints in Talos configuration")
	initCmd.Flags().BoolVarP(&initCmdFlags.encrypt, "encrypt", "e", false, "encrypt all sensitive files (secrets.yaml, talosconfig, kubeconfig)")
	initCmd.Flags().BoolVarP(&initCmdFlags.decrypt, "decrypt", "d", false, "decrypt all encrypted files (does not require preset)")

	addCommand(initCmd)
	// Don't mark preset as required - it's validated in PreRunE based on --encrypt/--decrypt flags
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
	requiredEntries := []string{"secrets.yaml", "talosconfig", "talm.key"}
	gitignoreFile := filepath.Join(Config.RootDir, ".gitignore")

	var existingStr string
	// If .gitignore exists, read it
	if _, err := os.Stat(gitignoreFile); err == nil {
		existingContent, err := os.ReadFile(gitignoreFile)
		if err != nil {
			return fmt.Errorf("failed to read existing .gitignore: %w", err)
		}
		existingStr = string(existingContent)
	} else {
		existingStr = "# Sensitive files\n"
	}

	// Check which entries are missing
	needsUpdate := false
	for _, entry := range requiredEntries {
		// Check if entry exists (as whole line or with comment)
		lines := strings.Split(existingStr, "\n")
		found := false
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == entry || strings.HasPrefix(line, entry+" ") || strings.HasPrefix(line, entry+"#") {
				found = true
				break
			}
		}
		if !found {
			if !strings.HasSuffix(existingStr, "\n") {
				existingStr += "\n"
			}
			existingStr += entry + "\n"
			needsUpdate = true
		}
	}

	// Only update if needed
	if !needsUpdate {
		return nil
	}

	// Write without validation (allow overwrite for .gitignore)
	parentDir := filepath.Dir(gitignoreFile)
	if err := os.MkdirAll(parentDir, os.ModePerm); err != nil {
		return fmt.Errorf("failed to create output dir: %w", err)
	}
	err := os.WriteFile(gitignoreFile, []byte(existingStr), 0o644)
	if _, statErr := os.Stat(gitignoreFile); statErr == nil {
		fmt.Fprintf(os.Stderr, "Updated %s\n", gitignoreFile)
	} else {
		fmt.Fprintf(os.Stderr, "Created %s\n", gitignoreFile)
	}
	return err
}

func fileExists(file string) bool {
	_, err := os.Stat(file)
	return err == nil
}

func printSecretsWarning() {
	keyFile := filepath.Join(Config.RootDir, "talm.key")
	keyFileExists := fileExists(keyFile)

	if !keyFileExists {
		return // No key file, no warning needed
	}

	fmt.Fprintf(os.Stderr, "\n")
	fmt.Fprintf(os.Stderr, "┌──────────────────────────────────────────────────────────────────────────────┐\n")
	fmt.Fprintf(os.Stderr, "│  Security Information                                                        │\n")
	fmt.Fprintf(os.Stderr, "├──────────────────────────────────────────────────────────────────────────────┤\n")
	fmt.Fprintf(os.Stderr, "│                                                                              │\n")
	fmt.Fprintf(os.Stderr, "│  Sensitive files (secrets.yaml, talosconfig, talm.key) have been added to    │\n")
	fmt.Fprintf(os.Stderr, "│  .gitignore and will not be tracked by git.                                  │\n")
	fmt.Fprintf(os.Stderr, "│                                                                              │\n")
	fmt.Fprintf(os.Stderr, "│  Important: Please make a backup of your talm.key file.                      │\n")
	fmt.Fprintf(os.Stderr, "│                                                                              │\n")
	fmt.Fprintf(os.Stderr, "│  The talm.key file is required to decrypt secrets.encrypted.yaml. Without it,│\n")
	fmt.Fprintf(os.Stderr, "│  you won't be able to decrypt your encrypted secrets.                        │\n")
	fmt.Fprintf(os.Stderr, "│                                                                              │\n")
	fmt.Fprintf(os.Stderr, "│  Key location: talm.key                                                      |\n")
	fmt.Fprintf(os.Stderr, "│                                                                              │\n")
	fmt.Fprintf(os.Stderr, "│  Recommended: Store the backup in a secure location (password manager,       │\n")
	fmt.Fprintf(os.Stderr, "│  encrypted storage, or other secure backup solution).                        │\n")
	fmt.Fprintf(os.Stderr, "│                                                                              │\n")
	fmt.Fprintf(os.Stderr, "└──────────────────────────────────────────────────────────────────────────────┘\n")
	fmt.Fprintf(os.Stderr, "\n")
}

// handleTalosconfigEncryption handles encryption/decryption logic for talosconfig file.
// It decrypts if encrypted file exists, encrypts if plain file exists.
// requireKeyForDecrypt: if true, returns error if key is missing when trying to decrypt.
// Returns true if key was created during this call, false otherwise.
func handleTalosconfigEncryption(requireKeyForDecrypt bool) (bool, error) {
	talosconfigFile := filepath.Join(Config.RootDir, "talosconfig")
	encryptedTalosconfigFile := filepath.Join(Config.RootDir, "talosconfig.encrypted")
	talosconfigFileExists := fileExists(talosconfigFile)
	encryptedTalosconfigFileExists := fileExists(encryptedTalosconfigFile)
	keyFile := filepath.Join(Config.RootDir, "talm.key")
	keyFileExists := fileExists(keyFile)
	keyWasCreated := false

	// If encrypted file exists, decrypt it
	if encryptedTalosconfigFileExists && !talosconfigFileExists {
		if !keyFileExists {
			if requireKeyForDecrypt {
				return false, fmt.Errorf("talosconfig.encrypted exists but talm.key is missing. Cannot decrypt without key")
			}
			// If key is not required, just return (don't decrypt)
			return false, nil
		}
		fmt.Fprintf(os.Stderr, "Decrypting talosconfig.encrypted -> talosconfig\n")
		if err := age.DecryptYAMLFile(Config.RootDir, "talosconfig.encrypted", "talosconfig"); err != nil {
			return false, fmt.Errorf("failed to decrypt talosconfig: %w", err)
		}
		talosconfigFileExists = true
	}

	// If talosconfig exists but encrypted file doesn't, encrypt it
	if talosconfigFileExists && !encryptedTalosconfigFileExists {
		// Ensure key exists
		if !keyFileExists {
			_, keyCreated, err := age.GenerateKey(Config.RootDir)
			if err != nil {
				return false, fmt.Errorf("failed to generate key: %w", err)
			}
			keyWasCreated = keyCreated
			if keyCreated {
				fmt.Fprintf(os.Stderr, "Generated new encryption key: talm.key\n")
			}
			keyFileExists = true
		}

		// Encrypt talosconfig
		fmt.Fprintf(os.Stderr, "Encrypting talosconfig -> talosconfig.encrypted\n")
		if err := age.EncryptYAMLFile(Config.RootDir, "talosconfig", "talosconfig.encrypted"); err != nil {
			return false, fmt.Errorf("failed to encrypt talosconfig: %w", err)
		}
	}

	return keyWasCreated, nil
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
