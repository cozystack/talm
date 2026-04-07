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

// filterPhysicalInterfaces removes interfaces that have empty MAC addresses
// (loopback, virtual interfaces without hardware).
func filterPhysicalInterfaces(interfaces []wizard.NetInterface) []wizard.NetInterface {
	var physical []wizard.NetInterface
	for _, iface := range interfaces {
		if iface.MAC != "" {
			physical = append(physical, iface)
		}
	}
	return physical
}
