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
	fieldRole         = 0
	fieldHostname     = 1
	fieldDisk         = 2
	fieldInterface    = 3
	fieldAddress      = 4
	fieldGateway      = 5
	fieldDNS          = 6
	fieldManagementIP = 7 // optional, for DNAT / split-horizon setups
	nodeFieldCount    = 8
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
	nodeInputs[fieldManagementIP].Placeholder = "(optional) reachable IP, default = node address"

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

// NewForExistingProject creates a wizard model for a project that is already
// initialized (secrets.yaml + Chart.yaml exist). Preset and cluster name are
// taken from the on-disk state rather than asked again, so the wizard can be
// used to just add or reconfigure nodes on top of an existing project.
func NewForExistingProject(scanner wizard.Scanner, existing wizard.WizardResult, generateFn GenerateFunc) Model {
	m := New(scanner, []string{existing.Preset}, generateFn)
	m.result.Preset = existing.Preset
	m.result.ClusterName = existing.ClusterName
	m.step = stepEndpoint
	return m
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
		// Start fresh: a rescan must not inherit selection/cursor/warnings
		// from the previous discovery, otherwise stale indexes can survive
		// into the configure flow and preselect the wrong hosts.
		m.discoveredNodes = msg.nodes
		m.scanWarnings = msg.warnings
		m.selectedNodes = nil
		m.cursor = 0
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
		// prevStep must point at the user-facing step that triggered the
		// scan (stepScanCIDR), not at stepScanning — otherwise Esc from
		// stepError would land on an inert spinner with no command running.
		m.err = msg.err
		prev := stepScanCIDR
		m.prevStep = &prev
		m.step = stepError
		return m, nil

	case generateDoneMsg:
		m.step = stepDone
		return m, nil

	case generateErrorMsg:
		// Same reasoning as scanErrorMsg: Esc from stepError must return to
		// stepConfirm where the user can retry, not to the stepGenerating
		// spinner (no back-path out of there).
		m.err = msg.err
		prev := stepConfirm
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
			// Keep the already-saved previous config: when the user returns
			// here they're editing, not re-entering. Rehydrate inputs from
			// the stored NodeConfig so edits (disk, iface, gw, DNS) survive.
			m.currentNodeIdx--
			m.restoreNodeInputs(m.currentNodeIdx)
		} else {
			m.step = stepSelectNodes
		}
	case stepConfirm:
		// Return to the last configured node for editing. Keep the saved
		// entry — the user may just want to tweak one field, not retype
		// everything. m.result.Nodes is cleared so confirm-page state is
		// recomputed on re-entry.
		m.result.Nodes = nil
		m.step = stepConfigureNode
		if m.currentNodeIdx >= len(m.selectedNodes) {
			m.currentNodeIdx = len(m.selectedNodes) - 1
		}
		m.restoreNodeInputs(m.currentNodeIdx)
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
	m.nodeInputs[fieldGateway].SetValue(node.DefaultGateway)
	// DNS starts empty — no preconceived default, user must choose.
	m.nodeInputs[fieldDNS].SetValue("")
	m.nodeInputs[fieldManagementIP].SetValue("")
	m.nodeInputFocus = 0
}

// restoreNodeInputs rehydrates the per-node inputs from a saved NodeConfig —
// used when the user backs into a node they already configured.
func (m *Model) restoreNodeInputs(idx int) {
	if idx < 0 || idx >= len(m.configuredNodes) {
		m.prepareNodeInputs()
		return
	}
	nc := m.configuredNodes[idx]
	m.nodeInputs[fieldRole].SetValue(nc.Role)
	m.nodeInputs[fieldHostname].SetValue(nc.Hostname)
	m.nodeInputs[fieldDisk].SetValue(nc.DiskPath)
	m.nodeInputs[fieldInterface].SetValue(nc.Interface)
	m.nodeInputs[fieldAddress].SetValue(nc.Addresses)
	m.nodeInputs[fieldGateway].SetValue(nc.Gateway)
	m.nodeInputs[fieldDNS].SetValue(strings.Join(nc.DNS, ","))
	m.nodeInputs[fieldManagementIP].SetValue(nc.ManagementIP)
	m.nodeInputFocus = 0
}

func (m Model) updateConfigureNode(msg tea.Msg) (tea.Model, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		// Role field is a toggle, not a text input — space/left/right flip
		// between the only two valid values instead of letting the user
		// type a free-form string and then fail validation.
		if m.nodeInputFocus == fieldRole {
			switch keyMsg.String() {
			case " ", "left", "right", "h", "l":
				if m.nodeInputs[fieldRole].Value() == "controlplane" {
					m.nodeInputs[fieldRole].SetValue("worker")
				} else {
					m.nodeInputs[fieldRole].SetValue("controlplane")
				}
				return m, nil
			}
		}
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
			// Update the existing slot when editing, append when adding a
			// fresh node. Prevents duplicates after back-navigation.
			if m.currentNodeIdx < len(m.configuredNodes) {
				m.configuredNodes[m.currentNodeIdx] = nc
			} else {
				m.configuredNodes = append(m.configuredNodes, nc)
			}
			m.currentNodeIdx++

			if m.currentNodeIdx >= len(m.selectedNodes) {
				m.result.Nodes = m.configuredNodes
				m.step = stepConfirm
				return m, nil
			}
			// Rehydrate from saved config if this node was already visited,
			// otherwise start from discovery defaults.
			if m.currentNodeIdx < len(m.configuredNodes) {
				m.restoreNodeInputs(m.currentNodeIdx)
			} else {
				m.prepareNodeInputs()
			}
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

	managementIP := strings.TrimSpace(m.nodeInputs[fieldManagementIP].Value())
	if managementIP != "" {
		if err := wizard.ValidateIP(managementIP); err != nil {
			return wizard.NodeConfig{}, fmt.Errorf("management IP: %w", err)
		}
	}

	return wizard.NodeConfig{
		Hostname:     hostname,
		Role:         role,
		DiskPath:     diskPath,
		Interface:    m.nodeInputs[fieldInterface].Value(),
		Addresses:    address,
		Gateway:      gateway,
		DNS:          dns,
		ManagementIP: managementIP,
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
