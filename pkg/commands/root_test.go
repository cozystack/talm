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

// TestProcessModelineAndUpdateGlobals_AcceptsLeadingComments pins
// that the modeline parser used by apply / upgrade / completion /
// wrapped talosctl commands shares the same file-shape contract as
// `talm template -I`. Without this, a node file produced by
// the in-place rewrite — which preserves operator comments above
// the modeline — would fail on the very next `talm apply -f` /
// `talm upgrade -f` call against the same file.
func TestProcessModelineAndUpdateGlobals_AcceptsLeadingComments(t *testing.T) {
	origNodes := GlobalArgs.Nodes
	origEndpoints := GlobalArgs.Endpoints
	t.Cleanup(func() {
		GlobalArgs.Nodes = origNodes
		GlobalArgs.Endpoints = origEndpoints
	})

	dir := t.TempDir()
	configFile := filepath.Join(dir, "node.yaml")
	content := "# Operator note: reset 2026-05-12 after ticket OPS-1234\n" +
		"# DO NOT edit values directly; modify values.yaml and re-template\n" +
		"# talm: nodes=[\"10.0.0.1\"], endpoints=[\"10.0.0.1\"], templates=[\"templates/cp.yaml\"]\n" +
		"machine:\n  type: controlplane\n"
	if err := os.WriteFile(configFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	GlobalArgs.Nodes = nil
	GlobalArgs.Endpoints = nil

	templates, err := processModelineAndUpdateGlobals(configFile, false, false, true)
	if err != nil {
		t.Fatalf("modeline parse must accept leading operator comments; got error: %v", err)
	}

	if len(templates) != 1 || templates[0] != "templates/cp.yaml" {
		t.Errorf("templates = %v, want [templates/cp.yaml]", templates)
	}

	if len(GlobalArgs.Nodes) != 1 || GlobalArgs.Nodes[0] != "10.0.0.1" {
		t.Errorf("GlobalArgs.Nodes = %v, want [10.0.0.1]", GlobalArgs.Nodes)
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
