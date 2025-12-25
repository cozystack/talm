package initwizard

type WizardState int

const (
     StatePreset WizardState = iota
     StateEndpoint
     StateScanning
     StateNodeSelect
     StateNodeConfig
     StateConfirm
     StateGenerate
     StateDone
     StateAddNodeScan
     StateCozystackScan
     StateVIPConfig
     StateNetworkConfig
     StateNodeDetails
 )

func (s WizardState) String() string {
     return [...]string{
         "preset",
         "endpoint",
         "scanning",
         "node_select",
         "node_config",
         "confirm",
         "generate",
         "done",
         "add_node_scan",
         "cozystack_scan",
         "vip_config",
         "network_config",
         "node_details",
     }[s]
 }
