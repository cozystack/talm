// Copyright Cozystack Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package applycheck_test

import (
	"strings"
	"testing"

	"github.com/cozystack/talm/pkg/applycheck"
)

func TestValidateRefs_LinkMissing_EmitsBlockerWithAvailableList(t *testing.T) {
	t.Parallel()

	refs := []applycheck.Ref{{
		Kind:   applycheck.RefKindLink,
		Name:   "eth9999",
		Source: "machine.network.interfaces[0].interface",
	}}
	snapshot := applycheck.HostSnapshot{
		Links: []string{"eth0", "eth1"},
	}

	findings := applycheck.ValidateRefs(refs, snapshot)
	if len(findings) != 1 {
		t.Fatalf("ValidateRefs returned %d findings, want 1: %+v", len(findings), findings)
	}

	f := findings[0]
	if !f.IsBlocker() {
		t.Errorf("missing link should be a blocker, got severity %v", f.Severity)
	}

	if !strings.Contains(f.Reason, "eth9999") {
		t.Errorf("reason should cite the offending name, got %q", f.Reason)
	}

	if !strings.Contains(f.Hint, "eth0") || !strings.Contains(f.Hint, "eth1") {
		t.Errorf("hint should list available links, got %q", f.Hint)
	}
}

// TestValidateRefs_LinkMissing_HintTruncatedAtTopN pins the hint
// length budget. A cozystack host can surface dozens of link names
// (bonds, VLANs, bridges, lo, eth*) — a flat join produces 1000+ char
// hints that drown the actual blocker line. The hint must show the
// first 10 names alphabetically and collapse the tail into "... and
// N more"; operators can re-run `talm get links` if they need the
// full list.
func TestValidateRefs_LinkMissing_HintTruncatedAtTopN(t *testing.T) {
	t.Parallel()

	links := make([]string, 0, 30)
	for i := range 30 {
		links = append(links, "eth"+itoa(i))
	}

	refs := []applycheck.Ref{{Kind: applycheck.RefKindLink, Name: "eth9999"}}
	snapshot := applycheck.HostSnapshot{Links: links}

	findings := applycheck.ValidateRefs(refs, snapshot)
	if len(findings) != 1 {
		t.Fatalf("expected one blocker, got %+v", findings)
	}

	hint := findings[0].Hint
	if !strings.Contains(hint, "... and 20 more") {
		t.Errorf("hint should collapse the tail into '... and N more', got %q", hint)
	}

	// First 10 in alphabetical sort: eth0, eth1, eth10..eth17.
	// The tail (eth18..eth9, eth20..eth29) must NOT appear inline.
	if strings.Contains(hint, "eth29") {
		t.Errorf("hint should not list eth29 (beyond top-10), got %q", hint)
	}

	if !strings.Contains(hint, "eth0") {
		t.Errorf("hint should list eth0 (first alphabetically), got %q", hint)
	}
}

// TestValidateRefs_DiskSelectorMismatch_HintTruncated pins the same
// budget for the disk-summary path: virtual devices are already
// filtered, but a host with many real disks (storage nodes, RAID
// hosts) still produces unwieldy hints. Cap at 10 disks + "... and N
// more".
func TestValidateRefs_DiskSelectorMismatch_HintTruncated(t *testing.T) {
	t.Parallel()

	disks := make([]applycheck.DiskInfo, 0, 25)
	for i := range 25 {
		disks = append(disks, applycheck.DiskInfo{
			DevPath: "/dev/sd" + string(rune('a'+i%26)) + itoa(i/26),
			Model:   "FakeModel",
			BusPath: "/pci0000:00",
		})
	}

	refs := []applycheck.Ref{{
		Kind:     applycheck.RefKindDiskSelector,
		Name:     "{model: NoSuchModel}",
		Selector: applycheck.DiskSelector{Model: "NoSuchModel"},
	}}
	snapshot := applycheck.HostSnapshot{Disks: disks}

	findings := applycheck.ValidateRefs(refs, snapshot)
	if len(findings) != 1 {
		t.Fatalf("expected one blocker, got %+v", findings)
	}

	hint := findings[0].Hint
	if !strings.Contains(hint, "... and 15 more") {
		t.Errorf("disk hint should collapse the tail, got %q", hint)
	}
}

