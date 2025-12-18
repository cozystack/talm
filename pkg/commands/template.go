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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cozystack/talm/pkg/engine"
	"github.com/cozystack/talm/pkg/modeline"
	"github.com/spf13/cobra"

	"github.com/siderolabs/talos/pkg/machinery/client"
	"github.com/siderolabs/talos/pkg/machinery/constants"
)

var templateCmdFlags struct {
	insecure          bool
	configFiles       []string // -f/--files
	valueFiles        []string // --values
	templateFiles     []string // -t/--template
	stringValues      []string // --set-string
	values            []string // --set
	fileValues        []string // --set-file
	jsonValues        []string // --set-json
	literalValues     []string // --set-literal
	talosVersion      string
	withSecrets       string
	full              bool
	debug             bool
	offline           bool
	kubernetesVersion string
	inplace           bool
	nodesFromArgs     bool
	endpointsFromArgs bool
	templatesFromArgs bool
}

var templateCmd = &cobra.Command{
	Use:   "template",
	Short: "Render templates locally and display the output",
	Long:  ``,
	Args:  cobra.NoArgs,
	PreRunE: func(cmd *cobra.Command, args []string) error {
		templateCmdFlags.valueFiles = append(Config.TemplateOptions.ValueFiles, templateCmdFlags.valueFiles...)
		templateCmdFlags.values = append(Config.TemplateOptions.Values, templateCmdFlags.values...)
		templateCmdFlags.stringValues = append(Config.TemplateOptions.StringValues, templateCmdFlags.stringValues...)
		templateCmdFlags.fileValues = append(Config.TemplateOptions.FileValues, templateCmdFlags.fileValues...)
		templateCmdFlags.jsonValues = append(Config.TemplateOptions.JsonValues, templateCmdFlags.jsonValues...)
		templateCmdFlags.literalValues = append(Config.TemplateOptions.LiteralValues, templateCmdFlags.literalValues...)
		if !cmd.Flags().Changed("talos-version") {
			templateCmdFlags.talosVersion = Config.TemplateOptions.TalosVersion
		}
		if !cmd.Flags().Changed("with-secrets") {
			templateCmdFlags.withSecrets = Config.TemplateOptions.WithSecrets
		}
		if !cmd.Flags().Changed("kubernetes-version") {
			templateCmdFlags.kubernetesVersion = Config.TemplateOptions.KubernetesVersion
		}
		if !cmd.Flags().Changed("full") {
			templateCmdFlags.full = Config.TemplateOptions.Full
		}
		if !cmd.Flags().Changed("debug") {
			templateCmdFlags.debug = Config.TemplateOptions.Debug
		}
		if !cmd.Flags().Changed("offline") {
			templateCmdFlags.offline = Config.TemplateOptions.Offline
		}
		templateCmdFlags.templatesFromArgs = len(templateCmdFlags.templateFiles) > 0
		templateCmdFlags.nodesFromArgs = len(GlobalArgs.Nodes) > 0
		templateCmdFlags.endpointsFromArgs = len(GlobalArgs.Endpoints) > 0
		// Set dummy endpoint to avoid errors on building clinet
		if len(GlobalArgs.Endpoints) == 0 {
			GlobalArgs.Endpoints = append(GlobalArgs.Endpoints, "127.0.0.1")
		}

		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		templateFunc := template
		if len(templateCmdFlags.configFiles) > 0 {
			templateFunc = templateWithFiles
			if len(templateCmdFlags.configFiles) == 0 {
				return fmt.Errorf("cannot use --in-place without --file")
			}
		}

		if templateCmdFlags.offline {
			return templateFunc(args)(context.Background(), nil)
		}
		if templateCmdFlags.insecure {
			return WithClientMaintenance(nil, templateFunc(args))
		}

		return WithClient(templateFunc(args))
	},
}

func template(args []string) func(ctx context.Context, c *client.Client) error {
	return func(ctx context.Context, c *client.Client) error {
		output, err := generateOutput(ctx, c, args)
		if err != nil {
			return err
		}

		fmt.Println(output)
		return nil
	}
}

