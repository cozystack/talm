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
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/cozystack/talm/pkg/age"
	"github.com/cozystack/talm/pkg/generated"
	"github.com/cozystack/talm/pkg/secureperm"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/siderolabs/talos/cmd/talosctl/cmd/mgmt/gen"
	"github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/generate"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	"k8s.io/apimachinery/pkg/util/validation"
)

var initCmdFlags struct {
	force        bool
	preset       string
	name         string
	talosVersion string
	image        string
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

		// --image rewrites the preset's values.yaml at write time, so it
		// only makes sense on initial init. Combining it with --encrypt /
		// --decrypt / --update would let the flag silently disappear —
		// surface the mismatch up front instead.
		if initCmdFlags.image != "" && (initCmdFlags.encrypt || initCmdFlags.decrypt || initCmdFlags.update) {
			return fmt.Errorf("--image is honored on initial init only; not valid with --encrypt, --decrypt, or --update")
		}

		// For -e, -d, and -u flags, always check that we're in a project root
		if initCmdFlags.encrypt || initCmdFlags.decrypt || initCmdFlags.update {
			// Verify that Config.RootDir is actually a project root
			detectedRoot, err := DetectProjectRoot(Config.RootDir)
			if err != nil {
				return fmt.Errorf("failed to verify project root: %w", err)
			}
			if detectedRoot == "" {
				return fmt.Errorf("not in a project root: Chart.yaml and secrets.yaml (or secrets.encrypted.yaml) must exist in %s or parent directories", Config.RootDir)
			}
			// Ensure Config.RootDir is set to the detected root
			absDetectedRoot, _ := filepath.Abs(detectedRoot)
			absConfigRoot, _ := filepath.Abs(Config.RootDir)
			if absDetectedRoot != absConfigRoot {
				Config.RootDir = detectedRoot
			}
		}

		// Preset and name are not required when using --encrypt or --decrypt flags
		if initCmdFlags.encrypt || initCmdFlags.decrypt {
			return nil
		}
		// For --update flag, only preset is required (name is not needed)
		if initCmdFlags.update {
			// Preset validation happens in updateTalmLibraryChart()
			// where it can come from -p flag or Chart.yaml
			return nil
		}
		if initCmdFlags.preset == "" {
			return fmt.Errorf("preset is required (use --preset or -p flag)")
		}
		if initCmdFlags.name == "" {
			return fmt.Errorf("cluster name is required (use --name or -N flag)")
		}
		// Validate the operator-supplied cluster name against the same
		// DNS-1123 subdomain rule the chart helpers enforce at render
		// time. Without this check an invalid name reaches the bundle
		// generator and surfaces as an opaque downstream error; pinning
		// it here means the operator sees the precise upstream message
		// (length, character class, etc.) before any file is written.
		if errs := validation.IsDNS1123Subdomain(initCmdFlags.name); len(errs) > 0 {
			return fmt.Errorf("--name %q is not a valid DNS-1123 subdomain: %s", initCmdFlags.name, strings.Join(errs, "; "))
		}

		// Refuse to init when CWD is inside an existing talm project but
		// the operator did not pass --root explicitly. DetectAndSetRoot
		// (run earlier on this command) walks up from CWD to find a
		// project root, which is right for `apply` / `template` /
		// `talosconfig` but wrong for `init` — without this guard, init
		// silently writes new files into the ancestor project and
		// partially overwrites it. The operator is almost certainly
		// trying to create a NEW project under CWD; tell them how.
		//
		// Bail out if os.Getwd() fails: this is the only point where
		// the guard can compare CWD against the detected ancestor;
		// silently skipping it on a getwd error would fail-open and
		// reintroduce the partial-overlay risk this guard exists to
		// prevent.
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to determine current working directory: %w", err)
		}
		absCwd, _ := filepath.Abs(cwd)
		absRootDir, _ := filepath.Abs(Config.RootDir)
		if !Config.RootDirExplicit && absRootDir != absCwd {
			// %s, not %q: %q calls strconv.Quote which escapes
			// backslashes in Windows paths (C:\Users\... renders as
			// "C:\\Users\\..."), making the message harder to read
			// for the operator and breaking substring tests.
			return fmt.Errorf("refusing to init: %s is inside an existing talm project at %s. To create a new project under the current directory, pass --root . explicitly. To re-initialise the parent, run from the parent directory", absCwd, absRootDir)
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

		// Pre-check every preset-loop destination so a fresh init is
		// all-or-nothing for the chart artefacts. Without this, the
		// previous behaviour failed at the first conflict (typically
		// Chart.yaml) AFTER talosconfig / talm.key /
		// secrets.encrypted.yaml were already on disk — leaving the
		// project in a partially-initialised state scripted callers
		// could not recover from. Files written outside the preset
		// loop (talosconfig, talm.key, secrets.*, kubeconfig.*,
		// .gitignore, nodes/) are not pre-checked here; each has its
		// own per-file existence handling downstream and the dominant
		// failure mode (Chart.yaml conflict) is what this pre-check
		// addresses.
		//
		// Skipped under --encrypt / --decrypt: those flags operate on
		// an already-initialised project where every preset file is
		// expected to exist. Running the pre-check there would refuse
		// the very flows the flags are designed for. Load presetFiles
		// up-front in either case so the later write loop can reuse
		// the same map.
		presetFiles, err := generated.PresetFiles()
		if err != nil {
			return fmt.Errorf("failed to get preset files: %w", err)
		}
		if !initCmdFlags.force && !initCmdFlags.encrypt && !initCmdFlags.decrypt {
			var conflicts []string
			for path := range presetFiles {
				parts := strings.SplitN(path, "/", 2)
				chartName := parts[0]
				var dest string
				// Library chart files always land under charts/talm/.
				// Checked first so a hypothetical preset literally
				// named "talm" cannot collide with the library
				// chart's destination (AvailablePresets excludes
				// "talm" today, but the dispatch should not depend
				// on that invariant).
				switch chartName {
				case "talm":
					dest = filepath.Join(Config.RootDir, "charts", path)
				case initCmdFlags.preset:
					dest = filepath.Join(Config.RootDir, filepath.Join(parts[1:]...))
				default:
					continue
				}
				if _, statErr := os.Stat(dest); statErr == nil {
					conflicts = append(conflicts, dest)
				}
			}
			if len(conflicts) > 0 {
				slices.Sort(conflicts)
				return fmt.Errorf("refusing to init: %d file(s) already exist in target directory; pass --force to overwrite, or --update to refresh only the talm library chart:\n  - %s",
					len(conflicts), strings.Join(conflicts, "\n  - "))
			}
		}

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

		clusterName := initCmdFlags.name

		// Handle talosconfig encryption logic
		talosconfigFile := filepath.Join(Config.RootDir, "talosconfig")
		encryptedTalosconfigFile := filepath.Join(Config.RootDir, "talosconfig.encrypted")
		talosconfigFileExists := fileExists(talosconfigFile)
		encryptedTalosconfigFileExists := fileExists(encryptedTalosconfigFile)

		// If encrypted file exists, decrypt it (don't require key - will generate if needed)
		if encryptedTalosconfigFileExists && !talosconfigFileExists {
			if _, err := handleTalosconfigEncryption(false); err != nil {
				return err
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

			if err = writeSecureToDestination(data, talosconfigFile); err != nil {
				return err
			}
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

		// presetFiles was loaded up-front for the pre-check above and
		// is reused here for the write loop.

		// Validate --image up-front so a flag/preset mismatch fails
		// the command before any file is written.
		if err := validateImageOverride(presetFiles, initCmdFlags.preset, initCmdFlags.image); err != nil {
			return err
		}

		for path, content := range presetFiles {
			parts := strings.SplitN(path, "/", 2)
			chartName := parts[0]
			// Write preset files
			if chartName == initCmdFlags.preset {
				file := filepath.Join(Config.RootDir, filepath.Join(parts[1:]...))
				switch parts[len(parts)-1] {
				case "Chart.yaml":
					err = writeToDestination(fmt.Appendf(nil, content, clusterName, Config.InitOptions.Version), file, 0o644)
				case "values.yaml":
					var rendered []byte
					rendered, err = applyImageOverride([]byte(content), initCmdFlags.image)
					if err != nil {
						return err
					}
					err = writeToDestination(rendered, file, 0o644)
				default:
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
					err = writeToDestination(fmt.Appendf(nil, content, "talm", Config.InitOptions.Version), file, 0o644)
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
	// validateFileExists is invoked inside writeSecureToDestination;
	// no need to duplicate the --force / existing-file gate here.
	return writeSecureToDestination(bundleBytes, secretsFile)
}

// readChartYamlPreset reads Chart.yaml and determines the preset name from dependencies
func readChartYamlPreset() (string, error) {
	chartYamlPath := filepath.Join(Config.RootDir, "Chart.yaml")
	data, err := os.ReadFile(chartYamlPath)
	if err != nil {
		return "", fmt.Errorf("failed to read Chart.yaml: %w", err)
	}

	var chartData struct {
		Dependencies []struct {
			Name string `yaml:"name"`
		} `yaml:"dependencies"`
	}

	if err := yaml.Unmarshal(data, &chartData); err != nil {
		return "", fmt.Errorf("failed to parse Chart.yaml: %w", err)
	}

	// Find preset in dependencies (exclude "talm" which is the library chart)
	for _, dep := range chartData.Dependencies {
		if dep.Name != "talm" {
			return dep.Name, nil
		}
	}

	return "", fmt.Errorf("preset not found in Chart.yaml dependencies")
}

// imageLineRe matches the top-level `image:` line in a preset
// values.yaml regardless of YAML serialization style — double-quoted,
// single-quoted, unquoted, with or without a trailing comment.
// Line-anchored (?m)^image:…$ so a nested key, indented entry, or
// commented `# image:` line is never substituted.
var imageLineRe = regexp.MustCompile(`(?m)^image:.*$`)

// applyImageOverride returns values with the top-level `image:` line
// replaced so it points at override. An empty override returns values
// unchanged. When override is non-empty but values has no top-level
// `image:` line, the helper returns an error rather than silently
// dropping the user's flag — a preset that does not declare an image
// field cannot be customized through this path, and the caller must
// surface that to the user before any file is written.
//
// The override is %q-quoted, which Go-escapes special characters and
// emits a double-quoted string. The substitution goes through
// ReplaceAllFunc rather than ReplaceAll because the latter expands
// `$0` / `$1` / `$name` / `${name}` sequences in the replacement —
// an image reference like `foo/$tenant/bar` would otherwise be
// rewritten to a different image, silently. ReplaceAllFunc returns
// the byte slice verbatim with no $-expansion.
//
// The regex matches every top-level `image:` line via ReplaceAllFunc,
// so a preset that ever declares two top-level image fields would have
// both rewritten to the same value. Today only `cozystack` has one
// occurrence; a future preset that breaks this assumption surfaces
// here as a behaviour change worth catching at review.
func applyImageOverride(values []byte, override string) ([]byte, error) {
	if override == "" {
		return values, nil
	}
	// In the talm init flow this guard is redundant: RunE invokes
	// validateImageOverride against the same byte content from
	// presetFiles before this loop runs, so a missing image: field
	// fails the command up front. The check is kept as defense in
	// depth for direct callers (unit tests, future code paths that
	// might skip the validator) so the helper is safe in isolation.
	if !imageLineRe.Match(values) {
		return nil, fmt.Errorf("--image was set but the preset values.yaml does not declare a top-level image: field; remove --image, choose a different preset, or add the image field manually")
	}
	replacement := fmt.Appendf(nil, "image: %q", override)
	return imageLineRe.ReplaceAllFunc(values, func([]byte) []byte {
		return replacement
	}), nil
}

// validateImageOverride scans presetFiles for the chosen preset's
// values.yaml and confirms a top-level image line is present when
// the user passed --image. The check runs before any file is written
// so a flag-vs-preset mismatch fails the command up front instead of
// leaving a half-initialized project on disk.
func validateImageOverride(presetFiles map[string]string, presetName, override string) error {
	if override == "" {
		return nil
	}
	for path, content := range presetFiles {
		parts := strings.SplitN(path, "/", 2)
		if len(parts) != 2 || parts[0] != presetName {
			continue
		}
		if parts[1] != "values.yaml" {
			continue
		}
		if !imageLineRe.MatchString(content) {
			return fmt.Errorf("--image was set but preset %q does not declare a top-level image: field in values.yaml; remove --image or choose a preset that exposes it (e.g. cozystack)", presetName)
		}
		return nil
	}
	return fmt.Errorf("--image was set but preset %q has no values.yaml in the embedded chart files", presetName)
}

// askUserOverwrite asks user if they want to overwrite a file
func askUserOverwrite(filePath string) (bool, error) {
	// Show relative path from project root
	relPath, err := filepath.Rel(Config.RootDir, filePath)
	if err != nil {
		// If we can't get relative path, use absolute
		relPath = filePath
	}

	reader := bufio.NewReader(os.Stdin)
	fmt.Fprintf(os.Stderr, "File %s differs from template. Overwrite? [y/N]: ", relPath)
	response, err := reader.ReadString('\n')
	if err != nil {
		return false, err
	}
	response = strings.TrimSpace(strings.ToLower(response))
	return response == "y" || response == "yes", nil
}

// filesDiffer checks if two files have different content
func filesDiffer(filePath string, newContent []byte) (bool, error) {
	existingContent, err := os.ReadFile(filePath)
	if err != nil {
		// File doesn't exist, so it differs
		if os.IsNotExist(err) {
			return true, nil
		}
		return false, err
	}
	return string(existingContent) != string(newContent), nil
}

// updateFileWithConfirmation updates a file if it differs, asking user for confirmation
func updateFileWithConfirmation(filePath string, newContent []byte, permissions os.FileMode) error {
	// Check if file exists
	exists := fileExists(filePath)

	if !exists {
		// File doesn't exist, create it without asking
		parentDir := filepath.Dir(filePath)
		if err := os.MkdirAll(parentDir, os.ModePerm); err != nil {
			return fmt.Errorf("failed to create output dir: %w", err)
		}
		if err := os.WriteFile(filePath, newContent, permissions); err != nil {
			return fmt.Errorf("failed to write file: %w", err)
		}
		// Show relative path from project root
		relPath, err := filepath.Rel(Config.RootDir, filePath)
		if err != nil {
			relPath = filePath
		}
		fmt.Fprintf(os.Stderr, "Created %s\n", relPath)
		return nil
	}

	// File exists, check if content differs
	differs, err := filesDiffer(filePath, newContent)
	if err != nil {
		return err
	}

	if !differs {
		// File is the same, skip silently
		return nil
	}

	// File differs, ask user
	overwrite, err := askUserOverwrite(filePath)
	if err != nil {
		return fmt.Errorf("failed to read user input: %w", err)
	}

	if !overwrite {
		fmt.Fprintf(os.Stderr, "Skipping %s\n", filePath)
		return nil
	}

	// Write file
	parentDir := filepath.Dir(filePath)
	if err := os.MkdirAll(parentDir, os.ModePerm); err != nil {
		return fmt.Errorf("failed to create output dir: %w", err)
	}

	if err := os.WriteFile(filePath, newContent, permissions); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	// Show relative path from project root
	relPath, err := filepath.Rel(Config.RootDir, filePath)
	if err != nil {
		relPath = filePath
	}
	fmt.Fprintf(os.Stderr, "Updated %s\n", relPath)
	return nil
}

func updateTalmLibraryChart() error {
	// --image is only honored on initial init (it customizes the
	// preset's values.yaml at write time). Refusing it on --update
	// surfaces the no-op trap explicitly instead of letting the
	// user's flag silently disappear.
	if initCmdFlags.image != "" {
		return fmt.Errorf("--image is honored on initial init only; for an existing project, edit the image field in values.yaml directly")
	}

	// Determine preset: use -p flag if provided, otherwise try to read from Chart.yaml
	var presetName string

	if initCmdFlags.preset != "" {
		// Use preset from flag
		presetName = initCmdFlags.preset
		// Validate preset
		availablePresets, err := generated.AvailablePresets()
		if err != nil {
			return fmt.Errorf("failed to get available presets: %w", err)
		}
		if !isValidPreset(presetName, availablePresets) {
			return fmt.Errorf("invalid preset: %s. Valid presets are: %v", presetName, availablePresets)
		}
	} else {
		// Try to read from Chart.yaml
		var err error
		presetName, err = readChartYamlPreset()
		if err != nil {
			return fmt.Errorf("preset is required: use --preset flag or ensure Chart.yaml has a preset dependency: %w", err)
		}
	}

	presetFiles, err := generated.PresetFiles()
	if err != nil {
		return fmt.Errorf("failed to get preset files: %w", err)
	}

	// Step 1: Update talm library chart files (without interactive confirmation)
	fmt.Fprintf(os.Stderr, "Updating talm library chart...\n")
	for path, content := range presetFiles {
		parts := strings.SplitN(path, "/", 2)
		chartName := parts[0]
		if chartName == "talm" {
			file := filepath.Join(Config.RootDir, filepath.Join("charts", path))
			var fileContent []byte
			if parts[len(parts)-1] == "Chart.yaml" {
				fileContent = fmt.Appendf(nil, content, "talm", Config.InitOptions.Version)
			} else {
				fileContent = []byte(content)
			}
			// For talm library, always update without asking
			parentDir := filepath.Dir(file)
			if err := os.MkdirAll(parentDir, os.ModePerm); err != nil {
				return fmt.Errorf("failed to create output dir: %w", err)
			}
			if err := os.WriteFile(file, fileContent, 0o644); err != nil {
				return fmt.Errorf("failed to write file: %w", err)
			}
			relPath, _ := filepath.Rel(Config.RootDir, file)
			fmt.Fprintf(os.Stderr, "Updated %s\n", relPath)
		}
	}

	// Step 2: Update preset template files (with interactive confirmation)
	if presetName != "" {
		fmt.Fprintf(os.Stderr, "Updating preset templates...\n")
		for path, content := range presetFiles {
			parts := strings.SplitN(path, "/", 2)
			chartName := parts[0]
			if chartName == presetName {
				file := filepath.Join(Config.RootDir, filepath.Join(parts[1:]...))
				var fileContent []byte
				if parts[len(parts)-1] == "Chart.yaml" {
					// Read cluster name from existing Chart.yaml
					existingChartPath := filepath.Join(Config.RootDir, "Chart.yaml")
					existingData, err := os.ReadFile(existingChartPath)
					if err != nil {
						return fmt.Errorf("failed to read existing Chart.yaml: %w", err)
					}
					var existingChart struct {
						Name string `yaml:"name"`
					}
					if err := yaml.Unmarshal(existingData, &existingChart); err != nil {
						return fmt.Errorf("failed to parse existing Chart.yaml: %w", err)
					}
					fileContent = fmt.Appendf(nil, content, existingChart.Name, Config.InitOptions.Version)
				} else {
					fileContent = []byte(content)
				}
				if err := updateFileWithConfirmation(file, fileContent, 0o644); err != nil {
					return err
				}
			}
		}
	}

	return nil
}

func init() {
	initCmd.Flags().StringVar(&initCmdFlags.talosVersion, "talos-version", "", "the desired Talos version to generate config for (backwards compatibility, e.g. v0.8)")
	initCmd.Flags().StringVarP(&initCmdFlags.preset, "preset", "p", "", "preset for file generation (not required with --encrypt, --decrypt, or --update)")
	initCmd.Flags().StringVarP(&initCmdFlags.name, "name", "N", "", "cluster name (not required with --encrypt, --decrypt, or --update)")
	initCmd.Flags().StringVar(&initCmdFlags.image, "image", "", "override the Talos installer image written to the preset's values.yaml (e.g. factory.talos.dev/installer/<sha256>:<version>)")
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
	return slices.Contains(availablePresets, preset)
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

	// Add kubeconfig to required entries (use path from config or default)
	kubeconfigPath := Config.GlobalOptions.Kubeconfig
	if kubeconfigPath == "" {
		kubeconfigPath = "kubeconfig"
	}
	// Only add base name (not full path) to gitignore
	kubeconfigBase := filepath.Base(kubeconfigPath)
	requiredEntries = append(requiredEntries, kubeconfigBase)

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
		}

		// Encrypt talosconfig
		fmt.Fprintf(os.Stderr, "Encrypting talosconfig -> talosconfig.encrypted\n")
		if err := age.EncryptYAMLFile(Config.RootDir, "talosconfig", "talosconfig.encrypted"); err != nil {
			return false, fmt.Errorf("failed to encrypt talosconfig: %w", err)
		}
	}

	return keyWasCreated, nil
}

// createdSink is where "Created <path>" messages go after a successful
// write. Swappable in tests to assert no message is emitted on failure.
var createdSink io.Writer = os.Stderr

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
	if err == nil {
		_, _ = fmt.Fprintf(createdSink, "Created %s\n", destination)
	}
	return err
}

// writeSecureToDestination writes a secret (talosconfig, secrets.yaml,
// talm.key) with owner-only permissions. On Windows the NTFS DACL is
// installed via secureperm so os.WriteFile's ignored mode bits aren't
// the only defense.
func writeSecureToDestination(data []byte, destination string) error {
	if err := validateFileExists(destination); err != nil {
		return err
	}

	parentDir := filepath.Dir(destination)

	// Use 0o700 so any newly-created parent dir for secrets is owner-only
	// even under a permissive umask. MkdirAll is a no-op when the dir
	// already exists, so this does not override pre-existing dir perms.
	if err := os.MkdirAll(parentDir, 0o700); err != nil {
		return fmt.Errorf("failed to create output dir: %w", err)
	}

	err := secureperm.WriteFile(destination, data)
	if err == nil {
		_, _ = fmt.Fprintf(createdSink, "Created %s\n", destination)
	}
	return err
}
