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

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/cozystack/talm/pkg/generated"
	"github.com/cozystack/talm/pkg/wizard"
	"github.com/cozystack/talm/pkg/wizard/scan"
	"github.com/cozystack/talm/pkg/wizard/tui"
)

// interactiveCmd starts terminal TUI for interactive configuration.
// Registered as a root-level command (not under init) to avoid flag conflicts
// with the existing init command's --encrypt/--decrypt/--update flag validation.
var interactiveCmd = &cobra.Command{
	Use:   "interactive",
	Short: "Start interactive TUI wizard for cluster initialization",
	Long:  `Start a terminal-based UI (TUI) wizard that guides through cluster initialization.`,
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		presets, err := generated.AvailablePresets()
		if err != nil {
			return fmt.Errorf("failed to get available presets: %w", err)
		}

		scanner := scan.New()
		existing, isExisting := detectExistingProject(Config.RootDir)

		var projectGenerated bool
		generateFn := func(result wizard.WizardResult) error {
			overrides := buildValuesOverrides(result)

			// Skip full project scaffolding when the project is already
			// initialized — only (re)write node stubs and values overrides.
			if !isExisting {
				if err := GenerateProject(GenerateOptions{
					RootDir:         Config.RootDir,
					Preset:          result.Preset,
					ClusterName:     result.ClusterName,
					TalosVersion:    Config.TemplateOptions.TalosVersion,
					Force:           false,
					Version:         Config.InitOptions.Version,
					ValuesOverrides: overrides,
					Endpoint:        result.Endpoint,
				}); err != nil {
					return err
				}
			} else {
				valuesPath := filepath.Join(Config.RootDir, "values.yaml")
				if err := mergeValuesOverrides(valuesPath, overrides); err != nil {
					return err
				}
			}

			if err := wizard.WriteNodeFiles(Config.RootDir, result.Nodes); err != nil {
				return err
			}

			projectGenerated = true
			return nil
		}

		var model tui.Model
		if isExisting {
			model = tui.NewForExistingProject(scanner, existing, generateFn)
		} else {
			model = tui.New(scanner, presets, generateFn)
		}
		p := tea.NewProgram(model, tea.WithAltScreen())

		finalModel, err := p.Run()
		if err != nil {
			return fmt.Errorf("wizard failed: %w", err)
		}

		if m, ok := finalModel.(tui.Model); ok && m.Err() != nil {
			return m.Err()
		}

		// p.Run() has returned — alternate screen is torn down and the main
		// terminal buffer is restored. Emit the encryption warning here so
		// users actually see it.
		if projectGenerated {
			fmt.Fprintln(os.Stderr, "\nNote: Secrets are not encrypted. Run 'talm init --encrypt' to encrypt sensitive files.")
		}

		return nil
	},
}

// detectExistingProject returns the pre-populated wizard result (preset +
// cluster name) when rootDir already looks like an initialized talm project.
// Allows the wizard to skip steps the user has already answered.
func detectExistingProject(rootDir string) (wizard.WizardResult, bool) {
	secretsExist := fileExists(filepath.Join(rootDir, "secrets.yaml")) ||
		fileExists(filepath.Join(rootDir, "secrets.yaml.encrypted"))
	chartYaml := filepath.Join(rootDir, "Chart.yaml")
	if !secretsExist || !fileExists(chartYaml) {
		return wizard.WizardResult{}, false
	}

	data, err := os.ReadFile(chartYaml)
	if err != nil {
		return wizard.WizardResult{}, false
	}
	var parsed struct {
		Name         string `yaml:"name"`
		Dependencies []struct {
			Name string `yaml:"name"`
		} `yaml:"dependencies"`
	}
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		return wizard.WizardResult{}, false
	}

	var preset string
	for _, dep := range parsed.Dependencies {
		if dep.Name != "talm" {
			preset = dep.Name
			break
		}
	}
	if parsed.Name == "" || preset == "" {
		return wizard.WizardResult{}, false
	}
	return wizard.WizardResult{Preset: preset, ClusterName: parsed.Name}, true
}

// buildValuesOverrides creates a map of values.yaml overrides from wizard results.
func buildValuesOverrides(result wizard.WizardResult) map[string]interface{} {
	overrides := map[string]interface{}{}

	if result.Endpoint != "" {
		overrides["endpoint"] = result.Endpoint
	}

	if result.PodSubnets != "" {
		overrides["podSubnets"] = []string{result.PodSubnets}
	}
	if result.ServiceSubnets != "" {
		overrides["serviceSubnets"] = []string{result.ServiceSubnets}
	}
	if result.AdvertisedSubnets != "" {
		overrides["advertisedSubnets"] = []string{result.AdvertisedSubnets}
	}

	// Cozystack-specific
	if result.ClusterDomain != "" {
		overrides["clusterDomain"] = result.ClusterDomain
	}
	if result.FloatingIP != "" {
		overrides["floatingIP"] = result.FloatingIP
	}
	if result.Image != "" {
		overrides["image"] = result.Image
	}

	return overrides
}

func init() {
	addCommand(interactiveCmd)
}
