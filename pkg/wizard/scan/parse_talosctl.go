package scan

import (
	"encoding/json"
	"strings"

	"github.com/cozystack/talm/pkg/wizard"
)

// talosResource represents the common structure of talosctl JSON output.
type talosResource struct {
	Metadata struct {
		ID string `json:"id"`
	} `json:"metadata"`
	Spec json.RawMessage `json:"spec"`
}

// ParseHostname extracts the hostname from talosctl get hostname -o json output.
func ParseHostname(data []byte) (string, error) {
	data = trimToFirstLine(data)
	if len(data) == 0 {
		return "", &json.SyntaxError{}
	}

	var res talosResource
	if err := json.Unmarshal(data, &res); err != nil {
		return "", err
	}

	var spec struct {
		Hostname string `json:"hostname"`
	}
	if err := json.Unmarshal(res.Spec, &spec); err != nil {
		return "", nil
	}

	return spec.Hostname, nil
}

// ParseDisks extracts disk information from talosctl get disks -o json output.
// talosctl outputs one JSON object per line (NDJSON).
func ParseDisks(data []byte) ([]wizard.Disk, error) {
	var disks []wizard.Disk

	for _, line := range splitJSONLines(data) {
		var res talosResource
		if err := json.Unmarshal(line, &res); err != nil {
			continue
		}

		var spec struct {
			DevPath string `json:"dev_path"`
			Model   string `json:"model"`
			Size    uint64 `json:"size"`
		}
		if err := json.Unmarshal(res.Spec, &spec); err != nil {
			continue
		}

		disks = append(disks, wizard.Disk{
			DevPath:   spec.DevPath,
			Model:     spec.Model,
			SizeBytes: spec.Size,
		})
	}

	return disks, nil
}

// ParseLinks extracts network interface information from talosctl get links -o json output.
// Only returns physical interfaces (has busPath, not loopback/bond/vlan).
func ParseLinks(data []byte) ([]wizard.NetInterface, error) {
	var interfaces []wizard.NetInterface

	for _, line := range splitJSONLines(data) {
		var res talosResource
		if err := json.Unmarshal(line, &res); err != nil {
			continue
		}

		var spec struct {
			HardwareAddr string `json:"hardwareAddr"`
			BusPath      string `json:"busPath"`
			Kind         string `json:"kind"`
			Type         string `json:"type"`
		}
		if err := json.Unmarshal(res.Spec, &spec); err != nil {
			continue
		}

		// Filter: only physical interfaces (has busPath, not virtual)
		if spec.BusPath == "" || spec.Kind != "" || spec.Type == "loopback" {
			continue
		}

		interfaces = append(interfaces, wizard.NetInterface{
			Name: res.Metadata.ID,
			MAC:  spec.HardwareAddr,
		})
	}

	return interfaces, nil
}

// splitJSONLines splits NDJSON (newline-delimited JSON) into individual lines.
func splitJSONLines(data []byte) [][]byte {
	var lines [][]byte
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, []byte(line))
		}
	}
	return lines
}

// trimToFirstLine returns the first non-empty line of data.
func trimToFirstLine(data []byte) []byte {
	s := strings.TrimSpace(string(data))
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		s = s[:idx]
	}
	return []byte(s)
}
