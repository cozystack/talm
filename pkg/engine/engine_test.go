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

package engine

import "testing"

func TestIsTalosConfigPatch(t *testing.T) {
	tests := []struct {
		name      string
		doc       string
		expected  bool
		expectErr bool
	}{
		{
			name:     "machine config",
			doc:      "machine:\n  type: worker",
			expected: true,
		},
		{
			name:     "cluster config",
			doc:      "cluster:\n  name: test",
			expected: true,
		},
		{
			name:     "both machine and cluster",
			doc:      "machine:\n  type: worker\ncluster:\n  name: test",
			expected: true,
		},
		{
			name:     "UserVolumeConfig",
			doc:      "apiVersion: v1alpha1\nkind: UserVolumeConfig\nname: test",
			expected: false,
		},
		{
			name:     "SideroLinkConfig",
			doc:      "apiVersion: v1alpha1\nkind: SideroLinkConfig\napiUrl: https://example.com",
			expected: false,
		},
		{
			name:     "HostnameConfig",
			doc:      "apiVersion: v1alpha1\nkind: HostnameConfig\nhostname: worker-1",
			expected: false,
		},
		{
			name:     "LinkConfig",
			doc:      "apiVersion: v1alpha1\nkind: LinkConfig\nname: enp0s3\naddresses:\n  - address: 192.168.1.100/24",
			expected: false,
		},
		{
			name:     "BondConfig",
			doc:      "apiVersion: v1alpha1\nkind: BondConfig\nname: bond0\nlinks:\n  - eth0\n  - eth1\nbondMode: 802.3ad",
			expected: false,
		},
		{
			name:     "VLANConfig",
			doc:      "apiVersion: v1alpha1\nkind: VLANConfig\nname: bond0.100\nvlanID: 100\nparent: bond0",
			expected: false,
		},
		{
			name:     "ResolverConfig",
			doc:      "apiVersion: v1alpha1\nkind: ResolverConfig\nnameservers:\n  - address: 8.8.8.8",
			expected: false,
		},
		{
			name:     "RegistryMirrorConfig",
			doc:      "apiVersion: v1alpha1\nkind: RegistryMirrorConfig\nname: docker.io\nendpoints:\n  - url: https://mirror.gcr.io",
			expected: false,
		},
		{
			name:     "Layer2VIPConfig",
			doc:      "apiVersion: v1alpha1\nkind: Layer2VIPConfig\nname: 192.168.100.10\nlink: bond0",
			expected: false,
		},
		{
			name:     "empty document",
			doc:      "",
			expected: false,
		},
		{
			name:      "invalid yaml",
			doc:       "not: valid: yaml: here",
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := isTalosConfigPatch(tt.doc)
			if tt.expectErr {
				if err == nil {
					t.Errorf("isTalosConfigPatch() expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Errorf("isTalosConfigPatch() unexpected error: %v", err)
				return
			}
			if got != tt.expected {
				t.Errorf("isTalosConfigPatch() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestExtractExtraDocuments(t *testing.T) {
	tests := []struct {
		name      string
		patches   []string
		wantTalos int
		wantExtra int
		wantErr   bool
	}{
		{
			name:      "issue #66 scenario - talos config with UserVolumeConfig",
			patches:   []string{"machine:\n  type: worker\ncluster:\n  name: test\n---\napiVersion: v1alpha1\nkind: UserVolumeConfig\nname: databig"},
			wantTalos: 1,
			wantExtra: 1,
		},
		{
			name:      "only talos config",
			patches:   []string{"machine:\n  type: worker"},
			wantTalos: 1,
			wantExtra: 0,
		},
		{
			name:      "only extra document",
			patches:   []string{"apiVersion: v1alpha1\nkind: UserVolumeConfig\nname: test"},
			wantTalos: 0,
			wantExtra: 1,
		},
		{
			name:      "multiple extra documents",
			patches:   []string{"machine:\n  type: worker\n---\napiVersion: v1alpha1\nkind: UserVolumeConfig\nname: vol1\n---\napiVersion: v1alpha1\nkind: SideroLinkConfig\napiUrl: https://example.com"},
			wantTalos: 1,
			wantExtra: 2,
		},
		{
			name:      "empty patch",
			patches:   []string{""},
			wantTalos: 0,
			wantExtra: 0,
		},
		{
			name:      "multiple patches with mixed content",
			patches:   []string{"machine:\n  type: controlplane", "cluster:\n  name: prod"},
			wantTalos: 2,
			wantExtra: 0,
		},
		{
			name:      "CRLF line endings",
			patches:   []string{"machine:\r\n  type: worker\r\n---\r\napiVersion: v1alpha1\r\nkind: UserVolumeConfig"},
			wantTalos: 1,
			wantExtra: 1,
		},
		{
			name:      "v1.12 multi-doc: talos patch + network and registry documents",
			patches:   []string{"machine:\n  type: worker\ncluster:\n  name: test\n---\napiVersion: v1alpha1\nkind: HostnameConfig\nhostname: worker-1\n---\napiVersion: v1alpha1\nkind: LinkConfig\nname: enp0s3\naddresses:\n  - address: 192.168.1.100/24\n---\napiVersion: v1alpha1\nkind: RegistryMirrorConfig\nname: docker.io\nendpoints:\n  - url: https://mirror.gcr.io"},
			wantTalos: 1,
			wantExtra: 3,
		},
		{
			name:      "v1.12 multi-doc: talos patch + bond, vlan, vip documents",
			patches:   []string{"machine:\n  type: controlplane\ncluster:\n  name: prod\n---\napiVersion: v1alpha1\nkind: BondConfig\nname: bond0\nlinks:\n  - eth0\n  - eth1\nbondMode: 802.3ad\n---\napiVersion: v1alpha1\nkind: VLANConfig\nname: bond0.100\nvlanID: 100\nparent: bond0\n---\napiVersion: v1alpha1\nkind: Layer2VIPConfig\nname: 192.168.100.10\nlink: bond0"},
			wantTalos: 1,
			wantExtra: 3,
		},
		{
			name:      "v1.12 multi-doc: talos patch + resolver config",
			patches:   []string{"machine:\n  type: worker\n---\napiVersion: v1alpha1\nkind: ResolverConfig\nnameservers:\n  - address: 8.8.8.8\n  - address: 8.8.4.4"},
			wantTalos: 1,
			wantExtra: 1,
		},
		{
			name:    "invalid yaml should return error",
			patches: []string{"machine:\n  network:\n    interfaces:\n    \n    []"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			talos, extra, err := extractExtraDocuments(tt.patches)
			if tt.wantErr {
				if err == nil {
					t.Errorf("extractExtraDocuments() expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Errorf("extractExtraDocuments() unexpected error: %v", err)
				return
			}
			if len(talos) != tt.wantTalos {
				t.Errorf("talosPatches count = %d, want %d", len(talos), tt.wantTalos)
			}
			if len(extra) != tt.wantExtra {
				t.Errorf("extraDocs count = %d, want %d", len(extra), tt.wantExtra)
			}
		})
	}
}

func TestNormalizeTemplatePath(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"unix path", "templates/file.yaml", "templates/file.yaml"},
		{"nested path", "templates/nested/file.yaml", "templates/nested/file.yaml"},
		{"simple file", "file.yaml", "file.yaml"},
		{"empty string", "", ""},
		{"trailing slash", "templates/", "templates/"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeTemplatePath(tt.input); got != tt.want {
				t.Errorf("NormalizeTemplatePath(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
