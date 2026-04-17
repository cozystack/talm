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
	"testing"
)

// TestResolveTemplatePaths_BackslashInput is the regression guard for
// issue #11: users running `talm apply` from PowerShell pass template
// arguments with backslash separators (e.g. "templates\worker.yaml").
// resolveTemplatePaths must normalize these to forward slashes because
// the downstream helm engine only looks up templates by forward-slash
// map keys.
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
// backslash path that resolves outside rootDir is still normalized to
// forward slashes (the engine map keys never contain backslashes, even
// for paths that the caller declined to resolve).
func TestResolveTemplatePaths_OutsideRoot_Backslash(t *testing.T) {
	rootDir := t.TempDir()
	// Absolute Windows path that is guaranteed not under rootDir.
	outside := `C:\elsewhere\templates\foo.yaml`

	got := resolveTemplatePaths([]string{outside}, rootDir)
	if len(got) != 1 {
		t.Fatalf("expected 1 result, got %d", len(got))
	}
	// The function returns the original path on "outside" — but still
	// normalized for the helm engine.
	if want := "C:/elsewhere/templates/foo.yaml"; got[0] != want {
		t.Errorf("outside-root normalization: got %q, want %q", got[0], want)
	}
}
