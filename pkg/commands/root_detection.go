// Copyright Cozystack Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// parseFlagFromArgs parses a flag value from command line arguments.
// Supports both -flag value and -flag=value formats, as well as comma-separated values.
func parseFlagFromArgs(args []string, shortFlag, longFlag string) []string {
	var values []string
	for i, arg := range args {
		if arg == shortFlag || arg == longFlag {
			// Get the next argument(s) as value(s)
			if i+1 < len(args) {
				nextArg := args[i+1]
				if !strings.HasPrefix(nextArg, "-") {
					values = parseCommaSeparatedValues(nextArg)
				}
			}
			break
		} else if strings.HasPrefix(arg, shortFlag+"=") || strings.HasPrefix(arg, longFlag+"=") {
			// Handle -flag=value or --flag=value format
			parts := strings.SplitN(arg, "=", 2)
			if len(parts) == 2 {
				values = parseCommaSeparatedValues(parts[1])
			}
			break
		}
	}
	return values
}

// parseCommaSeparatedValues parses comma-separated values and returns a slice of trimmed values.
func parseCommaSeparatedValues(value string) []string {
	var values []string
	if strings.Contains(value, ",") {
		parts := strings.Split(value, ",")
		for _, part := range parts {
			if trimmed := strings.TrimSpace(part); trimmed != "" {
				values = append(values, trimmed)
			}
		}
	} else {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			values = append(values, trimmed)
		}
	}
	return values
}

// getFlagValues tries to get flag values from cobra command flags.
// Returns empty slice if flag not found or has no values.
func getFlagValues(cmd *cobra.Command, flagName string) []string {
	// Try to get from command flags first
	if flag := cmd.Flags().Lookup(flagName); flag != nil {
		if values, err := cmd.Flags().GetStringSlice(flagName); err == nil && len(values) > 0 {
			return values
		}
	}
	// Try persistent flags
	if flag := cmd.PersistentFlags().Lookup(flagName); flag != nil {
		if values, err := cmd.PersistentFlags().GetStringSlice(flagName); err == nil && len(values) > 0 {
			return values
		}
	}
	return []string{}
}

// detectRootFromFiles detects project root from file paths.
func detectRootFromFiles(filePaths []string) (string, error) {
	if len(filePaths) == 0 {
		return "", nil
	}
	return ValidateAndDetectRootsForFiles(filePaths)
}

// detectRootFromTemplates detects project root from template file paths.
func detectRootFromTemplates(templatePaths []string) (string, error) {
	if len(templatePaths) == 0 {
		return "", nil
	}
	// Use first template to detect root
	return DetectRootForTemplate(templatePaths[0])
}

// detectRootFromCWD detects project root from current working directory.
func detectRootFromCWD() (string, error) {
	currentDir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get current working directory: %w", err)
	}
	return DetectProjectRoot(currentDir)
}

// checkRootConflict checks if detected root conflicts with explicitly set root.
func checkRootConflict(detectedRoot string, rootDirExplicit bool) error {
	if !rootDirExplicit {
		return nil
	}
	absConfigRoot, _ := filepath.Abs(Config.RootDir)
	absDetectedRoot, _ := filepath.Abs(detectedRoot)
	if absConfigRoot != absDetectedRoot {
		return fmt.Errorf("conflicting project roots: global --root=%s, but detected root=%s", absConfigRoot, absDetectedRoot)
	}
	return nil
}

// DetectAndSetRoot detects and sets the project root using fallback strategy:
// 1. From -f/--file flag (if files specified)
// 2. From -t/--template flag (if templates specified)
// 3. From current working directory
func DetectAndSetRoot(cmd *cobra.Command, args []string) error {
	// Check if --root was explicitly set
	Config.RootDirExplicit = cmd.PersistentFlags().Changed("root")

	// Get file paths from -f/--file flag
	configFiles := getFlagValues(cmd, "file")
	if len(configFiles) == 0 {
		// Parse from args if not found in flags
		allArgs := os.Args[1:]
		configFiles = parseFlagFromArgs(allArgs, "-f", "--file")
	}

	// Get template paths from -t/--template flag
	templateFiles := getFlagValues(cmd, "template")
	if len(templateFiles) == 0 {
		// Parse from args if not found in flags
		allArgs := os.Args[1:]
		templateFiles = parseFlagFromArgs(allArgs, "-t", "--template")
	}

	// Strategy 1: Detect root from files
	if len(configFiles) > 0 {
		detectedRoot, err := detectRootFromFiles(configFiles)
		if err != nil {
			return err
		}
		if detectedRoot != "" {
			if err := checkRootConflict(detectedRoot, Config.RootDirExplicit); err != nil {
				return err
			}
			Config.RootDir = detectedRoot
			return nil
		}
	}

	// Strategy 2: Detect root from templates (only if root not explicitly set)
	if !Config.RootDirExplicit && len(templateFiles) > 0 {
		detectedRoot, err := detectRootFromTemplates(templateFiles)
		if err == nil && detectedRoot != "" {
			Config.RootDir = detectedRoot
			return nil
		}
	}

	// Strategy 3: Detect root from current working directory (only if root not explicitly set)
	if !Config.RootDirExplicit {
		detectedRoot, err := detectRootFromCWD()
		if err == nil && detectedRoot != "" {
			Config.RootDir = detectedRoot
		}
	}

	return nil
}

