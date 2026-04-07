package wizard

import (
	"fmt"
	"net"
	"net/url"
	"regexp"
)

var clusterNameRegexp = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// ValidateClusterName checks that name is a valid DNS label:
// lowercase alphanumeric and hyphens, max 63 chars, no leading/trailing hyphens.
func ValidateClusterName(name string) error {
	if name == "" {
		return fmt.Errorf("cluster name must not be empty")
	}
	if len(name) > 63 {
		return fmt.Errorf("cluster name must be at most 63 characters, got %d", len(name))
	}
	if !clusterNameRegexp.MatchString(name) {
		return fmt.Errorf("cluster name must contain only lowercase letters, numbers, and hyphens, and must not start or end with a hyphen")
	}
	return nil
}

var hostnameRegexp = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?$`)

// ValidateHostname checks that hostname is a valid single-label hostname (no dots).
// FQDNs are not accepted — Talos nodes use single-label hostnames.
func ValidateHostname(hostname string) error {
	if hostname == "" {
		return fmt.Errorf("hostname must not be empty")
	}
	if len(hostname) > 63 {
		return fmt.Errorf("hostname label must be at most 63 characters, got %d", len(hostname))
	}
	if !hostnameRegexp.MatchString(hostname) {
		return fmt.Errorf("hostname must contain only letters, numbers, and hyphens, and must not start or end with a hyphen")
	}
	return nil
}

// ValidateCIDR checks that cidr is a valid CIDR notation (e.g. "192.168.1.0/24").
func ValidateCIDR(cidr string) error {
	if cidr == "" {
		return fmt.Errorf("CIDR must not be empty")
	}
	_, _, err := net.ParseCIDR(cidr)
	if err != nil {
		return fmt.Errorf("invalid CIDR notation: %w", err)
	}
	return nil
}

// ValidateEndpoint checks that endpoint is a valid https URL with a host and port.
func ValidateEndpoint(endpoint string) error {
	if endpoint == "" {
		return fmt.Errorf("endpoint must not be empty")
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" {
		return fmt.Errorf("invalid endpoint URL: %s", endpoint)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("endpoint must use https scheme, got %q", u.Scheme)
	}
	if u.Port() == "" {
		return fmt.Errorf("endpoint must include a port number")
	}
	return nil
}

// ValidateIP checks that ip is a valid IP address (v4 or v6).
func ValidateIP(ip string) error {
	if ip == "" {
		return fmt.Errorf("IP address must not be empty")
	}
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return fmt.Errorf("invalid IP address: %s", ip)
	}
	return nil
}

// ValidateNodeRole checks that role is either "controlplane" or "worker".
func ValidateNodeRole(role string) error {
	switch role {
	case "controlplane", "worker":
		return nil
	default:
		return fmt.Errorf("node role must be %q or %q, got %q", "controlplane", "worker", role)
	}
}
