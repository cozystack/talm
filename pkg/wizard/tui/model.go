package tui

import (
	"context"
	"fmt"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/cozystack/talm/pkg/wizard"
)

// step represents a stage in the wizard flow.
type step int

const (
	stepSelectPreset step = iota
	stepClusterName
	stepEndpoint
	stepScanCIDR
	stepScanning
	stepSelectNodes
	stepConfigureNode
	stepConfirm
	stepGenerating
	stepDone
	stepError
)

// Message types for async operations.
type (
	scanResultMsg    struct{ nodes []wizard.NodeInfo }
	scanErrorMsg     struct{ err error }
	generateDoneMsg  struct{}
	generateErrorMsg struct{ err error }
)

// GenerateFunc is called when the wizard completes to generate the project.
type GenerateFunc func(result wizard.WizardResult) error

// Model is the bubbletea model for the interactive wizard.
type Model struct {
	step step
	err  error

	// Wizard data
	result  wizard.WizardResult
	presets []string

	// Sub-models
	nameInput     textinput.Model
	endpointInput textinput.Model
	cidrInput     textinput.Model
	spinner       spinner.Model

	// Node selection state
	discoveredNodes []wizard.NodeInfo
	selectedNodes   []int // indices into discoveredNodes
	cursor          int   // for list navigation

	// Node configuration state
	configuredNodes []wizard.NodeConfig
	nodeInputs      [4]textinput.Model // hostname, disk, interface, address
	nodeInputFocus  int
	currentNodeIdx  int

	// Dependencies
	scanner    wizard.Scanner
	generateFn GenerateFunc

	// Terminal dimensions
	width, height int
}

// New creates a new wizard model.
func New(scanner wizard.Scanner, presets []string, generateFn GenerateFunc) Model {
	s := spinner.New()
	s.Spinner = spinner.Dot

	name := textinput.New()
	name.Placeholder = "my-cluster"
	name.CharLimit = 63

	endpoint := textinput.New()
	endpoint.Placeholder = "https://192.168.0.1:6443"

	cidr := textinput.New()
	cidr.Placeholder = "192.168.1.0/24"

	var nodeInputs [4]textinput.Model
	for i := range nodeInputs {
		nodeInputs[i] = textinput.New()
	}
	nodeInputs[0].Placeholder = "node-01"
	nodeInputs[1].Placeholder = "/dev/sda"
	nodeInputs[2].Placeholder = "eth0"
	nodeInputs[3].Placeholder = "192.168.1.10/24"

	return Model{
		step:    stepSelectPreset,
		presets: presets,
		scanner: scanner,

		nameInput:     name,
		endpointInput: endpoint,
		cidrInput:     cidr,
		spinner:       s,
		nodeInputs:    nodeInputs,
		generateFn:    generateFn,
	}
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd {
	return nil
}

// Err returns any error that occurred during the wizard.
func (m Model) Err() error {
	return m.err
}

// Result returns the wizard result after completion.
func (m Model) Result() wizard.WizardResult {
	return m.result
}

// Step returns the current step (for testing).
func (m Model) Step() step {
	return m.step
}

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "esc":
			return m.handleBack()
		}

	case scanResultMsg:
		m.discoveredNodes = msg.nodes
		if len(msg.nodes) == 0 {
			m.err = fmt.Errorf("no Talos nodes found in the specified network")
			m.step = stepError
			return m, nil
		}
		m.step = stepSelectNodes
		return m, nil

	case scanErrorMsg:
		m.err = msg.err
		m.step = stepError
		return m, nil

	case generateDoneMsg:
		m.step = stepDone
		return m, nil

	case generateErrorMsg:
		m.err = msg.err
		m.step = stepError
		return m, nil

	case spinner.TickMsg:
		if m.step == stepScanning || m.step == stepGenerating {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
		return m, nil
	}

	switch m.step {
	case stepSelectPreset:
		return m.updateSelectPreset(msg)
	case stepClusterName:
		return m.updateClusterName(msg)
	case stepEndpoint:
		return m.updateEndpoint(msg)
	case stepScanCIDR:
		return m.updateScanCIDR(msg)
	case stepSelectNodes:
		return m.updateSelectNodes(msg)
	case stepConfigureNode:
		return m.updateConfigureNode(msg)
	case stepConfirm:
		return m.updateConfirm(msg)
	case stepError:
		return m.updateError(msg)
	}

	return m, nil
}