// itoa is a tiny helper to avoid pulling strconv into the test file
// for a single integer formatting site.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}

	var buf [20]byte

	i := len(buf)

	neg := n < 0
	if neg {
		n = -n
	}

	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}

	if neg {
		i--
		buf[i] = '-'
	}

	return string(buf[i:])
}

func TestValidateRefs_LinkPresent_NoFinding(t *testing.T) {
	t.Parallel()

	refs := []applycheck.Ref{{Kind: applycheck.RefKindLink, Name: "eth0"}}
	snapshot := applycheck.HostSnapshot{Links: []string{"eth0", "eth1"}}

	findings := applycheck.ValidateRefs(refs, snapshot)
	if len(findings) != 0 {
		t.Errorf("ValidateRefs returned %d findings, want 0: %+v", len(findings), findings)
	}
}

func TestValidateRefs_DiskLiteralMissing_EmitsBlocker(t *testing.T) {
	t.Parallel()

	refs := []applycheck.Ref{{Kind: applycheck.RefKindDiskLiteral, Name: "/dev/sdz"}}
	snapshot := applycheck.HostSnapshot{
		Disks: []applycheck.DiskInfo{
			{DevPath: "/dev/sda", Model: "Samsung 980 Pro"},
			{DevPath: "/dev/nvme0n1"},
		},
	}

	findings := applycheck.ValidateRefs(refs, snapshot)
	if len(findings) != 1 || !findings[0].IsBlocker() {
		t.Fatalf("expected one blocker, got %+v", findings)
	}

	if !strings.Contains(findings[0].Hint, "/dev/sda") {
		t.Errorf("hint should list available disk paths, got %q", findings[0].Hint)
	}
}

// TestValidateRefs_DiskLiteralByID_AcceptedViaSymlink pins the
// contract: machine.install.disk accepts the by-id /
// by-path / by-diskseq stable forms Talos exposes via Disk.Symlinks.
// These are the recommended forms for stable boot ordering, so the
// gate must not block them — verified against a live Talos node
// where /dev/sda surfaces /dev/disk/by-id/wwn-0x602742… as a symlink.
func TestValidateRefs_DiskLiteralByID_AcceptedViaSymlink(t *testing.T) {
	t.Parallel()

	refs := []applycheck.Ref{{
		Kind: applycheck.RefKindDiskLiteral,
		Name: "/dev/disk/by-id/wwn-0x602742ce4e9046729ae81c05166f4d8e",
	}}
	snapshot := applycheck.HostSnapshot{
		Disks: []applycheck.DiskInfo{
			{
				DevPath: "/dev/sda",
				WWID:    "naa.602742ce4e9046729ae81c05166f4d8e",
				Symlinks: []string{
					"/dev/disk/by-diskseq/23",
					"/dev/disk/by-id/scsi-3602742ce4e9046729ae81c05166f4d8e",
					"/dev/disk/by-id/wwn-0x602742ce4e9046729ae81c05166f4d8e",
					"/dev/disk/by-path/pci-0000:00:04.0-scsi-0:0:0:1",
				},
			},
		},
	}

	findings := applycheck.ValidateRefs(refs, snapshot)
	if len(findings) != 0 {
		t.Errorf("by-id symlink should resolve to /dev/sda; gate must not block, got findings=%+v", findings)
	}
}

func TestValidateRefs_SelectorZeroMatches_Blocker(t *testing.T) {
	t.Parallel()

	refs := []applycheck.Ref{{
		Kind:     applycheck.RefKindDiskSelector,
		Selector: applycheck.DiskSelector{Model: "Samsumg*"}, // typo on purpose
	}}
	snapshot := applycheck.HostSnapshot{
		Disks: []applycheck.DiskInfo{
			{DevPath: "/dev/sda", Model: "Samsung 980 Pro"},
		},
	}

	findings := applycheck.ValidateRefs(refs, snapshot)
	if len(findings) != 1 || !findings[0].IsBlocker() {
		t.Fatalf("expected one blocker on zero matches, got %+v", findings)
	}
}