func templateWithFiles(args []string) func(ctx context.Context, c *client.Client) error {
	return func(ctx context.Context, c *client.Client) error {
		// Expand directories to YAML files
		expandedFiles, err := ExpandFilePaths(templateCmdFlags.configFiles)
		if err != nil {
			return err
		}

		// Detect root from files if specified, otherwise fallback to cwd
		if err := DetectAndSetRootFromFiles(expandedFiles); err != nil {
			return err
		}

		firstFileProcessed := false
		for _, configFile := range expandedFiles {
			modelineConfig, err := modeline.ReadAndParseModeline(configFile)
			if err != nil {
				return fmt.Errorf("modeline parsing failed: %v\n", err)
			}
			if !templateCmdFlags.templatesFromArgs {
				if len(modelineConfig.Templates) == 0 {
					return fmt.Errorf("modeline does not contain templates information")
				} else {
					templateCmdFlags.templateFiles = modelineConfig.Templates
				}
			}
			if !templateCmdFlags.nodesFromArgs {
				GlobalArgs.Nodes = modelineConfig.Nodes
			}
			if !templateCmdFlags.endpointsFromArgs {
				GlobalArgs.Endpoints = modelineConfig.Endpoints
			}
			fmt.Printf("- talm: file=%s, nodes=%s, endpoints=%s, templates=%s\n", configFile, GlobalArgs.Nodes, GlobalArgs.Endpoints, templateCmdFlags.templateFiles)

			if len(GlobalArgs.Nodes) < 1 {
				return errors.New("nodes are not set for the command: please use `--nodes` flag or configuration file to set the nodes to run the command against")
			}
			if len(templateCmdFlags.configFiles) != 0 && len(templateCmdFlags.templateFiles) < 1 {
				return errors.New("templates are not set for the command: please use `--template` flag to set the templates to render manifest from")
			}

			template := func(args []string) func(ctx context.Context, c *client.Client) error {
				return func(ctx context.Context, c *client.Client) error {
					output, err := generateOutput(ctx, c, args)
					if err != nil {
						return err
					}

					if templateCmdFlags.inplace {
						err = os.WriteFile(configFile, []byte(output), 0o644)
						fmt.Fprintf(os.Stderr, "Updated.\n")
					} else {
						if firstFileProcessed {
							fmt.Println("---")
						}
						fmt.Printf("%s", output)
					}

					return nil
				}
			}

			if templateCmdFlags.offline {
				err = template(args)(context.Background(), nil)
			} else if templateCmdFlags.insecure {
				err = WithClientMaintenance(nil, template(args))
			} else {
				err = WithClient(template(args))
			}
			if err != nil {
				return err
			}

			// Reset args
			firstFileProcessed = true
			if !templateCmdFlags.templatesFromArgs {
				templateCmdFlags.templateFiles = []string{}
			}
			if !templateCmdFlags.nodesFromArgs {
				GlobalArgs.Nodes = []string{}
			}
			if !templateCmdFlags.endpointsFromArgs {
				GlobalArgs.Endpoints = []string{}
			}
		}
		return nil
	}
}

