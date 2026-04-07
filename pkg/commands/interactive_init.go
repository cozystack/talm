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

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/cozystack/talm/pkg/generated"
	"github.com/cozystack/talm/pkg/wizard"
	"github.com/cozystack/talm/pkg/wizard/scan"
	"github.com/cozystack/talm/pkg/wizard/tui"
)

// interactiveCmd starts terminal TUI for interactive configuration.
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
		generateFn := func(result wizard.WizardResult) error {
			return GenerateProject(GenerateOptions{
				RootDir:     Config.RootDir,
				Preset:      result.Preset,
				ClusterName: result.ClusterName,
				Force:       false,
				Version:     Config.InitOptions.Version,
			})
		}

		model := tui.New(scanner, presets, generateFn)
		p := tea.NewProgram(model, tea.WithAltScreen())

		finalModel, err := p.Run()
		if err != nil {
			return fmt.Errorf("wizard failed: %w", err)
		}

		if m, ok := finalModel.(tui.Model); ok && m.Err() != nil {
			return m.Err()
		}

		return nil
	},
}

func init() {
	addCommand(interactiveCmd)
}
