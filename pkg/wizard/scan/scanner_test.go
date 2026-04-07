package scan

import (
	"testing"
)

func TestNew(t *testing.T) {
	s := New()
	if s.Port != defaultTalosPort {
		t.Errorf("Port = %d, want %d", s.Port, defaultTalosPort)
	}
	if s.Timeout != defaultTimeout {
		t.Errorf("Timeout = %v, want %v", s.Timeout, defaultTimeout)
	}
}

// Note: ScanNetwork and GetNodeInfo require real Talos nodes or network
// access, so they are tested via integration tests only.
// Unit tests for the underlying components are in:
//   - tcpscan_test.go (TCP port scanning, CIDR expansion)
//   - extract_test.go (gRPC response parsing)
