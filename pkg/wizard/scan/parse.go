package scan

import (
	"strings"
)

// ParseNmapGrepOutput extracts IP addresses of hosts with open ports
// from nmap grepable output (-oG format).
//
// Lines with open ports look like:
//
//	Host: 192.168.1.10 ()	Ports: 50000/open/tcp//unknown///
func ParseNmapGrepOutput(output string) []string {
	var ips []string

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "Host:") {
			continue
		}
		if !strings.Contains(line, "/open/") {
			continue
		}

		// Extract IP from "Host: <IP> (<hostname>)"
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		ips = append(ips, parts[1])
	}

	return ips
}
