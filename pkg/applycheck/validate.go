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

package applycheck

// HostSnapshot captures the host-side resource inventory the validator
// compares declared refs against. Fields are populated from COSI
// `links` and `disks` reads at apply time; tests construct fakes
// directly.
type HostSnapshot struct {
	Links []string
	Disks []DiskInfo
}

// DiskInfo describes one host block device. Fields mirror Talos's
// block.Disk COSI resource shape verbatim — InstallDiskSelector.type
// (an enum: ssd/hdd/nvme/sd) is NOT a stored field but a derived
// predicate over Transport + Rotational, mirrored in matchSelector.
// CDROM and Readonly devices are excluded by Talos's install-disk
// resolution; matchSelector mirrors that exclusion.
//
// Symlinks captures the alternate path forms Talos exposes for the
// same device (/dev/disk/by-id/wwn-…, /dev/disk/by-path/pci-…,
// /dev/disk/by-diskseq/…); RefKindDiskLiteral validation accepts any
// of these as equivalent to DevPath, because by-id paths are the
// recommended stable form for `machine.install.disk` and an operator
// using them is doing the right thing.
type DiskInfo struct {
	DevPath    string // /dev/sda
	Model      string
	Serial     string
	WWID       string
	Modalias   string
	UUID       string
	Transport  string // sata / nvme / scsi / mmc / virtio / ...
	BusPath    string
	Size       uint64 // bytes
	Rotational bool
	Readonly   bool
	CDROM      bool
	Symlinks   []string
}

// Severity classifies a finding's blocker status.
type Severity int

const (
	// SeverityBlocker fails the apply.
	SeverityBlocker Severity = iota
	// SeverityWarning prints the finding but does not block.
	SeverityWarning
)

// Finding is one validation result for one Ref. ValidateRefs returns one
// Finding per Ref; clean refs produce no findings.
type Finding struct {
	Ref      Ref
	Severity Severity
	Reason   string
	Hint     string
}

// IsBlocker returns true when the finding fails the apply.
func (f *Finding) IsBlocker() bool {
	return f.Severity == SeverityBlocker
}

// ValidateRefs checks each Ref against the host snapshot:
//
//   - RefKindLink: name must match an entry in snapshot.Links exactly.
//     Mismatch is a blocker.
//   - RefKindDiskLiteral: name must equal a DiskInfo.DevPath in
//     snapshot.Disks. Mismatch is a blocker.
//   - RefKindDiskSelector: selector must match >= 1 disk. Zero matches is
//     a blocker. Multiple matches is a warning (install picks the first
//     match — operators usually want to narrow the selector).
//
// ValidateRefs collects every finding in a single pass; callers can show
// them all at once instead of one-at-a-time.
func ValidateRefs(refs []Ref, snapshot HostSnapshot) []Finding {
	if len(refs) == 0 {
		return nil
	}

	linkSet := make(map[string]struct{}, len(snapshot.Links))
	for _, name := range snapshot.Links {
		linkSet[name] = struct{}{}
	}

	// Disk-literal validation accepts DevPath (`/dev/sda`) and every
	// stable Symlink alternative (/dev/disk/by-id/wwn-…, by-path/…,
	// by-diskseq/…). The recommended Talos pattern is by-id, so the
	// gate must not reject by-id literals.
	diskPaths := make(map[string]struct{}, len(snapshot.Disks)*4) //nolint:mnd // upper-bound estimate for sym-count.
	for i := range snapshot.Disks {
		d := &snapshot.Disks[i]
		diskPaths[d.DevPath] = struct{}{}

		for _, sym := range d.Symlinks {
			diskPaths[sym] = struct{}{}
		}
	}

	var findings []Finding

	for i := range refs {
		ref := &refs[i]
		switch ref.Kind {
		case RefKindLink:
			findings = appendIfMissing(findings, ref, linkSet, snapshot.Links, "link")
		case RefKindDiskLiteral:
			findings = appendIfMissing(findings, ref, diskPaths, diskPathList(snapshot.Disks), "disk")
		case RefKindDiskSelector:
			findings = appendSelectorFinding(findings, ref, snapshot.Disks)
		}
	}

	return findings
}

// appendIfMissing appends a blocker finding when ref.Name isn't in present.
// available is the sorted-by-the-caller list shown to the operator so they
// can pick the right name without re-running discovery.
func appendIfMissing(findings []Finding, ref *Ref, present map[string]struct{}, available []string, kind string) []Finding {
	if _, ok := present[ref.Name]; ok {
		return findings
	}

	return append(findings, Finding{
		Ref:      *ref,
		Severity: SeverityBlocker,
		Reason:   "declared " + kind + " " + quote(ref.Name) + " not found on target node",
		Hint:     "available " + kind + "s: " + joinAvailable(available),
	})
}

// appendSelectorFinding emits a blocker on zero matches and a warning on
// multiple matches. A single match is clean (no finding).
func appendSelectorFinding(findings []Finding, ref *Ref, disks []DiskInfo) []Finding {
	matches := matchSelector(&ref.Selector, disks)

	switch {
	case len(matches) == 0:
		return append(findings, Finding{
			Ref:      *ref,
			Severity: SeverityBlocker,
			Reason:   "disk selector matches zero disks on target node",
			Hint:     "available disks: " + summarizeDisks(disks),
		})
	case len(matches) > 1:
		return append(findings, Finding{
			Ref:      *ref,
			Severity: SeverityWarning,
			Reason:   "disk selector matches multiple disks; install picks the first match",
			Hint:     "matched: " + summarizeDisks(matches),
		})
	}

	return findings
}
