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
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cockroachdb/errors"

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

// TestCheckChartDrift_ExtraneousFile_DriftNamesPath pins two contracts for
// the "extra file in the talm-owned tree" shape (.DS_Store, editor backup,
// a file a newer library dropped): it IS drift — the vendored tree no
// longer matches the binary — and the message must name the offending
// path. Without the path the operator gets a warning that
// `init --update --preset` alone cannot clear and no way to locate why.
func TestCheckChartDrift_ExtraneousFile_DriftNamesPath(t *testing.T) {
	root := writeVendoredTalmLibrary(t, "0.30.0")

	if err := os.WriteFile(filepath.Join(root, "charts", "talm", ".DS_Store"), []byte{0x00, 0x01}, 0o644); err != nil {
		t.Fatalf("write extraneous file: %v", err)
	}

	drift, msg, err := CheckChartDrift(root, "0.30.0")
	if err != nil {
		t.Fatalf("CheckChartDrift: %v", err)
	}

	if !drift {
		t.Fatal("an extraneous file in the vendored tree must be reported as drift")
	}

	if !strings.Contains(msg, "extra: .DS_Store") {
		t.Errorf("drift message must name the extraneous path; got %q", msg)
	}
}

// TestCheckChartDrift_ModifiedFile_DriftNamesPath pins that a content
// change is reported with the modified path, so the operator can see WHAT
// drifted instead of diffing the tree by hand.
func TestCheckChartDrift_ModifiedFile_DriftNamesPath(t *testing.T) {
	root := writeVendoredTalmLibrary(t, "0.30.0")

	helpers := filepath.Join(root, "charts", "talm", "templates", "_helpers.tpl")
	if err := os.WriteFile(helpers, []byte("{{- /* divergent */ -}}\n"), 0o644); err != nil {
		t.Fatalf("write helpers: %v", err)
	}

	drift, msg, err := CheckChartDrift(root, "0.30.0")
	if err != nil {
		t.Fatalf("CheckChartDrift: %v", err)
	}

	if !drift {
		t.Fatal("a modified vendored file must be reported as drift")
	}

	if !strings.Contains(msg, "modified: templates/_helpers.tpl") {
		t.Errorf("drift message must name the modified path; got %q", msg)
	}
}

