package scan

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/cosi-project/runtime/pkg/resource"
	"github.com/cosi-project/runtime/pkg/resource/meta"
	"gopkg.in/yaml.v3"

	"github.com/cozystack/talm/pkg/wizard"
	"github.com/siderolabs/talos/cmd/talosctl/pkg/talos/helpers"
	"github.com/siderolabs/talos/pkg/machinery/client"
)

const (
	defaultTalosPort  = 50000
	defaultTimeout    = 30 * time.Second
	maxConcurrentJobs = 10
)


// TalosScanner discovers Talos nodes via TCP port scanning and collects
// hardware info via the Talos gRPC API. No external binaries required.
type TalosScanner struct {
	Port    int
	Timeout time.Duration
}

// New creates a scanner with default settings.
func New() *TalosScanner {
	return &TalosScanner{
		Port:    defaultTalosPort,
		Timeout: defaultTimeout,
	}
}

// ScanNetwork discovers Talos nodes in the given CIDR range by TCP-scanning
// the Talos API port, then querying each discovered node for hardware details.
func (s *TalosScanner) ScanNetwork(ctx context.Context, cidr string) ([]wizard.NodeInfo, error) {
	result, err := s.ScanNetworkFull(ctx, cidr)
	if err != nil {
		return nil, err
	}
	return result.Nodes, nil
}

// ScanNetworkFull is like ScanNetwork but also returns warnings about
// nodes that were discovered by TCP but failed gRPC info collection.
func (s *TalosScanner) ScanNetworkFull(ctx context.Context, cidr string) (wizard.ScanResult, error) {
	port := s.Port
	if port == 0 {
		port = defaultTalosPort
	}

	ips, err := scanTCPPort(ctx, cidr, port, maxConcurrentJobs)
	if err != nil {
		return wizard.ScanResult{}, err
	}
	if len(ips) == 0 {
		return wizard.ScanResult{}, nil
	}

	return s.collectNodeInfo(ctx, ips)
}

// GetNodeInfo connects to a single Talos node via gRPC and retrieves
// hostname, disks, memory, and network interface information.
func (s *TalosScanner) GetNodeInfo(ctx context.Context, ip string) (wizard.NodeInfo, error) {
	node := wizard.NodeInfo{IP: ip}

	timeout := s.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}
	infoCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	c, err := client.New(infoCtx,
		client.WithEndpoints(ip),
		client.WithTLSConfig(&tls.Config{InsecureSkipVerify: true}), //nolint:gosec
	)
	if err != nil {
		return node, err
	}
	defer func() { _ = c.Close() }()

	nodeCtx := client.WithNode(infoCtx, ip)

	// Collect hostname from Version response
	if versionResp, err := c.Version(nodeCtx); err == nil {
		node.Hostname = hostnameFromVersion(versionResp)
	}

	// Collect disks
	if disksResp, err := c.Disks(nodeCtx); err == nil {
		node.Disks = disksFromResponse(disksResp)
	}

	// Collect memory
	if memResp, err := c.Memory(nodeCtx); err == nil {
		node.RAMBytes = memoryFromResponse(memResp)
	}

	// Collect network interfaces via COSI resource API
	node.Interfaces = s.collectLinks(nodeCtx, c)

	// If no useful data was collected, treat as failure
	if node.Hostname == "" && len(node.Disks) == 0 && node.RAMBytes == 0 {
		return node, fmt.Errorf("node %s: gRPC connected but returned no useful data", ip)
	}

	return node, nil
}

// collectLinks retrieves network link resources via the COSI API and
// returns physical interfaces only.
func (s *TalosScanner) collectLinks(ctx context.Context, c *client.Client) []wizard.NetInterface {
	var interfaces []wizard.NetInterface

	callbackRD := func(_ *meta.ResourceDefinition) error { return nil }
	callbackResource := func(_ context.Context, _ string, r resource.Resource, callErr error) error {
		if callErr != nil {
			return nil
		}

		yamlData, err := resource.MarshalYAML(r)
		if err != nil {
			return nil
		}

		resMap, ok := yamlData.(map[string]interface{})
		if !ok {
			return nil
		}

		specRaw, ok := resMap["spec"]
		if !ok {
			return nil
		}

		specBytes, err := yaml.Marshal(specRaw)
		if err != nil {
			return nil
		}

		var specMap map[string]interface{}
		if err := yaml.Unmarshal(specBytes, &specMap); err != nil {
			return nil
		}

		name := r.Metadata().ID()
		mac, _ := specMap["hardwareAddr"].(string)
		busPath, _ := specMap["busPath"].(string)
		kind, _ := specMap["kind"].(string)

		// Only include physical interfaces: has busPath, not virtual (bond/vlan)
		if busPath != "" && kind == "" {
			interfaces = append(interfaces, wizard.NetInterface{
				Name: name,
				MAC:  mac,
			})
		}

		return nil
	}

	if err := helpers.ForEachResource(ctx, c, callbackRD, callbackResource, "network", "links"); err != nil {
		// Log but don't fail — interfaces are supplementary info
		fmt.Fprintf(os.Stderr, "Warning: failed to list network links: %v\n", err)
	}

	return interfaces
}

// collectNodeInfo queries multiple nodes concurrently with bounded parallelism.
// Returns discovered nodes and warnings for nodes that failed gRPC info collection.
func (s *TalosScanner) collectNodeInfo(ctx context.Context, ips []string) (wizard.ScanResult, error) {
	var (
		mu       sync.Mutex
		nodes    []wizard.NodeInfo
		warnings []string
		sem      = make(chan struct{}, maxConcurrentJobs)
		wg       sync.WaitGroup
	)

	for _, ip := range ips {
		wg.Add(1)
		go func(ip string) {
			defer wg.Done()

			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}

			node, err := s.GetNodeInfo(ctx, ip)
			if err != nil {
				mu.Lock()
				warnings = append(warnings, fmt.Sprintf("%s: %v", ip, err))
				mu.Unlock()
				return
			}
			if node.IP == "" {
				node.IP = ip
			}

			mu.Lock()
			nodes = append(nodes, node)
			mu.Unlock()
		}(ip)
	}

	wg.Wait()

	if len(nodes) == 0 && len(ips) > 0 {
		return wizard.ScanResult{Warnings: warnings},
			fmt.Errorf("found %d host(s) with open port %d but none responded as Talos nodes", len(ips), s.Port)
	}

	// Sort by IP numerically for deterministic ordering
	slices.SortFunc(nodes, func(a, b wizard.NodeInfo) int {
		ipA := net.ParseIP(a.IP).To4()
		ipB := net.ParseIP(b.IP).To4()
		if ipA != nil && ipB != nil {
			return bytes.Compare(ipA, ipB)
		}
		return strings.Compare(a.IP, b.IP)
	})

	return wizard.ScanResult{Nodes: nodes, Warnings: warnings}, nil
}
