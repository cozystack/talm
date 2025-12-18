// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cozystack/talm/pkg/age"
	"github.com/spf13/cobra"
)

// wrapKubeconfigCommand adds special handling for kubeconfig command
func wrapKubeconfigCommand(wrappedCmd *cobra.Command, originalRunE func(*cobra.Command, []string) error) {
	wrappedCmd.RunE = func(cmd *cobra.Command, args []string) error {
		// Always use kubeconfig path from Chart.yaml globalOptions
		kubeconfigPath := Config.GlobalOptions.Kubeconfig
		if kubeconfigPath == "" {
			// Default to "kubeconfig" if not specified in Chart.yaml
			kubeconfigPath = "kubeconfig"
		}

		// Replace args with path from Chart.yaml
		newArgs := []string{kubeconfigPath}
		// Execute original command with path from Chart.yaml
		if originalRunE != nil {
			if err := originalRunE(cmd, newArgs); err != nil {
				return err
			}
		} else if wrappedCmd.Run != nil {
			wrappedCmd.Run(cmd, newArgs)
		}

		// After command execution, set secure permissions and check if kubeconfig path is in project root
		// Check if path is relative and in project root scope
		var absPath string
		var err error
		if !filepath.IsAbs(kubeconfigPath) {
			absPath, err = filepath.Abs(filepath.Join(Config.RootDir, kubeconfigPath))
		} else {
			absPath = kubeconfigPath
		}

		if err == nil {
			// Set secure permissions (600) on kubeconfig file
			if err := os.Chmod(absPath, 0o600); err != nil {
				// Don't fail the command if chmod fails, but log warning
				fmt.Fprintf(os.Stderr, "Warning: failed to set permissions on kubeconfig: %v\n", err)
			}

			rootAbs, err := filepath.Abs(Config.RootDir)
			if err == nil {
				relPath, err := filepath.Rel(rootAbs, absPath)
				if err == nil && !strings.HasPrefix(relPath, "..") {
					// Path is within project root, add to .gitignore
					fileName := filepath.Base(kubeconfigPath)
					if fileName == "kubeconfig" {
						if err := addToGitignore("kubeconfig"); err != nil {
							// Don't fail the command if gitignore update fails
							fmt.Fprintf(os.Stderr, "Warning: failed to update .gitignore: %v\n", err)
						}
					}
				}
			}

			// Update kubeconfig server endpoint if endpoint is available
			if len(GlobalArgs.Endpoints) > 0 {
				endpoint := GlobalArgs.Endpoints[0]
				if err := updateKubeconfigServer(absPath, endpoint); err != nil {
					// Don't fail the command if update fails, but log warning
					fmt.Fprintf(os.Stderr, "Warning: failed to update kubeconfig server endpoint: %v\n", err)
				}
			}

			// Automatically update kubeconfig.encrypted if it exists and talm.key exists
			encryptedKubeconfigPath := kubeconfigPath + ".encrypted"
			encryptedKubeconfigFile := filepath.Join(Config.RootDir, encryptedKubeconfigPath)
			keyFile := filepath.Join(Config.RootDir, "talm.key")

			encryptedExists := fileExists(encryptedKubeconfigFile)
			keyExists := fileExists(keyFile)

			if encryptedExists && keyExists {
				// Both files exist, encrypt kubeconfig
				if err := age.EncryptYAMLFile(Config.RootDir, kubeconfigPath, encryptedKubeconfigPath); err != nil {
					// Don't fail the command if encryption fails, but log warning
					fmt.Fprintf(os.Stderr, "Warning: failed to encrypt kubeconfig: %v\n", err)
				} else {
					fmt.Fprintf(os.Stderr, "Updated %s\n", encryptedKubeconfigPath)
				}
			}
		}

		return nil
	}
	// Remove Args validation to allow command to work without arguments
	wrappedCmd.Args = cobra.NoArgs
}

