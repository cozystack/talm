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

	"github.com/spf13/cobra"
)

// talosconfigCmd represents the `talosconfig` command.
var talosconfigCmd = &cobra.Command{
	Use:   "talosconfig",
	Short: "Manage talosconfig file (decrypt/encrypt)",
	Long:  `Manage talosconfig file: decrypt if encrypted file exists, encrypt if plain file exists.`,
	Args:  cobra.NoArgs,
	PreRunE: func(cmd *cobra.Command, args []string) error {
		// Ensure project root is detected
		if !Config.RootDirExplicit {
			detectedRoot, err := detectRootFromCWD()
			if err == nil && detectedRoot != "" {
				Config.RootDir = detectedRoot
			}
		}
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		// Handle talosconfig encryption/decryption logic
		if _, err := handleTalosconfigEncryption(true); err != nil {
			return err
		}

		// Update .gitignore if needed
		if err := writeGitignoreFile(); err != nil {
			// Don't fail the command if gitignore update fails, but log warning
			fmt.Fprintf(os.Stderr, "Warning: failed to update .gitignore: %v\n", err)
		}

		return nil
	},
}

func init() {
	addCommand(talosconfigCmd)
}

