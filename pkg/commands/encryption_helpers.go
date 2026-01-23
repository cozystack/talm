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

	"github.com/cozystack/talm/pkg/age"
	clientconfig "github.com/siderolabs/talos/pkg/machinery/client/config"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	"gopkg.in/yaml.v3"
)

// SaveTalosconfigWithEncryption saves talosconfig and updates talosconfig.encrypted if it exists.
func SaveTalosconfigWithEncryption(config *clientconfig.Config, talosconfigPath string) error {
	// Save the talosconfig
	if err := config.Save(talosconfigPath); err != nil {
		return fmt.Errorf("failed to save talosconfig: %w", err)
	}

	// Update talosconfig.encrypted if it exists
	encryptedPath := filepath.Join(Config.RootDir, "talosconfig.encrypted")
	if fileExists(encryptedPath) {
		fmt.Fprintf(os.Stderr, "Updating talosconfig.encrypted...\n")
		if err := age.EncryptYAMLFile(Config.RootDir, "talosconfig", "talosconfig.encrypted"); err != nil {
			return fmt.Errorf("failed to encrypt talosconfig: %w", err)
		}
	}

	return nil
}

// UpdateKubeconfigEncryption updates kubeconfig.encrypted if it exists.
// kubeconfigPath should be an absolute path to the kubeconfig file.
// Returns nil if encrypted file doesn't exist or key is missing.
func UpdateKubeconfigEncryption(kubeconfigPath string) error {
	// Get relative path from project root
	rootAbs, err := filepath.Abs(Config.RootDir)
	if err != nil {
		return nil // Skip encryption if we can't get absolute path
	}

	relKubeconfigPath, err := filepath.Rel(rootAbs, kubeconfigPath)
	if err != nil || strings.HasPrefix(relKubeconfigPath, "..") {
		return nil // Skip encryption if path is outside project root
	}

	encryptedKubeconfigPath := relKubeconfigPath + ".encrypted"
	encryptedKubeconfigFile := filepath.Join(Config.RootDir, encryptedKubeconfigPath)
	keyFile := filepath.Join(Config.RootDir, "talm.key")

	if !fileExists(encryptedKubeconfigFile) || !fileExists(keyFile) {
		return nil // Skip if encrypted file or key doesn't exist
	}

	fmt.Fprintf(os.Stderr, "Updating %s...\n", encryptedKubeconfigPath)
	if err := age.EncryptYAMLFile(Config.RootDir, relKubeconfigPath, encryptedKubeconfigPath); err != nil {
		return fmt.Errorf("failed to encrypt kubeconfig: %w", err)
	}

	return nil
}

// UpdateTalosconfigEncryption updates talosconfig.encrypted if it exists.
// Returns nil if encrypted file doesn't exist or key is missing.
func UpdateTalosconfigEncryption() error {
	encryptedPath := filepath.Join(Config.RootDir, "talosconfig.encrypted")
	keyFile := filepath.Join(Config.RootDir, "talm.key")

	if !fileExists(encryptedPath) || !fileExists(keyFile) {
		return nil // Skip if encrypted file or key doesn't exist
	}

	fmt.Fprintf(os.Stderr, "Updating talosconfig.encrypted...\n")
	if err := age.EncryptYAMLFile(Config.RootDir, "talosconfig", "talosconfig.encrypted"); err != nil {
		return fmt.Errorf("failed to encrypt talosconfig: %w", err)
	}

	return nil
}

// SaveSecretsBundleWithEncryption saves secrets.yaml and updates secrets.encrypted.yaml if it exists.
func SaveSecretsBundleWithEncryption(bundle *secrets.Bundle) error {
	secretsPath := ResolveSecretsPath(Config.TemplateOptions.WithSecrets)

	// Marshal the bundle
	data, err := yaml.Marshal(bundle)
	if err != nil {
		return fmt.Errorf("failed to marshal secrets: %w", err)
	}

	// Save secrets.yaml
	if err := os.WriteFile(secretsPath, data, 0o600); err != nil {
		return fmt.Errorf("failed to write secrets.yaml: %w", err)
	}

	// Update secrets.encrypted.yaml if it exists
	encryptedPath := filepath.Join(Config.RootDir, "secrets.encrypted.yaml")
	if fileExists(encryptedPath) {
		fmt.Fprintf(os.Stderr, "Updating secrets.encrypted.yaml...\n")
		if err := age.EncryptSecretsFile(Config.RootDir); err != nil {
			return fmt.Errorf("failed to encrypt secrets.yaml: %w", err)
		}
	}

	return nil
}
