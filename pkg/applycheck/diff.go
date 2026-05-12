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
	"bytes"
	"fmt"
	"io"
	"maps"
	"reflect"
	"sort"

	"github.com/cockroachdb/errors"
	yaml "gopkg.in/yaml.v3"
)

// ChangeOp classifies a per-document diff between two MachineConfig
// snapshots.
type ChangeOp int

const (
	// OpAdd: doc present in desired, absent in current.
	OpAdd ChangeOp = iota
	// OpRemove: doc present in current, absent in desired (the leftover
	// class — eth1 lingering after migration to eth0).
	OpRemove
	// OpUpdate: doc present on both sides with field-level differences.
	OpUpdate
	// OpEqual: doc present on both sides, structurally identical.
	OpEqual
)

// String renders the op as a single-character glyph for the drift preview
// output.
func (o ChangeOp) String() string {
	switch o {
	case OpAdd:
		return "+"
	case OpRemove:
		return "-"
	case OpUpdate:
		return "~"
	case OpEqual:
		return "="
	}

	return "?"
}

// DocID is the structural identity used to pair up documents across the
// current/desired snapshots. v1.12 multi-doc keys by (kind, name). v1.11
// nested form is collapsed into a synthetic DocID{Kind: "MachineConfig",
// Name: ""} so the root config doc participates in the diff.
type DocID struct {
	Kind string
	Name string
}

// FieldChange describes a single leaf-level difference between matched
// documents. Only populated for ChangeOp.OpUpdate. Path is the YAML
// dotted accessor inside the document.
//
// HasOld / HasNew distinguish "field missing on this side" from "field
// present with literal nil/null value": a YAML leaf with value `~` (or
// `null`) is a legitimate value, not a missing field, and the diff has
// to be able to render it differently.
type FieldChange struct {
	Path   string
	Old    any
	New    any
	HasOld bool
	HasNew bool
}

// Change is one entry in the per-doc diff between two MachineConfig
// snapshots. Fields is non-empty only when Op == OpUpdate.
type Change struct {
	ID     DocID
	Op     ChangeOp
	Fields []FieldChange
}

// Diff parses current and desired MachineConfig bytes and returns every
// per-document difference. Equal documents are included as OpEqual so
// callers can render an unchanged section if they want; pass
// FilterChanged to drop them.
//
// The diff is doc-structural, not byte-level: re-serializing either side
// with different key ordering or whitespace produces the same Diff
// output. Talos-mutated leaf fields (cert hashes etc.) currently land
// as OpUpdate entries with FieldChange path pinpointing what differs;
// an allowlist will be layered on later (see open question in #172).
func Diff(current, desired []byte) ([]Change, error) {
	currentDocs, err := parseDocs(current)
	if err != nil {
		return nil, errors.Wrap(err, "applycheck: parsing current snapshot")
	}

	desiredDocs, err := parseDocs(desired)
	if err != nil {
		return nil, errors.Wrap(err, "applycheck: parsing desired snapshot")
	}

	ids := mergedIDs(currentDocs, desiredDocs)

	changes := make([]Change, 0, len(ids))

	for _, docID := range ids {
		cur, inCur := currentDocs[docID]
		des, inDes := desiredDocs[docID]

		switch {
		case !inCur && inDes:
			changes = append(changes, Change{ID: docID, Op: OpAdd})
		case inCur && !inDes:
			changes = append(changes, Change{ID: docID, Op: OpRemove})
		case reflect.DeepEqual(cur, des):
			changes = append(changes, Change{ID: docID, Op: OpEqual})
		default:
			changes = append(changes, Change{
				ID:     docID,
				Op:     OpUpdate,
				Fields: leafDiff(cur, des),
			})
		}
	}

	return changes, nil
}

// FilterChanged returns the subset of changes whose op is not OpEqual.
// Useful when the operator only wants to see drift, not pinned doc
// counts.
func FilterChanged(changes []Change) []Change {
	out := make([]Change, 0, len(changes))

	for i := range changes {
		if changes[i].Op != OpEqual {
			out = append(out, changes[i])
		}
	}

	return out
}

// parseDocs decodes a multi-doc YAML byte stream into a map keyed by
// DocID. v1.11 root (top-level `machine:` with no `kind:`) becomes the
// synthetic DocID{Kind: "MachineConfig"}. v1.12 multi-doc docs are keyed
// by their kind + name (or kind + "" for singletons like HostnameConfig).
// Unknown shapes are ignored so future doc kinds don't error parse.
//
// Two documents sharing the same (kind, name) in one stream are a
// config defect — Talos's behaviour on duplicates is unspecified and
// varies between versions, and a silent last-write-wins would mask
// the real problem behind a misleading drift line. Surface the
// duplicate as an error so Phase 2A can warn the operator.
func parseDocs(raw []byte) (map[DocID]map[string]any, error) {
	out := make(map[DocID]map[string]any)

	if len(bytes.TrimSpace(raw)) == 0 {
		return out, nil
	}

	dec := yaml.NewDecoder(bytes.NewReader(raw))

	for idx := 0; ; idx++ {
		var doc map[string]any

		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return nil, errors.Wrapf(err, "decoding YAML document %d", idx)
		}

		if doc == nil {
			continue
		}

		docID, ok := identityOf(doc)
		if !ok {
			continue
		}

		if _, exists := out[docID]; exists {
			//nolint:wrapcheck // cockroachdb/errors.Newf at boundary.
			return nil, errors.Newf("duplicate document %s{name: %q} in stream — Talos behaviour on duplicates is unspecified; remove or rename one", docID.Kind, docID.Name)
		}

		out[docID] = doc
	}

	return out, nil
}