func generateOutput(ctx context.Context, c *client.Client, args []string) (string, error) {
			// Resolve secrets.yaml path relative to project root if not absolute
			withSecretsPath := ResolveSecretsPath(templateCmdFlags.withSecrets)

	// Resolve template file paths relative to project root
	resolvedTemplateFiles := make([]string, len(templateCmdFlags.templateFiles))
	absRootDir, rootErr := filepath.Abs(Config.RootDir)
	if rootErr != nil {
		// If we can't get absolute root, use original paths
		resolvedTemplateFiles = templateCmdFlags.templateFiles
	} else {
		for i, templatePath := range templateCmdFlags.templateFiles {
			var absTemplatePath string
			if filepath.IsAbs(templatePath) {
				// Already absolute, use as is
				absTemplatePath = templatePath
			} else {
				// Resolve relative path from current working directory
				var absErr error
				absTemplatePath, absErr = filepath.Abs(templatePath)
				if absErr != nil {
					// If we can't get absolute path, use original
					resolvedTemplateFiles[i] = templatePath
					continue
				}
			}
			// Convert to relative path from root
			relPath, relErr := filepath.Rel(absRootDir, absTemplatePath)
			if relErr != nil {
				// If we can't get relative path, use original
				resolvedTemplateFiles[i] = templatePath
				continue
			}
			// Normalize the path (remove .. and .)
			relPath = filepath.Clean(relPath)
			// Check if path goes outside root
			if strings.HasPrefix(relPath, "..") {
				// Path goes outside root, try to find file in templates/ relative to root
				templateName := filepath.Base(templatePath)
				possiblePath := filepath.Join("templates", templateName)
				fullPath := filepath.Join(absRootDir, possiblePath)
				if _, statErr := os.Stat(fullPath); statErr == nil {
					relPath = possiblePath
				} else {
					// Can't resolve, use original
					resolvedTemplateFiles[i] = templatePath
					continue
				}
			}
			resolvedTemplateFiles[i] = relPath
		}
	}

	opts := engine.Options{
		Insecure:          templateCmdFlags.insecure,
		ValueFiles:        templateCmdFlags.valueFiles,
		StringValues:      templateCmdFlags.stringValues,
		Values:            templateCmdFlags.values,
		FileValues:        templateCmdFlags.fileValues,
		JsonValues:        templateCmdFlags.jsonValues,
		LiteralValues:     templateCmdFlags.literalValues,
		TalosVersion:      templateCmdFlags.talosVersion,
		WithSecrets:       withSecretsPath,
		Full:              templateCmdFlags.full,
		Debug:             templateCmdFlags.debug,
		Root:              Config.RootDir,
		Offline:           templateCmdFlags.offline,
		KubernetesVersion: templateCmdFlags.kubernetesVersion,
		TemplateFiles:     resolvedTemplateFiles,
	}

	result, err := engine.Render(ctx, c, opts)
	if err != nil {
		return "", fmt.Errorf("failed to render templates: %w", err)
	}

	// Convert template paths to relative paths from project root for modeline
	templatePathsForModeline := make([]string, len(templateCmdFlags.templateFiles))
	absRootDirModeline, err := filepath.Abs(Config.RootDir)
	if err != nil {
		// If we can't get absolute root, use original paths
		templatePathsForModeline = templateCmdFlags.templateFiles
	} else {
		for i, templatePath := range templateCmdFlags.templateFiles {
			var absTemplatePath string
			if filepath.IsAbs(templatePath) {
				// Already absolute, use as is
				absTemplatePath = templatePath
			} else {
				// Resolve relative path from current working directory
				absTemplatePath, err = filepath.Abs(templatePath)
				if err != nil {
					// If we can't get absolute path, use original
					templatePathsForModeline[i] = templatePath
					continue
				}
			}
			// Check if the resolved path is inside root project
			relPath, err := filepath.Rel(absRootDir, absTemplatePath)
			if err != nil {
				// If we can't get relative path, use original
				templatePathsForModeline[i] = templatePath
				continue
			}
			// Normalize the path (remove .. and .)
			relPath = filepath.Clean(relPath)
			// Check if path goes outside root
			if strings.HasPrefix(relPath, "..") {
				// Path goes outside root, try to find file in templates/ relative to root
				// This handles cases like "../templates/controlplane.yaml" when file is actually in root/templates/
				templateName := filepath.Base(templatePath)
				// Try common template locations
				possiblePaths := []string{
					filepath.Join("templates", templateName),
					templateName,
				}
				found := false
				for _, possiblePath := range possiblePaths {
					fullPath := filepath.Join(absRootDirModeline, possiblePath)
					if _, err := os.Stat(fullPath); err == nil {
						relPath = possiblePath
						found = true
						break
					}
				}
				if !found {
					// Can't resolve, use original
					templatePathsForModeline[i] = templatePath
					continue
				}
			} else {
				// Path is inside root, but check if file actually exists
				// If not, try to find it in templates/ relative to root
				if _, errModeline := os.Stat(absTemplatePath); errModeline != nil {
					templateName := filepath.Base(templatePath)
					possiblePath := filepath.Join("templates", templateName)
					fullPath := filepath.Join(absRootDirModeline, possiblePath)
					if _, errModeline := os.Stat(fullPath); errModeline == nil {
						relPath = possiblePath
					}
				} else {
					// File exists, but check if there's a shorter/canonical path
					// For example, if we have "nodes/templates/controlplane.yaml" but file is actually in "templates/controlplane.yaml"
					templateName := filepath.Base(templatePath)
					canonicalPath := filepath.Join("templates", templateName)
					canonicalFullPath := filepath.Join(absRootDirModeline, canonicalPath)
					// Check if canonical path exists and points to the same file
					if canonicalInfo, errModeline := os.Stat(canonicalFullPath); errModeline == nil {
						if originalInfo, errModeline := os.Stat(absTemplatePath); errModeline == nil {
							// Check if they point to the same file (same inode on Unix)
							if os.SameFile(originalInfo, canonicalInfo) {
								// Use canonical path (shorter, cleaner)
								relPath = canonicalPath
							}
						}
					}
				}
			}
			templatePathsForModeline[i] = relPath
		}
	}

	modeline, err := modeline.GenerateModeline(GlobalArgs.Nodes, GlobalArgs.Endpoints, templatePathsForModeline)
	if err != nil {
		return "", fmt.Errorf("failed to generate modeline: %w", err)
	}
	warn := "# THIS FILE IS AUTOGENERATED. PREFER TEMPLATE EDITS OVER MANUAL ONES."

	output := fmt.Sprintf("%s\n%s\n%s\n", modeline, warn, string(result))
	return output, nil
}

