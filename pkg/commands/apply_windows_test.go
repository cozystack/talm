//go:build windows

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

package commands

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestResolveTemplatePaths_BackslashInput pins that users running
// `talm apply` from PowerShell with template arguments that use
// backslash separators (e.g. "templates\worker.yaml") end up with
// forward-slash paths. The downstream helm engine only looks up
// templates by forward-slash map keys, so anything else fails with
// "template not found".
func TestResolveTemplatePaths_BackslashInput(t *testing.T) {
	rootDir := t.TempDir()
	absRoot, err := filepath.Abs(rootDir)
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "relative with backslash",
			input: `templates\controlplane.yaml`,
			want:  "templates/controlplane.yaml",
		},
		{
			name:  "relative nested backslashes",
			input: `templates\nested\worker.yaml`,
			want:  "templates/nested/worker.yaml",
		},
		{
			name:  "mixed separators",
			input: `templates\nested/worker.yaml`,
			want:  "templates/nested/worker.yaml",
		},
		{
			name:  "absolute path inside root",
			input: filepath.Join(absRoot, "templates", "controlplane.yaml"),
			want:  "templates/controlplane.yaml",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveTemplatePaths([]string{tt.input}, rootDir)
			if len(got) != 1 {
				t.Fatalf("expected 1 result, got %d", len(got))
			}
			if got[0] != tt.want {
				t.Errorf("resolveTemplatePaths(%q) = %q, want %q", tt.input, got[0], tt.want)
			}
		})
	}
}

// TestResolveTemplatePaths_OutsideRoot_Backslash asserts that a
// backslash path resolving outside rootDir still emerges without any
// backslashes — the helm engine only looks up templates by forward-
// slash map keys, so regardless of which internal branch the function
// takes (Rel-success, Rel-failure, prefix-checks), the result must be
// backslash-free. Constructing `outside` via filepath.Join on rootDir
// keeps the test on the same drive as t.TempDir() and works on any
// GitHub Actions runner image.
func TestResolveTemplatePaths_OutsideRoot_Backslash(t *testing.T) {
	rootDir := t.TempDir()
	outside := filepath.Join(rootDir, "..", "..", "..", "elsewhere", "templates", "foo.yaml")

	got := resolveTemplatePaths([]string{outside}, rootDir)
	if len(got) != 1 {
		t.Fatalf("expected 1 result, got %d", len(got))
	}
	if strings.ContainsRune(got[0], '\\') {
		t.Errorf("result still contains backslash: %q", got[0])
	}
}
