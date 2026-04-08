package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

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
	stepManualNodeEntry
	stepSelectNodes
	stepConfigureNode
	stepConfirm
	stepGenerating
	stepDone
	stepError
)

// Node configuration field indices.
const (
	fieldRole      = 0
	fieldHostname  = 1
	fieldDisk      = 2
	fieldInterface = 3
	fieldAddress   = 4
	fieldGateway   = 5
	fieldDNS       = 6
	nodeFieldCount = 7
)

// Message types for async operations.
type (
	scanResultMsg struct {
		nodes    []wizard.NodeInfo
		warnings []string
	}
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
	manualIPInput textinput.Model
	spinner       spinner.Model

	// Node selection state
	discoveredNodes []wizard.NodeInfo
	scanWarnings    []string
	selectedNodes   []int // indices into discoveredNodes
	cursor          int   // for list navigation

	// Manual node entry
	manualNodes []wizard.NodeInfo

	// Node configuration state
	configuredNodes []wizard.NodeConfig
	nodeInputs      [nodeFieldCount]textinput.Model
	nodeInputFocus  int
	currentNodeIdx  int

	// Dependencies
	scanner    wizard.Scanner
	generateFn GenerateFunc

	// Context for cancelling long-running operations
	cancelScan context.CancelFunc

	// Step before error occurred, for returning on Esc (nil = no previous step)
	prevStep *step

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

	manualIP := textinput.New()
	manualIP.Placeholder = "192.168.1.10"

	var nodeInputs [nodeFieldCount]textinput.Model
	for i := range nodeInputs {
		nodeInputs[i] = textinput.New()
	}
	nodeInputs[fieldRole].Placeholder = "controlplane"
	nodeInputs[fieldHostname].Placeholder = "node-01"
	nodeInputs[fieldDisk].Placeholder = "/dev/sda"
	nodeInputs[fieldInterface].Placeholder = "eth0"
	nodeInputs[fieldAddress].Placeholder = "192.168.1.10/24"
	nodeInputs[fieldGateway].Placeholder = "192.168.1.1"
	nodeInputs[fieldDNS].Placeholder = "8.8.8.8,1.1.1.1"

	return Model{
		step:    stepSelectPreset,
		presets: presets,
		scanner: scanner,

		nameInput:     name,
		endpointInput: endpoint,
		cidrInput:     cidr,
		manualIPInput: manualIP,
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
			if m.cancelScan != nil {
				m.cancelScan()
			}
			return m, tea.Quit
		case "esc":
			return m.handleBack()
		}

	case scanResultMsg:
		if m.step != stepScanning {
			return m, nil // stale result from cancelled scan
		}
		if m.cancelScan != nil {
			m.cancelScan()
			m.cancelScan = nil
		}
		m.discoveredNodes = msg.nodes
		m.scanWarnings = msg.warnings
		if len(msg.nodes) == 0 {
			m.err = fmt.Errorf("no Talos nodes found in the specified network")
			prev := stepScanCIDR
			m.prevStep = &prev
			m.step = stepError
			return m, nil
		}
		m.step = stepSelectNodes
		return m, nil

	case scanErrorMsg:
		if m.step != stepScanning {
			return m, nil // stale error from cancelled scan
		}
		if m.cancelScan != nil {
			m.cancelScan()
			m.cancelScan = nil
		}
		m.err = msg.err
		prev := m.step
		m.prevStep = &prev
		m.step = stepError
		return m, nil

	case generateDoneMsg:
		m.step = stepDone
		return m, nil

	case generateErrorMsg:
		m.err = msg.err
		prev := m.step
		m.prevStep = &prev
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
	case stepManualNodeEntry:
		return m.updateManualNodeEntry(msg)
	case stepSelectNodes:
		return m.updateSelectNodes(msg)
	case stepConfigureNode:
		return m.updateConfigureNode(msg)
	case stepConfirm:
		return m.updateConfirm(msg)
	case stepDone:
		return m.updateDone(msg)
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
	case stepManualNodeEntry:
		m.step = stepScanCIDR
		m.manualNodes = nil
	case stepScanning:
		if m.cancelScan != nil {
			m.cancelScan()
			m.cancelScan = nil
		}
		m.step = stepScanCIDR
	case stepSelectNodes:
		m.step = stepScanCIDR
	case stepConfigureNode:
		if m.currentNodeIdx > 0 {
			m.currentNodeIdx--
			m.configuredNodes = m.configuredNodes[:len(m.configuredNodes)-1]
			m.prepareNodeInputs()
		} else {
			m.step = stepSelectNodes
		}
	case stepConfirm:
		// Go back to the last configured node — remove the last entry
		// so the user can re-enter it without duplicates
		if len(m.configuredNodes) > 0 {
			m.configuredNodes = m.configuredNodes[:len(m.configuredNodes)-1]
		}
		if m.currentNodeIdx > 0 {
			m.currentNodeIdx--
		}
		m.result.Nodes = nil
		m.step = stepConfigureNode
		m.prepareNodeInputs()
	case stepError:
		if m.prevStep != nil {
			m.step = *m.prevStep
		} else {
			m.step = stepSelectPreset
		}
		m.err = nil
		m.prevStep = nil
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
		return m, m.cidrInput.Focus()
	}

	var cmd tea.Cmd
	m.endpointInput, cmd = m.endpointInput.Update(msg)
	return m, cmd
}

