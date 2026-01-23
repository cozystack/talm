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

	"github.com/spf13/cobra"
)

// wrapRotateCACommand adds special handling for rotate-ca command:
// - validates that only one node/endpoint is provided (must be a control-plane node)
// - updates command description
func wrapRotateCACommand(wrappedCmd *cobra.Command) {
	// Update command description to be more helpful
	wrappedCmd.Long = `Rotates Talos and/or Kubernetes root Certificate Authorities.

This command must be run against a SINGLE control-plane node. The specified node
will be used to coordinate the CA rotation across the entire cluster.

The command works by:
1. Auto-discovering all cluster nodes (control-plane and workers)
2. Generating new CA certificates
3. Gracefully rolling out the new CAs to all nodes
4. Updating the talosconfig with new credentials

IMPORTANT: You must specify exactly ONE control-plane node via --endpoints/-e or --nodes/-n
flags, or through a single config file (-f). The node must be a control-plane node.

By default, both Talos API CA and Kubernetes API CA are rotated. Use --talos=false
or --kubernetes=false to rotate only one of them.

The command runs in dry-run mode by default. Use --dry-run=false to perform actual rotation.`

	wrappedCmd.Example = `  # Dry-run CA rotation (recommended first step)
  talm rotate-ca -e 192.168.1.10

  # Rotate CAs using config file (must contain single control-plane node)
  talm rotate-ca -f nodes/controlplane-1.yaml

  # Actually perform the rotation
  talm rotate-ca -f nodes/controlplane-1.yaml --dry-run=false

  # Rotate only Talos API CA
  talm rotate-ca -f nodes/controlplane-1.yaml --kubernetes=false --dry-run=false

  # Rotate only Kubernetes API CA
  talm rotate-ca -f nodes/controlplane-1.yaml --talos=false --dry-run=false`

	// Store original PreRunE to chain it
	originalPreRunE := wrappedCmd.PreRunE

	wrappedCmd.PreRunE = func(cmd *cobra.Command, args []string) error {
		// Run original PreRunE first (processes modeline, syncs GlobalArgs, etc.)
		if originalPreRunE != nil {
			if err := originalPreRunE(cmd, args); err != nil {
				return err
			}
		}

		// Validate that only one endpoint/node is provided
		if len(GlobalArgs.Endpoints) > 1 {
			return fmt.Errorf("rotate-ca requires exactly one control-plane node, but %d endpoints were provided\n\nThe rotate-ca command coordinates CA rotation across the entire cluster from a single\ncontrol-plane node. Please specify only one endpoint using -e flag or a single config file", len(GlobalArgs.Endpoints))
		}

		if len(GlobalArgs.Nodes) > 1 {
			return fmt.Errorf("rotate-ca requires exactly one control-plane node, but %d nodes were provided\n\nThe rotate-ca command coordinates CA rotation across the entire cluster from a single\ncontrol-plane node. Please specify only one node using -n flag or a single config file", len(GlobalArgs.Nodes))
		}

		return nil
	}
}
