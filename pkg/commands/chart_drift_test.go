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
	"strings"
	"testing"

	"github.com/cozystack/talm/pkg/generated"
)

// writeVendoredTalmLibrary materializes a project's charts/talm/ tree from
// the binary's embedded library, stamping version into the library
// Chart.yaml so the result looks like a real `talm init` would produce. It
// returns the project root.
func writeVendoredTalmLibrary(t *testing.T, version string) string {
	t.Helper()

	files, err := generated.TalmLibraryFiles()
	if err != nil {
		t.Fatalf("TalmLibraryFiles: %v", err)
	}

	root := t.TempDir()
	for rel, content := range files {
		// TalmLibraryFiles normalizes Chart.yaml name/version to %s; fill
		// them back in (name, version) so the vendored copy mirrors init.
		if filepath.Base(rel) == "Chart.yaml" {
			content = strings.ReplaceAll(content, "name: %s", "name: talm")
			content = strings.ReplaceAll(content, "version: %s", "version: "+version)
		}

		dest := filepath.Join(root, "charts", "talm", filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			t.Fatalf("mkdir %q: %v", dest, err)
		}
		if err := os.WriteFile(dest, []byte(content), 0o644); err != nil {
			t.Fatalf("write %q: %v", dest, err)
		}
	}

	return root
}

// TestCheckChartDrift_MatchingCopy_NoDrift pins the happy path: a project
// whose vendored charts/talm/ matches the embedded library reports no
// drift, even though its Chart.yaml carries a concrete version stamp.
func TestCheckChartDrift_MatchingCopy_NoDrift(t *testing.T) {
	root := writeVendoredTalmLibrary(t, "0.30.0")

	drift, msg, err := CheckChartDrift(root, "0.30.0")
	if err != nil {
		t.Fatalf("CheckChartDrift: %v", err)
	}
	if drift {
		t.Errorf("reported drift for a matching vendored copy: %s", msg)
	}
}

// TestCheckChartDrift_VersionStampOnly_NoDrift is the core regression
// guard. A project vendored by an older release carries an older version
// stamp in charts/talm/Chart.yaml, but its helpers are byte-identical to
// the running binary's. That MUST NOT be reported as drift — flagging a
// pure version difference is exactly the false positive the version-number
// comparison approach produced.
func TestCheckChartDrift_VersionStampOnly_NoDrift(t *testing.T) {
	root := writeVendoredTalmLibrary(t, "0.1.0")

	drift, msg, err := CheckChartDrift(root, "0.30.0")
	if err != nil {
		t.Fatalf("CheckChartDrift: %v", err)
	}
	if drift {
		t.Errorf("reported drift for a version-only difference; this is a false positive: %s", msg)
	}
}

// TestCheckChartDrift_ContentChange_Drift pins detection: a vendored
// helpers template that diverges from the embedded copy is real drift and
// must be surfaced.
func TestCheckChartDrift_ContentChange_Drift(t *testing.T) {
	root := writeVendoredTalmLibrary(t, "0.30.0")

	helpers := filepath.Join(root, "charts", "talm", "templates", "_helpers.tpl")
	data, err := os.ReadFile(helpers)
	if err != nil {
		t.Fatalf("read helpers: %v", err)
	}
	if err := os.WriteFile(helpers, append(data, []byte("\n{{- /* stale local edit */ -}}\n")...), 0o644); err != nil {
		t.Fatalf("write helpers: %v", err)
	}

	drift, msg, err := CheckChartDrift(root, "0.30.0")
	if err != nil {
		t.Fatalf("CheckChartDrift: %v", err)
	}
	if !drift {
		t.Error("did not detect a divergent vendored helpers template; real drift went unreported")
	}
	// The remediation must carry --preset: bare `talm init --update` cannot
	// resolve the preset from an init'd project's Chart.yaml and errors out
	// without re-vendoring, so pointing at it would send operators to a
	// command that does not clear the drift.
	if !strings.Contains(msg, "talm init --update --preset") {
		t.Errorf("drift message must point at `talm init --update --preset`; got %q", msg)
	}
}

// TestCheckChartDrift_VendoredTalmIsFile_ErrorsGracefully pins that a
// charts/talm that exists as a file (not a directory) is surfaced as an
// error rather than a panic or a spurious drift result. Callers
// (evaluateChartDrift) downgrade this to a non-fatal warning, so a
// malformed project never crashes a command.
func TestCheckChartDrift_VendoredTalmIsFile_ErrorsGracefully(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "charts"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "charts", "talm"), []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	drift, _, err := CheckChartDrift(root, "0.30.0")
	if err == nil {
		t.Error("expected an error when charts/talm is a file, got nil")
	}
	if drift {
		t.Error("must not report drift when the vendored tree cannot be read")
	}
}

// TestCheckChartDrift_MissingVendoredDir_NoDriftNoError pins graceful
// handling: a project without a charts/talm/ directory (nothing vendored
// yet) yields no drift and no error, so the check never blocks a command
// on a tree it cannot compare.
func TestCheckChartDrift_MissingVendoredDir_NoDriftNoError(t *testing.T) {
	root := t.TempDir()

	drift, msg, err := CheckChartDrift(root, "0.30.0")
	if err != nil {
		t.Fatalf("CheckChartDrift returned an error for a missing vendored dir: %v", err)
	}
	if drift {
		t.Errorf("reported drift with no vendored library to compare: %s", msg)
	}
}
