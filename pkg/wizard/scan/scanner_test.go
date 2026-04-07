package scan

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// mockRunner is a test double for CommandRunner.
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

	runner := &mockRunner{
		outputs: map[string]mockResult{
			"nmap":     {output: []byte(nmapOutput), err: nil},
			"talosctl": {output: []byte("node-info"), err: nil},
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
			"nmap": {output: []byte("# Nmap done -- 0 hosts up"), err: nil},
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
			"nmap": {output: nil, err: fmt.Errorf("nmap not found")},
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
			"nmap":     {output: []byte(nmapOutput), err: nil},
			"talosctl": {output: nil, err: fmt.Errorf("connection refused")},
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

func TestGetNodeInfo_Success(t *testing.T) {
	runner := &mockRunner{
		outputs: map[string]mockResult{
			"talosctl": {output: []byte("hostname-data"), err: nil},
		},
	}

	scanner := &NmapScanner{Exec: runner}

	node, err := scanner.GetNodeInfo(context.Background(), "10.0.0.1")
	if err != nil {
		t.Fatalf("GetNodeInfo() error = %v", err)
	}
	if node.IP != "10.0.0.1" {
		t.Errorf("expected IP 10.0.0.1, got %s", node.IP)
	}
}

func TestGetNodeInfo_Error(t *testing.T) {
	runner := &mockRunner{
		outputs: map[string]mockResult{
			"talosctl": {output: nil, err: fmt.Errorf("timeout")},
		},
	}

	scanner := &NmapScanner{Exec: runner}

	node, err := scanner.GetNodeInfo(context.Background(), "10.0.0.1")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if node.IP != "10.0.0.1" {
		t.Errorf("expected IP to be set even on error, got %s", node.IP)
	}
}
