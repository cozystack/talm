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
	"slices"
	"testing"
)

// TestResolveProjectValueFiles pins the resolution contract for Chart.yaml-
// declared value files: relative entries are joined with the project root
// (so they resolve identically from any CWD, like ResolveSecretsPath), and
// absolute entries pass through unchanged. An empty input yields nil.
func TestResolveProjectValueFiles(t *testing.T) {
	abs := filepath.Join(testProjectRoot, "abs.yaml")

	cases := []struct {
		name string
		in   []string
		root string
		want []string
	}{
		{name: "nil input", in: nil, root: testProjectRoot, want: nil},
		{name: "empty input", in: []string{}, root: testProjectRoot, want: nil},
		{
			name: "relative joined with root",
			in:   []string{"values-secret.encrypted.yaml"},
			root: testProjectRoot,
			want: []string{filepath.Join(testProjectRoot, "values-secret.encrypted.yaml")},
		},
		{
			name: "absolute passthrough",
			in:   []string{abs},
			root: testProjectRoot,
			want: []string{abs},
		},
		{
			name: "mixed relative and absolute",
			in:   []string{"a.yaml", abs, "sub/b.yaml"},
			root: testProjectRoot,
			want: []string{filepath.Join(testProjectRoot, "a.yaml"), abs, filepath.Join(testProjectRoot, "sub/b.yaml")},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveProjectValueFiles(tc.in, tc.root)
			if !slices.Equal(got, tc.want) {
				t.Errorf("resolveProjectValueFiles(%v, %q) = %v, want %v", tc.in, tc.root, got, tc.want)
			}
		})
	}
}

// TestTemplateAndApply_ResolveConfigValueFilesIdentically pins the cross-
// command invariant: given the same Chart.yaml value files and project root,
// `talm template` (via its PreRunE merge) and `talm apply` (via
// setApplyValueOptions) must resolve the Chart.yaml-origin paths to the exact
// same absolute form. If they diverged, the secret-sealing path (PR C) would
// seal one file at template -I time and look for a different one at apply.
func TestTemplateAndApply_ResolveConfigValueFilesIdentically(t *testing.T) {
	applyRestore := snapshotApplyValueState()
	defer applyRestore()

	origTmplValueFiles := templateCmdFlags.valueFiles
	defer func() { templateCmdFlags.valueFiles = origTmplValueFiles }()

	Config.RootDir = testProjectRoot
	Config.TemplateOptions.ValueFiles = []string{"values-secret.encrypted.yaml", "/abs/extra.yaml"}

	// apply side: no CLI value files, so opts.ValueFiles is exactly the
	// resolved Chart.yaml set.
	applyCmdFlags.valueFiles = nil
	applyOpts := buildApplyRenderOptions([]string{testTemplateControlplaneRel}, testProjectRoot+"/secrets.yaml")

	// template side: replicate the PreRunE merge with no CLI value files.
	templateCmdFlags.valueFiles = nil
	templateResolved := append(resolveProjectValueFiles(Config.TemplateOptions.ValueFiles, Config.RootDir), templateCmdFlags.valueFiles...)

	if !slices.Equal(applyOpts.ValueFiles, templateResolved) {
		t.Errorf("template and apply must resolve Chart.yaml value files identically:\n apply=%v\ntmpl =%v", applyOpts.ValueFiles, templateResolved)
	}
}
