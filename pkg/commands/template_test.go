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
	"os"
	"path/filepath"
	"testing"
)

// TestResolveEngineTemplatePaths_DotDotPrefixedDir pins that a
// sibling directory whose name literally starts with ".." (e.g.
// "..templates") is not mistaken for an outside-root path and routed
// through the templates/<basename> fallback — that would silently
// substitute a different file when one exists under templates/.
//
// Not safe with t.Parallel — uses os.Chdir, which is process-global.
func TestResolveEngineTemplatePaths_DotDotPrefixedDir(t *testing.T) {
	// Resolve symlinks so the rootDir and the post-Chdir cwd share the
	// same canonical form — on macOS, t.TempDir lives under /var/...
	// but os.Getwd returns the realpath /private/var/... which throws
	// off the Rel computation.
	rootDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("eval symlinks: %v", err)
	}
	// Seed both ..templates/controlplane.yaml (the real target) and
	// templates/controlplane.yaml (a decoy the buggy fallback would
	// have picked instead).
	if err := os.MkdirAll(filepath.Join(rootDir, "..templates"), 0o755); err != nil {
		t.Fatalf("mkdir ..templates: %v", err)
	}
	if err := os.WriteFile(filepath.Join(rootDir, "..templates", "controlplane.yaml"), []byte("real"), 0o600); err != nil {
		t.Fatalf("seed ..templates/controlplane.yaml: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(rootDir, "templates"), 0o755); err != nil {
		t.Fatalf("mkdir templates: %v", err)
	}
	if err := os.WriteFile(filepath.Join(rootDir, "templates", "controlplane.yaml"), []byte("decoy"), 0o600); err != nil {
		t.Fatalf("seed templates/controlplane.yaml: %v", err)
	}

	t.Chdir(rootDir)

	got := resolveEngineTemplatePaths([]string{testTemplateControlplane}, rootDir)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0] != testTemplateControlplane {
		t.Errorf("got %q, want %q (the ..templates dir was misclassified as outside-root and routed through the basename fallback)", got[0], testTemplateControlplane)
	}
}
