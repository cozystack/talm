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

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dustin/go-humanize"
)

const (
	cmpGE    = ">="
	cmpLE    = "<="
	cmpEQ    = "=="
	cmpGT    = ">"
	cmpLT    = "<"
	noneText = "<none>"

	// maxHintItems caps the candidate list shown in selector / link
	// missing-ref hints. On cozystack hosts after virtual-filter the
	// real candidate count is small, but loose matchers (e.g. type:
	// ssd on a storage node) can still surface 20+ entries that drown
	// the actual finding line. Cap at 10 alphabetically; the tail
	// collapses into "... and N more" so the operator can scan
	// quickly and re-run discovery if they need the full set.
	maxHintItems = 10

	// transportNVMe is the value Talos's block.DiskSpec.Transport
	// takes for NVMe disks. Kept separate from selectorTypeNVMe
	// even though they're the same string today: a future Talos
	// rename of either side independently would otherwise silently
	// break only one of the two checks.
	transportNVMe = "nvme"
	// selectorTypeNVMe is the v1alpha1 InstallDiskSelector.type
	// enum value for NVMe disks (see predicateType).
	selectorTypeNVMe = "nvme"
)

// virtualBusPath is the BusPath value Talos assigns to kernel-virtual
// block devices (loop, dm, drbd, ram, ...). Real disks live under a
// PCI / virtio / etc. bus path; matchSelector excludes virtual devices
// from candidacy so a selector never silently lands on /dev/loop0.
const virtualBusPath = "/virtual"

// matchSelector returns every disk in candidates that satisfies every
// non-empty field of sel, excluding read-only, CD-ROM, and kernel-virtual
// devices the way Talos's install-disk resolution does. An empty
// selector matches every non-excluded candidate.
//
// Match semantics mirror Talos's InstallDiskSelector at the level the
// pre-apply gate cares about — model and busPath are shell-globs (Talos
// accepts globs in the YAML), size accepts comparators like ">= 1TB",
// serial/wwid/modalias/uuid/name are exact-match. The `type` field is
// not a stored disk attribute but a predicate over (Transport,
// Rotational), mirrored here from v1alpha1_provider.go:1325-1351.
func matchSelector(sel *DiskSelector, candidates []DiskInfo) []DiskInfo {
	var matches []DiskInfo

	for i := range candidates {
		disk := &candidates[i]

		if isExcludedDisk(disk) {
			continue
		}

		if diskMatches(sel, disk) {
			matches = append(matches, *disk)
		}
	}

	return matches
}

// isExcludedDisk reports whether a disk should never be a selector
// candidate. Readonly and CDROM are explicit Talos exclusions; virtual
// devices (dm-*, drbd*, loop*) carry bus_path "/virtual" and are
// inappropriate install / volume targets.
func isExcludedDisk(disk *DiskInfo) bool {
	return disk.Readonly || disk.CDROM || disk.BusPath == virtualBusPath
}

// diskPredicate is one rule the candidate disk has to satisfy. matchAll
// short-circuits on the first false.
type diskPredicate func(sel *DiskSelector, disk *DiskInfo) bool

//nolint:gochecknoglobals // dispatch table, static after init.
var diskPredicates = []diskPredicate{
	predicateGlob(func(s *DiskSelector) string { return s.Model }, func(d *DiskInfo) string { return d.Model }),
	predicateExact(func(s *DiskSelector) string { return s.Serial }, func(d *DiskInfo) string { return d.Serial }),
	predicateExact(func(s *DiskSelector) string { return s.WWID }, func(d *DiskInfo) string { return d.WWID }),
	predicateExact(func(s *DiskSelector) string { return s.Modalias }, func(d *DiskInfo) string { return d.Modalias }),
	predicateExact(func(s *DiskSelector) string { return s.UUID }, func(d *DiskInfo) string { return d.UUID }),
	predicateType,
	predicateGlob(func(s *DiskSelector) string { return s.BusPath }, func(d *DiskInfo) string { return d.BusPath }),
	predicateExact(func(s *DiskSelector) string { return s.Name }, func(d *DiskInfo) string { return d.DevPath }),
	predicateSize,
}

