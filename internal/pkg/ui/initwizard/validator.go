package initwizard

import (
	"fmt"
	"net"
	"regexp"
	"strings"
)

// ValidatorImpl implements the Validator interface
type ValidatorImpl struct{}

// NewValidator creates a new validator instance
func NewValidator() Validator {
	return &ValidatorImpl{}
}

// ValidateNetworkCIDR validates the correctness of CIDR network notation
func (v *ValidatorImpl) ValidateNetworkCIDR(cidr string) error {
	if strings.TrimSpace(cidr) == "" {
		return NewValidationError(
			"VAL_001", 
			"network for scanning cannot be empty", 
			"CIDR network field is required for scanning",
		)
	}

	_, _, err := net.ParseCIDR(cidr)
	if err != nil {
		return NewValidationErrorWithCause(
			"VAL_002", 
			"incorrect CIDR notation", 
			fmt.Sprintf("provided CIDR: %s", cidr), 
			err,
		)
	}

	return nil
}

// ValidateClusterName validates the correctness of the cluster name
func (v *ValidatorImpl) ValidateClusterName(name string) error {
	if strings.TrimSpace(name) == "" {
		return NewValidationError(
			"VAL_003", 
			"cluster name cannot be empty", 
			"cluster name field is required",
		)
	}

	// Check that the name contains only valid characters
	validName := regexp.MustCompile(`^[a-z0-9-]+$`)
	if !validName.MatchString(name) {
		return NewValidationError(
			"VAL_004", 
			"имя кластера может содержать только строчные буквы, цифры и дефисы", 
			fmt.Sprintf("предоставленное имя: %s", name),
		)
	}

	// Check the name length
	if len(name) > 50 {
		return NewValidationError(
			"VAL_005", 
			"имя кластера не должно превышать 50 символов", 
			fmt.Sprintf("текущая длина: %d", len(name)),
		)
	}

	return nil
}

// ValidateHostname validates the correctness of the hostname
func (v *ValidatorImpl) ValidateHostname(hostname string) error {
	if strings.TrimSpace(hostname) == "" {
		return NewValidationError(
			"VAL_006", 
			"hostname cannot be empty", 
			"hostname field is required",
		)
	}

	// Check that hostname complies with RFC standard
	validHostname := regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?)*$`)
	if !validHostname.MatchString(hostname) {
		return NewValidationError(
			"VAL_007", 
			"incorrect hostname", 
			fmt.Sprintf("provided hostname: %s", hostname),
		)
	}

	return nil
}

// ValidateRequiredField checks that the required field is not empty
func (v *ValidatorImpl) ValidateRequiredField(value, fieldName string) error {
	if strings.TrimSpace(value) == "" {
		return NewValidationError(
			"VAL_008", 
			fmt.Sprintf("field '%s' is required", fieldName), 
			"field value should not be empty",
		)
	}
	return nil
}

// ValidateIP validates the correctness of the IP address
func (v *ValidatorImpl) ValidateIP(ip string) error {
	if strings.TrimSpace(ip) == "" {
		return NewValidationError(
			"VAL_009", 
			"IP address cannot be empty", 
			"IP address field is required",
		)
	}

	if parsedIP := net.ParseIP(ip); parsedIP == nil {
		return NewValidationError(
			"VAL_010", 
			"incorrect IP address", 
			fmt.Sprintf("provided IP: %s", ip),
		)
	}

	return nil
}

// ValidateVIP validates the correctness of the virtual IP address
func (v *ValidatorImpl) ValidateVIP(vip string) error {
	if strings.TrimSpace(vip) == "" {
		// VIP is optional, empty string is allowed
		return nil
	}

	return v.ValidateIP(vip)
}

// ValidateDNSservers validates the correctness of the DNS servers list
func (v *ValidatorImpl) ValidateDNSservers(dns string) error {
	if strings.TrimSpace(dns) == "" {
		return NewValidationError(
			"VAL_011", 
			"DNS servers cannot be empty", 
			"at least one DNS server must be specified",
		)
	}

	// Split the DNS servers list
	dnsServers := strings.Split(dns, ",")

	var invalidServers []string
	for _, server := range dnsServers {
		server = strings.TrimSpace(server)
		if server == "" {
			continue
		}

		// Check each DNS server
		if err := v.ValidateIP(server); err != nil {
			invalidServers = append(invalidServers, server)
		}
	}

	if len(invalidServers) > 0 {
		return NewValidationError(
			"VAL_012", 
			"incorrect DNS servers found", 
			fmt.Sprintf("incorrect servers: %v", invalidServers),
		)
	}

	return nil
}

// ValidateNetworkConfig validates the correctness of the network configuration
func (v *ValidatorImpl) ValidateNetworkConfig(addresses, gateway, dnsServers string) error {
	// Check addresses
	if err := v.ValidateRequiredField(addresses, "Addresses"); err != nil {
		return err
	}

	// Check gateway
	if err := v.ValidateRequiredField(gateway, "Gateway"); err != nil {
		return err
	}

	// Check DNS servers
	if err := v.ValidateDNSservers(dnsServers); err != nil {
		return err
	}

	return nil
}

// ValidateNodeType validates the correctness of the node type
func (v *ValidatorImpl) ValidateNodeType(nodeType string) error {
	validTypes := []string{"controlplane", "worker", "control-plane"}

	for _, validType := range validTypes {
		if nodeType == validType {
			return nil
		}
	}

	return NewValidationError(
		"VAL_013", 
		"incorrect node type", 
		fmt.Sprintf("type: %s, valid values: %v", nodeType, validTypes),
	)
}

// ValidatePreset validates the correctness of the preset
func (v *ValidatorImpl) ValidatePreset(preset string) error {
	validPresets := []string{"generic", "cozystack"}

	for _, validPreset := range validPresets {
		if preset == validPreset {
			return nil
		}
	}

	return NewValidationError(
		"VAL_014", 
		"incorrect preset", 
		fmt.Sprintf("preset: %s, valid values: %v", preset, validPresets),
	)
}

// ValidateAPIServerURL validates the correctness of the API server URL
func (v *ValidatorImpl) ValidateAPIServerURL(url string) error {
	if strings.TrimSpace(url) == "" {
		return NewValidationError(
			"VAL_015", 
			"API server URL cannot be empty", 
			"cluster API server URL must be specified",
		)
	}

	// Check the basic URL format
	if !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "http://") {
		return NewValidationError(
			"VAL_016", 
			"API server URL must start with http:// or https://", 
			fmt.Sprintf("provided URL: %s", url),
		)
	}

	// Check that the URL contains a port
	if !strings.Contains(url, ":") {
		return NewValidationError(
			"VAL_017", 
			"API server URL must contain a port (e.g., :6443)", 
			fmt.Sprintf("provided URL: %s", url),
		)
	}

	return nil
}