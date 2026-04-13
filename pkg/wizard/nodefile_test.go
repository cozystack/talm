package wizard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteNodeFiles_CreatesFiles(t *testing.T) {
	rootDir := t.TempDir()
	nodesDir := filepath.Join(rootDir, "nodes")
	if err := os.MkdirAll(nodesDir, 0o755); err != nil {
		t.Fatal(err)
	}

	nodes := []NodeConfig{
		{Hostname: "cp-1", Role: "controlplane", Addresses: "10.0.0.1/24"},
		{Hostname: "worker-1", Role: "worker", Addresses: "10.0.0.2/24"},
	}

	if err := WriteNodeFiles(rootDir, nodes); err != nil {
		t.Fatalf("WriteNodeFiles() error = %v", err)
	}

	// Check files exist
	for _, node := range nodes {
		path := filepath.Join(nodesDir, node.Hostname+".yaml")
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("expected file %s to exist", path)
		}
	}
}

func TestWriteNodeFiles_ModelineContent(t *testing.T) {
	rootDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(rootDir, "nodes"), 0o755); err != nil {
		t.Fatal(err)
	}

	nodes := []NodeConfig{
		{Hostname: "cp-1", Role: "controlplane", Addresses: "10.0.0.1/24"},
	}

	if err := WriteNodeFiles(rootDir, nodes); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(rootDir, "nodes", "cp-1.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	if !strings.HasPrefix(content, "# talm:") {
		t.Errorf("file should start with modeline, got: %s", content[:min(len(content), 50)])
	}
	if !strings.Contains(content, `"10.0.0.1"`) {
		t.Error("modeline should contain node IP")
	}
	if !strings.Contains(content, "controlplane.yaml") {
		t.Error("modeline should reference controlplane template")
	}
}

func TestWriteNodeFiles_WorkerTemplate(t *testing.T) {
	rootDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(rootDir, "nodes"), 0o755); err != nil {
		t.Fatal(err)
	}

	nodes := []NodeConfig{
		{Hostname: "w-1", Role: "worker", Addresses: "10.0.0.5/24"},
	}

	if err := WriteNodeFiles(rootDir, nodes); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(rootDir, "nodes", "w-1.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	if !strings.Contains(content, "worker.yaml") {
		t.Error("modeline should reference worker template")
	}
}

func TestWriteNodeFiles_DoesNotOverwrite(t *testing.T) {
	rootDir := t.TempDir()
	nodesDir := filepath.Join(rootDir, "nodes")
	if err := os.MkdirAll(nodesDir, 0o755); err != nil {
		t.Fatal(err)
	}

	existing := filepath.Join(nodesDir, "cp-1.yaml")
	if err := os.WriteFile(existing, []byte("existing content"), 0o644); err != nil {
		t.Fatal(err)
	}

	nodes := []NodeConfig{
		{Hostname: "cp-1", Role: "controlplane", Addresses: "10.0.0.1/24"},
	}

	if err := WriteNodeFiles(rootDir, nodes); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(existing)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "existing content" {
		t.Error("existing file was overwritten")
	}
}

func TestWriteNodeFiles_ExtractsIPFromCIDR(t *testing.T) {
	rootDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(rootDir, "nodes"), 0o755); err != nil {
		t.Fatal(err)
	}

	nodes := []NodeConfig{
		{Hostname: "node-1", Role: "worker", Addresses: "192.168.1.100/24"},
	}

	if err := WriteNodeFiles(rootDir, nodes); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(rootDir, "nodes", "node-1.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	// Should contain bare IP (without /24) in modeline
	if !strings.Contains(content, `"192.168.1.100"`) {
		t.Errorf("modeline should contain bare IP without mask, got: %s", content)
	}
	if strings.Contains(content, `/24`) {
		t.Error("modeline should not contain CIDR mask")
	}
}

func TestWriteNodeFiles_CreatesNodesDir(t *testing.T) {
	rootDir := t.TempDir()
	// Don't create nodes/ dir - WriteNodeFiles should create it

	nodes := []NodeConfig{
		{Hostname: "n1", Role: "worker", Addresses: "10.0.0.1/24"},
	}

	if err := WriteNodeFiles(rootDir, nodes); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(rootDir, "nodes", "n1.yaml")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("file should be created even when nodes/ dir doesn't exist")
	}
}

