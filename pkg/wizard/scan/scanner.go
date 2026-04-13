package scan

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/cosi-project/runtime/pkg/resource"
	"github.com/cosi-project/runtime/pkg/resource/meta"

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
	node, _, err := s.getNodeInfoWithWarnings(ctx, ip)
	return node, err
}

// getNodeInfoWithWarnings is like GetNodeInfo but additionally returns non-fatal
// warnings (e.g. failed link listing) so the caller can surface them through
// the UI instead of the terminal while Bubble Tea owns the screen.
func (s *TalosScanner) getNodeInfoWithWarnings(ctx context.Context, ip string) (wizard.NodeInfo, []string, error) {
	node := wizard.NodeInfo{IP: ip}
	var warnings []string

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
		return node, warnings, err
	}
	defer func() { _ = c.Close() }()

	nodeCtx := client.WithNode(infoCtx, ip)

	if versionResp, err := c.Version(nodeCtx); err == nil {
		node.Hostname = hostnameFromVersion(versionResp)
	}
	if disksResp, err := c.Disks(nodeCtx); err == nil {
		node.Disks = disksFromResponse(disksResp)
	}
	if memResp, err := c.Memory(nodeCtx); err == nil {
		node.RAMBytes = memoryFromResponse(memResp)
	}

	ifaces, linkWarn := s.collectLinks(nodeCtx, c)
	warnings = append(warnings, linkWarn...)

	addrs, addrWarn := s.collectAddresses(nodeCtx, c)
	warnings = append(warnings, addrWarn...)
	// Merge addresses into interfaces by link name.
	for i := range ifaces {
		if ips, ok := addrs[ifaces[i].Name]; ok {
			ifaces[i].IPs = ips
		}
	}
	node.Interfaces = ifaces

	gateway, routeWarn := s.collectDefaultGateway(nodeCtx, c)
	warnings = append(warnings, routeWarn...)
	node.DefaultGateway = gateway

	if node.Hostname == "" && len(node.Disks) == 0 && node.RAMBytes == 0 {
		return node, warnings, fmt.Errorf("node %s: gRPC connected but returned no useful data", ip)
	}

	return node, warnings, nil
}

// collectLinks retrieves network link resources via the COSI API and
// returns physical interfaces only. Non-fatal errors are returned as warnings
// so the wizard can surface them through the TUI instead of the terminal.
func (s *TalosScanner) collectLinks(ctx context.Context, c *client.Client) ([]wizard.NetInterface, []string) {
	var (
		interfaces []wizard.NetInterface
		warnings   []string
	)

	callbackRD := func(_ *meta.ResourceDefinition) error { return nil }
	callbackResource := func(_ context.Context, _ string, r resource.Resource, callErr error) error {
		if callErr != nil {
			return nil
		}
		spec := specMapFromResource(r)
		if spec == nil {
			return nil
		}
		if iface := linkFromSpec(r.Metadata().ID(), spec); iface != nil {
			interfaces = append(interfaces, *iface)
		}
		return nil
	}

	if err := helpers.ForEachResource(ctx, c, callbackRD, callbackResource, "network", "links"); err != nil {
		warnings = append(warnings, fmt.Sprintf("failed to list network links: %v", err))
	}

	return interfaces, warnings
}

// collectAddresses returns a map of link name → [CIDR addresses] discovered
// via network.AddressStatus resources.
func (s *TalosScanner) collectAddresses(ctx context.Context, c *client.Client) (map[string][]string, []string) {
	result := map[string][]string{}
	var warnings []string

	callbackRD := func(_ *meta.ResourceDefinition) error { return nil }
	callbackResource := func(_ context.Context, _ string, r resource.Resource, callErr error) error {
		if callErr != nil {
			return nil
		}
		spec := specMapFromResource(r)
		if spec == nil {
			return nil
		}
		link, cidr := addressFromSpec(spec)
		if link == "" || cidr == "" {
			return nil
		}
		result[link] = append(result[link], cidr)
		return nil
	}

	if err := helpers.ForEachResource(ctx, c, callbackRD, callbackResource, "network", "addressstatuses"); err != nil {
		warnings = append(warnings, fmt.Sprintf("failed to list addresses: %v", err))
	}

	return result, warnings
}

// collectDefaultGateway returns the next-hop IP of the first default route
// found on the node, or an empty string if there isn't one.
func (s *TalosScanner) collectDefaultGateway(ctx context.Context, c *client.Client) (string, []string) {
	var (
		gateway  string
		warnings []string
	)

	callbackRD := func(_ *meta.ResourceDefinition) error { return nil }
	callbackResource := func(_ context.Context, _ string, r resource.Resource, callErr error) error {
		if callErr != nil || gateway != "" {
			return nil
		}
		spec := specMapFromResource(r)
		if spec == nil {
			return nil
		}
		if gw := defaultGatewayFromSpec(spec); gw != "" {
			gateway = gw
		}
		return nil
	}

	if err := helpers.ForEachResource(ctx, c, callbackRD, callbackResource, "network", "routestatuses"); err != nil {
		warnings = append(warnings, fmt.Sprintf("failed to list routes: %v", err))
	}

	return gateway, warnings
}

// specMapFromResource extracts the spec map of a COSI resource using a direct
// type assertion on the value produced by resource.MarshalYAML. Avoids the
// YAML round-trip the original implementation used.
func specMapFromResource(r resource.Resource) map[string]interface{} {
	yamlData, err := resource.MarshalYAML(r)
	if err != nil {
		return nil
	}
	resMap, ok := yamlData.(map[string]interface{})
	if !ok {
		return nil
	}
	spec, ok := resMap["spec"].(map[string]interface{})
	if !ok {
		return nil
	}
	return spec
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

			node, nodeWarn, err := s.getNodeInfoWithWarnings(ctx, ip)
			if err != nil {
				mu.Lock()
				warnings = append(warnings, fmt.Sprintf("%s: %v", ip, err))
				for _, w := range nodeWarn {
					warnings = append(warnings, fmt.Sprintf("%s: %s", ip, w))
				}
				mu.Unlock()
				return
			}
			if node.IP == "" {
				node.IP = ip
			}

			mu.Lock()
			nodes = append(nodes, node)
			for _, w := range nodeWarn {
				warnings = append(warnings, fmt.Sprintf("%s: %s", ip, w))
			}
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