// predicateType mirrors Talos's v1alpha1 type-selector resolution
// (config/types/v1alpha1/v1alpha1_provider.go:1325-1351):
//
//	type: nvme -> Transport == "nvme"
//	type: sd   -> Transport == "mmc"
//	type: hdd  -> Rotational == true
//	type: ssd  -> Rotational == false
//
// An empty selector type is "don't care".
//
// Case handling intentionally diverges from Talos: predicateType
// lowercases sel.Type before the switch, whereas Talos returns
// `unsupported disk type "SSD"` for mixed-case input. Phase 1's job
// is to surface declared-resource mismatches at render time, NOT to
// re-implement Talos's case-strict input validation — an operator
// who declared `type: SSD` in values.yaml will get either (a) a
// match here and a downstream Talos error at apply, or (b) the
// "matches zero disks" finding from the rest of the selector logic.
// Case (a) is acceptable: Phase 1 didn't introduce the typo, and
// the Talos error itself is clear. Tightening this branch would
// require its own enum hint and offer no protection beyond
// duplicating Talos's check.
func predicateType(sel *DiskSelector, disk *DiskInfo) bool {
	if sel.Type == "" {
		return true
	}

	switch strings.ToLower(sel.Type) {
	case selectorTypeNVMe:
		return disk.Transport == transportNVMe
	case "sd":
		return disk.Transport == "mmc"
	case "hdd":
		return disk.Rotational
	case "ssd":
		return !disk.Rotational
	}

	return false
}

func diskMatches(sel *DiskSelector, disk *DiskInfo) bool {
	for _, p := range diskPredicates {
		if !p(sel, disk) {
			return false
		}
	}

	return true
}

// predicateExact returns a predicate that compares two extracted string
// fields exactly. An empty selector field is "don't care" — passes.
func predicateExact(sel func(*DiskSelector) string, dsk func(*DiskInfo) string) diskPredicate {
	return func(s *DiskSelector, disk *DiskInfo) bool {
		want := sel(s)
		if want == "" {
			return true
		}

		return want == dsk(disk)
	}
}

// predicateGlob matches the disk field against the selector field as a
// shell-style glob (Talos accepts `Samsung*` and similar in model/busPath).
func predicateGlob(sel func(*DiskSelector) string, dsk func(*DiskInfo) string) diskPredicate {
	return func(s *DiskSelector, disk *DiskInfo) bool {
		pattern := sel(s)
		if pattern == "" {
			return true
		}

		return globMatch(pattern, dsk(disk))
	}
}

// predicateSize evaluates the Size comparator. Pulled out of the table
// because it's the only non-string predicate.
func predicateSize(s *DiskSelector, d *DiskInfo) bool {
	if s.Size == "" {
		return true
	}

	return sizeMatches(s.Size, d.Size)
}

// globMatch wraps filepath.Match with exact-match fallback. Talos
// selectors use shell-style globs; filepath.Match covers `*`/`?`/`[…]`.
func globMatch(pattern, value string) bool {
	if pattern == value {
		return true
	}

	ok, err := filepath.Match(pattern, value)
	if err != nil {
		return false
	}

	return ok
}

// sizeMatches evaluates a Talos-style size expression (e.g. "500GB",
// "<= 1TB", ">= 100GB") against a candidate disk's size in bytes.
// Unparseable expressions return false — surfaces as "matches zero",
// which is the right operator signal for a malformed selector.
func sizeMatches(expr string, sizeBytes uint64) bool {
	cmp, raw := splitSizeOp(expr)

	want, ok := parseSize(raw)
	if !ok {
		return false
	}

	switch cmp {
	case cmpGE:
		return sizeBytes >= want
	case cmpLE:
		return sizeBytes <= want
	case cmpGT:
		return sizeBytes > want
	case cmpLT:
		return sizeBytes < want
	case cmpEQ, "":
		return sizeBytes == want
	}

	return false
}

