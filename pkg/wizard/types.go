package wizard

// NodeInfo holds hardware and network information about a discovered Talos node.
type NodeInfo struct {
	IP         string
	Hostname   string
	MAC        string
	CPU        string // human-readable, e.g. "Intel Xeon E-2236 (12 threads)"
	RAMBytes   uint64
	Disks      []Disk
	Interfaces []NetInterface
}

// Disk represents a block device on a node.
type Disk struct {
	DevPath   string // e.g. "/dev/sda"
	Model     string
	SizeBytes uint64
}

// NetInterface represents a network interface on a node.
type NetInterface struct {
	Name string
	MAC  string
	IPs  []string
}

// NodeConfig holds user-specified configuration for a single node.
type NodeConfig struct {
	Hostname  string
	Role      string // "controlplane" or "worker"
	DiskPath  string // install disk, e.g. "/dev/sda"
	Interface string // primary network interface
	Addresses string // CIDR notation, e.g. "192.168.1.10/24"
	Gateway   string
	DNS       []string
	VIP       string // optional, controlplane only
}

// WizardResult holds all collected data from the wizard flow,
// ready to be passed to GenerateProject and values.yaml generation.
type WizardResult struct {
	Preset      string
	ClusterName string
	Endpoint    string // API server endpoint, e.g. "https://192.168.0.1:6443"
	Nodes       []NodeConfig

	// Network configuration
	PodSubnets        string // e.g. "10.244.0.0/16"
	ServiceSubnets    string // e.g. "10.96.0.0/16"
	AdvertisedSubnets string // e.g. "192.168.100.0/24"

	// Cozystack-specific fields
	ClusterDomain string
	FloatingIP    string
	Image         string
}
