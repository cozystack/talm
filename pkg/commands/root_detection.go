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
	"os"
	"path/filepath"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/spf13/cobra"
)

// localSecretsYamlName is the on-disk name of the plaintext secrets file
// used by ResolveSecretsPath when the operator did not pass --with-secrets.
// Defined as a file-local const so the goconst linter has a single
// canonical reference for the seven occurrences in this file alone.
const localSecretsYamlName = "secrets.yaml"

// parseFlagFromArgs parses a flag value from command line arguments.
// Supports both -flag value and -flag=value formats, as well as comma-separated values.
func parseFlagFromArgs(args []string, shortFlag, longFlag string) []string {
	var values []string

	for i, arg := range args {
		switch {
		case arg == shortFlag || arg == longFlag:
			// Get the next argument as a value if it isn't another flag.
			if i+1 < len(args) {
				nextArg := args[i+1]
				if !strings.HasPrefix(nextArg, "-") {
					values = parseCommaSeparatedValues(nextArg)
				}
			}

			return values
		case strings.HasPrefix(arg, shortFlag+"=") || strings.HasPrefix(arg, longFlag+"="):
			// Handle -flag=value or --flag=value form.
			parts := strings.SplitN(arg, "=", 2)
			if len(parts) == 2 {
				values = parseCommaSeparatedValues(parts[1])
			}

			return values
		}
	}

	return values
}

// parseCommaSeparatedValues parses comma-separated values and returns a slice of trimmed values.
func parseCommaSeparatedValues(value string) []string {
	var values []string

	if strings.Contains(value, ",") {
		parts := strings.SplitSeq(value, ",")
		for part := range parts {
			if trimmed := strings.TrimSpace(part); trimmed != "" {
				values = append(values, trimmed)
			}
		}
	} else if trimmed := strings.TrimSpace(value); trimmed != "" {
		values = append(values, trimmed)
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
		return "", errors.Wrap(err, "failed to get current working directory")
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
		return errors.WithHint(
			errors.Newf("conflicting project roots: global --root=%s, but detected root=%s", absConfigRoot, absDetectedRoot),
			"drop --root or move the files so they live under the explicit root",
		)
	}

	return nil
}

