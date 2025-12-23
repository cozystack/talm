package initwizard

// InitData содержит данные для инициализации кластера
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

// NodeInfo содержит информацию о ноде
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

// Hostname представляет имя хоста
type Hostname struct {
	Hostname string `json:"hostname"`
}

// Hardware представляет информацию об оборудовании
type Hardware struct {
	Processors   []Processor   `json:"processors"`
	Memory       Memory        `json:"memory"`
	Blockdevices []Blockdevice `json:"blockdevices"`
	Interfaces   []Interface   `json:"interfaces"`
}

// Processor представляет процессор
type Processor struct {
	Manufacturer string `json:"manufacturer"`
	ProductName  string `json:"productName"`
	ThreadCount  int    `json:"threadCount"`
}

// Memory представляет информацию о памяти
type Memory struct {
	Size int `json:"size"`
}

// Blockdevice представляет блочное устройство
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

// Interfaces представляет список сетевых интерфейсов
type Interfaces struct {
	Interfaces []Interface `json:"interfaces"`
}

// Interface представляет сетевой интерфейс
type Interface struct {
	Name string   `json:"name"`
	MAC  string   `json:"hardwareAddr"`
	IPs  []string `json:"ips,omitempty"`
}

// ValuesYAML представляет структуру values.yaml
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

// NodeConfig представляет конфигурацию ноды
type NodeConfig struct {
	Type string `yaml:"type"`
	IP   string `yaml:"ip"`
}

// ChartYAML представляет структуру Chart.yaml
type ChartYAML struct {
	APIVersion  string            `yaml:"apiVersion"`
	Name        string            `yaml:"name"`
	Version     string            `yaml:"version"`
	Description string            `yaml:"description"`
	Type        string            `yaml:"type"`
	AppVersion  string            `yaml:"appVersion"`
	Annotations map[string]string `yaml:"annotations,omitempty"`
}