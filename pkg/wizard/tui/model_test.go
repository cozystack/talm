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

func enterMsg() tea.Msg {
	return tea.KeyMsg{Type: tea.KeyEnter}
}

func escMsg() tea.Msg {
	return tea.KeyMsg{Type: tea.KeyEsc}
}

func keyMsg(key string) tea.Msg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
}

func TestInitialStep(t *testing.T) {
	m := New(&mockScanner{}, []string{"generic", "cozystack"}, nil)
	if m.Step() != stepSelectPreset {
		t.Errorf("initial step = %d, want stepSelectPreset (%d)", m.Step(), stepSelectPreset)
	}
}

func TestSelectPreset(t *testing.T) {
	m := New(&mockScanner{}, []string{"generic", "cozystack"}, nil)

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

	updated, _ := m.Update(enterMsg()) // select preset
	m = updated.(Model)
	updated, _ = m.Update(enterMsg()) // submit empty name
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

	updated, _ := m.Update(enterMsg()) // select preset
	m = updated.(Model)
	for _, ch := range "test" {
		updated, _ = m.Update(keyMsg(string(ch)))
		m = updated.(Model)
	}
	updated, _ = m.Update(enterMsg()) // submit name
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

	updated, _ := m.Update(enterMsg()) // go to cluster name
	m = updated.(Model)
	updated, _ = m.Update(escMsg()) // go back
	m = updated.(Model)

	if m.Step() != stepSelectPreset {
		t.Errorf("step = %d, want stepSelectPreset (%d)", m.Step(), stepSelectPreset)
	}
}