func TestValidateRefs_SelectorOneMatch_NoFinding(t *testing.T) {
	t.Parallel()

	refs := []applycheck.Ref{{
		Kind:     applycheck.RefKindDiskSelector,
		Selector: applycheck.DiskSelector{Model: "Samsung*"},
	}}
	snapshot := applycheck.HostSnapshot{
		Disks: []applycheck.DiskInfo{
			{DevPath: "/dev/sda", Model: "Samsung 980 Pro"},
			{DevPath: "/dev/nvme0n1", Model: "WD Black"},
		},
	}

	findings := applycheck.ValidateRefs(refs, snapshot)
	if len(findings) != 0 {
		t.Errorf("expected no findings on single match, got %+v", findings)
	}
}

func TestValidateRefs_SelectorMultipleMatches_Warning(t *testing.T) {
	t.Parallel()

	refs := []applycheck.Ref{{
		Kind:     applycheck.RefKindDiskSelector,
		Selector: applycheck.DiskSelector{Type: "ssd"},
	}}
	// type: ssd is derived from Rotational=false (see predicateType).
	snapshot := applycheck.HostSnapshot{
		Disks: []applycheck.DiskInfo{
			{DevPath: "/dev/sda", Transport: "sata", Rotational: false},
			{DevPath: "/dev/sdb", Transport: "nvme", Rotational: false},
		},
	}

	findings := applycheck.ValidateRefs(refs, snapshot)
	if len(findings) != 1 || findings[0].Severity != applycheck.SeverityWarning {
		t.Fatalf("expected one warning on multiple matches, got %+v", findings)
	}
}

// TestValidateRefs_TypeSelector_TalosSemantics pins the (Transport,
// Rotational) -> type-enum mapping the gate inherits from Talos's own
// install-disk resolution. Without this mapping, a perfectly valid
// `diskSelector: { type: ssd }` against a SATA SSD would block.
func TestValidateRefs_TypeSelector_TalosSemantics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		selector    applycheck.DiskSelector
		disk        applycheck.DiskInfo
		wantMatches bool
	}{
		{
			name:        "type ssd matches sata rotational=false",
			selector:    applycheck.DiskSelector{Type: "ssd"},
			disk:        applycheck.DiskInfo{DevPath: "/dev/sda", Transport: "sata", Rotational: false},
			wantMatches: true,
		},
		{
			name:        "type ssd rejects sata rotational=true",
			selector:    applycheck.DiskSelector{Type: "ssd"},
			disk:        applycheck.DiskInfo{DevPath: "/dev/sda", Transport: "sata", Rotational: true},
			wantMatches: false,
		},
		{
			name:        "type hdd matches scsi rotational=true",
			selector:    applycheck.DiskSelector{Type: "hdd"},
			disk:        applycheck.DiskInfo{DevPath: "/dev/sda", Transport: "scsi", Rotational: true},
			wantMatches: true,
		},
		{
			name:        "type nvme matches nvme transport",
			selector:    applycheck.DiskSelector{Type: "nvme"},
			disk:        applycheck.DiskInfo{DevPath: "/dev/nvme0n1", Transport: "nvme", Rotational: false},
			wantMatches: true,
		},
		{
			name:        "type sd matches mmc transport",
			selector:    applycheck.DiskSelector{Type: "sd"},
			disk:        applycheck.DiskInfo{DevPath: "/dev/mmcblk0", Transport: "mmc"},
			wantMatches: true,
		},
		{
			name:        "type SSD case-insensitive",
			selector:    applycheck.DiskSelector{Type: "SSD"},
			disk:        applycheck.DiskInfo{DevPath: "/dev/sda", Transport: "sata", Rotational: false},
			wantMatches: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			refs := []applycheck.Ref{{Kind: applycheck.RefKindDiskSelector, Selector: tc.selector}}
			snapshot := applycheck.HostSnapshot{Disks: []applycheck.DiskInfo{tc.disk}}

			findings := applycheck.ValidateRefs(refs, snapshot)

			matched := len(findings) == 0
			if matched != tc.wantMatches {
				t.Errorf("matched=%v want=%v findings=%+v", matched, tc.wantMatches, findings)
			}
		})
	}
}