func TestWriteNodeFiles_PathTraversal(t *testing.T) {
	rootDir := t.TempDir()

	nodes := []NodeConfig{
		{Hostname: "../escape", Role: "worker", Addresses: "10.0.0.1/24"},
	}

	if err := WriteNodeFiles(rootDir, nodes); err != nil {
		t.Fatal(err)
	}

	// Should create nodes/escape.yaml (base name only), NOT ../escape.yaml
	escapedPath := filepath.Join(rootDir, "escape.yaml")
	if _, err := os.Stat(escapedPath); err == nil {
		t.Error("path traversal: file created outside nodes/ directory")
	}

	safePath := filepath.Join(rootDir, "nodes", "escape.yaml")
	if _, err := os.Stat(safePath); os.IsNotExist(err) {
		t.Error("expected file at nodes/escape.yaml (sanitized)")
	}
}

func TestWriteNodeFiles_DuplicateHostnames(t *testing.T) {
	rootDir := t.TempDir()

	nodes := []NodeConfig{
		{Hostname: "node-1", Role: "controlplane", Addresses: "10.0.0.1/24"},
		{Hostname: "node-1", Role: "worker", Addresses: "10.0.0.2/24"},
	}

	err := WriteNodeFiles(rootDir, nodes)
	if err == nil {
		t.Error("expected error for duplicate hostnames")
	}
}

func TestWriteNodeFiles_SlashHostname(t *testing.T) {
	rootDir := t.TempDir()

	nodes := []NodeConfig{
		{Hostname: "/", Role: "worker", Addresses: "10.0.0.1/24"},
	}

	err := WriteNodeFiles(rootDir, nodes)
	if err == nil {
		t.Error("expected error for '/' hostname")
	}
}

func TestWriteNodeFiles_InvalidHostname(t *testing.T) {
	rootDir := t.TempDir()

	nodes := []NodeConfig{
		{Hostname: "..", Role: "worker", Addresses: "10.0.0.1/24"},
	}

	err := WriteNodeFiles(rootDir, nodes)
	if err == nil {
		t.Error("expected error for '..' hostname")
	}
}

// §8 — two hostnames that sanitize to the same safe name must be rejected

func TestWriteNodeFiles_NormalizedCollision(t *testing.T) {
	rootDir := t.TempDir()

	nodes := []NodeConfig{
		{Hostname: "cp-1", Role: "controlplane", Addresses: "10.0.0.1/24"},
		{Hostname: "../cp-1", Role: "worker", Addresses: "10.0.0.2/24"},
	}

	err := WriteNodeFiles(rootDir, nodes)
	if err == nil {
		t.Error("expected error for hostnames that collide after sanitization")
	}
}

// §8 — unknown role must return an error, not silently fall back to worker

func TestWriteNodeFiles_UnknownRole(t *testing.T) {
	rootDir := t.TempDir()

	nodes := []NodeConfig{
		{Hostname: "master-1", Role: "master", Addresses: "10.0.0.1/24"},
	}

	err := WriteNodeFiles(rootDir, nodes)
	if err == nil {
		t.Error("expected error for unknown role 'master', got nil (silent worker fallback)")
	}
}

// §4 — when ManagementIP differs from node IP, modeline must carry both
// (nodes = node IP extracted from Addresses; endpoints = ManagementIP)

func TestWriteNodeFiles_ManagementIPDistinctFromNodeIP(t *testing.T) {
	rootDir := t.TempDir()

	nodes := []NodeConfig{
		{
			Hostname:     "cp-1",
			Role:         "controlplane",
			Addresses:    "10.0.0.1/24",
			ManagementIP: "203.0.113.5",
		},
	}

	if err := WriteNodeFiles(rootDir, nodes); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(rootDir, "nodes", "cp-1.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	// nodes field must reference the internal address
	if !strings.Contains(content, `"10.0.0.1"`) {
		t.Errorf("modeline should contain node IP 10.0.0.1, got:\n%s", content)
	}
	// endpoints field must reference the management IP
	if !strings.Contains(content, `"203.0.113.5"`) {
		t.Errorf("modeline should contain management IP 203.0.113.5, got:\n%s", content)
	}
}