func (m Model) handleBack() (tea.Model, tea.Cmd) {
	switch m.step {
	case stepClusterName:
		m.step = stepSelectPreset
	case stepEndpoint:
		m.step = stepClusterName
	case stepScanCIDR:
		m.step = stepEndpoint
	case stepSelectNodes:
		m.step = stepScanCIDR
	case stepConfigureNode:
		if m.currentNodeIdx > 0 {
			m.currentNodeIdx--
		} else {
			m.step = stepSelectNodes
		}
	case stepConfirm:
		m.step = stepConfigureNode
	case stepError:
		m.step = stepSelectPreset
		m.err = nil
	}
	return m, nil
}

func (m Model) updateSelectPreset(msg tea.Msg) (tea.Model, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		switch keyMsg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.presets)-1 {
				m.cursor++
			}
		case "enter":
			m.result.Preset = m.presets[m.cursor]
			m.step = stepClusterName
			m.nameInput.Focus()
			m.cursor = 0
			return m, m.nameInput.Focus()
		}
	}
	return m, nil
}

func (m Model) updateClusterName(msg tea.Msg) (tea.Model, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyMsg); ok && keyMsg.String() == "enter" {
		name := m.nameInput.Value()
		if err := wizard.ValidateClusterName(name); err != nil {
			m.err = err
			return m, nil
		}
		m.result.ClusterName = name
		m.err = nil
		m.step = stepEndpoint
		m.endpointInput.Focus()
		return m, m.endpointInput.Focus()
	}

	var cmd tea.Cmd
	m.nameInput, cmd = m.nameInput.Update(msg)
	return m, cmd
}

func (m Model) updateEndpoint(msg tea.Msg) (tea.Model, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyMsg); ok && keyMsg.String() == "enter" {
		endpoint := m.endpointInput.Value()
		if err := wizard.ValidateEndpoint(endpoint); err != nil {
			m.err = err
			return m, nil
		}
		m.result.Endpoint = endpoint
		m.err = nil
		m.step = stepScanCIDR
		m.cidrInput.Focus()
		return m, m.cidrInput.Focus()
	}

	var cmd tea.Cmd
	m.endpointInput, cmd = m.endpointInput.Update(msg)
	return m, cmd
}

func (m Model) updateScanCIDR(msg tea.Msg) (tea.Model, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyMsg); ok && keyMsg.String() == "enter" {
		cidr := m.cidrInput.Value()
		if err := wizard.ValidateCIDR(cidr); err != nil {
			m.err = err
			return m, nil
		}
		m.err = nil
		m.step = stepScanning
		return m, tea.Batch(
			m.spinner.Tick,
			scanNetworkCmd(m.scanner, cidr),
		)
	}

	var cmd tea.Cmd
	m.cidrInput, cmd = m.cidrInput.Update(msg)
	return m, cmd
}

func (m Model) updateSelectNodes(msg tea.Msg) (tea.Model, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		switch keyMsg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.discoveredNodes)-1 {
				m.cursor++
			}
		case " ":
			m.toggleNodeSelection()
		case "enter":
			if len(m.selectedNodes) == 0 {
				m.err = fmt.Errorf("select at least one node")
				return m, nil
			}
			m.err = nil
			m.currentNodeIdx = 0
			m.step = stepConfigureNode
			m.prepareNodeInputs()
			return m, m.nodeInputs[0].Focus()
		}
	}
	return m, nil
}

