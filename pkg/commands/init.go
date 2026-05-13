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
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/cockroachdb/errors"
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

// Init-flow file and chart name constants. Cross-subcommand strings
// (initSubcommand, chartYamlName, defaultKubeconfigName,
// defaultLocalEndpoint) live in consts.go.
const (
	// secretsYamlName is the unencrypted secrets manifest written
	// during init and consumed by chart rendering.
	secretsYamlName = "secrets.yaml"
	// secretsEncryptedYamlName is the age-encrypted secrets manifest
	// committed to git in encrypted form.
	secretsEncryptedYamlName = "secrets.encrypted.yaml"
	// talmKeyName is the age private-key file the init flow generates
	// alongside the encrypted bundle; it is what `.gitignore` excludes
	// and what tests pin as a sensitive artefact name.
	talmKeyName = "talm.key"
	// talosconfigName is the talosctl client-config filename written
	// during init and rotated by --encrypt/--decrypt.
	talosconfigName = "talosconfig"
	// valuesYamlName is the chart's values.yaml manifest filename;
	// the init flow rewrites it when --image is provided and reads
	// it from updateTalmLibraryChart.
	valuesYamlName = "values.yaml"
	// presetTalmLibrary is the name of the bundled library chart;
	// it ships with every preset and is excluded from preset-name
	// detection.
	presetTalmLibrary = "talm"
	// presetFileMode is the permission applied to chart artefacts
	// (Chart.yaml, values.yaml, helpers, templates) that are not
	// secret-bearing.
	presetFileMode os.FileMode = 0o644
	// secureDirMode is the permission used for parent directories of
	// secret-bearing files so an over-permissive umask cannot widen
	// access.
	secureDirMode os.FileMode = 0o700
	// reportVerbCreated and reportVerbUpdated are the operator-facing
	// verbs the init flow prints when materialising or rewriting a
	// project artefact. Hoisted so goconst sees a single canonical
	// reference for each.
	reportVerbCreated = "Created"
	reportVerbUpdated = "Updated"
)

// resolveTalosconfigEndpoints picks the endpoint list to embed in
// the generated talosconfig's context. If the operator passed
// --endpoints, those values propagate; otherwise we seed the
// loopback placeholder so the rendered context is yaml-valid (a
// talosconfig context with empty endpoints fails downstream
// client construction). The operator edits the placeholder to a
// real endpoint after init if they did not supply --endpoints.
//
// Earlier versions hardcoded defaultLocalEndpoint at the
// assignment site and silently discarded the operator's
// --endpoints flag; the helper centralises the resolution so a
// future caller (e.g. talosconfig regenerate flow) inherits the
// same shape.
//
// Returns a fresh slice so callers mutating the talosconfig field
// don't alias the operator-visible GlobalArgs.Endpoints across
// init invocations in the same process.
func resolveTalosconfigEndpoints(globalEndpoints []string) []string {
	if len(globalEndpoints) > 0 {
		return append([]string(nil), globalEndpoints...)
	}

	return []string{defaultLocalEndpoint}
}

