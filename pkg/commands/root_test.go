package commands

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProcessModelineAndUpdateGlobals_ReturnsTemplates(t *testing.T) {
	origNodes := GlobalArgs.Nodes
	origEndpoints := GlobalArgs.Endpoints
	defer func() {
		GlobalArgs.Nodes = origNodes
		GlobalArgs.Endpoints = origEndpoints
	}()

	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "node0.yaml")
	content := `# talm: nodes=["10.0.0.1"], endpoints=["10.0.0.1"], templates=["templates/controlplane.yaml"]
machine:
  type: controlplane
`
	if err := os.WriteFile(configFile, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}

	GlobalArgs.Nodes = []string{}
	GlobalArgs.Endpoints = []string{}

	templates, err := processModelineAndUpdateGlobals(configFile, false, false, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(templates) != 1 {
		t.Fatalf("expected 1 template, got %d", len(templates))
	}
	if templates[0] != "templates/controlplane.yaml" {
		t.Errorf("expected templates/controlplane.yaml, got %s", templates[0])
	}
}

func TestProcessModelineAndUpdateGlobals_NoTemplates(t *testing.T) {
	origNodes := GlobalArgs.Nodes
	origEndpoints := GlobalArgs.Endpoints
	defer func() {
		GlobalArgs.Nodes = origNodes
		GlobalArgs.Endpoints = origEndpoints
	}()

	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "node0.yaml")
	content := `# talm: nodes=["10.0.0.1"], endpoints=["10.0.0.1"]
machine:
  type: controlplane
`
	if err := os.WriteFile(configFile, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}

	GlobalArgs.Nodes = []string{}
	GlobalArgs.Endpoints = []string{}

	templates, err := processModelineAndUpdateGlobals(configFile, false, false, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(templates) != 0 {
		t.Errorf("expected 0 templates, got %d: %v", len(templates), templates)
	}
}

func TestProcessModelineAndUpdateGlobals_InvalidModeline(t *testing.T) {
	origNodes := GlobalArgs.Nodes
	origEndpoints := GlobalArgs.Endpoints
	defer func() {
		GlobalArgs.Nodes = origNodes
		GlobalArgs.Endpoints = origEndpoints
	}()

	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "node0.yaml")
	content := `# talm: this is not valid modeline syntax
machine:
  type: controlplane
`
	if err := os.WriteFile(configFile, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}

	GlobalArgs.Nodes = []string{}
	GlobalArgs.Endpoints = []string{}

	templates, err := processModelineAndUpdateGlobals(configFile, false, false, true)
	if err == nil {
		t.Fatal("expected error for invalid modeline, got nil")
	}
	if templates != nil {
		t.Errorf("expected nil templates on error, got %v", templates)
	}
}

func TestProcessModelineAndUpdateGlobals_EmptyNodesError(t *testing.T) {
	origNodes := GlobalArgs.Nodes
	origEndpoints := GlobalArgs.Endpoints
	defer func() {
		GlobalArgs.Nodes = origNodes
		GlobalArgs.Endpoints = origEndpoints
	}()

	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "node0.yaml")
	// Valid modeline but with empty nodes
	content := `# talm: nodes=[], endpoints=["10.0.0.1"], templates=["templates/controlplane.yaml"]
machine:
  type: controlplane
`
	if err := os.WriteFile(configFile, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}

	GlobalArgs.Nodes = []string{}
	GlobalArgs.Endpoints = []string{}

	templates, err := processModelineAndUpdateGlobals(configFile, false, false, true)
	if err == nil {
		t.Fatal("expected error for empty nodes, got nil")
	}
	if templates != nil {
		t.Errorf("expected nil templates on error, got %v", templates)
	}
}
