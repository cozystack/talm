package tui

import (
	"fmt"
	"strings"

	"github.com/dustin/go-humanize"
)

// View implements tea.Model.
func (m Model) View() string {
	switch m.step {
	case stepSelectPreset:
		return m.viewSelectPreset()
	case stepClusterName:
		return m.viewClusterName()
	case stepEndpoint:
		return m.viewEndpoint()
	case stepScanCIDR:
		return m.viewScanCIDR()
	case stepScanning:
		return m.viewScanning()
	case stepSelectNodes:
		return m.viewSelectNodes()
	case stepConfigureNode:
		return m.viewConfigureNode()
	case stepConfirm:
		return m.viewConfirm()
	case stepGenerating:
		return m.viewGenerating()
	case stepDone:
		return m.viewDone()
	case stepError:
		return m.viewError()
	default:
		return ""
	}
}

func (m Model) viewSelectPreset() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Select a preset"))
	b.WriteString("\n\n")

	for i, preset := range m.presets {
		cursor := "  "
		style := blurredStyle
		if i == m.cursor {
			cursor = "> "
			style = selectedStyle
		}
		b.WriteString(cursor + style.Render(preset) + "\n")
	}

	b.WriteString(helpStyle.Render("\n↑/↓ navigate • enter select • ctrl+c quit"))
	return b.String()
}

func (m Model) viewClusterName() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Cluster name"))
	b.WriteString("\n\n")
	b.WriteString(m.nameInput.View())

	if m.err != nil {
		b.WriteString("\n" + errorStyle.Render(m.err.Error()))
	}

	b.WriteString(helpStyle.Render("\nenter confirm • esc back"))
	return b.String()
}

func (m Model) viewEndpoint() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("API server endpoint"))
	b.WriteString("\n\n")
	b.WriteString(m.endpointInput.View())

	if m.err != nil {
		b.WriteString("\n" + errorStyle.Render(m.err.Error()))
	}

	b.WriteString(helpStyle.Render("\nenter confirm • esc back"))
	return b.String()
}

func (m Model) viewScanCIDR() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Network to scan"))
	b.WriteString("\n")
	b.WriteString(subtitleStyle.Render("Enter CIDR range to discover Talos nodes"))
	b.WriteString("\n\n")
	b.WriteString(m.cidrInput.View())

	if m.err != nil {
		b.WriteString("\n" + errorStyle.Render(m.err.Error()))
	}

	b.WriteString(helpStyle.Render("\nenter scan • esc back"))
	return b.String()
}

func (m Model) viewScanning() string {
	return titleStyle.Render("Scanning network...") + "\n\n" +
		m.spinner.View() + " Discovering Talos nodes...\n"
}

func (m Model) viewSelectNodes() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Select nodes"))
	fmt.Fprintf(&b, "\n%d node(s) discovered\n\n", len(m.discoveredNodes))

	for i, node := range m.discoveredNodes {
		cursor := "  "
		if i == m.cursor {
			cursor = "> "
		}

		selected := "[ ]"
		for _, idx := range m.selectedNodes {
			if idx == i {
				selected = "[x]"
				break
			}
		}

		info := node.IP
		if node.Hostname != "" {
			info += " (" + node.Hostname + ")"
		}
		if node.RAMBytes > 0 {
			info += " " + humanize.IBytes(node.RAMBytes) + " RAM"
		}

		fmt.Fprintf(&b, "%s%s %s\n", cursor, selected, info)
	}

	if m.err != nil {
		b.WriteString("\n" + errorStyle.Render(m.err.Error()))
	}

	b.WriteString(helpStyle.Render("\n↑/↓ navigate • space toggle • enter confirm • esc back"))
	return b.String()
}

func (m Model) viewConfigureNode() string {
	var b strings.Builder
	nodeIdx := m.selectedNodes[m.currentNodeIdx]
	node := m.discoveredNodes[nodeIdx]

	b.WriteString(titleStyle.Render(fmt.Sprintf("Configure node %d/%d", m.currentNodeIdx+1, len(m.selectedNodes))))
	fmt.Fprintf(&b, "\nIP: %s\n\n", node.IP)

	labels := []string{"Hostname:", "Install disk:", "Interface:", "Address (CIDR):"}
	for i, label := range labels {
		style := blurredStyle
		if i == m.nodeInputFocus {
			style = focusedStyle
		}
		b.WriteString(style.Render(label) + " " + m.nodeInputs[i].View() + "\n")
	}

	b.WriteString(helpStyle.Render("\ntab next field • enter confirm • esc back"))
	return b.String()
}

func (m Model) viewConfirm() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Confirm configuration"))
	b.WriteString("\n\n")
	fmt.Fprintf(&b, "Preset:  %s\n", m.result.Preset)
	fmt.Fprintf(&b, "Cluster: %s\n", m.result.ClusterName)
	fmt.Fprintf(&b, "Endpoint: %s\n", m.result.Endpoint)
	fmt.Fprintf(&b, "Nodes:   %d\n\n", len(m.result.Nodes))

	for _, node := range m.result.Nodes {
		fmt.Fprintf(&b, "  %s (%s) - %s on %s\n",
			node.Hostname, node.Role, node.Addresses, node.DiskPath)
	}

	b.WriteString(helpStyle.Render("\ny/enter generate • n restart • esc back"))
	return b.String()
}

func (m Model) viewGenerating() string {
	return titleStyle.Render("Generating configuration...") + "\n\n" +
		m.spinner.View() + " Creating secrets and config files...\n"
}

func (m Model) viewDone() string {
	return successStyle.Render("Configuration generated successfully!") + "\n\n" +
		"Files created in the current directory.\n" +
		"Run 'talm template' to render node configs, then 'talm apply' to apply them.\n"
}

func (m Model) viewError() string {
	var b strings.Builder
	b.WriteString(errorStyle.Render("Error"))
	b.WriteString("\n\n")
	if m.err != nil {
		b.WriteString(m.err.Error())
	}
	b.WriteString(helpStyle.Render("\nr retry • enter/q quit"))
	return b.String()
}
