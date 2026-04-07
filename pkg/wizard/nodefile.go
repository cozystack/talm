package wizard

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cozystack/talm/pkg/modeline"
)

// WriteNodeFiles creates stub node config files in the nodes/ directory.
// Each file contains a modeline pointing to the node's IP and the appropriate template.
// Existing files are not overwritten.
func WriteNodeFiles(rootDir string, nodes []NodeConfig) error {
	nodesDir := filepath.Join(rootDir, "nodes")
	if err := os.MkdirAll(nodesDir, 0o755); err != nil {
		return fmt.Errorf("failed to create nodes directory: %w", err)
	}

	for _, node := range nodes {
		filePath := filepath.Join(nodesDir, node.Hostname+".yaml")

		// Skip if file already exists
		if _, err := os.Stat(filePath); err == nil {
			fmt.Fprintf(os.Stderr, "Skipping %s (already exists)\n", filePath)
			continue
		}

		ip := extractIP(node.Addresses)
		templateFile := templateForRole(node.Role)

		ml, err := modeline.GenerateModeline(
			[]string{ip},
			[]string{ip},
			[]string{templateFile},
		)
		if err != nil {
			return fmt.Errorf("failed to generate modeline for %s: %w", node.Hostname, err)
		}

		if err := os.WriteFile(filePath, []byte(ml+"\n"), 0o644); err != nil {
			return fmt.Errorf("failed to write %s: %w", filePath, err)
		}

		fmt.Fprintf(os.Stderr, "Created %s\n", filePath)
	}

	return nil
}

// extractIP returns the IP address without CIDR mask.
func extractIP(address string) string {
	if idx := strings.IndexByte(address, '/'); idx >= 0 {
		return address[:idx]
	}
	return address
}

// templateForRole returns the template file path for the given node role.
func templateForRole(role string) string {
	switch role {
	case "controlplane":
		return "templates/controlplane.yaml"
	case "worker":
		return "templates/worker.yaml"
	default:
		return "templates/worker.yaml"
	}
}
