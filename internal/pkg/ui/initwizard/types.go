package initwizard

// InitData contains data for cluster initialization
type InitData struct {
	TalosVersion      string
	Preset            string
	ClusterName       string
	APIServerURL      string
	PodSubnets        string
	ServiceSubnets    string
	AdvertisedSubnets string
	ClusterDomain     string
	FloatingIP        string
	Image             string
	OIDCIssuerURL     string
	NrHugepages       int
	NetworkToScan     string
	SelectedNode      string
	SelectedNodeInfo  NodeInfo
	NodeType          string
	DiscoveredNodes   []NodeInfo
	Hostname          string
	Disk              string
	Interface         string
	Addresses         string
	Gateway           string
	DNSServers        string
	VIP               string
	MachineConfig     string
}

// NodeInfo contains node information
type NodeInfo struct {
	Name         string
	IP           string
	MAC          string
	Hostname     string
	Type         string
	Configured   bool
	Manufacturer string
	CPU          int
	RAM          int
	Disks        []Blockdevice
	Hardware     Hardware
}

// Hostname represents hostname
type Hostname struct {
	Hostname string `json:"hostname"`
}

// Hardware represents hardware information
type Hardware struct {
	Processors   []Processor   `json:"processors"`
	Memory       Memory        `json:"memory"`
	Blockdevices []Blockdevice `json:"blockdevices"`
	Interfaces   []Interface   `json:"interfaces"`
}

// Processor represents processor
type Processor struct {
	Manufacturer string `json:"manufacturer"`
	ProductName  string `json:"productName"`
	ThreadCount  int    `json:"threadCount"`
}

// Memory represents memory information
type Memory struct {
	Size int `json:"size"`
}

// Blockdevice represents block device
type Blockdevice struct {
	Name      string `json:"-"`
	Size      int    `json:"size"`
	DevPath   string `json:"dev_path"`
	Model     string `json:"model"`
	Transport string `json:"transport"`
	Metadata  struct {
		ID string `json:"id"`
	} `json:"metadata"`
}

// Interfaces represents list of network interfaces
type Interfaces struct {
	Interfaces []Interface `json:"interfaces"`
}

// Interface represents network interface
type Interface struct {
	Name string   `json:"name"`
	MAC  string   `json:"hardwareAddr"`
	IPs  []string `json:"ips,omitempty"`
}

// ValuesYAML represents values.yaml structure
type ValuesYAML struct {
	ClusterName        string                `yaml:"clusterName"`
	FloatingIP         string                `yaml:"floatingIP,omitempty"`
	KubernetesEndpoint string                `yaml:"kubernetesEndpoint"`
	EtcdBootstrapped   bool                  `yaml:"etcdBootstrapped"`
	Preset             string                `yaml:"preset"`
	TalosVersion       string                `yaml:"talosVersion"`
	APIServerURL       string                `yaml:"apiServerURL,omitempty"`
	PodSubnets         string                `yaml:"podSubnets,omitempty"`
	ServiceSubnets     string                `yaml:"serviceSubnets,omitempty"`
	AdvertisedSubnets  string                `yaml:"advertisedSubnets,omitempty"`
	ClusterDomain      string                `yaml:"clusterDomain,omitempty"`
	Image              string                `yaml:"image,omitempty"`
	OIDCIssuerURL      string                `yaml:"oidcIssuerURL,omitempty"`
	NrHugepages        int                   `yaml:"nrHugepages,omitempty"`
	Nodes              map[string]NodeConfig `yaml:"nodes,omitempty"`
}

// NodeConfig represents node configuration
type NodeConfig struct {
	Type string `yaml:"type"`
	IP   string `yaml:"ip"`
}

// ChartYAML represents Chart.yaml structure
type ChartYAML struct {
	APIVersion  string            `yaml:"apiVersion"`
	Name        string            `yaml:"name"`
	Version     string            `yaml:"version"`
	Description string            `yaml:"description"`
	Type        string            `yaml:"type"`
	AppVersion  string            `yaml:"appVersion"`
	Annotations map[string]string `yaml:"annotations,omitempty"`
}