package wizard

import (
	"strings"
	"testing"
)

func TestValidateClusterName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid simple", "my-cluster", false},
		{"valid with numbers", "cluster-01", false},
		{"valid single word", "test", false},
		{"empty", "", true},
		{"uppercase", "MyCluster", true},
		{"starts with dash", "-cluster", true},
		{"ends with dash", "cluster-", true},
		{"contains underscore", "my_cluster", true},
		{"contains space", "my cluster", true},
		{"contains dot", "my.cluster", true},
		{"too long", strings.Repeat("a", 64), true},
		{"max valid length", strings.Repeat("a", 63), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateClusterName(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateClusterName(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestValidateHostname(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid", "node-01", false},
		{"valid short", "n", false},
		{"empty", "", true},
		{"uppercase allowed", "Node01", false},
		{"starts with dash", "-node", true},
		{"ends with dash", "node-", true},
		{"contains space", "my node", true},
		{"too long", strings.Repeat("a", 64), true},
		{"max valid length", strings.Repeat("a", 63), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateHostname(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateHostname(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestValidateCIDR(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid /24", "192.168.1.0/24", false},
		{"valid /16", "10.0.0.0/16", false},
		{"valid /32", "10.0.0.1/32", false},
		{"empty", "", true},
		{"no mask", "192.168.1.0", true},
		{"invalid ip", "999.999.999.999/24", true},
		{"invalid mask", "192.168.1.0/33", true},
		{"garbage", "not-a-cidr", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateCIDR(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateCIDR(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestValidateEndpoint(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid https with port", "https://192.168.0.1:6443", false},
		{"valid https with hostname", "https://api.example.com:6443", false},
		{"empty", "", true},
		{"no scheme", "192.168.0.1:6443", true},
		{"http scheme", "http://192.168.0.1:6443", true},
		{"no port", "https://192.168.0.1", true},
		{"garbage", "not-a-url", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateEndpoint(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateEndpoint(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestValidateIP(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid ipv4", "192.168.1.1", false},
		{"valid ipv6", "::1", false},
		{"valid ipv6 full", "2001:db8::1", false},
		{"empty", "", true},
		{"invalid", "not-an-ip", true},
		{"cidr notation", "192.168.1.0/24", true},
		{"out of range", "256.1.1.1", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateIP(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateIP(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestValidateNodeRole(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"controlplane", "controlplane", false},
		{"worker", "worker", false},
		{"empty", "", true},
		{"master", "master", true},
		{"uppercase", "Controlplane", true},
		{"unknown", "other", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateNodeRole(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateNodeRole(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}