func (m Model) updateScanCIDR(msg tea.Msg) (tea.Model, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		switch keyMsg.String() {
		case "enter":
			cidr := m.cidrInput.Value()
			if err := wizard.ValidateCIDR(cidr); err != nil {
				m.err = err
				return m, nil
			}
			m.err = nil
			m.step = stepScanning
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			m.cancelScan = cancel
			return m, tea.Batch(
				m.spinner.Tick,
				scanNetworkCmd(ctx, m.scanner, cidr),
			)
		case "ctrl+s":
			m.err = nil
			m.step = stepManualNodeEntry
			m.manualNodes = nil
			return m, m.manualIPInput.Focus()
		}
	}

	var cmd tea.Cmd
	m.cidrInput, cmd = m.cidrInput.Update(msg)
	return m, cmd
}

func (m Model) updateManualNodeEntry(msg tea.Msg) (tea.Model, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		switch keyMsg.String() {
		case "enter":
			ip := m.manualIPInput.Value()
			if ip == "" {
				return m, nil
			}
			if err := wizard.ValidateIP(ip); err != nil {
				m.err = err
				return m, nil
			}
			m.err = nil
			m.manualNodes = append(m.manualNodes, wizard.NodeInfo{IP: ip})
			m.manualIPInput.SetValue("")
			return m, nil
		case "ctrl+d":
			if len(m.manualNodes) == 0 {
				m.err = fmt.Errorf("add at least one node")
				return m, nil
			}
			m.err = nil
			m.discoveredNodes = m.manualNodes
			// Pre-select all manual nodes
			m.selectedNodes = make([]int, len(m.manualNodes))
			for i := range m.manualNodes {
				m.selectedNodes[i] = i
			}
			m.step = stepSelectNodes
			return m, nil
		}
	}

	var cmd tea.Cmd
	m.manualIPInput, cmd = m.manualIPInput.Update(msg)
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
			m.configuredNodes = nil
			m.step = stepConfigureNode
			m.prepareNodeInputs()
			return m, m.nodeInputs[fieldRole].Focus()
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

	// Default role: first node is controlplane, rest are workers
	if m.currentNodeIdx == 0 {
		m.nodeInputs[fieldRole].SetValue("controlplane")
	} else {
		m.nodeInputs[fieldRole].SetValue("worker")
	}

	m.nodeInputs[fieldHostname].SetValue(node.Hostname)
	if len(node.Disks) > 0 {
		m.nodeInputs[fieldDisk].SetValue(node.Disks[0].DevPath)
	} else {
		m.nodeInputs[fieldDisk].SetValue("")
	}
	if len(node.Interfaces) > 0 {
		m.nodeInputs[fieldInterface].SetValue(node.Interfaces[0].Name)
	} else {
		m.nodeInputs[fieldInterface].SetValue("")
	}
	if len(node.Interfaces) > 0 && len(node.Interfaces[0].IPs) > 0 {
		m.nodeInputs[fieldAddress].SetValue(node.Interfaces[0].IPs[0])
	} else {
		m.nodeInputs[fieldAddress].SetValue("")
	}
	m.nodeInputs[fieldGateway].SetValue("")
	m.nodeInputs[fieldDNS].SetValue("8.8.8.8")
	m.nodeInputFocus = 0
}