// TestValidateRefs_ReadonlyAndCDROMExcluded mirrors Talos's
// install-disk resolution, which never picks readonly or cdrom devices.
// The gate must exclude them too — otherwise a node with a CD-ROM
// surfaces a spurious "multiple matches" warning on every selector.
func TestValidateRefs_ReadonlyAndCDROMExcluded(t *testing.T) {
	t.Parallel()

	refs := []applycheck.Ref{{
		Kind:     applycheck.RefKindDiskSelector,
		Selector: applycheck.DiskSelector{Type: "ssd"},
	}}
	snapshot := applycheck.HostSnapshot{
		Disks: []applycheck.DiskInfo{
			{DevPath: "/dev/sda", Transport: "sata", Rotational: false},
			{DevPath: "/dev/sr0", Transport: "sata", Rotational: false, CDROM: true},
			{DevPath: "/dev/sdz", Transport: "sata", Rotational: false, Readonly: true},
		},
	}

	findings := applycheck.ValidateRefs(refs, snapshot)
	if len(findings) != 0 {
		t.Errorf("expected one clean match (sda); cdrom + readonly disks must be excluded, got findings=%+v", findings)
	}
}

// TestValidateRefs_VirtualDisksExcluded pins the exclusion of kernel-
// virtual block devices (loop, dm, drbd, ram). Talos block.DiskSpec
// reports BusPath="/virtual" for those; the gate must skip them so a
// selector like `type: ssd` on a cozystack host (which hosts many
// loop/dm/drbd devices for DRBD-replicated PVs) doesn't surface a
// spurious "multiple matches" warning across every install. Verified
// on a live OCI cluster: real disks (sda, sdb) have PCI bus paths,
// virtual disks (dm-*, drbd*, loop*) carry "/virtual".
func TestValidateRefs_VirtualDisksExcluded(t *testing.T) {
	t.Parallel()

	refs := []applycheck.Ref{{
		Kind:     applycheck.RefKindDiskSelector,
		Selector: applycheck.DiskSelector{Type: "ssd"},
	}}
	snapshot := applycheck.HostSnapshot{
		Disks: []applycheck.DiskInfo{
			{DevPath: "/dev/sda", BusPath: "/pci0000:00/0000:00:04.0", Transport: "virtio", Rotational: false},
			{DevPath: "/dev/dm-0", BusPath: "/virtual", Rotational: false},
			{DevPath: "/dev/drbd1000", BusPath: "/virtual", Rotational: false},
			{DevPath: "/dev/loop0", BusPath: "/virtual", Rotational: false},
		},
	}

	findings := applycheck.ValidateRefs(refs, snapshot)
	if len(findings) != 0 {
		t.Errorf("expected one clean match (sda); virtual disks must be excluded, got findings=%+v", findings)
	}
}

// TestValidateRefs_DiskLiteralHint_OmitsVirtual pins the hint output
// scope: when a literal disk path doesn't resolve, the operator-facing
// hint must list real block devices only, not virtual noise.
func TestValidateRefs_DiskLiteralHint_OmitsVirtual(t *testing.T) {
	t.Parallel()

	refs := []applycheck.Ref{{
		Kind: applycheck.RefKindDiskLiteral,
		Name: "/dev/sdz",
	}}
	snapshot := applycheck.HostSnapshot{
		Disks: []applycheck.DiskInfo{
			{DevPath: "/dev/sda", BusPath: "/pci0000:00/0000:00:04.0"},
			{DevPath: "/dev/dm-0", BusPath: "/virtual"},
			{DevPath: "/dev/loop0", BusPath: "/virtual"},
		},
	}

	findings := applycheck.ValidateRefs(refs, snapshot)
	if len(findings) != 1 {
		t.Fatalf("expected one blocker, got %+v", findings)
	}

	hint := findings[0].Hint
	if strings.Contains(hint, "/dev/dm-0") || strings.Contains(hint, "/dev/loop0") {
		t.Errorf("hint should omit virtual devices, got %q", hint)
	}

	if !strings.Contains(hint, "/dev/sda") {
		t.Errorf("hint should list real disks, got %q", hint)
	}
}

func TestValidateRefs_SelectorBySize_GreaterThanOrEqual(t *testing.T) {
	t.Parallel()

	refs := []applycheck.Ref{{
		Kind:     applycheck.RefKindDiskSelector,
		Selector: applycheck.DiskSelector{Size: ">= 500GB"},
	}}
	snapshot := applycheck.HostSnapshot{
		Disks: []applycheck.DiskInfo{
			{DevPath: "/dev/sda", Size: 250_000_000_000}, // 250GB — below cut
			{DevPath: "/dev/sdb", Size: 500_000_000_000}, // 500GB — at cut
			{DevPath: "/dev/sdc", Size: 1_000_000_000_000},
		},
	}

	findings := applycheck.ValidateRefs(refs, snapshot)
	if len(findings) != 1 || findings[0].Severity != applycheck.SeverityWarning {
		t.Fatalf("expected one warning (two disks above 500GB), got %+v", findings)
	}
}

