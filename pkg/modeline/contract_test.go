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

// Contract: modeline parsing, file-reading, and generation. The
// modeline is the first line of every per-node values file (e.g.
// `nodes/cp1.yaml`); talm reads it to discover which nodes / endpoints
// / templates apply to a given node config. Format:
//
//   # talm: nodes=["1.2.3.4"], endpoints=["1.2.3.4"], templates=["templates/controlplane.yaml"]
//
// Each value is a JSON array. Keys are case-sensitive; unknown keys
// are silently ignored so future keys can be added without breaking
// older talm versions reading the same file.

package modeline

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// === ParseModeline ===

// Contract: ParseModeline rejects lines without the `# talm: ` prefix.
// The prefix requires the `# talm: ` substring exactly (hash, space,
// `talm`, colon, space) so picking up a foreign comment or a YAML key
// by accident is impossible. The indented form is accepted and tested
// in TestContract_ParseModeline_TrimsLine.
func TestContract_ParseModeline_RejectsWithoutPrefix(t *testing.T) {
	cases := []string{
		"",
		"# nothing",
		"# vim: set ft=yaml", // editor modeline (Vim)
		"# talm noprefix",    // missing colon
		`#talm: nodes=["x"]`, // missing space between # and talm
		`# talm:nodes=["x"]`, // missing space after colon
	}
	for _, line := range cases {
		t.Run(line, func(t *testing.T) {
			_, err := ParseModeline(line)
			if err == nil {
				t.Errorf("expected error for %q, got nil", line)
			}
		})
	}
}

// Contract: leading and trailing whitespace around the entire
// modeline is tolerated. Operators may indent the modeline for
// readability; talm strips before parsing.
func TestContract_ParseModeline_TrimsLine(t *testing.T) {
	indented := `   # talm: nodes=["1.2.3.4"]   `
	got, err := ParseModeline(indented)
	if err != nil {
		t.Fatalf("expected indented modeline to parse, got: %v", err)
	}
	if !reflect.DeepEqual(got.Nodes, []string{"1.2.3.4"}) {
		t.Errorf("expected Nodes=[1.2.3.4], got %v", got.Nodes)
	}
}

// Contract: each key must be in `key=value` form where the value is
// a JSON array. Malformed parts surface a precise error mentioning
// the offending segment.
func TestContract_ParseModeline_RejectsMalformedKeyValue(t *testing.T) {
	cases := []struct {
		name string
		line string
	}{
		{"missing equals", `# talm: nodes`},
		{"value not JSON", `# talm: nodes=invalid`},
		{"value JSON not array", `# talm: nodes={"key":"val"}`},
		{"empty value", `# talm: nodes=`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseModeline(tc.line)
			if err == nil {
				t.Errorf("expected error for %q, got nil", tc.line)
			}
		})
	}
}

// Contract: keys are separated by `, ` (comma then a single space).
// This is the same separator GenerateModeline emits, so a generated
// modeline always parses back. Missing the space before the next key
// fails to find the part-boundary; trailing whitespace inside a JSON
// array is tolerated (json.Unmarshal handles it).
func TestContract_ParseModeline_KeyValueSeparatorContract(t *testing.T) {
	// Canonical (matches GenerateModeline output).
	canonical := `# talm: nodes=["a"], endpoints=["b"], templates=["c"]`
	got, err := ParseModeline(canonical)
	if err != nil {
		t.Fatalf("canonical line failed: %v", err)
	}
	want := &Config{
		Nodes:     []string{"a"},
		Endpoints: []string{"b"},
		Templates: []string{"c"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("canonical parse mismatch\n got: %+v\nwant: %+v", got, want)
	}
}

// Contract: empty JSON arrays are valid. Producing `nodes=[]` is the
// way to express "no nodes" without dropping the key entirely.
func TestContract_ParseModeline_EmptyArrays(t *testing.T) {
	line := `# talm: nodes=[], endpoints=[], templates=[]`
	got, err := ParseModeline(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got.Nodes) != 0 || len(got.Endpoints) != 0 || len(got.Templates) != 0 {
		t.Errorf("expected all-empty Config, got %+v", got)
	}
}

// === ReadAndParseModeline ===

// Contract: ReadAndParseModeline opens a file, reads only the first
// line, and parses it as a modeline. Subsequent lines are not read
// (the modeline is conventionally the very first line, and the rest
// of the file is YAML the helm engine consumes).
func TestContract_ReadAndParseModeline_FirstLineOnly(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "node.yaml")
	content := `# talm: nodes=["1.2.3.4"]
# talm: nodes=["should-be-ignored"]
machine:
  type: worker
`
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ReadAndParseModeline(file)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(got.Nodes, []string{"1.2.3.4"}) {
		t.Errorf("expected first line only, got %+v", got)
	}
}

