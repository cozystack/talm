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
	if !reflect.DeepEqual(got.Nodes, []string{testNodeIP1}) {
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

// Contract: keys are separated by `,` at JSON-array depth 0. The
// canonical separator GenerateModeline emits is `, ` (comma+space),
// but the parser is depth-aware so it also accepts the no-space form
// and arbitrary whitespace around the comma. A comma INSIDE a
// `nodes=["a", "b"]` array is array-internal and never splits the
// key-pair. This relaxation lets operators hand-author shared
// side-patches with multi-IP modelines in the natural form.
//
// All four forms below MUST parse to the same Config — the parser is
// liberal in what it accepts; GenerateModeline stays strict on output.
func TestContract_ParseModeline_KeyValueSeparator(t *testing.T) {
	want := &Config{
		Nodes:     []string{"a"},
		Endpoints: []string{"b"},
		Templates: []string{"c"},
	}
	cases := []struct {
		name string
		line string
	}{
		{"canonical (matches GenerateModeline)", `# talm: nodes=["a"], endpoints=["b"], templates=["c"]`},
		{"no space after comma", `# talm: nodes=["a"],endpoints=["b"],templates=["c"]`},
		{"multiple spaces after comma", `# talm: nodes=["a"],   endpoints=["b"],   templates=["c"]`},
		{"space before comma too", `# talm: nodes=["a"] , endpoints=["b"] , templates=["c"]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseModeline(tc.line)
			if err != nil {
				t.Fatalf("expected to parse, got: %v", err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("parse mismatch\n got: %+v\nwant: %+v", got, want)
			}
		})
	}
}

// Contract: a comma inside a JSON array (multi-element value) never
// splits the key-pair. This is the depth-0 promise that the old
// literal `, ` split broke: `nodes=["a", "b"]` was cut to
// `nodes=["a"` + ` "b"]` and the first half failed JSON parsing.
func TestContract_ParseModeline_CommaInsideArrayNeverSplits(t *testing.T) {
	cases := []struct {
		name string
		line string
		want *Config
	}{
		{
			"multi-element nodes",
			`# talm: nodes=["1.2.3.4", "5.6.7.8"]`,
			&Config{Nodes: []string{testNodeIP1, "5.6.7.8"}},
		},
		{
			"comma inside string literal",
			`# talm: nodes=["a,b","c"]`,
			&Config{Nodes: []string{"a,b", "c"}},
		},
		{
			"escaped quote inside string literal",
			`# talm: nodes=["a\"b","c"]`,
			&Config{Nodes: []string{`a"b`, "c"}},
		},
		{
			// Square brackets inside a string literal must not affect
			// the depth counter — inString short-circuits the `[`/`]`
			// arms of the splitter switch. A regression that drops the
			// short-circuit would miscount depth and either split on a
			// `,` inside the string, or fail to split on a legitimate
			// key-pair `,` that follows.
			"square brackets inside string literal",
			`# talm: nodes=["[,]","x"]`,
			&Config{Nodes: []string{"[,]", "x"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseModeline(tc.line)
			if err != nil {
				t.Fatalf("expected to parse, got: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("parse mismatch\n got: %+v\nwant: %+v", got, tc.want)
			}
		})
	}
}

// Contract: a trailing `,` at depth 0 (e.g. `nodes=["a"],` with
// nothing after it) is REJECTED. The splitter emits an empty token
// for the missing key=value pair, which fails the `SplitN(part, "=", 2)`
// length check with `invalid format of modeline part`. Pinned so a
// future "helpfully ignore empty tokens" patch can't silently flip
// rejection semantics.
func TestContract_ParseModeline_TrailingCommaRejected(t *testing.T) {
	cases := []string{
		`# talm: nodes=["a"],`,
		`# talm: nodes=["a"], `,
		`# talm: nodes=["a"], endpoints=["b"],`,
	}
	for _, line := range cases {
		t.Run(line, func(t *testing.T) {
			_, err := ParseModeline(line)
			if err == nil {
				t.Errorf("expected rejection for trailing comma in %q, got nil", line)
			}
		})
	}
}

// Contract: an unbalanced closing `]` does not panic the depth
// counter. The splitter clamps depth at zero, so a stray `]` at
// depth 0 stays at depth 0; subsequent commas are still treated as
// key-pair separators. The resulting token shape fails downstream
// JSON unmarshalling, producing a parse error rather than a panic.
// A regression that drops the `if depth > 0` clamp would let depth
// go negative — at which point an inner array's content would be
// misclassified.
func TestContract_ParseModeline_UnbalancedClosingBracketDoesNotPanic(t *testing.T) {
	// No assertion on specific error text; the contract is "rejects
	// without panicking". A nil error would mean the parser somehow
	// accepted clearly malformed JSON, which is a regression too.
	cases := []string{
		`# talm: nodes="a"]`,
		`# talm: nodes=]`,
		`# talm: nodes=["a"]], endpoints=["b"]`,
	}
	for _, line := range cases {
		t.Run(line, func(t *testing.T) {
			_, err := ParseModeline(line)
			if err == nil {
				t.Errorf("expected error for unbalanced bracket in %q, got nil", line)
			}
		})
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
			[]string{testNodeIP1, "5.6.7.8"},
			[]string{testNodeIP1},
			[]string{testTemplateControlPln},
		},
		{"empty all", nil, nil, nil},
		{
			"only nodes",
			[]string{testNodeIP1},
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