func (m Model) updateConfigureNode(msg tea.Msg) (tea.Model, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		switch keyMsg.String() {
		case "tab":
			m.nodeInputFocus = (m.nodeInputFocus + 1) % nodeFieldCount
			return m, m.nodeInputs[m.nodeInputFocus].Focus()
		case "shift+tab":
			m.nodeInputFocus = (m.nodeInputFocus - 1 + nodeFieldCount) % nodeFieldCount
			return m, m.nodeInputs[m.nodeInputFocus].Focus()
		case "enter":
			nc, err := m.validateAndBuildNodeConfig()
			if err != nil {
				m.err = err
				return m, nil
			}
			m.err = nil
			m.configuredNodes = append(m.configuredNodes, nc)
			m.currentNodeIdx++

			if m.currentNodeIdx >= len(m.selectedNodes) {
				m.result.Nodes = m.configuredNodes
				m.step = stepConfirm
				return m, nil
			}
			m.prepareNodeInputs()
			return m, m.nodeInputs[fieldRole].Focus()
		}
	}

	var cmd tea.Cmd
	m.nodeInputs[m.nodeInputFocus], cmd = m.nodeInputs[m.nodeInputFocus].Update(msg)
	return m, cmd
}

func (m Model) validateAndBuildNodeConfig() (wizard.NodeConfig, error) {
	role := m.nodeInputs[fieldRole].Value()
	if err := wizard.ValidateNodeRole(role); err != nil {
		return wizard.NodeConfig{}, err
	}

	hostname := m.nodeInputs[fieldHostname].Value()
	if err := wizard.ValidateHostname(hostname); err != nil {
		return wizard.NodeConfig{}, err
	}

	diskPath := m.nodeInputs[fieldDisk].Value()
	if diskPath == "" {
		return wizard.NodeConfig{}, fmt.Errorf("install disk is required")
	}

	address := m.nodeInputs[fieldAddress].Value()
	if address == "" {
		return wizard.NodeConfig{}, fmt.Errorf("address (CIDR) is required")
	}
	if err := wizard.ValidateCIDR(address); err != nil {
		return wizard.NodeConfig{}, fmt.Errorf("address: %w", err)
	}

	gateway := m.nodeInputs[fieldGateway].Value()
	if gateway != "" {
		if err := wizard.ValidateIP(gateway); err != nil {
			return wizard.NodeConfig{}, fmt.Errorf("gateway: %w", err)
		}
	}

	var dns []string
	dnsStr := m.nodeInputs[fieldDNS].Value()
	if dnsStr != "" {
		for _, d := range strings.Split(dnsStr, ",") {
			d = strings.TrimSpace(d)
			if d == "" {
				continue
			}
			if err := wizard.ValidateIP(d); err != nil {
				return wizard.NodeConfig{}, fmt.Errorf("DNS %q: %w", d, err)
			}
			dns = append(dns, d)
		}
	}

	return wizard.NodeConfig{
		Hostname:  hostname,
		Role:      role,
		DiskPath:  diskPath,
		Interface: m.nodeInputs[fieldInterface].Value(),
		Addresses: address,
		Gateway:   gateway,
		DNS:       dns,
	}, nil
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

func (m Model) updateDone(msg tea.Msg) (tea.Model, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		switch keyMsg.String() {
		case "enter", "q":
			return m, tea.Quit
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

func scanNetworkCmd(ctx context.Context, scanner wizard.Scanner, cidr string) tea.Cmd {
	return func() tea.Msg {
		result, err := scanner.ScanNetworkFull(ctx, cidr)
		if err != nil {
			return scanErrorMsg{err: err}
		}
		return scanResultMsg{nodes: result.Nodes, warnings: result.Warnings}
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
