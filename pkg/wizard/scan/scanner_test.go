package scan

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// mockRunner is a test double for CommandRunner.
// It matches commands by substring pattern in the full command string.
type mockRunner struct {
	outputs map[string]mockResult
}

type mockResult struct {
	output []byte
	err    error
}

func (m *mockRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	key := name + " " + strings.Join(args, " ")
	for pattern, result := range m.outputs {
		if strings.Contains(key, pattern) {
			return result.output, result.err
		}
	}
	return nil, fmt.Errorf("unexpected command: %s", key)
}

func TestScanNetwork_ParsesDiscoveredNodes(t *testing.T) {
	nmapOutput := `# Nmap 7.94 scan
Host: 10.0.0.1 ()	Ports: 50000/open/tcp//unknown///
Host: 10.0.0.2 ()	Ports: 50000/open/tcp//unknown///
# Nmap done`

	hostnameJSON := `{"metadata":{"id":"hostname"},"spec":{"hostname":"node-01"}}` + "\n"
	disksJSON := `{"metadata":{"id":"sda"},"spec":{"dev_path":"/dev/sda","model":"VBOX","size":53687091200}}` + "\n"
	linksJSON := `{"metadata":{"id":"eth0"},"spec":{"hardwareAddr":"aa:bb:cc:dd:ee:01","busPath":"0000:00:03.0","kind":"","type":"ether"}}` + "\n"

	runner := &mockRunner{
		outputs: map[string]mockResult{
			"nmap":     {output: []byte(nmapOutput)},
			"hostname": {output: []byte(hostnameJSON)},
			"disks":    {output: []byte(disksJSON)},
			"links":    {output: []byte(linksJSON)},
		},
	}

	scanner := &NmapScanner{
		TalosPort: 50000,
		Exec:      runner,
	}

	nodes, err := scanner.ScanNetwork(context.Background(), "10.0.0.0/24")
	if err != nil {
		t.Fatalf("ScanNetwork() error = %v", err)
	}

	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(nodes))
	}

	ips := map[string]bool{}
	for _, n := range nodes {
		ips[n.IP] = true
	}
	if !ips["10.0.0.1"] || !ips["10.0.0.2"] {
		t.Errorf("expected IPs 10.0.0.1 and 10.0.0.2, got %v", nodes)
	}
}

func TestScanNetwork_NoNodes(t *testing.T) {
	runner := &mockRunner{
		outputs: map[string]mockResult{
			"nmap": {output: []byte("# Nmap done -- 0 hosts up")},
		},
	}

	scanner := &NmapScanner{Exec: runner}

	nodes, err := scanner.ScanNetwork(context.Background(), "10.0.0.0/24")
	if err != nil {
		t.Fatalf("ScanNetwork() error = %v", err)
	}
	if len(nodes) != 0 {
		t.Errorf("expected 0 nodes, got %d", len(nodes))
	}
}

func TestScanNetwork_NmapError(t *testing.T) {
	runner := &mockRunner{
		outputs: map[string]mockResult{
			"nmap": {err: fmt.Errorf("nmap not found")},
		},
	}

	scanner := &NmapScanner{Exec: runner}

	_, err := scanner.ScanNetwork(context.Background(), "10.0.0.0/24")
	if err == nil {
		t.Fatal("expected error when nmap fails, got nil")
	}
	if !strings.Contains(err.Error(), "nmap scan failed") {
		t.Errorf("expected nmap scan failed error, got: %v", err)
	}
}

func TestScanNetwork_NodeInfoErrorDoesNotFailScan(t *testing.T) {
	nmapOutput := `Host: 10.0.0.1 ()	Ports: 50000/open/tcp//unknown///`

	runner := &mockRunner{
		outputs: map[string]mockResult{
			"nmap":     {output: []byte(nmapOutput)},
			"hostname": {err: fmt.Errorf("connection refused")},
			"disks":    {err: fmt.Errorf("connection refused")},
			"links":    {err: fmt.Errorf("connection refused")},
		},
	}

	scanner := &NmapScanner{Exec: runner}

	nodes, err := scanner.ScanNetwork(context.Background(), "10.0.0.0/24")
	if err != nil {
		t.Fatalf("ScanNetwork() should not fail when individual nodes fail: %v", err)
	}

	if len(nodes) != 1 {
		t.Fatalf("expected 1 node (with partial info), got %d", len(nodes))
	}
	if nodes[0].IP != "10.0.0.1" {
		t.Errorf("expected IP 10.0.0.1, got %s", nodes[0].IP)
	}
}

func TestGetNodeInfo_CollectsAllData(t *testing.T) {
	hostnameJSON := `{"metadata":{"id":"hostname"},"spec":{"hostname":"talos-cp-1"}}` + "\n"
	disksJSON := `{"metadata":{"id":"sda"},"spec":{"dev_path":"/dev/sda","model":"Samsung SSD","size":500107862016}}` + "\n"
	linksJSON := `{"metadata":{"id":"eth0"},"spec":{"hardwareAddr":"aa:bb:cc:dd:ee:01","busPath":"0000:00:03.0","kind":"","type":"ether"}}` + "\n"

	runner := &mockRunner{
		outputs: map[string]mockResult{
			"hostname": {output: []byte(hostnameJSON)},
			"disks":    {output: []byte(disksJSON)},
			"links":    {output: []byte(linksJSON)},
		},
	}

	scanner := &NmapScanner{Exec: runner}

	node, err := scanner.GetNodeInfo(context.Background(), "10.0.0.1")
	if err != nil {
		t.Fatalf("GetNodeInfo() error = %v", err)
	}
	if node.IP != "10.0.0.1" {
		t.Errorf("IP = %q, want 10.0.0.1", node.IP)
	}
	if node.Hostname != "talos-cp-1" {
		t.Errorf("Hostname = %q, want talos-cp-1", node.Hostname)
	}
	if len(node.Disks) != 1 || node.Disks[0].DevPath != "/dev/sda" {
		t.Errorf("Disks = %+v, want 1 disk at /dev/sda", node.Disks)
	}
	if len(node.Interfaces) != 1 || node.Interfaces[0].Name != "eth0" {
		t.Errorf("Interfaces = %+v, want 1 interface eth0", node.Interfaces)
	}
}

func TestGetNodeInfo_PartialFailure(t *testing.T) {
	hostnameJSON := `{"metadata":{"id":"hostname"},"spec":{"hostname":"node-01"}}` + "\n"

	runner := &mockRunner{
		outputs: map[string]mockResult{
			"hostname": {output: []byte(hostnameJSON)},
			"disks":    {err: fmt.Errorf("timeout")},
			"links":    {err: fmt.Errorf("timeout")},
		},
	}

	scanner := &NmapScanner{Exec: runner}

	node, err := scanner.GetNodeInfo(context.Background(), "10.0.0.1")
	if err != nil {
		t.Fatalf("GetNodeInfo() should succeed with partial data: %v", err)
	}
	if node.Hostname != "node-01" {
		t.Errorf("Hostname = %q, want node-01", node.Hostname)
	}
	if len(node.Disks) != 0 {
		t.Errorf("expected 0 disks on failure, got %d", len(node.Disks))
	}
}
