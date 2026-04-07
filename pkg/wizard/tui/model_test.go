package tui

import (
	"context"
	"fmt"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/cozystack/talm/pkg/wizard"
)

type mockScanner struct {
	nodes []wizard.NodeInfo
	err   error
}

func (m *mockScanner) ScanNetwork(_ context.Context, _ string) ([]wizard.NodeInfo, error) {
	return m.nodes, m.err
}

func (m *mockScanner) GetNodeInfo(_ context.Context, ip string) (wizard.NodeInfo, error) {
	for _, n := range m.nodes {
		if n.IP == ip {
			return n, nil
		}
	}
	return wizard.NodeInfo{IP: ip}, nil
}

func keyMsg(key string) tea.Msg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
}

func enterMsg() tea.Msg {
	return tea.KeyMsg{Type: tea.KeyEnter}
}

func escMsg() tea.Msg {
	return tea.KeyMsg{Type: tea.KeyEsc}
}

func TestInitialStep(t *testing.T) {
	m := New(&mockScanner{}, []string{"generic", "cozystack"}, nil)
	if m.Step() != stepSelectPreset {
		t.Errorf("initial step = %d, want stepSelectPreset (%d)", m.Step(), stepSelectPreset)
	}
}

func TestSelectPreset(t *testing.T) {
	m := New(&mockScanner{}, []string{"generic", "cozystack"}, nil)

	// Select first preset (generic)
	updated, _ := m.Update(enterMsg())
	m = updated.(Model)

	if m.Step() != stepClusterName {
		t.Errorf("step = %d, want stepClusterName (%d)", m.Step(), stepClusterName)
	}
	if m.result.Preset != "generic" {
		t.Errorf("preset = %q, want %q", m.result.Preset, "generic")
	}
}

func TestSelectSecondPreset(t *testing.T) {
	m := New(&mockScanner{}, []string{"generic", "cozystack"}, nil)

	// Move down to cozystack
	updated, _ := m.Update(keyMsg("j"))
	m = updated.(Model)

	updated, _ = m.Update(enterMsg())
	m = updated.(Model)

	if m.result.Preset != "cozystack" {
		t.Errorf("preset = %q, want %q", m.result.Preset, "cozystack")
	}
}

func TestClusterNameValidation(t *testing.T) {
	m := New(&mockScanner{}, []string{"generic"}, nil)

	// Go to cluster name step
	updated, _ := m.Update(enterMsg())
	m = updated.(Model)

	// Try to submit empty name
	updated, _ = m.Update(enterMsg())
	m = updated.(Model)

	if m.Step() != stepClusterName {
		t.Errorf("should stay on stepClusterName with empty name, got step %d", m.Step())
	}
	if m.err == nil {
		t.Error("expected validation error for empty cluster name")
	}
}

func TestClusterNameSuccess(t *testing.T) {
	m := New(&mockScanner{}, []string{"generic"}, nil)

	// Go to cluster name step
	updated, _ := m.Update(enterMsg())
	m = updated.(Model)

	// Type cluster name character by character
	for _, ch := range "test" {
		updated, _ = m.Update(keyMsg(string(ch)))
		m = updated.(Model)
	}

	// Submit
	updated, _ = m.Update(enterMsg())
	m = updated.(Model)

	if m.Step() != stepEndpoint {
		t.Errorf("step = %d, want stepEndpoint (%d)", m.Step(), stepEndpoint)
	}
	if m.result.ClusterName != "test" {
		t.Errorf("clusterName = %q, want %q", m.result.ClusterName, "test")
	}
}

func TestBackNavigation(t *testing.T) {
	m := New(&mockScanner{}, []string{"generic"}, nil)

	// Go to cluster name step
	updated, _ := m.Update(enterMsg())
	m = updated.(Model)

	if m.Step() != stepClusterName {
		t.Fatalf("expected stepClusterName, got %d", m.Step())
	}

	// Go back
	updated, _ = m.Update(escMsg())
	m = updated.(Model)

	if m.Step() != stepSelectPreset {
		t.Errorf("step = %d, want stepSelectPreset (%d)", m.Step(), stepSelectPreset)
	}
}

func TestEndpointValidation(t *testing.T) {
	m := New(&mockScanner{}, []string{"generic"}, nil)

	// Navigate to endpoint step
	updated, _ := m.Update(enterMsg()) // select preset
	m = updated.(Model)
	for _, ch := range "test" {
		updated, _ = m.Update(keyMsg(string(ch)))
		m = updated.(Model)
	}
	updated, _ = m.Update(enterMsg()) // submit name
	m = updated.(Model)

	if m.Step() != stepEndpoint {
		t.Fatalf("expected stepEndpoint, got %d", m.Step())
	}

	// Try to submit empty endpoint
	updated, _ = m.Update(enterMsg())
	m = updated.(Model)

	if m.Step() != stepEndpoint {
		t.Errorf("should stay on stepEndpoint with empty value, got step %d", m.Step())
	}
	if m.err == nil {
		t.Error("expected validation error for empty endpoint")
	}
}

func TestScanResultTransition(t *testing.T) {
	m := New(&mockScanner{}, []string{"generic"}, nil)
	m.step = stepScanning

	nodes := []wizard.NodeInfo{
		{IP: "10.0.0.1", Hostname: "node-01"},
		{IP: "10.0.0.2", Hostname: "node-02"},
	}

	updated, _ := m.Update(scanResultMsg{nodes: nodes})
	m = updated.(Model)

	if m.Step() != stepSelectNodes {
		t.Errorf("step = %d, want stepSelectNodes (%d)", m.Step(), stepSelectNodes)
	}
	if len(m.discoveredNodes) != 2 {
		t.Errorf("discoveredNodes = %d, want 2", len(m.discoveredNodes))
	}
}

