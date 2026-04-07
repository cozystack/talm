package scan

import (
	"context"
	"fmt"
	"os/exec"
	"sync"
	"time"

	"github.com/cozystack/talm/pkg/wizard"
)

const (
	defaultTalosPort  = 50000
	defaultTimeout    = 30 * time.Second
	maxConcurrentJobs = 10
)

// CommandRunner abstracts command execution for testability.
type CommandRunner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

// ExecRunner is the default CommandRunner that uses os/exec.
type ExecRunner struct{}

// Run executes a command and returns its combined output.
func (r *ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// NmapScanner discovers Talos nodes using nmap and collects info via talosctl.
type NmapScanner struct {
	TalosPort int
	Timeout   time.Duration
	Exec      CommandRunner
}

// New creates a scanner with default settings.
func New() *NmapScanner {
	return &NmapScanner{
		TalosPort: defaultTalosPort,
		Timeout:   defaultTimeout,
		Exec:      &ExecRunner{},
	}
}

// ScanNetwork discovers Talos nodes in the given CIDR range by running nmap
// and then querying each discovered node for hardware details.
func (s *NmapScanner) ScanNetwork(ctx context.Context, cidr string) ([]wizard.NodeInfo, error) {
	scanCtx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()

	port := s.TalosPort
	if port == 0 {
		port = defaultTalosPort
	}

	output, err := s.Exec.Run(scanCtx, "nmap",
		"-p", fmt.Sprintf("%d", port),
		"--open",
		"-oG", "-",
		cidr,
	)
	if err != nil {
		return nil, fmt.Errorf("nmap scan failed: %w", err)
	}

	ips := ParseNmapGrepOutput(string(output))
	if len(ips) == 0 {
		return nil, nil
	}

	return s.collectNodeInfo(ctx, ips)
}

// GetNodeInfo connects to a single Talos node and retrieves hardware information
// by running talosctl commands to collect hostname, disks, and network interfaces.
func (s *NmapScanner) GetNodeInfo(ctx context.Context, ip string) (wizard.NodeInfo, error) {
	node := wizard.NodeInfo{IP: ip}

	infoCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	baseArgs := []string{"--nodes", ip, "--insecure", "get"}

	// Collect hostname
	if output, err := s.Exec.Run(infoCtx, "talosctl", append(baseArgs, "hostname", "--output", "json")...); err == nil {
		if hostname, err := ParseHostname(output); err == nil && hostname != "" {
			node.Hostname = hostname
		}
	}

	// Collect disks
	if output, err := s.Exec.Run(infoCtx, "talosctl", append(baseArgs, "disks", "--output", "json")...); err == nil {
		if disks, err := ParseDisks(output); err == nil {
			node.Disks = disks
		}
	}

	// Collect network interfaces
	if output, err := s.Exec.Run(infoCtx, "talosctl", append(baseArgs, "links", "--output", "json")...); err == nil {
		if links, err := ParseLinks(output); err == nil {
			node.Interfaces = links
		}
	}

	return node, nil
}

// collectNodeInfo queries multiple nodes concurrently with bounded parallelism.
func (s *NmapScanner) collectNodeInfo(ctx context.Context, ips []string) ([]wizard.NodeInfo, error) {
	var (
		mu    sync.Mutex
		nodes []wizard.NodeInfo
		sem   = make(chan struct{}, maxConcurrentJobs)
		wg    sync.WaitGroup
	)

	for _, ip := range ips {
		wg.Add(1)
		go func(ip string) {
			defer wg.Done()

			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}

			node, err := s.GetNodeInfo(ctx, ip)
			if err != nil {
				node = wizard.NodeInfo{IP: ip}
			}

			mu.Lock()
			nodes = append(nodes, node)
			mu.Unlock()
		}(ip)
	}

	wg.Wait()
	return nodes, nil
}