// Contract: a missing file produces an error mentioning the path so
// the operator can fix it without grepping.
func TestContract_ReadAndParseModeline_MissingFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.yaml")
	_, err := ReadAndParseModeline(missing)
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

// Contract: an empty file produces a precise error.
func TestContract_ReadAndParseModeline_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "empty.yaml")
	if err := os.WriteFile(file, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ReadAndParseModeline(file)
	if err == nil {
		t.Fatal("expected error for empty file")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("error must mention 'empty', got: %v", err)
	}
}

// Contract: a file whose first line is not a modeline surfaces the
// parse error verbatim — the file-read path is a thin wrapper.
func TestContract_ReadAndParseModeline_NonModelineFirstLine(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "no-modeline.yaml")
	if err := os.WriteFile(file, []byte("machine:\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ReadAndParseModeline(file)
	if err == nil {
		t.Fatal("expected error for first line without modeline")
	}
}

// === GenerateModeline ===

// Contract: GenerateModeline emits a line that ParseModeline accepts
// without losing information (round-trip stability). All three keys
// are emitted in a fixed order (nodes, endpoints, templates), even
// when a slice is empty — empty arrays roundtrip as `key=[]`.
func TestContract_GenerateModeline_RoundTrip(t *testing.T) {
	cases := []struct {
		name      string
		nodes     []string
		endpoints []string
		templates []string
	}{
		{
			"all populated",
			[]string{"1.2.3.4", "5.6.7.8"},
			[]string{"1.2.3.4"},
			[]string{"templates/controlplane.yaml"},
		},
		{"empty all", nil, nil, nil},
		{
			"only nodes",
			[]string{"1.2.3.4"},
			nil,
			nil,
		},
		{
			"special characters in path",
			[]string{"node.example.com"},
			[]string{"https://api.example.com:6443"},
			[]string{"path/with spaces/template.yaml"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			line, err := GenerateModeline(tc.nodes, tc.endpoints, tc.templates)
			if err != nil {
				t.Fatalf("generate: %v", err)
			}
			parsed, err := ParseModeline(line)
			if err != nil {
				t.Fatalf("parse generated modeline %q: %v", line, err)
			}
			// Slice equality with nil-vs-empty difference: GenerateModeline
			// emits [], ParseModeline returns nil for empty arrays. Compare
			// after normalising.
			normalize := func(s []string) []string {
				if len(s) == 0 {
					return nil
				}
				return s
			}
			if !reflect.DeepEqual(normalize(parsed.Nodes), normalize(tc.nodes)) {
				t.Errorf("nodes round-trip mismatch: got %v, want %v", parsed.Nodes, tc.nodes)
			}
			if !reflect.DeepEqual(normalize(parsed.Endpoints), normalize(tc.endpoints)) {
				t.Errorf("endpoints round-trip mismatch: got %v, want %v", parsed.Endpoints, tc.endpoints)
			}
			if !reflect.DeepEqual(normalize(parsed.Templates), normalize(tc.templates)) {
				t.Errorf("templates round-trip mismatch: got %v, want %v", parsed.Templates, tc.templates)
			}
		})
	}
}

// Contract: the generated modeline starts with `# talm: ` and emits
// keys in a fixed order — pinning so editor-side highlight/lint
// tooling can rely on it.
func TestContract_GenerateModeline_KeyOrder(t *testing.T) {
	line, err := GenerateModeline([]string{"a"}, []string{"b"}, []string{"c"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(line, "# talm: ") {
		t.Errorf("expected '# talm: ' prefix, got %q", line)
	}
	// nodes appears before endpoints, endpoints before templates.
	nodesIdx := strings.Index(line, "nodes=")
	endpointsIdx := strings.Index(line, "endpoints=")
	templatesIdx := strings.Index(line, "templates=")
	if nodesIdx >= endpointsIdx || endpointsIdx >= templatesIdx {
		t.Errorf("key order mismatch: nodes=%d endpoints=%d templates=%d in %q", nodesIdx, endpointsIdx, templatesIdx, line)
	}
}
