package scan

import (
	"testing"

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

// §9 — linkFromSpec must parse spec via direct type assertion (no YAML round-trip)

func TestLinkFromSpec_PhysicalInterface(t *testing.T) {
	spec := map[string]interface{}{
		"hardwareAddr": "aa:bb:cc:dd:ee:ff",
		"busPath":      "0000:00:1f.6",
		// "kind" absent → physical
	}

	iface := linkFromSpec("eth0", spec)
	if iface == nil {
		t.Fatal("expected NetInterface, got nil")
	}
	if iface.Name != "eth0" {
		t.Errorf("Name = %q, want eth0", iface.Name)
	}
	if iface.MAC != "aa:bb:cc:dd:ee:ff" {
		t.Errorf("MAC = %q", iface.MAC)
	}
}

func TestLinkFromSpec_SkipsVirtual(t *testing.T) {
	// Bond: no busPath
	if linkFromSpec("bond0", map[string]interface{}{"hardwareAddr": "xx"}) != nil {
		t.Error("bond (no busPath) should be skipped")
	}
	// VLAN: has kind
	if linkFromSpec("eth0.10", map[string]interface{}{"busPath": "x", "kind": "vlan"}) != nil {
		t.Error("vlan (kind!=\"\") should be skipped")
	}
}

// §2 — addressFromSpec extracts linkName + CIDR for matching to interface

func TestAddressFromSpec(t *testing.T) {
	link, cidr := addressFromSpec(map[string]interface{}{
		"linkName": "eth0",
		"address":  "10.0.0.5/24",
	})
	if link != "eth0" || cidr != "10.0.0.5/24" {
		t.Errorf("got (%q, %q), want (eth0, 10.0.0.5/24)", link, cidr)
	}
}

// §2 — defaultGatewayFromSpec returns gateway only for default route

func TestDefaultGatewayFromSpec_DefaultRoute(t *testing.T) {
	gw := defaultGatewayFromSpec(map[string]interface{}{
		"destination": "0.0.0.0/0",
		"gateway":     "10.0.0.1",
	})
	if gw != "10.0.0.1" {
		t.Errorf("default route gateway = %q, want 10.0.0.1", gw)
	}

	// Empty destination also means default route in COSI output
	gw = defaultGatewayFromSpec(map[string]interface{}{"gateway": "10.0.0.2"})
	if gw != "10.0.0.2" {
		t.Errorf("empty-destination gateway = %q, want 10.0.0.2", gw)
	}
}

func TestDefaultGatewayFromSpec_NonDefault(t *testing.T) {
	gw := defaultGatewayFromSpec(map[string]interface{}{
		"destination": "192.168.1.0/24",
		"gateway":     "10.0.0.1",
	})
	if gw != "" {
		t.Errorf("non-default route should return empty, got %q", gw)
	}
}

