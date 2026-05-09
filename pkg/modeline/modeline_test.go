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
