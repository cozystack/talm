package scan

import (
	"testing"
)

func TestParseHostname(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
		wantErr  bool
	}{
		{
			name: "valid response",
			input: `{"metadata":{"namespace":"network","type":"HostnameStatuses.net","id":"hostname","version":"1"},"spec":{"hostname":"talos-cp-1","domainname":""}}
`,
			expected: "talos-cp-1",
		},
		{
			name: "with domain",
			input: `{"metadata":{"namespace":"network","type":"HostnameStatuses.net","id":"hostname","version":"1"},"spec":{"hostname":"node-01","domainname":"example.com"}}
`,
			expected: "node-01",
		},
		{
			name:     "empty input",
			input:    "",
			expected: "",
			wantErr:  true,
		},
		{
			name:     "invalid json",
			input:    "not json",
			expected: "",
			wantErr:  true,
		},
		{
			name:     "missing spec",
			input:    `{"metadata":{}}`,
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseHostname([]byte(tt.input))
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseHostname() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.expected {
				t.Errorf("ParseHostname() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestParseDisks(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected int
		wantErr  bool
	}{
		{
			name: "two disks",
			input: `{"metadata":{"namespace":"runtime","type":"Disks.block","id":"sda","version":"1"},"spec":{"dev_path":"/dev/sda","model":"VBOX HARDDISK","serial":"VB12345","wwid":"","pretty_size":"50 GB","size":53687091200,"transport":"sata"}}
{"metadata":{"namespace":"runtime","type":"Disks.block","id":"nvme0n1","version":"1"},"spec":{"dev_path":"/dev/nvme0n1","model":"Samsung SSD 980","serial":"S123","wwid":"nvme-samsung","pretty_size":"500 GB","size":500107862016,"transport":"nvme"}}
`,
			expected: 2,
		},
		{
			name:     "empty input",
			input:    "",
			expected: 0,
		},
		{
			name: "single disk",
			input: `{"metadata":{"namespace":"runtime","type":"Disks.block","id":"sda","version":"1"},"spec":{"dev_path":"/dev/sda","model":"QEMU HARDDISK","size":10737418240}}
`,
			expected: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			disks, err := ParseDisks([]byte(tt.input))
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseDisks() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if len(disks) != tt.expected {
				t.Errorf("ParseDisks() returned %d disks, want %d", len(disks), tt.expected)
				return
			}
			if tt.expected > 0 {
				if disks[0].DevPath == "" {
					t.Error("first disk DevPath is empty")
				}
			}
		})
	}
}

func TestParseDisks_Fields(t *testing.T) {
	input := `{"metadata":{"id":"sda"},"spec":{"dev_path":"/dev/sda","model":"Samsung SSD","size":500107862016}}
`
	disks, err := ParseDisks([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(disks) != 1 {
		t.Fatalf("expected 1 disk, got %d", len(disks))
	}
	d := disks[0]
	if d.DevPath != "/dev/sda" {
		t.Errorf("DevPath = %q, want /dev/sda", d.DevPath)
	}
	if d.Model != "Samsung SSD" {
		t.Errorf("Model = %q, want Samsung SSD", d.Model)
	}
	if d.SizeBytes != 500107862016 {
		t.Errorf("SizeBytes = %d, want 500107862016", d.SizeBytes)
	}
}

func TestParseLinks(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected int
		wantErr  bool
	}{
		{
			name: "two interfaces",
			input: `{"metadata":{"namespace":"network","type":"LinkStatuses.net","id":"eth0","version":"3"},"spec":{"hardwareAddr":"aa:bb:cc:dd:ee:01","busPath":"0000:00:03.0","driver":"virtio_net","kind":"","type":"ether","operationalState":"up"}}
{"metadata":{"namespace":"network","type":"LinkStatuses.net","id":"eth1","version":"2"},"spec":{"hardwareAddr":"aa:bb:cc:dd:ee:02","busPath":"0000:00:04.0","driver":"virtio_net","kind":"","type":"ether","operationalState":"up"}}
`,
			expected: 2,
		},
		{
			name:     "empty input",
			input:    "",
			expected: 0,
		},
		{
			name: "filters non-physical interfaces",
			input: `{"metadata":{"id":"lo"},"spec":{"hardwareAddr":"","busPath":"","driver":"","kind":"","type":"loopback"}}
{"metadata":{"id":"eth0"},"spec":{"hardwareAddr":"aa:bb:cc:dd:ee:01","busPath":"0000:00:03.0","driver":"virtio_net","kind":"","type":"ether"}}
{"metadata":{"id":"bond0"},"spec":{"hardwareAddr":"aa:bb:cc:dd:ee:03","busPath":"","driver":"","kind":"bond","type":"ether"}}
`,
			expected: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			links, err := ParseLinks([]byte(tt.input))
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseLinks() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if len(links) != tt.expected {
				t.Errorf("ParseLinks() returned %d links, want %d", len(links), tt.expected)
			}
		})
	}
}

func TestParseLinks_Fields(t *testing.T) {
	input := `{"metadata":{"id":"enp3s0"},"spec":{"hardwareAddr":"aa:bb:cc:dd:ee:ff","busPath":"0000:03:00.0","driver":"e1000e","kind":"","type":"ether"}}
`
	links, err := ParseLinks([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 1 {
		t.Fatalf("expected 1 link, got %d", len(links))
	}
	l := links[0]
	if l.Name != "enp3s0" {
		t.Errorf("Name = %q, want enp3s0", l.Name)
	}
	if l.MAC != "aa:bb:cc:dd:ee:ff" {
		t.Errorf("MAC = %q, want aa:bb:cc:dd:ee:ff", l.MAC)
	}
}
