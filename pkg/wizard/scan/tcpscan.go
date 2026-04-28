package scan

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"sync"
	"time"
)

const dialTimeout = 2 * time.Second

// scanTCPPort scans all hosts in the given CIDR for an open TCP port.
// Returns a list of IPs that accepted the connection.
//
// Uses a fixed worker pool of maxWorkers goroutines reading from a jobs
// channel — goroutine count stays bounded regardless of the input range.
// A goroutine-per-host approach would spike to thousands for /16 inputs.
func scanTCPPort(ctx context.Context, cidr string, port int, maxWorkers int) ([]string, error) {
	hosts, err := enumerateHosts(cidr)
	if err != nil {
		return nil, fmt.Errorf("failed to enumerate hosts: %w", err)
	}
	if maxWorkers < 1 {
		return nil, fmt.Errorf("maxWorkers must be >= 1, got %d", maxWorkers)
	}

	var (
		mu      sync.Mutex
		results []string
		jobs    = make(chan string)
		wg      sync.WaitGroup
	)

	worker := func() {
		defer wg.Done()
		dialer := net.Dialer{Timeout: dialTimeout}
		for ip := range jobs {
			addr := net.JoinHostPort(ip, fmt.Sprintf("%d", port))
			conn, err := dialer.DialContext(ctx, "tcp", addr)
			if err != nil {
				continue
			}
			_ = conn.Close()

			mu.Lock()
			results = append(results, ip)
			mu.Unlock()
		}
	}

	for range maxWorkers {
		wg.Add(1)
		go worker()
	}

feed:
	for _, host := range hosts {
		select {
		case jobs <- host.String():
		case <-ctx.Done():
			break feed
		}
	}
	close(jobs)
	wg.Wait()

	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	return results, nil
}

// enumerateHosts expands a CIDR notation to a list of usable host IPs.
// It skips the network and broadcast addresses for subnets larger than /31.
func enumerateHosts(cidr string) ([]net.IP, error) {
	_, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, err
	}

	ones, bits := ipNet.Mask.Size()
	if bits != 32 {
		return nil, fmt.Errorf("only IPv4 CIDR is supported, got /%d bits", bits)
	}

	// Reject unreasonably large scans (>/16 = 65534 hosts)
	if ones < 16 {
		return nil, fmt.Errorf("CIDR range /%d is too large (max /%d), would scan %d hosts", ones, 16, 1<<(32-ones))
	}

	// /32 — single host
	if ones == 32 {
		return []net.IP{ipNet.IP.To4()}, nil
	}

	// /31 — point-to-point, both addresses are usable (RFC 3021)
	if ones == 31 {
		start := ipToUint32(ipNet.IP.To4())
		return []net.IP{uint32ToIP(start), uint32ToIP(start + 1)}, nil
	}

	// For /30 and larger: enumerate usable hosts (skip network and broadcast addresses).
	// totalHosts includes network + broadcast, so usable = totalHosts - 2.
	// start = network + 1 (first usable), end = network + totalHosts - 2 (last usable).
	totalHosts := uint32(1) << (32 - ones)
	start := ipToUint32(ipNet.IP.To4()) + 1
	end := start + totalHosts - 3 // -1 (inclusive range) -1 (skip broadcast) -1 (start already +1)

	hosts := make([]net.IP, 0, end-start+1)
	for i := start; i <= end; i++ {
		hosts = append(hosts, uint32ToIP(i))
	}

	return hosts, nil
}

func ipToUint32(ip net.IP) uint32 {
	return binary.BigEndian.Uint32(ip)
}

func uint32ToIP(n uint32) net.IP {
	ip := make(net.IP, 4)
	binary.BigEndian.PutUint32(ip, n)
	return ip
}
