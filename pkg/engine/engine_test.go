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
		name     string
		doc      string
		expected bool
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
			name:     "empty document",
			doc:      "",
			expected: false,
		},
		{
			name:     "invalid yaml",
			doc:      "not: valid: yaml: here",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isTalosConfigPatch(tt.doc); got != tt.expected {
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			talos, extra := extractExtraDocuments(tt.patches)
			if len(talos) != tt.wantTalos {
				t.Errorf("talosPatches count = %d, want %d", len(talos), tt.wantTalos)
			}
			if len(extra) != tt.wantExtra {
				t.Errorf("extraDocs count = %d, want %d", len(extra), tt.wantExtra)
			}
		})
	}
}
