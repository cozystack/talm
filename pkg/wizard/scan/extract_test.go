package scan

import (
	"testing"

	"github.com/cozystack/talm/pkg/wizard"
	"github.com/siderolabs/talos/pkg/machinery/api/common"
	machineapi "github.com/siderolabs/talos/pkg/machinery/api/machine"
	storageapi "github.com/siderolabs/talos/pkg/machinery/api/storage"
)

func TestHostnameFromVersion(t *testing.T) {
	tests := []struct {
		name     string
		resp     *machineapi.VersionResponse
		expected string
	}{
		{
			name: "with hostname",
			resp: &machineapi.VersionResponse{
				Messages: []*machineapi.Version{
					{Metadata: &common.Metadata{Hostname: "talos-cp-1"}},
				},
			},
			expected: "talos-cp-1",
		},
		{
			name:     "nil response",
			resp:     nil,
			expected: "",
		},
		{
			name: "empty messages",
			resp: &machineapi.VersionResponse{
				Messages: []*machineapi.Version{},
			},
			expected: "",
		},
		{
			name: "nil metadata",
			resp: &machineapi.VersionResponse{
				Messages: []*machineapi.Version{
					{Metadata: nil},
				},
			},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hostnameFromVersion(tt.resp)
			if got != tt.expected {
				t.Errorf("hostnameFromVersion() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestDisksFromResponse(t *testing.T) {
	resp := &storageapi.DisksResponse{
		Messages: []*storageapi.Disks{
			{
				Disks: []*storageapi.Disk{
					{DeviceName: "sda", Model: "Samsung SSD", Size: 500107862016},
					{DeviceName: "nvme0n1", Model: "Intel NVMe", Size: 1000204886016},
				},
			},
		},
	}

	disks := disksFromResponse(resp)
	if len(disks) != 2 {
		t.Fatalf("expected 2 disks, got %d", len(disks))
	}

	if disks[0].DevPath != "/dev/sda" {
		t.Errorf("disk[0].DevPath = %q, want /dev/sda", disks[0].DevPath)
	}
	if disks[0].Model != "Samsung SSD" {
		t.Errorf("disk[0].Model = %q, want Samsung SSD", disks[0].Model)
	}
	if disks[0].SizeBytes != 500107862016 {
		t.Errorf("disk[0].SizeBytes = %d, want 500107862016", disks[0].SizeBytes)
	}
}

func TestDisksFromResponse_Nil(t *testing.T) {
	disks := disksFromResponse(nil)
	if len(disks) != 0 {
		t.Errorf("expected 0 disks for nil response, got %d", len(disks))
	}
}

func TestMemoryFromResponse(t *testing.T) {
	resp := &machineapi.MemoryResponse{
		Messages: []*machineapi.Memory{
			{
				Meminfo: &machineapi.MemInfo{
					Memtotal: 16384000, // in kB
				},
			},
		},
	}

	bytes := memoryFromResponse(resp)
	expected := uint64(16384000) * 1024
	if bytes != expected {
		t.Errorf("memoryFromResponse() = %d, want %d", bytes, expected)
	}
}

func TestMemoryFromResponse_Nil(t *testing.T) {
	if memoryFromResponse(nil) != 0 {
		t.Error("expected 0 for nil response")
	}
}

func TestFilterPhysicalInterfaces(t *testing.T) {
	all := []wizard.NetInterface{
		{Name: "eth0", MAC: "aa:bb:cc:dd:ee:01"},
		{Name: "lo", MAC: ""},
		{Name: "enp3s0", MAC: "aa:bb:cc:dd:ee:02"},
	}

	physical := filterPhysicalInterfaces(all)
	if len(physical) != 2 {
		t.Errorf("expected 2 physical interfaces, got %d: %v", len(physical), physical)
	}
}
