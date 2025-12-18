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
	"net"
	"os"
	"path/filepath"
	"strings"

	taloscommands "github.com/siderolabs/talos/cmd/talosctl/cmd/talos"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"k8s.io/client-go/tools/clientcmd"
)

// wrapTalosCommand wraps a talosctl command to add --file flag support
func wrapTalosCommand(cmd *cobra.Command, cmdName string) *cobra.Command {
	// Create a copy of the command to avoid modifying the original
	wrappedCmd := &cobra.Command{
		Use:                cmd.Use,
		Short:              cmd.Short,
		Long:               cmd.Long,
		Example:            cmd.Example,
		Aliases:            cmd.Aliases,
		SuggestFor:         cmd.SuggestFor,
		Args:               cmd.Args,
		ValidArgsFunction:  cmd.ValidArgsFunction,
		RunE:               cmd.RunE,
		Run:                cmd.Run,
		DisableFlagParsing: cmd.DisableFlagParsing,
		TraverseChildren:   cmd.TraverseChildren,
	}

	// Copy all flags from original command and handle -f flag conflict
	fileFlagExists := false
	cmd.Flags().VisitAll(func(flag *pflag.Flag) {
		// Check if this is the --file flag
		if flag.Name == "file" {
			fileFlagExists = true
		}

		// If this flag has shorthand 'f', we need to change it to 'F'
		if flag.Shorthand == "f" {
			// Create a copy with new shorthand
			newFlag := &pflag.Flag{
				Name:        flag.Name,
				Usage:       flag.Usage,
				Value:       flag.Value,
				DefValue:    flag.DefValue,
				Changed:     flag.Changed,
				Deprecated:  flag.Deprecated,
				Hidden:      flag.Hidden,
				Shorthand:   "F", // Change shorthand from 'f' to 'F'
				Annotations: flag.Annotations,
			}
			wrappedCmd.Flags().AddFlag(newFlag)
		} else {
			wrappedCmd.Flags().AddFlag(flag)
		}
	})

	// Add --file flag only if it doesn't already exist in the original command
	var configFiles []string
	if !fileFlagExists {
		// Double-check that the flag doesn't exist after copying
		if wrappedCmd.Flags().Lookup("file") == nil {
			wrappedCmd.Flags().StringSliceVarP(&configFiles, "file", "f", nil, "specify config files or patches in a YAML file (can specify multiple)")
		}
	}
	// Note: If --file flag already exists, we'll get its value in PreRunE via cmd.Flags().GetStringSlice("file")

	// Wrap PreRunE to process modeline files and sync GlobalArgs
	originalPreRunE := cmd.PreRunE
	wrappedCmd.PreRunE = func(cmd *cobra.Command, args []string) error {
		// Special handling for kubeconfig command: set default values for --merge and --force
		baseCmdName := cmdName
		if idx := strings.Index(cmdName, " "); idx > 0 {
			baseCmdName = cmdName[:idx]
		}
		if baseCmdName == "kubeconfig" {
			if !cmd.Flags().Changed("force") {
				if err := cmd.Flags().Set("force", "true"); err != nil {
					// Flag might not exist, ignore error
				}
			}
		}

		nodesFromArgs := len(GlobalArgs.Nodes) > 0
		endpointsFromArgs := len(GlobalArgs.Endpoints) > 0

		// Get config files from flag (either our new flag or existing talosctl flag)
		var filesToProcess []string
		if len(configFiles) > 0 {
			// Use our variable if flag was added by us
			filesToProcess = configFiles
		} else if fileFlag := cmd.Flags().Lookup("file"); fileFlag != nil {
			// Get value from existing flag
			if fileFlagValue, err := cmd.Flags().GetStringSlice("file"); err == nil {
				filesToProcess = fileFlagValue
			}
		}

		// Expand directories to YAML files
		expandedFiles, err := ExpandFilePaths(filesToProcess)
		if err != nil {
			return err
		}

		// Detect root from files if specified, otherwise fallback to cwd
		if err := DetectAndSetRootFromFiles(expandedFiles); err != nil {
			return err
		}

		for _, configFile := range expandedFiles {
			if err := processModelineAndUpdateGlobals(configFile, nodesFromArgs, endpointsFromArgs, false); err != nil {
				return err
			}
		}
		
		// Ensure talosconfig path is set to project root if not explicitly set via flag
		EnsureTalosconfigPath(cmd)
		
		// Sync GlobalArgs to talosctl commands
		taloscommands.GlobalArgs = GlobalArgs
		if originalPreRunE != nil {
			return originalPreRunE(cmd, args)
		}
		return nil
	}

	// Extract base command name for comparison
	baseCmdName := cmdName
	if idx := strings.Index(cmdName, " "); idx > 0 {
		baseCmdName = cmdName[:idx]
	}

	originalRunE := wrappedCmd.RunE

	// Special handling for kubeconfig command
	if baseCmdName == "kubeconfig" {
		wrapKubeconfigCommand(wrappedCmd, originalRunE)
	}

	// Special handling for upgrade command
	if baseCmdName == "upgrade" {
		wrapUpgradeCommand(wrappedCmd, originalRunE)
	}

	// Copy all subcommands
	for _, subCmd := range cmd.Commands() {
		wrappedCmd.AddCommand(wrapTalosCommand(subCmd, subCmd.Name()))
	}

	return wrappedCmd
}

