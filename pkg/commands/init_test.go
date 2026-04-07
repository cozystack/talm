package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateProject_Generic(t *testing.T) {
	rootDir := t.TempDir()
	opts := GenerateOptions{
		RootDir:     rootDir,
		Preset:      "generic",
		ClusterName: "test-cluster",
		Version:     "0.1.0",
		Force:       false,
	}

	if err := GenerateProject(opts); err != nil {
		t.Fatalf("GenerateProject failed: %v", err)
	}

	assertFileExists(t, rootDir, "secrets.yaml")
	assertFileExists(t, rootDir, "talosconfig")
	assertFileExists(t, rootDir, "Chart.yaml")
	assertFileExists(t, rootDir, "values.yaml")
	assertFileExists(t, rootDir, ".gitignore")
	assertDirExists(t, rootDir, "nodes")
	assertDirExists(t, rootDir, "templates")
	assertDirExists(t, rootDir, "charts/talm")
	assertFileExists(t, rootDir, "charts/talm/Chart.yaml")
	assertFileExists(t, rootDir, "charts/talm/templates/_helpers.tpl")

	assertFileContains(t, rootDir, "Chart.yaml", "test-cluster")
	assertFileContains(t, rootDir, "Chart.yaml", "0.1.0")

	gitignore := readFile(t, rootDir, ".gitignore")
	for _, entry := range []string{"secrets.yaml", "talosconfig", "talm.key", "kubeconfig"} {
		if !strings.Contains(gitignore, entry) {
			t.Errorf(".gitignore missing entry %q", entry)
		}
	}
}

func TestGenerateProject_Cozystack(t *testing.T) {
	rootDir := t.TempDir()
	opts := GenerateOptions{
		RootDir:     rootDir,
		Preset:      "cozystack",
		ClusterName: "cozy-cluster",
		Version:     "1.0.0",
		Force:       false,
	}

	if err := GenerateProject(opts); err != nil {
		t.Fatalf("GenerateProject failed: %v", err)
	}

	assertFileExists(t, rootDir, "secrets.yaml")
	assertFileExists(t, rootDir, "talosconfig")
	assertFileExists(t, rootDir, "Chart.yaml")
	assertFileExists(t, rootDir, "values.yaml")
	assertFileExists(t, rootDir, "nodes")

	assertFileContains(t, rootDir, "Chart.yaml", "cozy-cluster")
	assertFileContains(t, rootDir, "values.yaml", "floatingIP")
}

func TestGenerateProject_InvalidPreset(t *testing.T) {
	rootDir := t.TempDir()
	opts := GenerateOptions{
		RootDir:     rootDir,
		Preset:      "nonexistent",
		ClusterName: "test",
		Version:     "0.1.0",
	}

	err := GenerateProject(opts)
	if err == nil {
		t.Fatal("expected error for invalid preset, got nil")
	}
	if !strings.Contains(err.Error(), "invalid preset") {
		t.Errorf("expected 'invalid preset' error, got: %v", err)
	}
}

func TestGenerateProject_NoOverwriteWithoutForce(t *testing.T) {
	rootDir := t.TempDir()

	// Create existing secrets.yaml
	secretsFile := filepath.Join(rootDir, "secrets.yaml")
	if err := os.WriteFile(secretsFile, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}

	opts := GenerateOptions{
		RootDir:     rootDir,
		Preset:      "generic",
		ClusterName: "test",
		Version:     "0.1.0",
		Force:       false,
	}

	err := GenerateProject(opts)
	if err == nil {
		t.Fatal("expected error when file exists without force, got nil")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("expected 'already exists' error, got: %v", err)
	}
}

func TestGenerateProject_ForceOverwrite(t *testing.T) {
	rootDir := t.TempDir()

	// Create existing secrets.yaml
	secretsFile := filepath.Join(rootDir, "secrets.yaml")
	if err := os.WriteFile(secretsFile, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}

	opts := GenerateOptions{
		RootDir:     rootDir,
		Preset:      "generic",
		ClusterName: "test",
		Version:     "0.1.0",
		Force:       true,
	}

	if err := GenerateProject(opts); err != nil {
		t.Fatalf("GenerateProject with force failed: %v", err)
	}

	content := readFile(t, rootDir, "secrets.yaml")
	if content == "existing" {
		t.Error("secrets.yaml was not overwritten with force=true")
	}
}

func TestGenerateProject_DefaultVersion(t *testing.T) {
	rootDir := t.TempDir()
	opts := GenerateOptions{
		RootDir:     rootDir,
		Preset:      "generic",
		ClusterName: "test",
		Version:     "", // should default to "0.1.0"
	}

	if err := GenerateProject(opts); err != nil {
		t.Fatalf("GenerateProject failed: %v", err)
	}

	assertFileContains(t, rootDir, "Chart.yaml", "0.1.0")
}

func TestGenerateProject_ValuesOverrides(t *testing.T) {
	rootDir := t.TempDir()
	opts := GenerateOptions{
		RootDir:     rootDir,
		Preset:      "generic",
		ClusterName: "test",
		Version:     "0.1.0",
		ValuesOverrides: map[string]interface{}{
			"endpoint": "https://10.0.0.1:6443",
		},
	}

	if err := GenerateProject(opts); err != nil {
		t.Fatalf("GenerateProject failed: %v", err)
	}

	content := readFile(t, rootDir, "values.yaml")
	if !strings.Contains(content, "https://10.0.0.1:6443") {
		t.Errorf("values.yaml should contain overridden endpoint, got:\n%s", content)
	}
	// Original default endpoint should be replaced
	if strings.Contains(content, "192.168.100.10") {
		t.Error("values.yaml still contains default endpoint after override")
	}
}

func TestGenerateProject_ValuesOverridesPreservesOtherFields(t *testing.T) {
	rootDir := t.TempDir()
	opts := GenerateOptions{
		RootDir:     rootDir,
		Preset:      "generic",
		ClusterName: "test",
		Version:     "0.1.0",
		ValuesOverrides: map[string]interface{}{
			"endpoint": "https://custom:6443",
		},
	}

	if err := GenerateProject(opts); err != nil {
		t.Fatalf("GenerateProject failed: %v", err)
	}

	// podSubnets should still be present from preset defaults
	assertFileContains(t, rootDir, "values.yaml", "podSubnets")
	assertFileContains(t, rootDir, "values.yaml", "serviceSubnets")
}

// Test helpers

func assertFileExists(t *testing.T, rootDir, relPath string) {
	t.Helper()
	path := filepath.Join(rootDir, relPath)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Errorf("expected file %s to exist", relPath)
	}
}

func assertDirExists(t *testing.T, rootDir, relPath string) {
	t.Helper()
	path := filepath.Join(rootDir, relPath)
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		t.Errorf("expected directory %s to exist", relPath)
		return
	}
	if !info.IsDir() {
		t.Errorf("expected %s to be a directory", relPath)
	}
}

func assertFileContains(t *testing.T, rootDir, relPath, substring string) {
	t.Helper()
	content := readFile(t, rootDir, relPath)
	if !strings.Contains(content, substring) {
		t.Errorf("file %s does not contain %q", relPath, substring)
	}
}

func readFile(t *testing.T, rootDir, relPath string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(rootDir, relPath))
	if err != nil {
		t.Fatalf("failed to read %s: %v", relPath, err)
	}
	return string(data)
}