// TestCheckChartDrift_CRLFVendoredCopy_NoDrift pins EOL-insensitivity: a
// project cloned on Windows with core.autocrlf=true (the Git for Windows
// default) materializes the vendored charts/talm/ with CRLF endings while
// the embedded library is LF. Line endings are checkout artifacts, not
// chart content — flagging them would WARN on every command for a
// byte-identical-modulo-EOL tree, and hard-fail teams that set
// strictCharts: true precisely for CI enforcement.
func TestCheckChartDrift_CRLFVendoredCopy_NoDrift(t *testing.T) {
	root := writeVendoredTalmLibrary(t, "0.30.0")

	base := filepath.Join(root, "charts", "talm")
	walkErr := filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			t.Fatalf("walking vendored tree at %q: %v", p, err)
		}

		if d.IsDir() {
			return nil
		}

		data, readErr := os.ReadFile(p)
		if readErr != nil {
			t.Fatalf("read %q: %v", p, readErr)
		}

		crlf := strings.ReplaceAll(string(data), "\n", "\r\n")
		if writeErr := os.WriteFile(p, []byte(crlf), 0o644); writeErr != nil {
			t.Fatalf("write %q: %v", p, writeErr)
		}

		return nil
	})
	if walkErr != nil {
		t.Fatalf("converting vendored tree to CRLF: %v", walkErr)
	}

	drift, msg, err := CheckChartDrift(root, "0.30.0")
	if err != nil {
		t.Fatalf("CheckChartDrift: %v", err)
	}

	if drift {
		t.Errorf("CRLF line endings were misreported as drift: %s", msg)
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

// TestCheckChartDrift_MissingVendoredDir_NoBaselineSentinel pins the
// missing-baseline contract: a project without a charts/talm/ directory
// (nothing vendored yet) yields no drift and an error matching
// ErrNoBaseline, so the caller can keep non-strict runs silent while
// strict runs block — deleting the vendored tree must not be a quieter
// bypass than corrupting it.
func TestCheckChartDrift_MissingVendoredDir_NoBaselineSentinel(t *testing.T) {
	root := t.TempDir()

	drift, msg, err := CheckChartDrift(root, "0.30.0")
	if !errors.Is(err, ErrNoBaseline) {
		t.Fatalf("expected an ErrNoBaseline-matching error for a missing vendored dir, got: %v", err)
	}
	if drift {
		t.Errorf("reported drift with no vendored library to compare: %s", msg)
	}
}

// === preset drift (.talm-preset.lock) ===

// TestPresetLock_RoundTrip_NoDrift pins the happy path: WritePresetLock pins
// the current embedded preset hash, and CheckPresetDrift against the same
// binary reports no drift.
func TestPresetLock_RoundTrip_NoDrift(t *testing.T) {
	root := t.TempDir()
	if err := WritePresetLock(root, "cozystack"); err != nil {
		t.Fatalf("WritePresetLock: %v", err)
	}

	drift, msg, err := CheckPresetDrift(root, "0.30.0")
	if err != nil {
		t.Fatalf("CheckPresetDrift: %v", err)
	}
	if drift {
		t.Errorf("reported preset drift for a freshly-pinned lock: %s", msg)
	}
}

// TestCheckPresetDrift_StaleBaseline_Drift simulates a project pinned by an
// older binary whose preset content differs: the lock carries a baseline
// hash that no longer matches the embedded preset, which is real drift and
// must point the operator at `talm init --update --preset <preset>`.
func TestCheckPresetDrift_StaleBaseline_Drift(t *testing.T) {
	root := t.TempDir()
	// A baseline hash that cannot match the embedded cozystack preset.
	lock := "preset: cozystack\npresetHash: 0000000000000000000000000000000000000000000000000000000000000000\n"
	if err := os.WriteFile(filepath.Join(root, ".talm-preset.lock"), []byte(lock), 0o644); err != nil {
		t.Fatalf("write lock: %v", err)
	}

	drift, msg, err := CheckPresetDrift(root, "0.30.0")
	if err != nil {
		t.Fatalf("CheckPresetDrift: %v", err)
	}
	if !drift {
		t.Fatal("did not detect drift against a stale preset baseline")
	}
	if !strings.Contains(msg, "talm init --update --preset cozystack") {
		t.Errorf("drift message must point at `talm init --update --preset cozystack`; got %q", msg)
	}
}

// TestCheckPresetDrift_NoLock_NoBaselineSentinel pins the missing-baseline
// contract: a project with no .talm-preset.lock (generated before preset
// pinning, or never init'd from a preset) yields no drift and an error
// matching ErrNoBaseline. The caller keeps non-strict runs silent — no
// baseline, no nag — while strict runs block, so a lock deleted by a bad
// merge resolution cannot pass more quietly than a corrupted one.
func TestCheckPresetDrift_NoLock_NoBaselineSentinel(t *testing.T) {
	root := t.TempDir()

	drift, msg, err := CheckPresetDrift(root, "0.30.0")
	if !errors.Is(err, ErrNoBaseline) {
		t.Fatalf("expected an ErrNoBaseline-matching error with no lock, got: %v", err)
	}
	if drift {
		t.Errorf("reported preset drift with no baseline lock: %s", msg)
	}
}

// TestCheckPresetDrift_OperatorEditedTemplates_NoDrift is the core
// false-positive guard and the whole reason the preset check is
// baseline-hash-based rather than content-based: the operator is EXPECTED to
// edit templates/, and that must never be reported as preset drift. The lock
// pins the pristine embedded hash; editing the project's templates/ leaves it
// untouched, so the check stays silent.
func TestCheckPresetDrift_OperatorEditedTemplates_NoDrift(t *testing.T) {
	root := t.TempDir()
	if err := WritePresetLock(root, "cozystack"); err != nil {
		t.Fatalf("WritePresetLock: %v", err)
	}

	tmpl := filepath.Join(root, "templates")
	if err := os.MkdirAll(tmpl, 0o755); err != nil {
		t.Fatalf("mkdir templates: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpl, "_helpers.tpl"), []byte("{{- /* heavily customized by the operator */ -}}\n"), 0o644); err != nil {
		t.Fatalf("write operator template: %v", err)
	}

	drift, msg, err := CheckPresetDrift(root, "0.30.0")
	if err != nil {
		t.Fatalf("CheckPresetDrift: %v", err)
	}
	if drift {
		t.Errorf("operator edits to templates/ were misreported as preset drift: %s", msg)
	}
}

// TestCheckPresetDrift_MalformedLock_ErrorsGracefully pins the failure mode
// for a corrupted .talm-preset.lock: unparseable YAML must surface as an
// error with drift=false — never a panic, never a spurious drift verdict.
// Callers (evaluatePresetDrift → decideDrift) downgrade the error to a
// non-fatal warning, so a mangled lock never blocks a command.
func TestCheckPresetDrift_MalformedLock_ErrorsGracefully(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".talm-preset.lock"), []byte("preset: [unclosed\n"), 0o644); err != nil {
		t.Fatalf("write lock: %v", err)
	}

	drift, _, err := CheckPresetDrift(root, "0.30.0")
	if err == nil {
		t.Error("expected an error for an unparseable lock, got nil")
	}

	if drift {
		t.Error("must not report drift when the lock cannot be parsed")
	}
}