func init() {
	// Import all commands from talosctl package, except those in the exclusion list
	// Commands to exclude (these are talm-specific or should not be exposed)
	excludedCommands := map[string]bool{
		"apply-config": true, // talm has its own apply command
		"config":       true, // talm manages config differently
		"patch":        true, // not needed in talm
		"upgrade-k8s":  true, // not needed in talm
		"talosconfig": true, // talm has its own talosconfig command
	}

	// Import and wrap each command from talosctl
	for _, cmd := range taloscommands.Commands {
		// Extract base command name (without arguments)
		baseName := cmd.Use
		if idx := len(baseName); idx > 0 {
			// Remove arguments from command name (everything after first space)
			for i, r := range baseName {
				if r == ' ' || r == '<' || r == '[' {
					baseName = baseName[:i]
					break
				}
			}
		}

		// Skip excluded commands
		if excludedCommands[baseName] {
			continue
		}

		// Wrap the command and add it to our commands list
		// Note: wrapTalosCommand recursively processes all subcommands, so they already have
		// the -f flag handling, --file flag (if needed), and PreRunE set automatically
		wrappedCmd := wrapTalosCommand(cmd, baseName)
		// Keep the original command name from talosctl
		addCommand(wrappedCmd)
	}
}

// normalizeEndpoint normalizes an endpoint by removing any existing port and protocol, then adding https:// and :6443
// Examples:
//   - "1.2.3.4" -> "https://1.2.3.4:6443"
//   - "1.2.3.4:50000" -> "https://1.2.3.4:6443"
//   - "https://1.2.3.4:50000" -> "https://1.2.3.4:6443"
//   - "http://1.2.3.4" -> "https://1.2.3.4:6443"
func normalizeEndpoint(endpoint string) string {
	// Remove protocol if present
	endpoint = strings.TrimPrefix(endpoint, "https://")
	endpoint = strings.TrimPrefix(endpoint, "http://")

	// Split host and port
	host, _, err := net.SplitHostPort(endpoint)
	if err != nil {
		// No port in endpoint, use as-is
		host = endpoint
	}

	// Return normalized endpoint with https:// and :6443 port
	return fmt.Sprintf("https://%s:6443", host)
}

// updateKubeconfigServer updates the server field in all clusters of the kubeconfig file
func updateKubeconfigServer(kubeconfigPath, endpoint string) error {
	// Load kubeconfig
	config, err := clientcmd.LoadFromFile(kubeconfigPath)
	if err != nil {
		return fmt.Errorf("failed to load kubeconfig: %w", err)
	}

	// Normalize endpoint
	normalizedEndpoint := normalizeEndpoint(endpoint)

	// Update server for all clusters
	updated := false
	for clusterName, cluster := range config.Clusters {
		if cluster.Server != normalizedEndpoint {
			cluster.Server = normalizedEndpoint
			updated = true
			fmt.Fprintf(os.Stderr, "Updated cluster %s server to %s\n", clusterName, normalizedEndpoint)
		}
	}

	// Save kubeconfig if updated
	if updated {
		if err := clientcmd.WriteToFile(*config, kubeconfigPath); err != nil {
			return fmt.Errorf("failed to write kubeconfig: %w", err)
		}
	}

	return nil
}

// addToGitignore adds an entry to .gitignore if it doesn't already exist
func addToGitignore(entry string) error {
	gitignoreFile := filepath.Join(Config.RootDir, ".gitignore")

	// Read existing .gitignore if it exists
	var content string
	if _, err := os.Stat(gitignoreFile); err == nil {
		existingContent, err := os.ReadFile(gitignoreFile)
		if err != nil {
			return fmt.Errorf("failed to read .gitignore: %w", err)
		}
		content = string(existingContent)

		// Check if entry already exists
		lines := strings.Split(content, "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == entry || strings.HasPrefix(line, entry+"/") {
				return nil // Already exists
			}
		}
	}

	// Append entry
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += entry + "\n"

	// Write back
	return os.WriteFile(gitignoreFile, []byte(content), 0o644)
}
