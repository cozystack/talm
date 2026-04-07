package wizard

import "context"

// Scanner discovers Talos nodes on the network and collects hardware information.
type Scanner interface {
	// ScanNetwork discovers Talos nodes in the given CIDR range.
	ScanNetwork(ctx context.Context, cidr string) ([]NodeInfo, error)

	// GetNodeInfo connects to a single node and retrieves its hardware details.
	GetNodeInfo(ctx context.Context, ip string) (NodeInfo, error)
}
