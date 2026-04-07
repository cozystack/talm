package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cozystack/talm/pkg/wizard"
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

func TestGenerateProject_SkipsExistingWithoutForce(t *testing.T) {
	rootDir := t.TempDir()

	// Create existing secrets.yaml with known content
	secretsFile := filepath.Join(rootDir, "secrets.yaml")
	if err := os.WriteFile(secretsFile, []byte("existing-secret"), 0o600); err != nil {
		t.Fatal(err)
	}

	opts := GenerateOptions{
		RootDir:     rootDir,
		Preset:      "generic",
		ClusterName: "test",
		Version:     "0.1.0",
		Force:       false,
	}

	// Should succeed, skipping existing files
	if err := GenerateProject(opts); err != nil {
		t.Fatalf("GenerateProject should skip existing files, got error: %v", err)
	}

	// Verify existing file was NOT overwritten
	content := readFile(t, rootDir, "secrets.yaml")
	if content != "existing-secret" {
		t.Error("secrets.yaml was overwritten despite Force=false")
	}

	// But new files should still be created
	assertFileExists(t, rootDir, "Chart.yaml")
	assertFileExists(t, rootDir, "talosconfig")
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

func TestMergeValuesOverrides_RejectsNestedMaps(t *testing.T) {
	tmpDir := t.TempDir()
	valuesPath := filepath.Join(tmpDir, "values.yaml")

	// Write a values.yaml with a nested map
	content := "network:\n  podSubnets:\n  - 10.244.0.0/16\n  serviceSubnets:\n  - 10.96.0.0/16\n"
	if err := os.WriteFile(valuesPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	// Attempt to override the nested map — should be rejected
	overrides := map[string]interface{}{
		"network": map[string]interface{}{
			"podSubnets": []string{"custom"},
		},
	}

	err := mergeValuesOverrides(valuesPath, overrides)
	if err == nil {
		t.Fatal("expected error for nested map override, got nil")
	}
	if !strings.Contains(err.Error(), "nested map override") {
		t.Errorf("expected 'nested map override' error, got: %v", err)
	}
}

func TestMergeValuesOverrides_ListReplacement(t *testing.T) {
	tmpDir := t.TempDir()
	valuesPath := filepath.Join(tmpDir, "values.yaml")

	content := "podSubnets:\n- 10.244.0.0/16\n- 10.245.0.0/16\n"
	if err := os.WriteFile(valuesPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	overrides := map[string]interface{}{
		"podSubnets": []string{"10.244.0.0/16"},
	}

	if err := mergeValuesOverrides(valuesPath, overrides); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(valuesPath)
	// List should be replaced entirely (only 1 entry, not 2)
	if strings.Contains(string(data), "10.245.0.0/16") {
		t.Error("second subnet should have been replaced (shallow merge replaces entire list)")
	}
}

func TestGenerateProject_Idempotent(t *testing.T) {
	rootDir := t.TempDir()
	opts := GenerateOptions{
		RootDir:     rootDir,
		Preset:      "generic",
		ClusterName: "test",
		Version:     "0.1.0",
		Force:       false,
	}

	if err := GenerateProject(opts); err != nil {
		t.Fatalf("first GenerateProject failed: %v", err)
	}

	secretsBefore := readFile(t, rootDir, "secrets.yaml")

	// Run again — should succeed and NOT overwrite existing files
	if err := GenerateProject(opts); err != nil {
		t.Fatalf("second GenerateProject should be idempotent, got: %v", err)
	}

	secretsAfter := readFile(t, rootDir, "secrets.yaml")
	if secretsBefore != secretsAfter {
		t.Error("secrets.yaml was overwritten on idempotent re-run")
	}
}

func TestMergeValuesOverrides_FlatKeysWork(t *testing.T) {
	tmpDir := t.TempDir()
	valuesPath := filepath.Join(tmpDir, "values.yaml")

	content := "endpoint: \"https://old:6443\"\npodSubnets:\n- 10.244.0.0/16\n"
	if err := os.WriteFile(valuesPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	overrides := map[string]interface{}{
		"endpoint": "https://new:6443",
	}

	if err := mergeValuesOverrides(valuesPath, overrides); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(valuesPath)
	if !strings.Contains(string(data), "https://new:6443") {
		t.Error("endpoint not updated")
	}
	if !strings.Contains(string(data), "podSubnets") {
		t.Error("podSubnets lost after merge")
	}
}

func TestBuildValuesOverrides_EmptyEndpoint(t *testing.T) {
	result := wizard.WizardResult{Endpoint: ""}
	overrides := buildValuesOverrides(result)
	if _, ok := overrides["endpoint"]; ok {
		t.Error("empty endpoint should not be included in overrides")
	}
}

func TestBuildValuesOverrides_PopulatesFields(t *testing.T) {
	result := wizard.WizardResult{
		Endpoint:          "https://10.0.0.1:6443",
		PodSubnets:        "10.244.0.0/16",
		ServiceSubnets:    "10.96.0.0/16",
		AdvertisedSubnets: "192.168.1.0/24",
		FloatingIP:        "10.0.0.100",
	}
	overrides := buildValuesOverrides(result)

	if overrides["endpoint"] != "https://10.0.0.1:6443" {
		t.Errorf("endpoint = %v", overrides["endpoint"])
	}
	if overrides["floatingIP"] != "10.0.0.100" {
		t.Errorf("floatingIP = %v", overrides["floatingIP"])
	}
	if _, ok := overrides["podSubnets"]; !ok {
		t.Error("podSubnets missing")
	}
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