// splitSizeOp lifts a leading comparator from a size expression.
// Whitespace between the operator and the literal is tolerated. Returns
// ("", expr) when no comparator is present (equality is implied).
func splitSizeOp(expr string) (string, string) {
	expr = strings.TrimSpace(expr)

	for _, prefix := range []string{cmpGE, cmpLE, cmpEQ, cmpGT, cmpLT} {
		if rest, ok := strings.CutPrefix(expr, prefix); ok {
			return prefix, strings.TrimSpace(rest)
		}
	}

	return "", expr
}

// parseSize converts a human-readable size literal ("500GB", "1.5tb",
// "100 MiB", "500gb") to bytes via humanize.ParseBytes, which mirrors
// Talos's own size parser (block/InstallDiskSelector resolution
// delegates to it). SI (kB/MB/GB/TB) and IEC (KiB/MiB/GiB/TiB) units
// are accepted case-insensitively. A bare number is bytes.
func parseSize(raw string) (uint64, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}

	value, err := humanize.ParseBytes(raw)
	if err != nil {
		return 0, false
	}

	return value, true
}

// diskPathList returns the sorted set of DiskInfo.DevPath values for
// non-excluded candidates so error messages list them in a stable,
// scannable order without burying real candidates under noise from
// dm/drbd/loop entries.
func diskPathList(disks []DiskInfo) []string {
	var paths []string

	for i := range disks {
		if isExcludedDisk(&disks[i]) {
			continue
		}

		paths = append(paths, disks[i].DevPath)
	}

	sort.Strings(paths)

	return paths
}

// quote returns the value wrapped in double quotes; used in finding
// messages so a name with spaces (rare but possible for disk identifiers)
// stays readable.
func quote(s string) string {
	return `"` + s + `"`
}

// joinAvailable returns the input as a comma-separated list, sorted.
// Empty input becomes "<none>" so the operator sees an explicit signal
// instead of an awkward "available links: ". Lists longer than
// maxHintItems are truncated with a "... and N more" suffix.
func joinAvailable(items []string) string {
	if len(items) == 0 {
		return noneText
	}

	sorted := append([]string(nil), items...)
	sort.Strings(sorted)

	return strings.Join(truncateAtN(sorted, maxHintItems), ", ")
}

// truncateAtN caps a pre-sorted list of human-readable items at n
// entries; the (len - n) tail collapses into a single "... and K
// more" string appended at the end. Returns the input unmodified
// when len <= n.
func truncateAtN(items []string, n int) []string {
	if len(items) <= n {
		return items
	}

	head := make([]string, 0, n+1)
	head = append(head, items[:n]...)
	head = append(head, fmt.Sprintf("... and %d more", len(items)-n))

	return head
}

// summarizeDisks returns a compact human-readable list of non-excluded
// disks, one per `path model serial size` triple. Used in selector-
// mismatch findings so the operator can pick the right disk without
// running another command. Excluded devices (virtual, readonly, CD)
// are omitted from the summary to keep the hint actionable.
func summarizeDisks(disks []DiskInfo) string {
	var parts []string

	for i := range disks {
		if isExcludedDisk(&disks[i]) {
			continue
		}

		parts = append(parts, formatDisk(&disks[i]))
	}

	if len(parts) == 0 {
		return noneText
	}

	sort.Strings(parts)

	return strings.Join(truncateAtN(parts, maxHintItems), "; ")
}

func formatDisk(disk *DiskInfo) string {
	var builder strings.Builder

	builder.WriteString(disk.DevPath)

	if disk.Model != "" {
		builder.WriteString(" model=")
		builder.WriteString(disk.Model)
	}

	if disk.Serial != "" {
		builder.WriteString(" serial=")
		builder.WriteString(disk.Serial)
	}

	if disk.Size > 0 {
		builder.WriteString(" size=")
		builder.WriteString(formatBytes(disk.Size))
	}

	return builder.String()
}

// formatBytes renders a byte count via humanize.Bytes, matching the
// parse-side parseSize implementation symmetrically.
func formatBytes(b uint64) string {
	return humanize.Bytes(b)
}
