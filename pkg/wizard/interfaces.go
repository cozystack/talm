package wizard

import "context"

// ScanResult holds the result of a network scan.
type ScanResult struct {
	Nodes    []NodeInfo
	Warnings []string
}

// Scanner discovers Talos nodes on the network and collects hardware information.
type Scanner interface {
	// ScanNetwork discovers Talos nodes in the given CIDR range.
	ScanNetwork(ctx context.Context, cidr string) ([]NodeInfo, error)

	// ScanNetworkFull is like ScanNetwork but also returns warnings.
	ScanNetworkFull(ctx context.Context, cidr string) (ScanResult, error)

	// GetNodeInfo connects to a single node and retrieves its hardware details.
	GetNodeInfo(ctx context.Context, ip string) (NodeInfo, error)
}