// identityOf returns the DocID for a single parsed document. v1.11 form
// (has `machine:`) collapses to the synthetic MachineConfig identity;
// v1.12 multi-doc uses kind + optional name.
func identityOf(doc map[string]any) (DocID, bool) {
	if _, ok := doc["machine"]; ok {
		return DocID{Kind: "MachineConfig"}, true
	}

	kind, ok := doc["kind"].(string)
	if !ok || kind == "" {
		return DocID{}, false
	}

	name, _ := doc["name"].(string)

	return DocID{Kind: kind, Name: name}, true
}

// mergedIDs returns the union of DocIDs across current/desired, sorted
// for stable output. Kind orders first, then name within kind.
func mergedIDs(a, b map[DocID]map[string]any) []DocID {
	seen := make(map[DocID]struct{}, len(a)+len(b))
	for id := range a {
		seen[id] = struct{}{}
	}

	for id := range b {
		seen[id] = struct{}{}
	}

	ids := make([]DocID, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}

	sort.Slice(ids, func(i, j int) bool {
		if ids[i].Kind != ids[j].Kind {
			return ids[i].Kind < ids[j].Kind
		}

		return ids[i].Name < ids[j].Name
	})

	return ids
}

// leafDiff returns every leaf-level field change between two parsed
// documents. Paths use dotted notation; nested objects are recursed,
// arrays are compared whole-list (a single FieldChange whose Old/New
// carry the slices). Stable order keyed by path string.
func leafDiff(cur, des map[string]any) []FieldChange {
	flatCur := flatten(cur, "")
	flatDes := flatten(des, "")

	paths := mergedPaths(flatCur, flatDes)

	out := make([]FieldChange, 0, len(paths))

	for _, path := range paths {
		oldVal, hasOld := flatCur[path]
		newVal, hasNew := flatDes[path]

		switch {
		case !hasOld && hasNew:
			out = append(out, FieldChange{Path: path, New: newVal, HasNew: true})
		case hasOld && !hasNew:
			out = append(out, FieldChange{Path: path, Old: oldVal, HasOld: true})
		case !reflect.DeepEqual(oldVal, newVal):
			out = append(out, FieldChange{Path: path, Old: oldVal, New: newVal, HasOld: true, HasNew: true})
		}
	}

	return out
}

// flatten walks a parsed YAML map and produces a flat key→leaf-value
// map. Nested maps recurse, slices are kept as-is (lists are treated as
// atomic for the leaf diff). Empty prefix yields top-level keys.
//
// Empty nested maps (`field: {}` — a real Talos default shape for
// fields like kubelet.extraArgs) are emitted as their own leaf entry
// with value map[string]any{}. Without this, a doc with an empty
// nested map and a doc that omits the field entirely would produce
// flat representations that differ only at the doc level, and
// leafDiff would emit OpUpdate with Fields=[] — leaving the operator
// with a content-free "~" line that says "something changed, can't
// tell you what".
func flatten(in map[string]any, prefix string) map[string]any {
	out := make(map[string]any)

	for key, val := range in {
		path := key
		if prefix != "" {
			path = prefix + "." + key
		}

		if nested, ok := val.(map[string]any); ok {
			if len(nested) == 0 {
				// Empty nested map: emit as a leaf so the
				// add/remove of the field itself surfaces in
				// the diff. See godoc above.
				out[path] = nested

				continue
			}

			maps.Copy(out, flatten(nested, path))

			continue
		}

		out[path] = val
	}

	return out
}

// mergedPaths returns the sorted union of keys across two flat maps so
// leafDiff's output ordering is stable.
func mergedPaths(a, b map[string]any) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	for k := range a {
		seen[k] = struct{}{}
	}

	for k := range b {
		seen[k] = struct{}{}
	}

	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}

	sort.Strings(out)

	return out
}

// FormatChange returns a one-line, grep-friendly representation of one
// Change. Used by the preflight hook to write the drift preview to
// stderr; tests pin the format.
func FormatChange(c *Change) string {
	if c.ID.Name == "" {
		return fmt.Sprintf("  %s %s", c.Op, c.ID.Kind)
	}

	return fmt.Sprintf("  %s %s{name: %s}", c.Op, c.ID.Kind, c.ID.Name)
}