// DetectAndSetRoot detects and sets the project root using fallback strategy:
// 1. From -f/--file flag (if files specified)
// 2. From -t/--template flag (if templates specified)
// 3. From current working directory
//
// args is part of the cobra.PositionalArgs / PreRunE signature; the
// function does not consult positional arguments — root selection is
// driven entirely by --root, --file, --template, and the CWD walk-up.
func DetectAndSetRoot(cmd *cobra.Command, _ []string) error {
	// Check if --root was explicitly set. Use cmd.Flag(name).Changed
	// rather than cmd.PersistentFlags().Changed("root"):
	// PersistentFlags lists ONLY flags declared persistent on cmd
	// itself, but --root is declared on rootCmd as a persistent
	// flag, so for any subcommand other than rootCmd itself,
	// cmd.PersistentFlags().Changed("root") returned false even
	// when the operator passed --root explicitly — the entire
	// "operator opted in to a specific root" escape hatch was
	// effectively dead. cmd.Flag(name) walks the inheritance chain
	// (local -> persistent -> parent persistent) and returns the
	// merged flag definition with its real Changed state.
	Config.RootDirExplicit = false
	if flag := cmd.Flag("root"); flag != nil {
		Config.RootDirExplicit = flag.Changed
	}

	configFiles := lookupFileArg(cmd, "file", "-f", "--file")
	templateFiles := lookupFileArg(cmd, "template", "-t", "--template")

	// Strategy 1: Detect root from files
	if applied, err := applyFileBasedRoot(configFiles); err != nil || applied {
		return err
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

// lookupFileArg fetches the named flag value from cobra and falls back
// to scanning os.Args[1:] for short/long forms if cobra returns nothing.
// Split out so DetectAndSetRoot stays linear instead of repeating the
// same fetch-then-fallback pattern for -f and -t.
func lookupFileArg(cmd *cobra.Command, flagName, shortFlag, longFlag string) []string {
	values := getFlagValues(cmd, flagName)
	if len(values) == 0 {
		values = parseFlagFromArgs(os.Args[1:], shortFlag, longFlag)
	}

	return values
}

// applyFileBasedRoot runs strategy 1 of DetectAndSetRoot: if files are
// passed, derive the root from them and pin it after a conflict check.
// Returns (true, nil) when the root was applied (caller should stop),
// (false, nil) when files yielded no root and the caller should fall
// through to the next strategy, and (false, err) on a hard failure.
func applyFileBasedRoot(configFiles []string) (bool, error) {
	if len(configFiles) == 0 {
		return false, nil
	}

	detectedRoot, err := detectRootFromFiles(configFiles)
	if err != nil {
		return false, err
	}

	if detectedRoot == "" {
		return false, nil
	}

	if err := checkRootConflict(detectedRoot, Config.RootDirExplicit); err != nil {
		return false, err
	}

	Config.RootDir = detectedRoot

	return true, nil
}

// DetectAndSetRootFromFiles detects and sets project root from file paths.
// This is a common pattern used in commands like apply, upgrade, and talosctl wrapper.
// It detects root from files if provided, otherwise falls back to current working directory.
func DetectAndSetRootFromFiles(filePaths []string) error {
	if applied, err := applyExplicitFilesRoot(filePaths); err != nil || applied {
		return err
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

// applyExplicitFilesRoot derives Config.RootDir from explicit file
// paths. Returns (true, nil) on success, (false, nil) when filePaths is
// empty or yields no root, and (false, err) on conflict / detection
// failure. Split out of DetectAndSetRootFromFiles to flatten the
// surrounding nestif.
func applyExplicitFilesRoot(filePaths []string) (bool, error) {
	if len(filePaths) == 0 {
		return false, nil
	}

	detectedRoot, err := ValidateAndDetectRootsForFiles(filePaths)
	if err != nil {
		return false, err
	}

	if detectedRoot == "" {
		return false, nil
	}

	absConfigRoot, _ := filepath.Abs(Config.RootDir)
	absDetectedRoot, _ := filepath.Abs(detectedRoot)
	// Root from files has priority unless --root was set explicitly
	// to a different location, in which case the operator's intent
	// must win over the file-derived guess.
	if absConfigRoot != absDetectedRoot && Config.RootDirExplicit {
		return false, errors.WithHint(
			errors.Newf("conflicting project roots: global --root=%s, but files belong to root=%s", absConfigRoot, absDetectedRoot),
			"drop --root or pass files that live under the explicit root",
		)
	}

	Config.RootDir = detectedRoot

	return true, nil
}

// ResolveSecretsPath resolves secrets.yaml path relative to project root if not absolute.
func ResolveSecretsPath(withSecrets string) string {
	if withSecrets == "" {
		withSecrets = localSecretsYamlName
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
			return nil, errors.Wrapf(err, "failed to get absolute path for %s", path)
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
				return nil, errors.Wrapf(err, "failed to find YAML files in %s", path)
			}

			if len(yamlFiles) == 0 {
				return nil, errors.WithHint(
					errors.Newf("no YAML files found in directory %s", path),
					"point at a directory that contains .yaml or .yml files, or pass individual files",
				)
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
			return errors.Wrapf(err, "walking %s", path)
		}

		if !info.IsDir() {
			ext := filepath.Ext(path)
			if ext == ".yaml" || ext == ".yml" {
				absPath, err := filepath.Abs(path)
				if err != nil {
					return errors.Wrapf(err, "failed to get absolute path for %s", path)
				}

				yamlFiles = append(yamlFiles, absPath)
			}
		}

		return nil
	})
	if err != nil {
		return nil, errors.Wrapf(err, "failed to walk directory %s", dir)
	}

	return yamlFiles, nil
}