// TestValidateRefs_SelectorBySize_HumanizedUnits pins the parser
// against humanize.ParseBytes — lowercase units, mixed case, optional
// spaces, IEC binary, and SI decimal all work. Without this the gate
// would block `size: ">= 100gb"` selectors that Talos parses cleanly.
func TestValidateRefs_SelectorBySize_HumanizedUnits(t *testing.T) {
	t.Parallel()

	disks := []applycheck.DiskInfo{
		{DevPath: "/dev/sda", Size: 100_000_000_000}, // 100 GB SI
		{DevPath: "/dev/sdb", Size: 99_000_000_000},
	}

	tests := []struct {
		name        string
		size        string
		wantMatches int
	}{
		{"lowercase gb", ">= 100gb", 1},
		{"lowercase gib", ">= 1gib", 2},
		{"mixed case MiB", "<= 200000MiB", 2},
		{"spaced unit", ">= 100 GB", 1},
		{"bare numeric bytes", ">= 100000000000", 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			refs := []applycheck.Ref{{
				Kind:     applycheck.RefKindDiskSelector,
				Selector: applycheck.DiskSelector{Size: tc.size},
			}}
			findings := applycheck.ValidateRefs(refs, applycheck.HostSnapshot{Disks: disks})

			// 0 matches -> 1 blocker; 1 match -> 0 findings; >1 -> 1 warning.
			var actualMatches int

			switch {
			case len(findings) == 0:
				actualMatches = 1
			case findings[0].Severity == applycheck.SeverityBlocker:
				actualMatches = 0
			default:
				actualMatches = 2
			}

			if actualMatches != tc.wantMatches {
				t.Errorf("size=%q matched %d disks, want %d (findings=%+v)", tc.size, actualMatches, tc.wantMatches, findings)
			}
		})
	}
}

func TestValidateRefs_SelectorBySerial_ExactMatch(t *testing.T) {
	t.Parallel()

	refs := []applycheck.Ref{{
		Kind:     applycheck.RefKindDiskSelector,
		Selector: applycheck.DiskSelector{Serial: "S649NJ0R602345"},
	}}
	snapshot := applycheck.HostSnapshot{
		Disks: []applycheck.DiskInfo{
			{DevPath: "/dev/sda", Serial: "S649NJ0R602345"},
			{DevPath: "/dev/sdb", Serial: "S649NJ0R602346"},
		},
	}

	findings := applycheck.ValidateRefs(refs, snapshot)
	if len(findings) != 0 {
		t.Errorf("exact serial should match exactly one disk, got %+v", findings)
	}
}

func TestValidateRefs_MultipleProblems_AllSurfacedInOnePass(t *testing.T) {
	t.Parallel()

	refs := []applycheck.Ref{
		{Kind: applycheck.RefKindLink, Name: "eth9999"},
		{Kind: applycheck.RefKindLink, Name: "br0"},
		{Kind: applycheck.RefKindDiskLiteral, Name: "/dev/sdz"},
		{Kind: applycheck.RefKindDiskSelector, Selector: applycheck.DiskSelector{Model: "Nope*"}},
	}
	snapshot := applycheck.HostSnapshot{
		Links: []string{"eth0", "eth1", "br0"},
		Disks: []applycheck.DiskInfo{{DevPath: "/dev/sda", Model: "Samsung"}},
	}

	findings := applycheck.ValidateRefs(refs, snapshot)
	if len(findings) != 3 {
		t.Errorf("expected 3 findings (1 missing link, 1 missing disk literal, 1 zero-match selector), got %d: %+v", len(findings), findings)
	}
}

func TestValidateRefs_EmptyRefs_NoFindings(t *testing.T) {
	t.Parallel()

	findings := applycheck.ValidateRefs(nil, applycheck.HostSnapshot{Links: []string{"eth0"}})
	if len(findings) != 0 {
		t.Errorf("ValidateRefs(nil) returned %d findings, want 0", len(findings))
	}
}
