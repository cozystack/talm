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

	// Validate + dedup by the *sanitized* filename so inputs like "cp-1" and
	// "../cp-1" can't collide silently.
	seen := make(map[string]bool, len(nodes))
	for _, node := range nodes {
		safeName := filepath.Base(node.Hostname)
		if safeName == "." || safeName == ".." || safeName == "" || strings.ContainsAny(safeName, "/\\") {
			return fmt.Errorf("invalid hostname for file creation: %q", node.Hostname)
		}
		if err := ValidateHostname(safeName); err != nil {
			return fmt.Errorf("invalid hostname for file creation: %w", err)
		}
		if seen[safeName] {
			return fmt.Errorf("duplicate hostname after sanitization: %q", safeName)
		}
		seen[safeName] = true
	}

	for _, node := range nodes {
		safeName := filepath.Base(node.Hostname)
		filePath := filepath.Join(nodesDir, safeName+".yaml")

		if _, err := os.Stat(filePath); err == nil {
			fmt.Fprintf(os.Stderr, "Skipping %s (already exists)\n", filePath)
			continue
		}

		nodeIP := extractIP(node.Addresses)
		managementIP := node.ManagementIP
		if managementIP == "" {
			managementIP = nodeIP
		}

		templateFile, err := templateForRole(node.Role)
		if err != nil {
			return err
		}

		ml, err := modeline.GenerateModeline(
			[]string{nodeIP},
			[]string{managementIP},
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
// Unknown roles return an error rather than silently falling back to worker —
// that would mask typos like "master" as correctly-generated artifacts.
func templateForRole(role string) (string, error) {
	switch role {
	case "controlplane":
		return "templates/controlplane.yaml", nil
	case "worker":
		return "templates/worker.yaml", nil
	default:
		return "", fmt.Errorf("unsupported node role: %q (expected %q or %q)", role, "controlplane", "worker")
	}
}
