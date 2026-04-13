package scan

import (
	"context"
	"fmt"
	"net"
	"runtime"
	"sync/atomic"
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

	closedPort, err := pickClosedPort(t)
	if err != nil {
		t.Fatal(err)
	}

	ips, err := scanTCPPort(ctx, "127.0.0.1/32", closedPort, 1)
	if err != nil {
		t.Fatalf("scanTCPPort() error = %v", err)
	}

	if len(ips) != 0 {
		t.Errorf("scanTCPPort() = %v, want empty", ips)
	}
}

// §11 — pickClosedPort returns a port that is *confirmed* to refuse connections.
// Picks ephemeral ports, closes them, probes with net.Dial to make sure no one
// raced in. Retries on collision.
func pickClosedPort(t *testing.T) (int, error) {
	t.Helper()
	for i := 0; i < 10; i++ {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return 0, err
		}
		port := l.Addr().(*net.TCPAddr).Port
		_ = l.Close()

		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond)
		if err != nil {
			return port, nil // port refused — good
		}
		_ = conn.Close()
	}
	return 0, fmt.Errorf("could not find a closed ephemeral port after 10 tries")
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

func TestEnumerateHosts_RejectsLargeCIDR(t *testing.T) {
	_, err := enumerateHosts("10.0.0.0/8")
	if err == nil {
		t.Fatal("expected error for /8 CIDR, got nil")
	}
}

func TestEnumerateHosts_AcceptsSlash16(t *testing.T) {
	hosts, err := enumerateHosts("10.0.0.0/16")
	if err != nil {
		t.Fatalf("expected /16 to be accepted, got error: %v", err)
	}
	if len(hosts) != 65534 {
		t.Errorf("expected 65534 hosts, got %d", len(hosts))
	}
}

// §10 — goroutine count must stay bounded by maxWorkers + small overhead,
// regardless of host count. Current goroutine-per-host implementation will
// spike to 1022 (for /22) and fail this test.
func TestScanTCPPort_BoundedGoroutines(t *testing.T) {
	baseGoroutines := runtime.NumGoroutine()

	// Use a sink listener so dials succeed/fail cleanly.
	// We don't care about results — only about runtime goroutine count.
	var peak atomic.Int64
	done := make(chan struct{})
	defer close(done)

	go func() {
		ticker := time.NewTicker(1 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				cur := int64(runtime.NumGoroutine())
				if cur > peak.Load() {
					peak.Store(cur)
				}
			}
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	// /22 = 1022 hosts. With goroutine-per-host, peak goroutines will spike
	// far above maxWorkers.
	maxWorkers := 10
	_, _ = scanTCPPort(ctx, "127.0.0.0/22", 59999, maxWorkers)

	// Allow: base + maxWorkers (dial workers) + small overhead (ticker, test runtime).
	budget := int64(baseGoroutines) + int64(maxWorkers) + 10
	if peak.Load() > budget {
		t.Errorf("goroutine peak %d exceeds budget %d (base=%d, maxWorkers=%d) — worker pool not bounded",
			peak.Load(), budget, baseGoroutines, maxWorkers)
	}
}
