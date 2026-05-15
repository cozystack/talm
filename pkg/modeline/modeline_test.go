package modeline

import (
	"reflect"
	"testing"
)

func TestParseModeline(t *testing.T) {
	testCases := []struct {
		name    string
		line    string
		want    *Config
		wantErr bool
	}{
		{
			name: "valid modeline with all known keys",
			line: `# talm: nodes=["192.168.100.2"], endpoints=["1.2.3.4","127.0.0.1","192.168.100.2"], templates=["templates/controlplane.yaml","templates/worker.yaml"]`,
			want: &Config{
				Nodes:     []string{testNodeIP2},
				Endpoints: []string{testNodeIP1, testLoopback, testNodeIP2},
				Templates: []string{testTemplateControlPln, "templates/worker.yaml"},
			},
			wantErr: false,
		},
		{
			name: "modeline with unknown key",
			line: `# talm: nodes=["192.168.100.2"], endpoints=["1.2.3.4","127.0.0.1","192.168.100.2"], unknown=["value"]`,
			want: &Config{
				Nodes:     []string{testNodeIP2},
				Endpoints: []string{testNodeIP1, testLoopback, testNodeIP2},
			},
			wantErr: false,
		},
		{
			// Human-written form of a shared side-patch modeline: each
			// element of the JSON array is followed by a single space.
			// The old `SplitSeq(content, ", ")` literal split treated
			// the inner space-comma as the key-pair separator and cut
			// the array value at the first element.
			name: "multi-element array with space after comma",
			line: `# talm: nodes=["1.2.3.4", "127.0.0.1", "192.168.100.2"]`,
			want: &Config{
				Nodes: []string{testNodeIP1, testLoopback, testNodeIP2},
			},
			wantErr: false,
		},
		{
			// Same payload, all three keys, mixed spacing — guards the
			// depth-0 boundary so a comma INSIDE an array never collides
			// with a comma BETWEEN key-pairs.
			name: "all keys multi-element mixed spacing",
			line: `# talm: nodes=["1.2.3.4", "127.0.0.1"], endpoints=["1.2.3.4","127.0.0.1"], templates=["templates/controlplane.yaml",  "templates/worker.yaml"]`,
			want: &Config{
				Nodes:     []string{testNodeIP1, testLoopback},
				Endpoints: []string{testNodeIP1, testLoopback},
				Templates: []string{testTemplateControlPln, "templates/worker.yaml"},
			},
			wantErr: false,
		},
		{
			// Comma inside a JSON string literal must NOT split. Rare in
			// practice (IPs and template paths have no commas) but the
			// splitter's depth/string tracking promises this.
			name: "comma inside string literal",
			line: `# talm: nodes=["a,b","c"]`,
			want: &Config{
				Nodes: []string{"a,b", "c"},
			},
			wantErr: false,
		},
		{
			// Key-pair separator without the trailing space: a stricter
			// form than canonical talm output but operators may hand-edit
			// to this shape. Now accepted (was previously rejected).
			name: "key-pair separator without trailing space",
			line: `# talm: nodes=["1.2.3.4"],endpoints=["127.0.0.1"]`,
			want: &Config{
				Nodes:     []string{testNodeIP1},
				Endpoints: []string{testLoopback},
			},
			wantErr: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseModeline(tc.line)
			if (err != nil) != tc.wantErr {
				t.Errorf("parseModeline() error = %v, wantErr %v", err, tc.wantErr)
				return
			}
			if !tc.wantErr && !reflect.DeepEqual(got, tc.want) {
				t.Errorf("parseModeline() got = %v, want %v", got, tc.want)
			}
		})
	}
}