func init() {
	templateCmd.Flags().BoolVarP(&templateCmdFlags.insecure, "insecure", "i", false, "template using the insecure (encrypted with no auth) maintenance service")
	templateCmd.Flags().StringSliceVarP(&templateCmdFlags.configFiles, "file", "f", nil, "specify config files for in-place update (can specify multiple)")
	templateCmd.Flags().BoolVarP(&templateCmdFlags.inplace, "in-place", "I", false, "re-template and update generated files in place (overwrite them)")
	templateCmd.Flags().StringSliceVarP(&templateCmdFlags.valueFiles, "values", "", []string{}, "specify values in a YAML file (can specify multiple)")
	templateCmd.Flags().StringSliceVarP(&templateCmdFlags.templateFiles, "template", "t", []string{}, "specify templates to render manifest from (can specify multiple)")
	templateCmd.Flags().StringArrayVar(&templateCmdFlags.values, "set", []string{}, "set values on the command line (can specify multiple or separate values with commas: key1=val1,key2=val2)")
	templateCmd.Flags().StringArrayVar(&templateCmdFlags.stringValues, "set-string", []string{}, "set STRING values on the command line (can specify multiple or separate values with commas: key1=val1,key2=val2)")
	templateCmd.Flags().StringArrayVar(&templateCmdFlags.fileValues, "set-file", []string{}, "set values from respective files specified via the command line (can specify multiple or separate values with commas: key1=path1,key2=path2)")
	templateCmd.Flags().StringArrayVar(&templateCmdFlags.jsonValues, "set-json", []string{}, "set JSON values on the command line (can specify multiple or separate values with commas: key1=jsonval1,key2=jsonval2)")
	templateCmd.Flags().StringArrayVar(&templateCmdFlags.literalValues, "set-literal", []string{}, "set a literal STRING value on the command line")
	templateCmd.Flags().StringVar(&templateCmdFlags.talosVersion, "talos-version", "", "the desired Talos version to generate config for (backwards compatibility, e.g. v0.8)")
	templateCmd.Flags().StringVar(&templateCmdFlags.withSecrets, "with-secrets", "", "use a secrets file generated using 'gen secrets'")
	templateCmd.Flags().BoolVarP(&templateCmdFlags.full, "full", "", false, "show full resulting config, not only patch")
	templateCmd.Flags().BoolVarP(&templateCmdFlags.debug, "debug", "", false, "show only rendered patches")
	templateCmd.Flags().BoolVarP(&templateCmdFlags.offline, "offline", "", false, "disable gathering information and lookup functions")
	templateCmd.Flags().StringVar(&templateCmdFlags.kubernetesVersion, "kubernetes-version", constants.DefaultKubernetesVersion, "desired kubernetes version to run")

	addCommand(templateCmd)
}

// generateModeline creates a modeline string using JSON formatting for values
func generateModeline(templates []string) (string, error) {
	// Convert Nodes to JSON
	nodesJSON, err := json.Marshal(GlobalArgs.Nodes)
	if err != nil {
		return "", fmt.Errorf("failed to marshal nodes: %v", err)
	}

	// Convert Endpoints to JSON
	endpointsJSON, err := json.Marshal(GlobalArgs.Endpoints)
	if err != nil {
		return "", fmt.Errorf("failed to marshal endpoints: %v", err)
	}

	// Convert Templates to JSON
	templatesJSON, err := json.Marshal(templates)
	if err != nil {
		return "", fmt.Errorf("failed to marshal templates: %v", err)
	}

	// Form the final modeline string
	modeline := fmt.Sprintf(`# talm: nodes=%s, endpoints=%s, templates=%s`, string(nodesJSON), string(endpointsJSON), string(templatesJSON))
	return modeline, nil
}