func TestEndpointValidation(t *testing.T) {
	m := New(&mockScanner{}, []string{"generic"}, nil)

	// Navigate to endpoint step
	updated, _ := m.Update(enterMsg())
	m = updated.(Model)
	for _, ch := range "test" {
		updated, _ = m.Update(keyMsg(string(ch)))
		m = updated.(Model)
	}
	updated, _ = m.Update(enterMsg())
	m = updated.(Model)

	// Submit empty endpoint
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
	m := New(&mockScanner{}, []string{"generic"}, func(_ wizard.WizardResult) error {
		return nil
	})
	m.step = stepConfirm
	m.result = wizard.WizardResult{
		Preset:      "generic",
		ClusterName: "test",
		Endpoint:    "https://10.0.0.1:6443",
	}

	updated, _ := m.Update(keyMsg("y"))
	m = updated.(Model)

	if m.Step() != stepGenerating {
		t.Errorf("step = %d, want stepGenerating (%d)", m.Step(), stepGenerating)
	}
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

// Manual node entry tests

func TestSkipScanTransition(t *testing.T) {
	m := New(&mockScanner{}, []string{"generic"}, nil)
	m.step = stepScanCIDR

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = updated.(Model)

	if m.Step() != stepManualNodeEntry {
		t.Errorf("step = %d, want stepManualNodeEntry (%d)", m.Step(), stepManualNodeEntry)
	}
}

func TestManualNodeEntry_AddAndDone(t *testing.T) {
	m := New(&mockScanner{}, []string{"generic"}, nil)
	m.step = stepManualNodeEntry

	// Set IP directly (textinput doesn't process rune messages without Focus)
	m.manualIPInput.SetValue("10.0.0.1")

	// Add it
	updated, _ := m.Update(enterMsg())
	m = updated.(Model)

	if len(m.manualNodes) != 1 {
		t.Fatalf("expected 1 manual node, got %d", len(m.manualNodes))
	}
	if m.manualNodes[0].IP != "10.0.0.1" {
		t.Errorf("IP = %q, want 10.0.0.1", m.manualNodes[0].IP)
	}

	// Press ctrl+d to finish
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	m = updated.(Model)

	if m.Step() != stepSelectNodes {
		t.Errorf("step = %d, want stepSelectNodes (%d)", m.Step(), stepSelectNodes)
	}
	if len(m.selectedNodes) != 1 {
		t.Error("manual nodes should be pre-selected")
	}
}

func TestManualNodeEntry_InvalidIP(t *testing.T) {
	m := New(&mockScanner{}, []string{"generic"}, nil)
	m.step = stepManualNodeEntry

	m.manualIPInput.SetValue("not-an-ip")

	updated, _ := m.Update(enterMsg())
	m = updated.(Model)

	if m.err == nil {
		t.Error("expected validation error for invalid IP")
	}
	if m.Step() != stepManualNodeEntry {
		t.Error("should stay on manual entry step")
	}
}

func TestManualNodeEntry_DoneWithoutNodes(t *testing.T) {
	m := New(&mockScanner{}, []string{"generic"}, nil)
	m.step = stepManualNodeEntry

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	m = updated.(Model)

	if m.err == nil {
		t.Error("expected error when pressing done with no nodes")
	}
	if m.Step() != stepManualNodeEntry {
		t.Error("should stay on manual entry step")
	}
}

// Node configuration validation tests

func TestNodeConfigValidation_InvalidRole(t *testing.T) {
	m := New(&mockScanner{}, []string{"generic"}, nil)
	m.step = stepConfigureNode
	m.discoveredNodes = []wizard.NodeInfo{{IP: "10.0.0.1"}}
	m.selectedNodes = []int{0}
	m.currentNodeIdx = 0
	m.prepareNodeInputs()

	// Set invalid role
	m.nodeInputs[fieldRole].SetValue("master")
	m.nodeInputs[fieldHostname].SetValue("node-01")

	updated, _ := m.Update(enterMsg())
	m = updated.(Model)

	if m.err == nil {
		t.Error("expected validation error for invalid role")
	}
	if m.Step() != stepConfigureNode {
		t.Error("should stay on configure step on validation error")
	}
}

func TestNodeConfigValidation_InvalidHostname(t *testing.T) {
	m := New(&mockScanner{}, []string{"generic"}, nil)
	m.step = stepConfigureNode
	m.discoveredNodes = []wizard.NodeInfo{{IP: "10.0.0.1"}}
	m.selectedNodes = []int{0}
	m.currentNodeIdx = 0
	m.prepareNodeInputs()

	m.nodeInputs[fieldRole].SetValue("controlplane")
	m.nodeInputs[fieldHostname].SetValue("-bad-name")

	updated, _ := m.Update(enterMsg())
	m = updated.(Model)

	if m.err == nil {
		t.Error("expected validation error for invalid hostname")
	}
}

func TestNodeConfigValidation_Success(t *testing.T) {
	m := New(&mockScanner{}, []string{"generic"}, nil)
	m.step = stepConfigureNode
	m.discoveredNodes = []wizard.NodeInfo{{IP: "10.0.0.1"}}
	m.selectedNodes = []int{0}
	m.currentNodeIdx = 0
	m.prepareNodeInputs()

	m.nodeInputs[fieldRole].SetValue("controlplane")
	m.nodeInputs[fieldHostname].SetValue("cp-1")
	m.nodeInputs[fieldDisk].SetValue("/dev/sda")
	m.nodeInputs[fieldInterface].SetValue("eth0")
	m.nodeInputs[fieldAddress].SetValue("10.0.0.1/24")
	m.nodeInputs[fieldGateway].SetValue("10.0.0.254")
	m.nodeInputs[fieldDNS].SetValue("8.8.8.8,1.1.1.1")

	updated, _ := m.Update(enterMsg())
	m = updated.(Model)

	if m.Step() != stepConfirm {
		t.Errorf("step = %d, want stepConfirm (%d), err = %v", m.Step(), stepConfirm, m.err)
	}
	if len(m.result.Nodes) != 1 {
		t.Fatalf("expected 1 configured node, got %d", len(m.result.Nodes))
	}
	n := m.result.Nodes[0]
	if n.Role != "controlplane" {
		t.Errorf("role = %q, want controlplane", n.Role)
	}
	if n.Gateway != "10.0.0.254" {
		t.Errorf("gateway = %q, want 10.0.0.254", n.Gateway)
	}
	if len(n.DNS) != 2 || n.DNS[0] != "8.8.8.8" || n.DNS[1] != "1.1.1.1" {
		t.Errorf("DNS = %v, want [8.8.8.8 1.1.1.1]", n.DNS)
	}
}

func TestNodeConfigValidation_EmptyDisk(t *testing.T) {
	m := New(&mockScanner{}, []string{"generic"}, nil)
	m.step = stepConfigureNode
	m.discoveredNodes = []wizard.NodeInfo{{IP: "10.0.0.1"}}
	m.selectedNodes = []int{0}
	m.currentNodeIdx = 0
	m.prepareNodeInputs()

	m.nodeInputs[fieldRole].SetValue("controlplane")
	m.nodeInputs[fieldHostname].SetValue("cp-1")
	m.nodeInputs[fieldDisk].SetValue("") // empty disk

	updated, _ := m.Update(enterMsg())
	m = updated.(Model)

	if m.err == nil {
		t.Error("expected validation error for empty disk path")
	}
	if m.Step() != stepConfigureNode {
		t.Error("should stay on configure step")
	}
}

func TestNodeConfigDefaultRole(t *testing.T) {
	m := New(&mockScanner{}, []string{"generic"}, nil)
	m.step = stepConfigureNode
	m.discoveredNodes = []wizard.NodeInfo{{IP: "10.0.0.1"}, {IP: "10.0.0.2"}}
	m.selectedNodes = []int{0, 1}

	m.currentNodeIdx = 0
	m.prepareNodeInputs()
	if m.nodeInputs[fieldRole].Value() != "controlplane" {
		t.Errorf("first node role = %q, want controlplane", m.nodeInputs[fieldRole].Value())
	}

	m.currentNodeIdx = 1
	m.prepareNodeInputs()
	if m.nodeInputs[fieldRole].Value() != "worker" {
		t.Errorf("second node role = %q, want worker", m.nodeInputs[fieldRole].Value())
	}
}

// Verify the done step allows exiting the program

func TestDoneStep_EnterQuits(t *testing.T) {
	m := New(&mockScanner{}, []string{"generic"}, nil)
	m.step = stepDone

	_, cmd := m.Update(enterMsg())
	if cmd == nil {
		t.Fatal("expected tea.Quit cmd on enter at stepDone, got nil")
	}
}

func TestDoneStep_QKeyQuits(t *testing.T) {
	m := New(&mockScanner{}, []string{"generic"}, nil)
	m.step = stepDone

	_, cmd := m.Update(keyMsg("q"))
	if cmd == nil {
		t.Fatal("expected tea.Quit cmd on 'q' at stepDone, got nil")
	}
}

// Verify back navigation restores previous node's data in the input fields

func TestBackFromConfigureNode_RestoresInputs(t *testing.T) {
	m := New(&mockScanner{}, []string{"generic"}, nil)
	m.step = stepConfigureNode
	m.discoveredNodes = []wizard.NodeInfo{
		{IP: "10.0.0.1", Hostname: "first-node"},
		{IP: "10.0.0.2", Hostname: "second-node"},
	}
	m.selectedNodes = []int{0, 1}

	// Configure first node
	m.currentNodeIdx = 0
	m.prepareNodeInputs()
	m.nodeInputs[fieldHostname].SetValue("first-node")
	m.nodeInputs[fieldRole].SetValue("controlplane")
	m.configuredNodes = append(m.configuredNodes, wizard.NodeConfig{Hostname: "first-node"})
	m.currentNodeIdx = 1
	m.prepareNodeInputs()

	// Now go back
	updated, _ := m.Update(escMsg())
	m = updated.(Model)

	if m.currentNodeIdx != 0 {
		t.Errorf("currentNodeIdx = %d, want 0", m.currentNodeIdx)
	}
	// After back, prepareNodeInputs should have restored first-node's hostname
	if m.nodeInputs[fieldHostname].Value() != "first-node" {
		t.Errorf("hostname = %q, want first-node", m.nodeInputs[fieldHostname].Value())
	}
}

// Verify back from confirm doesn't panic and restores last node

func TestBackFromConfirm_NoPanic(t *testing.T) {
	m := New(&mockScanner{}, []string{"generic"}, nil)
	m.step = stepConfirm
	m.discoveredNodes = []wizard.NodeInfo{{IP: "10.0.0.1", Hostname: "cp-1"}}
	m.selectedNodes = []int{0}
	m.currentNodeIdx = 1 // past the last node (confirm was reached)
	m.configuredNodes = []wizard.NodeConfig{{Hostname: "cp-1", Role: "controlplane"}}
	m.result.Nodes = m.configuredNodes

	// Press Esc — should not panic
	updated, _ := m.Update(escMsg())
	m = updated.(Model)

	if m.Step() != stepConfigureNode {
		t.Errorf("step = %d, want stepConfigureNode", m.Step())
	}
	if m.currentNodeIdx != 0 {
		t.Errorf("currentNodeIdx = %d, want 0", m.currentNodeIdx)
	}
}

// Verify Esc from scanning cancels context and returns to CIDR step

func TestEscFromScanning(t *testing.T) {
	m := New(&mockScanner{}, []string{"generic"}, nil)
	m.step = stepScanning
	cancelled := false
	m.cancelScan = func() { cancelled = true }

	updated, _ := m.Update(escMsg())
	m = updated.(Model)

	if m.Step() != stepScanCIDR {
		t.Errorf("step = %d, want stepScanCIDR", m.Step())
	}
	if !cancelled {
		t.Error("scan context should have been cancelled")
	}
	if m.cancelScan != nil {
		t.Error("cancelScan should be nil after cancellation")
	}
}

// Verify error recovery returns to the step that triggered the error

func TestErrorBack_ReturnsToPreviousStep(t *testing.T) {
	m := New(&mockScanner{}, []string{"generic"}, nil)
	m.step = stepGenerating

	// Simulate generation error
	updated, _ := m.Update(generateErrorMsg{err: fmt.Errorf("disk full")})
	m = updated.(Model)

	if m.Step() != stepError {
		t.Fatalf("expected stepError, got %d", m.Step())
	}

	// Press Esc to go back
	updated, _ = m.Update(escMsg())
	m = updated.(Model)

	if m.Step() != stepGenerating {
		t.Errorf("expected to return to stepGenerating, got %d", m.Step())
	}
}

// View rendering tests

func TestViewRendersWithoutPanic(t *testing.T) {
	m := New(&mockScanner{}, []string{"generic", "cozystack"}, nil)

	steps := []step{
		stepSelectPreset, stepClusterName, stepEndpoint,
		stepScanCIDR, stepScanning, stepManualNodeEntry, stepDone,
	}

	for _, s := range steps {
		m.step = s
		output := m.View()
		if output == "" {
			t.Errorf("View() returned empty string for step %d", s)
		}
	}

	// Error view
	m.step = stepError
	m.err = fmt.Errorf("test error")
	if m.View() == "" {
		t.Error("View() returned empty for error step")
	}

	// Select nodes view
	m.step = stepSelectNodes
	m.discoveredNodes = []wizard.NodeInfo{{IP: "10.0.0.1", Hostname: "node-01"}}
	if m.View() == "" {
		t.Error("View() returned empty for selectNodes step")
	}

	// Configure node view
	m.step = stepConfigureNode
	m.discoveredNodes = []wizard.NodeInfo{{IP: "10.0.0.1"}}
	m.selectedNodes = []int{0}
	m.currentNodeIdx = 0
	if m.View() == "" {
		t.Error("View() returned empty for configureNode step")
	}

	// Confirm view
	m.step = stepConfirm
	m.result = wizard.WizardResult{
		Preset:      "generic",
		ClusterName: "test",
		Endpoint:    "https://10.0.0.1:6443",
		Nodes:       []wizard.NodeConfig{{Hostname: "cp-1", Role: "controlplane", DNS: []string{"8.8.8.8"}}},
	}
	if m.View() == "" {
		t.Error("View() returned empty for confirm step")
	}
}
