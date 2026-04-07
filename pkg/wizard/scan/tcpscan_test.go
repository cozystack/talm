package scan

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestEnumerateHosts(t *testing.T) {
	tests := []struct {
		name     string
		cidr     string
		expected int
		wantErr  bool
	}{
		{"slash 30", "10.0.0.0/30", 2, false},
		{"slash 32", "10.0.0.1/32", 1, false},
		{"slash 31", "10.0.0.0/31", 2, false},
		{"slash 24", "192.168.1.0/24", 254, false},
		{"invalid cidr", "not-a-cidr", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hosts, err := enumerateHosts(tt.cidr)
			if (err != nil) != tt.wantErr {
				t.Errorf("enumerateHosts(%q) error = %v, wantErr %v", tt.cidr, err, tt.wantErr)
				return
			}
			if len(hosts) != tt.expected {
				t.Errorf("enumerateHosts(%q) returned %d hosts, want %d", tt.cidr, len(hosts), tt.expected)
			}
		})
	}
}

func TestEnumerateHosts_SkipsNetworkAndBroadcast(t *testing.T) {
	hosts, err := enumerateHosts("10.0.0.0/30")
	if err != nil {
		t.Fatal(err)
	}

	for _, h := range hosts {
		ip := h.String()
		if ip == "10.0.0.0" {
			t.Error("should not include network address 10.0.0.0")
		}
		if ip == "10.0.0.3" {
			t.Error("should not include broadcast address 10.0.0.3")
		}
	}
}

func TestScanTCPPort_FindsOpenPort(t *testing.T) {
	// Start a real TCP listener
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()

	port := listener.Addr().(*net.TCPAddr).Port

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ips, err := scanTCPPort(ctx, "127.0.0.1/32", port, 1)
	if err != nil {
		t.Fatalf("scanTCPPort() error = %v", err)
	}

	if len(ips) != 1 || ips[0] != "127.0.0.1" {
		t.Errorf("scanTCPPort() = %v, want [127.0.0.1]", ips)
	}
}

func TestScanTCPPort_NoOpenPort(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Use a port that's very unlikely to be open
	ips, err := scanTCPPort(ctx, "127.0.0.1/32", 19999, 1)
	if err != nil {
		t.Fatalf("scanTCPPort() error = %v", err)
	}

	if len(ips) != 0 {
		t.Errorf("scanTCPPort() = %v, want empty", ips)
	}
}

func TestScanTCPPort_MultipleHosts(t *testing.T) {
	listener1, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener1.Close() }()

	port := listener1.Addr().(*net.TCPAddr).Port

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// /32 has only one host — verify concurrency doesn't break anything
	ips, err := scanTCPPort(ctx, "127.0.0.1/32", port, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ips) != 1 {
		t.Errorf("expected 1 IP, got %d", len(ips))
	}
}