// DetectAndSetRootFromFiles detects and sets project root from file paths.
// This is a common pattern used in commands like apply, upgrade, and talosctl wrapper.
// It detects root from files if provided, otherwise falls back to current working directory.
func DetectAndSetRootFromFiles(filePaths []string) error {
	if len(filePaths) > 0 {
		detectedRoot, err := ValidateAndDetectRootsForFiles(filePaths)
		if err != nil {
			return err
		}
		if detectedRoot != "" {
			absConfigRoot, _ := filepath.Abs(Config.RootDir)
			absDetectedRoot, _ := filepath.Abs(detectedRoot)
			// Root from files has priority
			if absConfigRoot != absDetectedRoot {
				// If --root was explicitly set and differs from files root, error
				if Config.RootDirExplicit {
					return fmt.Errorf("conflicting project roots: global --root=%s, but files belong to root=%s", absConfigRoot, absDetectedRoot)
				}
			}
			// Use root from files (has priority)
			Config.RootDir = detectedRoot
			return nil
		}
	}

	// Fallback: detect root from current working directory if not explicitly set
	if !Config.RootDirExplicit {
		currentDir, err := os.Getwd()
		if err == nil {
			detectedRoot, err := DetectProjectRoot(currentDir)
			if err == nil && detectedRoot != "" {
				Config.RootDir = detectedRoot
			}
		}
	}

	return nil
}

// ResolveSecretsPath resolves secrets.yaml path relative to project root if not absolute.
func ResolveSecretsPath(withSecrets string) string {
	if withSecrets == "" {
		withSecrets = "secrets.yaml"
	}
	if !filepath.IsAbs(withSecrets) {
		withSecrets = filepath.Join(Config.RootDir, withSecrets)
	}
	return withSecrets
}

// EnsureTalosconfigPath ensures talosconfig path is set to project root if not explicitly set via flag.
func EnsureTalosconfigPath(cmd *cobra.Command) {
	if cmd.PersistentFlags().Changed("talosconfig") {
		return
	}

	var talosconfigPath string
	if GlobalArgs.Talosconfig != "" {
		// Use existing path from Chart.yaml or default
		talosconfigPath = GlobalArgs.Talosconfig
	} else {
		// Use talosconfig from project root
		talosconfigPath = Config.GlobalOptions.Talosconfig
		if talosconfigPath == "" {
			talosconfigPath = "talosconfig"
		}
	}
	// Make it absolute path relative to project root if it's relative
	if !filepath.IsAbs(talosconfigPath) {
		GlobalArgs.Talosconfig = filepath.Join(Config.RootDir, talosconfigPath)
	} else {
		GlobalArgs.Talosconfig = talosconfigPath
	}
}

// ExpandFilePaths expands file paths: if a path is a directory, finds all YAML files in it.
// Returns a list of file paths, with directories expanded to their YAML files.
func ExpandFilePaths(paths []string) ([]string, error) {
	var expanded []string
	for _, path := range paths {
		absPath, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("failed to get absolute path for %s: %w", path, err)
		}

		info, err := os.Stat(absPath)
		if err != nil {
			// If path doesn't exist, treat it as a file (let the caller handle the error)
			expanded = append(expanded, absPath)
			continue
		}

		if info.IsDir() {
			// Find all YAML files in the directory
			yamlFiles, err := findYAMLFiles(absPath)
			if err != nil {
				return nil, fmt.Errorf("failed to find YAML files in %s: %w", path, err)
			}
			if len(yamlFiles) == 0 {
				return nil, fmt.Errorf("no YAML files found in directory %s", path)
			}
			expanded = append(expanded, yamlFiles...)
		} else {
			// It's a file, add it as is
			expanded = append(expanded, absPath)
		}
	}
	return expanded, nil
}

// findYAMLFiles recursively finds all YAML files in a directory.
func findYAMLFiles(dir string) ([]string, error) {
	var yamlFiles []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			ext := filepath.Ext(path)
			if ext == ".yaml" || ext == ".yml" {
				absPath, err := filepath.Abs(path)
				if err != nil {
					return err
				}
				yamlFiles = append(yamlFiles, absPath)
			}
		}
		return nil
	})
	return yamlFiles, err
}