// initCmdFlags is the package-level flag struct backing the init
// subcommand; cobra binds Flags() entries directly to these fields,
// which forces a global. The global also exposes the flag values to
// helpers (validateImageOverride, updateTalmLibraryChart) that share
// the same configuration without threading it through every signature.
//
//nolint:gochecknoglobals // cobra flag binding requires a stable address
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
//
//nolint:gochecknoglobals // cobra command registration requires a package-level value
var initCmd = &cobra.Command{
	Use:   initSubcommand,
	Short: "Initialize a new project and generate default values",
	Long:  ``,
	Args:  cobra.NoArgs,
	PreRunE: func(cmd *cobra.Command, _ []string) error {
		if !cmd.Flags().Changed("talos-version") {
			initCmdFlags.talosVersion = Config.TemplateOptions.TalosVersion
		}

		// --image rewrites the preset's values.yaml at write time, so it
		// only makes sense on initial init. Combining it with --encrypt /
		// --decrypt / --update would let the flag silently disappear —
		// surface the mismatch up front instead.
		if initCmdFlags.image != "" && (initCmdFlags.encrypt || initCmdFlags.decrypt || initCmdFlags.update) {
			return errors.WithHint(
				errors.New("--image is honored on initial init only; not valid with --encrypt, --decrypt, or --update"),
				"drop --image, or run init without --encrypt/--decrypt/--update",
			)
		}

		// For -e, -d, and -u flags, always check that we're in a project root
		if initCmdFlags.encrypt || initCmdFlags.decrypt || initCmdFlags.update {
			// Verify that Config.RootDir is actually a project root
			detectedRoot, err := DetectProjectRoot(Config.RootDir)
			if err != nil {
				return errors.Wrap(err, "failed to verify project root")
			}

			if detectedRoot == "" {
				return errors.WithHintf(
					errors.Newf("not in a project root: Chart.yaml and secrets.yaml (or secrets.encrypted.yaml) must exist in %s or parent directories", Config.RootDir),
					"run from a project directory or pass --root explicitly",
				)
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
			return errors.WithHint(
				errors.New("preset is required (use --preset or -p flag)"),
				"pass --preset cozystack (or another available preset) to choose a chart layout",
			)
		}

		if initCmdFlags.name == "" {
			return errors.WithHint(
				errors.New("cluster name is required (use --name or -N flag)"),
				"pass --name <cluster-name> to set the new project's cluster identifier",
			)
		}
		// Validate the operator-supplied cluster name against the same
		// DNS-1123 subdomain rule the chart helpers enforce at render
		// time. Without this check an invalid name reaches the bundle
		// generator and surfaces as an opaque downstream error; pinning
		// it here means the operator sees the precise upstream message
		// (length, character class, etc.) before any file is written.
		if errs := validation.IsDNS1123Subdomain(initCmdFlags.name); len(errs) > 0 {
			return errors.WithHintf(
				errors.Newf("--name %q is not a valid DNS-1123 subdomain: %s", initCmdFlags.name, strings.Join(errs, "; ")),
				"cluster names must be lowercase, alphanumeric or '-'/'.', and start/end with an alphanumeric character",
			)
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
			return errors.Wrap(err, "failed to determine current working directory")
		}

		absCwd, err := filepath.Abs(cwd)
		if err != nil {
			return errors.Wrap(err, "failed to resolve absolute path of current working directory")
		}
		// Config.RootDir may be a relative path (defaults to ".");
		// filepath.Abs calls os.Getwd internally for relative inputs,
		// which can fail under TOCTOU (CWD removed between the call
		// above and here). Treat that the same as the Getwd guard
		// above — fail closed rather than silently zero out absRootDir
		// and let the comparison go the wrong way.
		absRootDir, err := filepath.Abs(Config.RootDir)
		if err != nil {
			return errors.Wrapf(err, "failed to resolve absolute path of project root %q", Config.RootDir)
		}

		if !Config.RootDirExplicit && absRootDir != absCwd {
			// %s, not %q: %q calls strconv.Quote which escapes
			// backslashes in Windows paths (C:\Users\... renders as
			// "C:\\Users\\..."), making the message harder to read
			// for the operator and breaking substring tests.
			return errors.WithHint(
				errors.Newf("refusing to init: %s is inside an existing talm project at %s. To create a new project under the current directory, pass --root . explicitly. To re-initialise the parent, run from the parent directory", absCwd, absRootDir),
				"pass --root . to create a new project here, or run from the parent directory to re-initialise it",
			)
		}

		return nil
	},
	RunE: func(_ *cobra.Command, _ []string) error {
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
				return errors.Wrap(err, "invalid talos-version")
			}
		}

		secretsBundle, err = secrets.NewBundle(secrets.NewFixedClock(time.Now()),
			versionContract,
		)
		if err != nil {
			return errors.Wrap(err, "failed to create secrets bundle")
		}

		var genOptions []generate.Option

		// Validate preset only if not using --encrypt or --decrypt
		if !initCmdFlags.encrypt && !initCmdFlags.decrypt {
			availablePresets, err := generated.AvailablePresets()
			if err != nil {
				return errors.Wrap(err, "failed to get available presets")
			}

			if !isValidPreset(initCmdFlags.preset, availablePresets) {
				return errors.WithHintf(
					errors.Newf("invalid preset: %s. Valid presets are: %v", initCmdFlags.preset, availablePresets),
					"pick one of the listed presets and pass it via --preset",
				)
			}
		}

		if initCmdFlags.talosVersion != "" {
			var versionContract *config.VersionContract

			versionContract, err = config.ParseContractFromVersion(initCmdFlags.talosVersion)
			if err != nil {
				return errors.Wrap(err, "invalid talos-version")
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
		// the very flows the flags are designed for. Both flags
		// also early-return below before the write loop reaches
		// presetFiles, so loading the map at all is wasted work
		// under those flags — gate the load on the same condition.
		var presetFiles map[string]string
		if !initCmdFlags.encrypt && !initCmdFlags.decrypt {
			presetFiles, err = generated.PresetFiles()
			if err != nil {
				return errors.Wrap(err, "failed to get preset files")
			}

			if !initCmdFlags.force {
				var conflicts []string

				for path := range presetFiles {
					parts := strings.SplitN(path, "/", 2)
					// PresetFiles walks an embed.FS so it only ever
					// returns real file paths (chart/file...), never
					// bare directory entries. Defensive guard anyway:
					// if a future change to PresetFiles ever surfaced a
					// path with no separator, parts[1:] would be empty
					// and dest would resolve to Config.RootDir — which
					// always exists, producing a guaranteed false
					// positive that blocks every init.
					if len(parts) < 2 {
						continue
					}

					chartName := parts[0]

					var dest string
					// Library chart files always land under charts/talm/.
					// Checked first so a hypothetical preset literally
					// named "talm" cannot collide with the library
					// chart's destination (AvailablePresets excludes
					// "talm" today, but the dispatch should not depend
					// on that invariant).
					switch chartName {
					case presetTalmLibrary:
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

					return errors.WithHint(
						errors.Newf("refusing to init: %d file(s) already exist in target directory; pass --force to overwrite, or --update to refresh only the talm library chart:\n  - %s",
							len(conflicts), strings.Join(conflicts, "\n  - ")),
						"rerun with --force to overwrite, --update to refresh only the talm library chart, or remove the listed files manually",
					)
				}
			}
		}

		// Handle age encryption logic
		secretsFile := filepath.Join(Config.RootDir, secretsYamlName)
		encryptedSecretsFile := filepath.Join(Config.RootDir, secretsEncryptedYamlName)
		keyFile := filepath.Join(Config.RootDir, talmKeyName)

		secretsFileExists := fileExists(secretsFile)
		encryptedSecretsFileExists := fileExists(encryptedSecretsFile)
		keyFileExists := fileExists(keyFile)
		keyWasCreated := false // Track if key was created during this init

		// Check for invalid state: encrypted file exists but secrets.yaml and key don't
		if encryptedSecretsFileExists && !secretsFileExists && !keyFileExists {
			return errors.WithHint(
				errors.New("secrets.encrypted.yaml exists but secrets.yaml and talm.key are missing. Cannot decrypt without key"),
				"restore talm.key from your backup, or recreate the project from scratch if the key is unrecoverable",
			)
		}

		// Handle --encrypt flag (early return, doesn't need preset)
		if initCmdFlags.encrypt {
			// Ensure key exists before encryption
			keyFile := filepath.Join(Config.RootDir, talmKeyName)
			keyFileExists := fileExists(keyFile)

			if !keyFileExists {
				_, keyCreated, err := age.GenerateKey(Config.RootDir)
				if err != nil {
					return errors.Wrap(err, "failed to generate key")
				}

				if keyCreated {
					fmt.Fprintf(os.Stderr, "Generated new encryption key: talm.key\n")
					printSecretsWarning()
				}
			}

			// Encrypt all sensitive files
			secretsFile := filepath.Join(Config.RootDir, secretsYamlName)
			talosconfigFile := filepath.Join(Config.RootDir, "talosconfig")

			kubeconfigPath := Config.GlobalOptions.Kubeconfig
			if kubeconfigPath == "" {
				kubeconfigPath = defaultKubeconfigName
			}

			kubeconfigFile := filepath.Join(Config.RootDir, kubeconfigPath)

			encryptedCount := 0

			// Encrypt secrets.yaml
			if fileExists(secretsFile) {
				fmt.Fprintf(os.Stderr, "Encrypting secrets.yaml -> secrets.encrypted.yaml\n")

				err := age.EncryptSecretsFile(Config.RootDir)
				if err != nil {
					return errors.Wrap(err, "failed to encrypt secrets")
				}

				encryptedCount++
			}

			// Encrypt talosconfig
			if fileExists(talosconfigFile) {
				fmt.Fprintf(os.Stderr, "Encrypting talosconfig -> talosconfig.encrypted\n")

				err := age.EncryptYAMLFile(Config.RootDir, "talosconfig", "talosconfig.encrypted")
				if err != nil {
					return errors.Wrap(err, "failed to encrypt talosconfig")
				}

				encryptedCount++
			} else {
				fmt.Fprintf(os.Stderr, "Skipping talosconfig (file not found)\n")
			}

			// Encrypt kubeconfig
			if fileExists(kubeconfigFile) {
				fmt.Fprintf(os.Stderr, "Encrypting %s -> %s.encrypted\n", kubeconfigPath, kubeconfigPath)

				err = age.EncryptYAMLFile(Config.RootDir, kubeconfigPath, kubeconfigPath+".encrypted")
				if err != nil {
					return errors.Wrap(err, "failed to encrypt kubeconfig")
				}

				encryptedCount++
			} else {
				fmt.Fprintf(os.Stderr, "Skipping %s (file not found)\n", kubeconfigPath)
			}

			// Update .gitignore file
			err = writeGitignoreFile()
			if err != nil {
				return errors.Wrap(err, "failed to update .gitignore")
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
			encryptedSecretsFile := filepath.Join(Config.RootDir, secretsEncryptedYamlName)
			encryptedTalosconfigFile := filepath.Join(Config.RootDir, "talosconfig.encrypted")

			kubeconfigPath := Config.GlobalOptions.Kubeconfig
			if kubeconfigPath == "" {
				kubeconfigPath = defaultKubeconfigName
			}

			encryptedKubeconfigFile := filepath.Join(Config.RootDir, kubeconfigPath+".encrypted")

			decryptedCount := 0

			// Decrypt secrets.encrypted.yaml
			if fileExists(encryptedSecretsFile) {
				fmt.Fprintf(os.Stderr, "Decrypting secrets.encrypted.yaml -> secrets.yaml\n")

				if err := age.DecryptSecretsFile(Config.RootDir); err != nil {
					return errors.Wrap(err, "failed to decrypt secrets")
				}

				decryptedCount++
			} else {
				fmt.Fprintf(os.Stderr, "Skipping secrets.encrypted.yaml (file not found)\n")
			}

			// Decrypt talosconfig.encrypted
			if fileExists(encryptedTalosconfigFile) {
				fmt.Fprintf(os.Stderr, "Decrypting talosconfig.encrypted -> talosconfig\n")

				if err := age.DecryptYAMLFile(Config.RootDir, "talosconfig.encrypted", "talosconfig"); err != nil {
					return errors.Wrap(err, "failed to decrypt talosconfig")
				}

				decryptedCount++
			} else {
				fmt.Fprintf(os.Stderr, "Skipping talosconfig.encrypted (file not found)\n")
			}

			// Decrypt kubeconfig.encrypted
			if fileExists(encryptedKubeconfigFile) {
				fmt.Fprintf(os.Stderr, "Decrypting %s.encrypted -> %s\n", kubeconfigPath, kubeconfigPath)

				if err := age.DecryptYAMLFile(Config.RootDir, kubeconfigPath+".encrypted", kubeconfigPath); err != nil {
					return errors.Wrap(err, "failed to decrypt kubeconfig")
				}

				decryptedCount++
			} else {
				fmt.Fprintf(os.Stderr, "Skipping %s.encrypted (file not found)\n", kubeconfigPath)
			}

			// Update .gitignore file
			if err := writeGitignoreFile(); err != nil {
				return errors.Wrap(err, "failed to update .gitignore")
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
				return errors.Wrap(err, "failed to decrypt secrets")
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
					return errors.Wrap(err, "failed to generate key")
				}

				keyFileExists = true // Update flag after creation
				keyWasCreated = keyCreated
			}

			// Encrypt secrets
			if err := age.EncryptSecretsFile(Config.RootDir); err != nil {
				return errors.Wrap(err, "failed to encrypt secrets")
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
				return errors.Wrap(err, "generating talos config bundle")
			}

			configBundle.TalosConfig().Contexts[clusterName].Endpoints = resolveTalosconfigEndpoints(GlobalArgs.Endpoints)

			data, err := yaml.Marshal(configBundle.TalosConfig())
			if err != nil {
				return errors.Wrap(err, "failed to marshal config")
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
			kubeconfigPath = defaultKubeconfigName
		}

		kubeconfigFile := filepath.Join(Config.RootDir, kubeconfigPath)
		encryptedKubeconfigFile := filepath.Join(Config.RootDir, kubeconfigPath+".encrypted")
		kubeconfigFileExists := fileExists(kubeconfigFile)
		encryptedKubeconfigFileExists := fileExists(encryptedKubeconfigFile)

		// If encrypted file exists, decrypt it
		if encryptedKubeconfigFileExists && !kubeconfigFileExists {
			if err := age.DecryptYAMLFile(Config.RootDir, kubeconfigPath+".encrypted", kubeconfigPath); err != nil {
				return errors.Wrap(err, "failed to decrypt kubeconfig")
			}

			kubeconfigFileExists = true
		}

		// If kubeconfig exists but encrypted file doesn't, encrypt it
		if kubeconfigFileExists && !encryptedKubeconfigFileExists {
			// Ensure key exists
			if !keyFileExists {
				_, keyCreated, err := age.GenerateKey(Config.RootDir)
				if err != nil {
					return errors.Wrap(err, "failed to generate key")
				}

				keyWasCreated = keyCreated
			}

			// Encrypt kubeconfig
			if err := age.EncryptYAMLFile(Config.RootDir, kubeconfigPath, kubeconfigPath+".encrypted"); err != nil {
				return errors.Wrap(err, "failed to encrypt kubeconfig")
			}
		}

		// Create or update .gitignore file
		if err = writeGitignoreFile(); err != nil {
			return err
		}

		nodesDir := filepath.Join(Config.RootDir, "nodes")
		if err := os.MkdirAll(nodesDir, os.ModePerm); err != nil {
			return errors.Wrap(err, "failed to create nodes directory")
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
				case chartYamlName:
					err = writeToDestination(fmt.Appendf(nil, content, clusterName, Config.InitOptions.Version), file, presetFileMode)
				case valuesYamlName:
					var rendered []byte

					rendered, err = applyImageOverride([]byte(content), initCmdFlags.image)
					if err != nil {
						return err
					}

					err = writeToDestination(rendered, file, presetFileMode)
				default:
					err = writeToDestination([]byte(content), file, presetFileMode)
				}

				if err != nil {
					return err
				}
			}
			// Write library chart
			if chartName == presetTalmLibrary {
				file := filepath.Join(Config.RootDir, filepath.Join("charts", path))
				if parts[len(parts)-1] == chartYamlName {
					err = writeToDestination(fmt.Appendf(nil, content, presetTalmLibrary, Config.InitOptions.Version), file, presetFileMode)
				} else {
					err = writeToDestination([]byte(content), file, presetFileMode)
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
		return errors.Wrap(err, "marshalling secrets bundle")
	}

	secretsFile := filepath.Join(Config.RootDir, secretsYamlName)
	// validateFileExists is invoked inside writeSecureToDestination;
	// no need to duplicate the --force / existing-file gate here.
	return writeSecureToDestination(bundleBytes, secretsFile)
}

// readChartYamlPreset reads Chart.yaml and determines the preset name from dependencies.
func readChartYamlPreset() (string, error) {
	chartYamlPath := filepath.Join(Config.RootDir, chartYamlName)

	data, err := os.ReadFile(chartYamlPath)
	if err != nil {
		return "", errors.Wrap(err, "failed to read Chart.yaml")
	}

	var chartData struct {
		Dependencies []struct {
			Name string `yaml:"name"`
		} `yaml:"dependencies"`
	}

	if err := yaml.Unmarshal(data, &chartData); err != nil {
		return "", errors.Wrap(err, "failed to parse Chart.yaml")
	}

	// Find preset in dependencies (exclude "talm" which is the library chart)
	for _, dep := range chartData.Dependencies {
		if dep.Name != presetTalmLibrary {
			return dep.Name, nil
		}
	}

	//nolint:wrapcheck // cockroachdb/errors.WithHint at boundary.
	return "", errors.WithHint(
		errors.New("preset not found in Chart.yaml dependencies"),
		"add a preset chart (e.g. cozystack) to Chart.yaml's dependencies, or pass --preset on the command line",
	)
}

// imageLineRe matches the top-level `image:` line in a preset
// values.yaml regardless of YAML serialization style — double-quoted,
// single-quoted, unquoted, with or without a trailing comment.
//
// Line-anchored (?m)^image: requires at least one space (or end-of-
// line) after the colon. Without the space requirement the regex
// would also match `image:noSpaceValue`, which is not valid YAML
// (yaml.v3 rejects key:value with no space after colon) — silently
// rewriting a broken preset masks the underlying preset bug. With
// the space requirement, only valid YAML key-value pairs match.
var imageLineRe = regexp.MustCompile(`(?m)^image:(\s|$).*$`)

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
		//nolint:wrapcheck // cockroachdb/errors.WithHint at boundary.
		return nil, errors.WithHint(
			errors.New("--image was set but the preset values.yaml does not declare a top-level image: field; remove --image, choose a different preset, or add the image field manually"),
			"choose a preset that exposes a top-level image: field (e.g. cozystack), or omit --image",
		)
	}

	replacement := fmt.Appendf(nil, "image: %q", override)

	return imageLineRe.ReplaceAllFunc(values, func([]byte) []byte {
		return replacement
	}), nil
}

// validateImageRefShape rejects --image values that cannot be a
// plausible installer image reference. The check is intentionally
// loose — it does not vouch for the image existing in any registry
// or for the tag being a valid Talos version — only that the value
// has the structural shape of an OCI ref: a registry-or-path
// component, at least one path separator, and either a ":TAG"
// suffix or an "@sha256:" / "@sha512:" digest pin.
//
// Catches: bare ":malformed", "no-slash:tag" (docker-hub-style
// shortcuts that won't resolve in the talm context), trailing
// "/" with no tag, empty-tag colon prefixes. Misses (intentionally):
// non-existent registries, invalid version tags, unsupported
// platforms — those surface at apply time as expected.
func validateImageRefShape(ref string) error {
	if strings.HasPrefix(ref, ":") || strings.HasPrefix(ref, "@") || strings.HasPrefix(ref, "/") {
		//nolint:wrapcheck // cockroachdb/errors.WithHint at boundary.
		return errors.WithHint(
			errors.Newf("--image %q is malformed: starts with a reserved separator (':' or '@' or '/')", ref),
			"pass a full image reference such as 'ghcr.io/siderolabs/installer:v1.13.0' or 'factory.talos.dev/installer/<sha>:<version>'",
		)
	}

	if strings.HasSuffix(ref, ":") || strings.HasSuffix(ref, "/") || strings.HasSuffix(ref, "@") {
		//nolint:wrapcheck // cockroachdb/errors.WithHint at boundary.
		return errors.WithHint(
			errors.Newf("--image %q is malformed: ends with a separator with nothing after it", ref),
			"add a tag (e.g. ':v1.13.0') or a digest pin (e.g. '@sha256:<hex>') after the last path component",
		)
	}

	if !strings.Contains(ref, "/") {
		//nolint:wrapcheck // cockroachdb/errors.WithHint at boundary.
		return errors.WithHint(
			errors.Newf("--image %q is missing a registry / path component", ref),
			"Talos installer images are pulled by full reference — pass at least 'registry/path:tag', not a bare 'name:tag' shortcut",
		)
	}

	lastSlash := strings.LastIndex(ref, "/")
	tail := ref[lastSlash+1:]

	hasTag := false
	if idx := strings.LastIndex(tail, ":"); idx > 0 && idx < len(tail)-1 {
		hasTag = true
	}

	hasDigest := strings.Contains(ref, "@sha256:") || strings.Contains(ref, "@sha512:")

	if !hasTag && !hasDigest {
		//nolint:wrapcheck // cockroachdb/errors.WithHint at boundary.
		return errors.WithHint(
			errors.Newf("--image %q has no tag or digest", ref),
			"append ':<version>' (e.g. ':v1.13.0') or '@sha256:<hex>' to pin the installer build",
		)
	}

	return nil
}

// validateImageOverride scans presetFiles for the chosen preset's
// values.yaml and confirms a top-level image line is present when
// the user passed --image. The check runs before any file is written
// so a flag-vs-preset mismatch fails the command up front instead of
// leaving a half-initialized project on disk.
//
// Also performs a basic shape check on the override itself —
// malformed values like "::malformed" or "no-slash:tag" are rejected
// here, instead of being silently written to values.yaml and only
// surfacing on the next talm template / apply call deep inside
// configloader.NewFromBytes.
func validateImageOverride(presetFiles map[string]string, presetName, override string) error {
	if override == "" {
		return nil
	}

	if err := validateImageRefShape(override); err != nil {
		return err
	}

	for path, content := range presetFiles {
		parts := strings.SplitN(path, "/", 2)
		if len(parts) != 2 || parts[0] != presetName {
			continue
		}

		if parts[1] != valuesYamlName {
			continue
		}

		if !imageLineRe.MatchString(content) {
			//nolint:wrapcheck // cockroachdb/errors.WithHint at boundary.
			return errors.WithHint(
				errors.Newf("--image was set but preset %q does not declare a top-level image: field in values.yaml; remove --image or choose a preset that exposes it (e.g. cozystack)", presetName),
				"choose a preset that exposes a top-level image: field, or omit --image",
			)
		}

		return nil
	}

	//nolint:wrapcheck // cockroachdb/errors.WithHint at boundary.
	return errors.WithHint(
		errors.Newf("--image was set but preset %q has no values.yaml in the embedded chart files", presetName),
		"this looks like a build-time issue with the embedded chart files; rebuild talm or pick a different preset",
	)
}

// overwritePolicy describes how a single update-time file conflict
// should be resolved. The decision is made once per call so the unit
// tests (and the --force gate) can short-circuit the interactive
// prompt deterministically.
type overwritePolicy int

const (
	// overwritePolicyAsk prompts the user on stdin; the historical
	// behaviour, used when stdin is a real tty and --force is not set.
	overwritePolicyAsk overwritePolicy = iota
	// overwritePolicyForce always accepts the overwrite. Used when
	// --force is set; the operator opted in.
	overwritePolicyForce
	// overwritePolicyNonInteractive blocks the call with a hint to
	// rerun under a tty or with --force. Used when stdin is not a tty
	// and --force is not set — distinguishes "operator declined" from
	// "talm couldn't even ask" so a scripted refresh fails loudly
	// instead of silently leaving the project on a stale preset.
	overwritePolicyNonInteractive
)

// stdinReader is the io.Reader the interactive prompt reads from.
// Var-typed so unit tests can supply canned input.
//
//nolint:gochecknoglobals // injection seam for testability.
var stdinReader io.Reader = os.Stdin

// askUserOverwrite resolves a single update-time file conflict
// according to overwritePolicy. Returns (true, nil) to accept the
// overwrite, (false, nil) to skip, or an error when policy is
// non-interactive (with a hint pointing the operator at --force).
func askUserOverwrite(filePath string, policy overwritePolicy) (bool, error) {
	// Show relative path from project root
	relPath, err := filepath.Rel(Config.RootDir, filePath)
	if err != nil {
		// If we can't get relative path, use absolute
		relPath = filePath
	}

	switch policy {
	case overwritePolicyForce:
		fmt.Fprintf(os.Stderr, "Overwriting %s (--force)\n", relPath)

		return true, nil
	case overwritePolicyNonInteractive:
		//nolint:wrapcheck // cockroachdb/errors.WithHint at boundary.
		return false, errors.WithHint(
			errors.Newf("file %q differs from the preset template, but talm is running non-interactively and cannot prompt for confirmation", relPath),
			"rerun under a tty to confirm interactively, or pass --force to accept all preset-template overwrites.",
		)
	case overwritePolicyAsk:
		// fall through to interactive prompt below
	}

	reader := bufio.NewReader(stdinReader)

	fmt.Fprintf(os.Stderr, "File %s differs from template. Overwrite? [y/N]: ", relPath)

	response, err := reader.ReadString('\n')
	if err != nil {
		return false, errors.Wrap(err, "reading interactive overwrite confirmation")
	}

	response = strings.TrimSpace(strings.ToLower(response))

	return response == "y" || response == "yes", nil
}

// resolveOverwritePolicy picks the policy for the current invocation
// from the (--force, tty) inputs. Pure function for testability.
func resolveOverwritePolicy(force, isTTY bool) overwritePolicy {
	switch {
	case force:
		return overwritePolicyForce
	case !isTTY:
		return overwritePolicyNonInteractive
	}

	return overwritePolicyAsk
}

// filesDiffer checks if two files have different content.
func filesDiffer(filePath string, newContent []byte) (bool, error) {
	existingContent, err := os.ReadFile(filePath)
	if err != nil {
		// File doesn't exist, so it differs
		if os.IsNotExist(err) {
			return true, nil
		}

		return false, errors.Wrapf(err, "reading %s for diff", filePath)
	}

	return !bytes.Equal(existingContent, newContent), nil
}

// createReportedFile writes a fresh file (parent dir created as
// needed) and prints a one-line `Created path/to/file` summary so
// the operator sees what the update added. Shared between the
// initial-create and post-overwrite branches.
func createReportedFile(filePath string, newContent []byte, permissions os.FileMode) error {
	parentDir := filepath.Dir(filePath)
	if err := os.MkdirAll(parentDir, os.ModePerm); err != nil {
		return errors.Wrap(err, "failed to create output dir")
	}

	if err := os.WriteFile(filePath, newContent, permissions); err != nil {
		return errors.Wrap(err, "failed to write file")
	}

	relPath, err := filepath.Rel(Config.RootDir, filePath)
	if err != nil {
		relPath = filePath
	}

	fmt.Fprintf(os.Stderr, "%s %s\n", reportVerbCreated, relPath)

	return nil
}

// updateFileWithConfirmation updates a file if it differs, asking user for confirmation.
func updateFileWithConfirmation(filePath string, newContent []byte, permissions os.FileMode) error {
	if !fileExists(filePath) {
		return createReportedFile(filePath, newContent, permissions)
	}

	differs, err := filesDiffer(filePath, newContent)
	if err != nil {
		return err
	}

	if !differs {
		return nil
	}

	// File differs — pick the policy from (--force, tty) and resolve.
	policy := resolveOverwritePolicy(initCmdFlags.force, stdinIsTTY())

	overwrite, err := askUserOverwrite(filePath, policy)
	if err != nil {
		return errors.Wrap(err, "failed to read user input")
	}

	if !overwrite {
		fmt.Fprintf(os.Stderr, "Skipping %s\n", filePath)

		return nil
	}

	// Write file
	parentDir := filepath.Dir(filePath)
	if err := os.MkdirAll(parentDir, os.ModePerm); err != nil {
		return errors.Wrap(err, "failed to create output dir")
	}

	if err := os.WriteFile(filePath, newContent, permissions); err != nil {
		return errors.Wrap(err, "failed to write file")
	}

	// Show relative path from project root
	relPath, err := filepath.Rel(Config.RootDir, filePath)
	if err != nil {
		relPath = filePath
	}

	fmt.Fprintf(os.Stderr, "%s %s\n", reportVerbUpdated, relPath)

	return nil
}

// updateTalmLibraryChart implements `talm init --update`: it refreshes
// the bundled talm library chart and (optionally) the preset template
// files in an existing project, leaving secrets and operator-edited
// values intact. The function is a flat dispatcher over presetFiles
// entries; splitting it apart at every nestif layer would scatter the
// per-file dispatch across helpers without making any single branch
// easier to follow.
//
//nolint:gocognit,gocyclo,cyclop,nestif,funlen // see doc above
func updateTalmLibraryChart() error {
	// --image is only honored on initial init (it customizes the
	// preset's values.yaml at write time). Refusing it on --update
	// surfaces the no-op trap explicitly instead of letting the
	// user's flag silently disappear.
	if initCmdFlags.image != "" {
		//nolint:wrapcheck // cockroachdb/errors.WithHint at boundary.
		return errors.WithHint(
			errors.New("--image is honored on initial init only; for an existing project, edit the image field in values.yaml directly"),
			"drop --image and edit values.yaml manually if you need to change the installer image on an existing project",
		)
	}

	// Determine preset: use -p flag if provided, otherwise try to read from Chart.yaml
	var presetName string

	if initCmdFlags.preset != "" {
		// Use preset from flag
		presetName = initCmdFlags.preset
		// Validate preset
		availablePresets, err := generated.AvailablePresets()
		if err != nil {
			return errors.Wrap(err, "failed to get available presets")
		}

		if !isValidPreset(presetName, availablePresets) {
			//nolint:wrapcheck // cockroachdb/errors.WithHint at boundary.
			return errors.WithHint(
				errors.Newf("invalid preset: %s. Valid presets are: %v", presetName, availablePresets),
				"pick one of the listed presets and pass it via --preset",
			)
		}
	} else {
		// Try to read from Chart.yaml
		var err error

		presetName, err = readChartYamlPreset()
		if err != nil {
			// readChartYamlPreset already returns errors.WithHint("preset
			// not found in Chart.yaml dependencies", "add a preset chart…");
			// wrapping that again would double-message the operator with
			// the same fact.
			return err
		}
	}

	presetFiles, err := generated.PresetFiles()
	if err != nil {
		return errors.Wrap(err, "failed to get preset files")
	}

	// Step 1: Update talm library chart files (without interactive confirmation)
	fmt.Fprintf(os.Stderr, "Updating talm library chart...\n")

	for path, content := range presetFiles {
		parts := strings.SplitN(path, "/", 2)

		chartName := parts[0]
		if chartName == presetTalmLibrary {
			file := filepath.Join(Config.RootDir, filepath.Join("charts", path))

			var fileContent []byte
			if parts[len(parts)-1] == chartYamlName {
				fileContent = fmt.Appendf(nil, content, presetTalmLibrary, Config.InitOptions.Version)
			} else {
				fileContent = []byte(content)
			}

			// For talm library, always update without asking
			parentDir := filepath.Dir(file)
			if err := os.MkdirAll(parentDir, os.ModePerm); err != nil {
				return errors.Wrap(err, "failed to create output dir")
			}

			// Library chart files are public (Chart.yaml, helpers,
			// templates) — 0o644 is the documented Helm convention,
			// not a secret leak.
			if err := os.WriteFile(file, fileContent, presetFileMode); err != nil {
				return errors.Wrap(err, "failed to write file")
			}

			relPath, _ := filepath.Rel(Config.RootDir, file)
			fmt.Fprintf(os.Stderr, "%s %s\n", reportVerbUpdated, relPath)
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

				if parts[len(parts)-1] == chartYamlName {
					// Read cluster name from existing Chart.yaml
					existingChartPath := filepath.Join(Config.RootDir, chartYamlName)

					existingData, err := os.ReadFile(existingChartPath)
					if err != nil {
						return errors.Wrap(err, "failed to read existing Chart.yaml")
					}

					var existingChart struct {
						Name string `yaml:"name"`
					}
					if err := yaml.Unmarshal(existingData, &existingChart); err != nil {
						return errors.Wrap(err, "failed to parse existing Chart.yaml")
					}

					fileContent = fmt.Appendf(nil, content, existingChart.Name, Config.InitOptions.Version)
				} else {
					fileContent = []byte(content)
				}

				if err := updateFileWithConfirmation(file, fileContent, presetFileMode); err != nil {
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
	initCmd.Flags().BoolVar(&initCmdFlags.force, "force", false, "overwrite existing files; on --update also auto-accepts every preset-template diff without the interactive prompt")
	initCmd.Flags().BoolVarP(&initCmdFlags.update, "update", "u", false, "update Talm library chart")
	// Override persistent -e flag for init command to use for encrypt
	// Remove the persistent endpoints flag from init command and add our own -e flag
	initCmd.Flags().StringSliceVarP(&GlobalArgs.Endpoints, "endpoints", "", []string{}, "override default endpoints in Talos configuration")
	initCmd.Flags().BoolVarP(&initCmdFlags.encrypt, "encrypt", "e", false, "encrypt all sensitive files (secrets.yaml, talosconfig, kubeconfig)")
	initCmd.Flags().BoolVarP(&initCmdFlags.decrypt, "decrypt", "d", false, "decrypt all encrypted files (does not require preset)")

	// Shell completion for `talm init --preset` (#170): preset names
	// are baked in at build time via pkg/generated.
	_ = initCmd.RegisterFlagCompletionFunc("preset", completePresetNames)

	addCommand(initCmd)
	// Don't mark preset as required - it's validated in PreRunE based on --encrypt/--decrypt flags
}

func isValidPreset(preset string, availablePresets []string) bool {
	return slices.Contains(availablePresets, preset)
}

func validateFileExists(file string) error {
	if !initCmdFlags.force {
		if _, err := os.Stat(file); err == nil {
			//nolint:wrapcheck // cockroachdb/errors.WithHint at boundary.
			return errors.WithHint(
				errors.Newf("file %q already exists, use --force to overwrite, and --update to update Talm library chart only", file),
				"rerun with --force to overwrite, --update to refresh only the talm library chart, or remove the file manually",
			)
		}
	}

	return nil
}

// gitignoreEntryCount is the size of the requiredEntries slice in
// writeGitignoreFile: three secret-bearing artefacts plus the
// kubeconfig base name. Hoisting it into a const sidesteps mnd's
// magic-number lint without inlining the comment at every call site.
const gitignoreEntryCount = 4

//nolint:funlen // wrapping the secrets-list assembly in helpers buys nothing in clarity
func writeGitignoreFile() error {
	// Capacity gitignoreEntryCount: three secret-bearing artefacts
	// (secrets.yaml, talosconfig, talm.key) plus the kubeconfig base
	// name appended just below. Preallocating avoids the slice growth
	// prealloc flags.
	requiredEntries := make([]string, 0, gitignoreEntryCount)
	requiredEntries = append(requiredEntries, secretsYamlName, talosconfigName, talmKeyName)

	// Add kubeconfig to required entries (use path from config or default)
	kubeconfigPath := Config.GlobalOptions.Kubeconfig
	if kubeconfigPath == "" {
		kubeconfigPath = defaultKubeconfigName
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
			return errors.Wrap(err, "failed to read existing .gitignore")
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
		return errors.Wrap(err, "failed to create output dir")
	}
	// Capture existence BEFORE the write so we can distinguish "created"
	// from "updated" in the operator-facing log line. Doing the stat
	// after WriteFile would always succeed (the file exists post-write)
	// and the message would be wrong on a fresh init — we'd report
	// "Updated" for a file we just created.
	//
	// Branch on os.IsNotExist explicitly so that ambiguous stat
	// failures (e.g. EACCES on the parent directory, ENOTDIR mid-path)
	// fall into the same bucket as "exists" — the file may already be
	// there, we just can't see it. Reporting "Created" for an
	// inscrutable stat error would be a lie when the WriteFile
	// succeeded only because the operator separately fixed the
	// permission. The "Updated" wording is correct in the ambiguous
	// case because it does not falsely promise the absence we never
	// confirmed.
	_, statErrBefore := os.Stat(gitignoreFile)

	// .gitignore is checked into the repo and read by every developer
	// who clones the project — 0o644 is the standard, not a leak.
	err := os.WriteFile(gitignoreFile, []byte(existingStr), presetFileMode) //nolint:gosec // .gitignore is world-readable by design
	if err == nil {
		fmt.Fprintf(os.Stderr, "%s %s\n", gitignoreReportVerb(statErrBefore), gitignoreFile)
	}

	return errors.Wrap(err, "writing .gitignore")
}

// gitignoreReportVerb returns the operator-facing verb for the
// .gitignore write report. The branch is hoisted out of
// writeGitignoreFile so the IsNotExist contract is unit-testable
// without an os.Stat fault injection — see
// TestGitignoreReportVerb_*.
func gitignoreReportVerb(statErrBefore error) string {
	if os.IsNotExist(statErrBefore) {
		return reportVerbCreated
	}

	return reportVerbUpdated
}

func fileExists(file string) bool {
	_, err := os.Stat(file)

	return err == nil
}

func printSecretsWarning() {
	keyFile := filepath.Join(Config.RootDir, talmKeyName)
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

// handleTalosconfigEncryption handles encryption/decryption logic for
// talosconfig file. It decrypts if encrypted file exists, encrypts if
// plain file exists. requireKeyForDecrypt: if true, returns error if
// key is missing when trying to decrypt. Returns true if key was
// created during this call, false otherwise.
//
// requireKeyForDecrypt always receives false today, but the parameter
// pins the intended contract for the planned init-time branch that
// requires the key for an existing encrypted talosconfig. nestif fires
// on the well-isolated decrypt/encrypt pair; splitting it into helpers
// wouldn't make either branch easier to follow.
//
//nolint:unparam,nestif // see doc above
func handleTalosconfigEncryption(requireKeyForDecrypt bool) (bool, error) {
	talosconfigFile := filepath.Join(Config.RootDir, "talosconfig")
	encryptedTalosconfigFile := filepath.Join(Config.RootDir, "talosconfig.encrypted")
	talosconfigFileExists := fileExists(talosconfigFile)
	encryptedTalosconfigFileExists := fileExists(encryptedTalosconfigFile)
	keyFile := filepath.Join(Config.RootDir, talmKeyName)
	keyFileExists := fileExists(keyFile)
	keyWasCreated := false

	// If encrypted file exists, decrypt it
	if encryptedTalosconfigFileExists && !talosconfigFileExists {
		if !keyFileExists {
			if requireKeyForDecrypt {
				//nolint:wrapcheck // cockroachdb/errors.WithHint at boundary.
				return false, errors.WithHint(
					errors.New("talosconfig.encrypted exists but talm.key is missing. Cannot decrypt without key"),
					"restore talm.key from your backup before re-running this command",
				)
			}
			// If key is not required, just return (don't decrypt)
			return false, nil
		}

		fmt.Fprintf(os.Stderr, "Decrypting talosconfig.encrypted -> talosconfig\n")

		if err := age.DecryptYAMLFile(Config.RootDir, "talosconfig.encrypted", "talosconfig"); err != nil {
			return false, errors.Wrap(err, "failed to decrypt talosconfig")
		}

		talosconfigFileExists = true
	}

	// If talosconfig exists but encrypted file doesn't, encrypt it
	if talosconfigFileExists && !encryptedTalosconfigFileExists {
		// Ensure key exists
		if !keyFileExists {
			_, keyCreated, err := age.GenerateKey(Config.RootDir)
			if err != nil {
				return false, errors.Wrap(err, "failed to generate key")
			}

			keyWasCreated = keyCreated
			if keyCreated {
				fmt.Fprintf(os.Stderr, "Generated new encryption key: talm.key\n")
			}
		}

		// Encrypt talosconfig
		fmt.Fprintf(os.Stderr, "Encrypting talosconfig -> talosconfig.encrypted\n")

		if err := age.EncryptYAMLFile(Config.RootDir, "talosconfig", "talosconfig.encrypted"); err != nil {
			return false, errors.Wrap(err, "failed to encrypt talosconfig")
		}
	}

	return keyWasCreated, nil
}

// createdSink is where "Created <path>" messages go after a successful
// write. Swappable in tests to assert no message is emitted on failure.
//
//nolint:gochecknoglobals // test seam: tests swap this to capture output without spinning up a fake fd
var createdSink io.Writer = os.Stderr

// writeToDestination writes a chart artefact (Chart.yaml, values.yaml,
// helpers, templates) to destination with the supplied permissions.
// permissions is always presetFileMode today, but the parameter pins
// the signature for the planned per-file mode dispatch (e.g.
// owner-only modes for embedded scripts).
//
//nolint:unparam // see doc above
func writeToDestination(data []byte, destination string, permissions os.FileMode) error {
	if err := validateFileExists(destination); err != nil {
		return err
	}

	parentDir := filepath.Dir(destination)

	// Create dir path, ignoring "already exists" messages
	if err := os.MkdirAll(parentDir, os.ModePerm); err != nil {
		return errors.Wrap(err, "failed to create output dir")
	}

	// Permissions are caller-supplied; chart artefacts use
	// presetFileMode (0o644) by design — they are world-readable.
	err := os.WriteFile(destination, data, permissions)
	if err == nil {
		_, _ = fmt.Fprintf(createdSink, "%s %s\n", reportVerbCreated, destination)
	}

	return errors.Wrapf(err, "writing %s", destination)
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
	if err := os.MkdirAll(parentDir, secureDirMode); err != nil {
		return errors.Wrap(err, "failed to create output dir")
	}

	err := secureperm.WriteFile(destination, data)
	if err == nil {
		_, _ = fmt.Fprintf(createdSink, "%s %s\n", reportVerbCreated, destination)
	}

	return errors.Wrapf(err, "writing secret %s", destination)
}