// TestCheckPresetDrift_LockMissingFields_ErrorsGracefully pins that a lock
// which parses as YAML but lacks preset or presetHash (hand-edited, or
// truncated by a bad merge) is an error with drift=false. Treating it as
// "no drift" would silently disable the check; inventing drift would nag on
// a baseline that was never pinned.
func TestCheckPresetDrift_LockMissingFields_ErrorsGracefully(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".talm-preset.lock"), []byte("preset: cozystack\n"), 0o644); err != nil {
		t.Fatalf("write lock: %v", err)
	}

	drift, _, err := CheckPresetDrift(root, "0.30.0")
	if err == nil {
		t.Error("expected an error for a lock without presetHash, got nil")
	}

	if drift {
		t.Error("must not report drift for a lock with no pinned baseline")
	}
}

// TestCheckPresetDrift_LockUnknownPreset_ErrorsGracefully pins the
// cross-binary shape: a well-formed lock naming a preset this binary does
// not ship (pinned by a different talm build) is an error with drift=false,
// so the caller's warning names the unknown preset instead of fabricating a
// drift verdict against a baseline that cannot be recomputed.
func TestCheckPresetDrift_LockUnknownPreset_ErrorsGracefully(t *testing.T) {
	root := t.TempDir()
	lock := "preset: does-not-exist\npresetHash: 0000000000000000000000000000000000000000000000000000000000000000\n"
	if err := os.WriteFile(filepath.Join(root, ".talm-preset.lock"), []byte(lock), 0o644); err != nil {
		t.Fatalf("write lock: %v", err)
	}

	drift, _, err := CheckPresetDrift(root, "0.30.0")
	if err == nil {
		t.Error("expected an error for a lock naming an unknown preset, got nil")
	}

	if drift {
		t.Error("must not report drift when the baseline preset is not embedded")
	}
}

// TestWritePresetLock_UnknownPreset_Errors pins that pinning a preset the
// binary does not ship fails loudly rather than writing an empty baseline.
func TestWritePresetLock_UnknownPreset_Errors(t *testing.T) {
	root := t.TempDir()

	err := WritePresetLock(root, "does-not-exist")
	if err == nil {
		t.Fatal("expected an error pinning an unknown preset, got nil")
	}
	if !strings.Contains(err.Error(), "does-not-exist") {
		t.Errorf("error should name the offending preset; got: %v", err)
	}
}