func (m *Model) toggleNodeSelection() {
	for i, idx := range m.selectedNodes {
		if idx == m.cursor {
			m.selectedNodes = append(m.selectedNodes[:i], m.selectedNodes[i+1:]...)
			return
		}
	}
	m.selectedNodes = append(m.selectedNodes, m.cursor)
}

func (m *Model) prepareNodeInputs() {
	if m.currentNodeIdx >= len(m.selectedNodes) {
		return
	}
	node := m.discoveredNodes[m.selectedNodes[m.currentNodeIdx]]

	m.nodeInputs[0].SetValue(node.Hostname)
	if len(node.Disks) > 0 {
		m.nodeInputs[1].SetValue(node.Disks[0].DevPath)
	} else {
		m.nodeInputs[1].SetValue("")
	}
	if len(node.Interfaces) > 0 {
		m.nodeInputs[2].SetValue(node.Interfaces[0].Name)
	} else {
		m.nodeInputs[2].SetValue("")
	}
	m.nodeInputs[3].SetValue("")
	m.nodeInputFocus = 0
}

func (m Model) updateConfigureNode(msg tea.Msg) (tea.Model, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		switch keyMsg.String() {
		case "tab":
			m.nodeInputFocus = (m.nodeInputFocus + 1) % len(m.nodeInputs)
			return m, m.nodeInputs[m.nodeInputFocus].Focus()
		case "shift+tab":
			m.nodeInputFocus = (m.nodeInputFocus - 1 + len(m.nodeInputs)) % len(m.nodeInputs)
			return m, m.nodeInputs[m.nodeInputFocus].Focus()
		case "enter":
			role := "worker"
			if m.currentNodeIdx == 0 {
				role = "controlplane"
			}
			nc := wizard.NodeConfig{
				Hostname:  m.nodeInputs[0].Value(),
				Role:      role,
				DiskPath:  m.nodeInputs[1].Value(),
				Interface: m.nodeInputs[2].Value(),
				Addresses: m.nodeInputs[3].Value(),
			}
			m.configuredNodes = append(m.configuredNodes, nc)
			m.currentNodeIdx++

			if m.currentNodeIdx >= len(m.selectedNodes) {
				m.result.Nodes = m.configuredNodes
				m.step = stepConfirm
				return m, nil
			}
			m.prepareNodeInputs()
			return m, m.nodeInputs[0].Focus()
		}
	}

	var cmd tea.Cmd
	m.nodeInputs[m.nodeInputFocus], cmd = m.nodeInputs[m.nodeInputFocus].Update(msg)
	return m, cmd
}

func (m Model) updateConfirm(msg tea.Msg) (tea.Model, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		switch keyMsg.String() {
		case "y", "enter":
			m.step = stepGenerating
			return m, tea.Batch(
				m.spinner.Tick,
				generateCmd(m.generateFn, m.result),
			)
		case "n":
			m.step = stepSelectPreset
			m.configuredNodes = nil
			m.selectedNodes = nil
			return m, nil
		}
	}
	return m, nil
}

func (m Model) updateError(msg tea.Msg) (tea.Model, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		switch keyMsg.String() {
		case "enter", "q":
			return m, tea.Quit
		case "r":
			m.step = stepSelectPreset
			m.err = nil
			return m, nil
		}
	}
	return m, nil
}

// Async command functions.

func scanNetworkCmd(scanner wizard.Scanner, cidr string) tea.Cmd {
	return func() tea.Msg {
		nodes, err := scanner.ScanNetwork(context.Background(), cidr)
		if err != nil {
			return scanErrorMsg{err: err}
		}
		return scanResultMsg{nodes: nodes}
	}
}

func generateCmd(fn GenerateFunc, result wizard.WizardResult) tea.Cmd {
	return func() tea.Msg {
		if fn == nil {
			return generateDoneMsg{}
		}
		if err := fn(result); err != nil {
			return generateErrorMsg{err: err}
		}
		return generateDoneMsg{}
	}
}
