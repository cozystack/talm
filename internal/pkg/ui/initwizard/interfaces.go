package initwizard

import (
	"context"

	"github.com/rivo/tview"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
)

// Wizard interface of the main initialization wizard component
type Wizard interface {
	Run() error
	getData() *InitData
	getApp() *tview.Application
	getPages() *tview.Pages
	setupInputCapture()
	GetScanner() NetworkScanner
}

// Validator interface of the validation component
type Validator interface {
	ValidateNetworkCIDR(cidr string) error
	ValidateClusterName(name string) error
	ValidateHostname(hostname string) error
	ValidateRequiredField(value, fieldName string) error
	ValidateIP(ip string) error
	ValidateVIP(vip string) error
	ValidateDNSservers(dns string) error
	ValidateNetworkConfig(addresses, gateway, dnsServers string) error
	ValidateNodeType(nodeType string) error
	ValidatePreset(preset string) error
	ValidateAPIServerURL(url string) error
}

// DataProcessor interface of the data processing component
type DataProcessor interface {
	FilterAndSortNodes(nodes []NodeInfo) []NodeInfo
	ExtractHardwareInfo(ip string) (Hardware, error)
	ProcessScanResults(results []NodeInfo) []NodeInfo
	CalculateResourceStats(node NodeInfo) (cpu, ram, disks int)
	RemoveDuplicatesByMAC(nodes []NodeInfo) []NodeInfo
}

// Generator interface of the configuration generation component
type Generator interface {
	GenerateChartYAML(clusterName, preset string) (ChartYAML, error)
	GenerateValuesYAML(data *InitData) (ValuesYAML, error)
	GenerateMachineConfig(data *InitData) (string, error)
	GenerateNodeConfig(filename string, data *InitData, values *ValuesYAML) error
	SaveChartYAML(chart ChartYAML) error
	SaveValuesYAML(values ValuesYAML) error
	LoadValuesYAML() (*ValuesYAML, error)
	GenerateBootstrapConfig(data *InitData) error
	UpdateValuesYAMLWithNode(data *InitData) error
	// Secrets management
	GenerateSecretsBundle(data *InitData) error
	LoadSecretsBundle() (interface{}, error)
	ValidateSecretsBundle() error
	SaveSecretsBundle(bundle *secrets.Bundle) error
}

// NetworkScanner interface of the network scanning component
type NetworkScanner interface {
	ScanNetwork(ctx context.Context, cidr string) ([]NodeInfo, error)
	ScanNetworkWithProgress(ctx context.Context, cidr string, progressFunc func(int)) ([]NodeInfo, error)
	IsTalosNode(ctx context.Context, ip string) bool
	CollectNodeInfo(ctx context.Context, ip string) (NodeInfo, error)
	CollectNodeInfoEnhanced(ctx context.Context, ip string) (NodeInfo, error)
	ParallelScan(ctx context.Context, ips []string) ([]NodeInfo, error)
}

// Presenter interface of the user interface component
type Presenter interface {
	ShowStep1Form(data *InitData) *tview.Form
	ShowGenericStep2(data *InitData)
	ShowCozystackScan(data *InitData)
	ShowAddNodeWizard(data *InitData)
	ShowNodeSelection(data *InitData, title string)
	ShowNodeConfig(data *InitData)
	ShowNetworkConfig(data *InitData)
	ShowProgressModal(message string, task func())
	ShowScanningModal(scanFunc func(context.Context, func(int)), ctx context.Context)
	ShowErrorModal(message string)
	ShowSuccessModal(message string)
	ShowConfigConfirmation(data *InitData)
	ShowBootstrapPrompt(data *InitData, nodeFileName string)
	ShowFirstNodeConfig(data *InitData)
}

// UIHelper interface of UI helper functions
type UIHelper interface {
	CreateButton(text string, handler func()) *tview.Button
	CreateInputField(label, initialText string, fieldWidth int, validator func(string), changed func(string)) *tview.InputField
	CreateDropDown(label string, options []string, initialIndex int, changed func(string, int)) *tview.DropDown
	SetFormStyle(form *tview.Form, title string)
	AddFormButtons(form *tview.Form, buttons map[string]func())
	SwitchPage(pages *tview.Pages, pageName string)
}