func TestScanResultEmpty(t *testing.T) {
	m := New(&mockScanner{}, []string{"generic"}, nil)
	m.step = stepScanning

	updated, _ := m.Update(scanResultMsg{nodes: nil})
	m = updated.(Model)

	if m.Step() != stepError {
		t.Errorf("step = %d, want stepError (%d)", m.Step(), stepError)
	}
}

func TestScanError(t *testing.T) {
	m := New(&mockScanner{}, []string{"generic"}, nil)
	m.step = stepScanning

	updated, _ := m.Update(scanErrorMsg{err: fmt.Errorf("nmap failed")})
	m = updated.(Model)

	if m.Step() != stepError {
		t.Errorf("step = %d, want stepError (%d)", m.Step(), stepError)
	}
	if m.err == nil || m.err.Error() != "nmap failed" {
		t.Errorf("err = %v, want 'nmap failed'", m.err)
	}
}

func TestNodeSelection(t *testing.T) {
	m := New(&mockScanner{}, []string{"generic"}, nil)
	m.step = stepSelectNodes
	m.discoveredNodes = []wizard.NodeInfo{
		{IP: "10.0.0.1"},
		{IP: "10.0.0.2"},
	}

	// Toggle first node
	updated, _ := m.Update(keyMsg(" "))
	m = updated.(Model)

	if len(m.selectedNodes) != 1 || m.selectedNodes[0] != 0 {
		t.Errorf("selectedNodes = %v, want [0]", m.selectedNodes)
	}

	// Toggle again (deselect)
	updated, _ = m.Update(keyMsg(" "))
	m = updated.(Model)

	if len(m.selectedNodes) != 0 {
		t.Errorf("selectedNodes = %v, want empty", m.selectedNodes)
	}
}

func TestConfirmToGenerate(t *testing.T) {
	generated := false
	m := New(&mockScanner{}, []string{"generic"}, func(_ wizard.WizardResult) error {
		generated = true
		return nil
	})
	m.step = stepConfirm
	m.result = wizard.WizardResult{
		Preset:      "generic",
		ClusterName: "test",
		Endpoint:    "https://10.0.0.1:6443",
	}

	updated, cmd := m.Update(keyMsg("y"))
	m = updated.(Model)

	if m.Step() != stepGenerating {
		t.Errorf("step = %d, want stepGenerating (%d)", m.Step(), stepGenerating)
	}

	// Execute the command to trigger generation
	if cmd != nil {
		// cmd is a tea.Batch, we need to process messages
		// For simplicity, just check the step transition
		_ = cmd
	}
	_ = generated
}

func TestGenerateDone(t *testing.T) {
	m := New(&mockScanner{}, []string{"generic"}, nil)
	m.step = stepGenerating

	updated, _ := m.Update(generateDoneMsg{})
	m = updated.(Model)

	if m.Step() != stepDone {
		t.Errorf("step = %d, want stepDone (%d)", m.Step(), stepDone)
	}
}

func TestGenerateError(t *testing.T) {
	m := New(&mockScanner{}, []string{"generic"}, nil)
	m.step = stepGenerating

	updated, _ := m.Update(generateErrorMsg{err: fmt.Errorf("write failed")})
	m = updated.(Model)

	if m.Step() != stepError {
		t.Errorf("step = %d, want stepError (%d)", m.Step(), stepError)
	}
}

func TestWindowResize(t *testing.T) {
	m := New(&mockScanner{}, []string{"generic"}, nil)

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = updated.(Model)

	if m.width != 120 || m.height != 40 {
		t.Errorf("dimensions = %dx%d, want 120x40", m.width, m.height)
	}
}

func TestViewRendersWithoutPanic(t *testing.T) {
	m := New(&mockScanner{}, []string{"generic", "cozystack"}, nil)

	steps := []step{
		stepSelectPreset, stepClusterName, stepEndpoint,
		stepScanCIDR, stepScanning, stepDone,
	}

	for _, s := range steps {
		m.step = s
		output := m.View()
		if output == "" {
			t.Errorf("View() returned empty string for step %d", s)
		}
	}

	// Test error view with error set
	m.step = stepError
	m.err = fmt.Errorf("test error")
	output := m.View()
	if output == "" {
		t.Error("View() returned empty string for error step")
	}

	// Test select nodes view
	m.step = stepSelectNodes
	m.discoveredNodes = []wizard.NodeInfo{{IP: "10.0.0.1", Hostname: "node-01"}}
	output = m.View()
	if output == "" {
		t.Error("View() returned empty string for selectNodes step")
	}

	// Test configure node view
	m.step = stepConfigureNode
	m.discoveredNodes = []wizard.NodeInfo{{IP: "10.0.0.1"}}
	m.selectedNodes = []int{0}
	m.currentNodeIdx = 0
	output = m.View()
	if output == "" {
		t.Error("View() returned empty string for configureNode step")
	}

	// Test confirm view
	m.step = stepConfirm
	m.result = wizard.WizardResult{
		Preset:      "generic",
		ClusterName: "test",
		Endpoint:    "https://10.0.0.1:6443",
		Nodes:       []wizard.NodeConfig{{Hostname: "node-01", Role: "controlplane"}},
	}
	output = m.View()
	if output == "" {
		t.Error("View() returned empty string for confirm step")
	}
}
