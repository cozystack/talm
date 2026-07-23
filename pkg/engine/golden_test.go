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

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// updateGolden reports whether to regenerate the committed golden
// snapshots instead of comparing against them. Set TALM_UPDATE_GOLDEN=1
// to regenerate (an env toggle avoids a package-level flag global):
//
//	TALM_UPDATE_GOLDEN=1 go test ./pkg/engine/ -run TestGoldenRender
func updateGolden() bool {
	return os.Getenv("TALM_UPDATE_GOLDEN") != ""
}

// TestGoldenRender pins the full, byte-for-byte rendered output of the
// shipped charts across the whole schema matrix. Unlike the substring
// contract tests elsewhere in this package — which assert only the
// specific fields each one names — this catches any change that alters
// whitespace, key ordering, number/bool formatting, document count,
// `---` separator placement, the trailing newline, or any field no
// other test happens to check.
//
// The matrix is charts/cozystack + charts/generic × controlplane +
// worker × multi-doc (v1.12+) + legacy single-doc (pre-v1.12) schemas.
// Discovery is driven by the deterministic simpleNicLookup fixture so
// the output depends on nothing outside the repo.
//
// It exists as a safety net for library upgrades (e.g. the Helm v3→v4
// swap): regenerate the golden files on the old version, then run this
// test unchanged on the new one — any diff is a behavioral regression
// to investigate, not something to regenerate away.
func TestGoldenRender(t *testing.T) {
	cases := []struct {
		name         string
		chartPath    string
		templateFile string
		talosVersion string
	}{
		{"cozystack-controlplane-multidoc", cozystackChartPath, "templates/controlplane.yaml", "v1.12"},
		{"cozystack-worker-multidoc", cozystackChartPath, "templates/worker.yaml", "v1.12"},
		{"cozystack-controlplane-legacy", cozystackChartPath, "templates/controlplane.yaml", "v1.11"},
		{"cozystack-worker-legacy", cozystackChartPath, "templates/worker.yaml", "v1.11"},
		{"generic-controlplane-multidoc", genericChartPath, "templates/controlplane.yaml", "v1.12"},
		{"generic-worker-multidoc", genericChartPath, "templates/worker.yaml", "v1.12"},
		{"generic-controlplane-legacy", genericChartPath, "templates/controlplane.yaml", "v1.11"},
		{"generic-worker-legacy", genericChartPath, "templates/worker.yaml", "v1.11"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := renderChartTemplateWithLookup(t, tc.chartPath, tc.templateFile, simpleNicLookup(), tc.talosVersion)
			goldenPath := filepath.Join("testdata", "golden", tc.name+".golden.yaml")

			if updateGolden() {
				if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
					t.Fatalf("mkdir golden dir: %v", err)
				}
				if err := os.WriteFile(goldenPath, []byte(got), 0o644); err != nil {
					t.Fatalf("write golden %s: %v", goldenPath, err)
				}
				return
			}

			wantBytes, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("read golden %s: %v (regenerate with TALM_UPDATE_GOLDEN=1)", goldenPath, err)
			}

			if got != string(wantBytes) {
				t.Errorf("rendered output for %s differs from golden %s.\n"+
					"A byte-level diff here means rendering behavior changed — investigate before regenerating.\n%s",
					tc.name, goldenPath, firstLineDiff(string(wantBytes), got))
			}
		})
	}
}

// firstLineDiff returns a compact description of the first line where
// want and got differ, with a little surrounding context, so a golden
// mismatch points at the exact drift instead of dumping both full files.
func firstLineDiff(want, got string) string {
	wantLines := strings.Split(want, "\n")
	gotLines := strings.Split(got, "\n")

	maxLen := max(len(wantLines), len(gotLines))

	for i := range maxLen {
		var wl, gl string
		if i < len(wantLines) {
			wl = wantLines[i]
		}
		if i < len(gotLines) {
			gl = gotLines[i]
		}
		if wl != gl {
			return "first difference at line " + itoa(i+1) + ":\n" +
				"  want: " + quote(wl) + "\n" +
				"  got:  " + quote(gl)
		}
	}

	return "files differ only in trailing content (line count: want " +
		itoa(len(wantLines)) + ", got " + itoa(len(gotLines)) + ")"
}

// itoa is a tiny strconv.Itoa alias kept local so the diff helper reads
// without an extra import at the call sites.
func itoa(n int) string {
	return strconv.Itoa(n)
}

// quote renders a line with visible quotes so trailing-whitespace drift
// is legible in the failure message.
func quote(s string) string {
	return "\"" + s + "\""
}
