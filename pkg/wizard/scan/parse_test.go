package scan

import "testing"

func TestParseNmapGrepOutput(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name: "single host",
			input: `# Nmap 7.94 scan initiated Mon Jan 06 10:00:00 2025 as: nmap -p 50000 --open -oG - 192.168.1.0/24
Host: 192.168.1.10 ()	Status: Up
Host: 192.168.1.10 ()	Ports: 50000/open/tcp//unknown///
# Nmap done at Mon Jan 06 10:00:05 2025 -- 256 IP addresses (1 host up) scanned in 5.00 seconds`,
			expected: []string{"192.168.1.10"},
		},
		{
			name: "multiple hosts",
			input: `# Nmap 7.94 scan
Host: 10.0.0.1 ()	Ports: 50000/open/tcp//unknown///
Host: 10.0.0.2 ()	Ports: 50000/open/tcp//unknown///
Host: 10.0.0.5 ()	Ports: 50000/open/tcp//unknown///
# Nmap done`,
			expected: []string{"10.0.0.1", "10.0.0.2", "10.0.0.5"},
		},
		{
			name:     "no hosts found",
			input:    "# Nmap 7.94 scan\n# Nmap done at Mon Jan 06 -- 256 IP addresses (0 hosts up) scanned",
			expected: nil,
		},
		{
			name:     "empty input",
			input:    "",
			expected: nil,
		},
		{
			name: "hosts with status only (no open ports)",
			input: `Host: 192.168.1.10 ()	Status: Up
Host: 192.168.1.10 ()	Ports: 50000/filtered/tcp//unknown///`,
			expected: nil,
		},
		{
			name: "mixed open and closed",
			input: `Host: 10.0.0.1 ()	Ports: 50000/open/tcp//unknown///
Host: 10.0.0.2 ()	Ports: 50000/closed/tcp//unknown///
Host: 10.0.0.3 ()	Ports: 50000/open/tcp//unknown///`,
			expected: []string{"10.0.0.1", "10.0.0.3"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseNmapGrepOutput(tt.input)
			if len(got) != len(tt.expected) {
				t.Fatalf("ParseNmapGrepOutput() returned %d IPs, want %d\ngot:  %v\nwant: %v",
					len(got), len(tt.expected), got, tt.expected)
			}
			for i := range got {
				if got[i] != tt.expected[i] {
					t.Errorf("IP[%d] = %q, want %q", i, got[i], tt.expected[i])
				}
			}
		})
	}
}
