package scan

import (
	"fmt"

	"github.com/cozystack/talm/pkg/wizard"
	machineapi "github.com/siderolabs/talos/pkg/machinery/api/machine"
	storageapi "github.com/siderolabs/talos/pkg/machinery/api/storage"
)

// hostnameFromVersion extracts the hostname from a Version gRPC response.
func hostnameFromVersion(resp *machineapi.VersionResponse) string {
	if resp == nil || len(resp.Messages) == 0 {
		return ""
	}
	msg := resp.Messages[0]
	if msg.Metadata == nil {
		return ""
	}
	return msg.Metadata.Hostname
}

// disksFromResponse extracts disk information from a Disks gRPC response.
func disksFromResponse(resp *storageapi.DisksResponse) []wizard.Disk {
	if resp == nil || len(resp.Messages) == 0 {
		return nil
	}

	var disks []wizard.Disk
	for _, d := range resp.Messages[0].Disks {
		disks = append(disks, wizard.Disk{
			DevPath:   fmt.Sprintf("/dev/%s", d.DeviceName),
			Model:     d.Model,
			SizeBytes: d.Size,
		})
	}
	return disks
}

// memoryFromResponse extracts total memory in bytes from a Memory gRPC response.
// Memtotal is in kB.
func memoryFromResponse(resp *machineapi.MemoryResponse) uint64 {
	if resp == nil || len(resp.Messages) == 0 {
		return 0
	}
	msg := resp.Messages[0]
	if msg.Meminfo == nil {
		return 0
	}
	return msg.Meminfo.Memtotal * 1024
}

// linkFromSpec builds a NetInterface from the spec map of a network.LinkStatus
// resource. Returns nil for non-physical links (bonds, vlans, links without
// a PCI/USB bus path). Pure helper — no gRPC.
func linkFromSpec(name string, spec map[string]interface{}) *wizard.NetInterface {
	busPath, _ := spec["busPath"].(string)
	kind, _ := spec["kind"].(string)
	if busPath == "" || kind != "" {
		return nil
	}
	mac, _ := spec["hardwareAddr"].(string)
	return &wizard.NetInterface{
		Name: name,
		MAC:  mac,
	}
}

// addressFromSpec extracts the CIDR address and its link name from the spec of
// a network.AddressStatus resource. Returns empty strings for non-static or
// malformed addresses.
func addressFromSpec(spec map[string]interface{}) (linkName, cidr string) {
	linkName, _ = spec["linkName"].(string)
	cidr, _ = spec["address"].(string)
	return linkName, cidr
}

// defaultGatewayFromSpec extracts the next-hop gateway IP from the spec of a
// network.RouteStatus resource when it describes a default route. Returns an
// empty string otherwise.
func defaultGatewayFromSpec(spec map[string]interface{}) string {
	dest, _ := spec["destination"].(string)
	// Default route: destination empty or "0.0.0.0/0" / "::/0".
	if dest != "" && dest != "0.0.0.0/0" && dest != "::/0" {
		return ""
	}
	gw, _ := spec["gateway"].(string)
	return gw
}

