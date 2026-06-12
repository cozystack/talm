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
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/cockroachdb/errors"
	"gopkg.in/yaml.v3"

	"github.com/cozystack/talm/pkg/generated"
)

// vendoredTalmFiles reads a project's vendored talm library from
// rootDir/charts/talm/, keyed by forward-slash path relative to that
// directory (so the keys line up with the embedded TalmLibraryFiles output)
// and with Chart.yaml metadata normalized the same way. The bool is false
// when no vendored library exists — an unconfigured or freshly cloned
// project the drift check should simply skip rather than treat as an error.
//
// Reads go through an os.Root rooted at charts/talm/, confining traversal to
// that subtree so a symlink inside it cannot redirect a read outside the
// project (gosec G122).
func vendoredTalmFiles(rootDir string) (map[string]string, bool, error) {
	base := filepath.Join(rootDir, "charts", "talm")

	root, err := os.OpenRoot(base)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}

		return nil, false, errors.Wrapf(err, "opening vendored talm library %q", base)
	}
	defer root.Close()

	fsys := root.FS()
	filesMap := make(map[string]string)

	err = fs.WalkDir(fsys, ".", func(filePath string, entry fs.DirEntry, err error) error {
		if err != nil {
			return errors.Wrapf(err, "walking vendored talm library at %q", filePath)
		}

		if entry.IsDir() {
			return nil
		}

		data, err := fs.ReadFile(fsys, filePath)
		if err != nil {
			return errors.Wrapf(err, "reading vendored talm file %q", filePath)
		}

		// Normalize CRLF to LF before comparing: a Windows clone with
		// core.autocrlf=true (the Git for Windows default) materializes the
		// vendored tree with CRLF while the embedded library is LF-only.
		// Line endings are checkout artifacts, not chart content — without
		// this, every command on such a clone reports false drift, and
		// strictCharts: true hard-fails a byte-identical-modulo-EOL tree.
		content := strings.ReplaceAll(string(data), "\r\n", "\n")

		// root.FS() keys are already "/"-separated and relative to base, so
		// they match the embedded TalmLibraryFiles output on every platform.
		filesMap[filePath] = generated.NormalizeChartMeta(path.Base(filePath), content)

		return nil
	})
	if err != nil {
		return nil, false, errors.Wrap(err, "collecting vendored talm library")
	}

	return filesMap, true, nil
}

// CheckChartDrift reports whether a project's vendored charts/talm/ library
// diverges, by content, from the copy built into this talm binary.
//
// talm vendors its library chart into the project at `talm init` time;
// rendering (template/apply/upgrade) reads that local copy, never the
// binary's embedded charts. Upgrading the binary therefore leaves
// charts/talm/ frozen at the version that last ran init. CheckChartDrift
// surfaces that staleness so the operator can re-run
// `talm init --update --preset <preset>`.
//
// The comparison is by content: the Chart.yaml version stamp is normalized
// away on both sides, so a pure version bump that left the library
// byte-identical is NOT reported as drift. binaryVersion is used only for
// the operator-facing message.
//
// It returns (false, "", nil) — staying silent — when the project has no
// vendored library to compare. A read or walk failure is returned as an
// error; callers treat drift detection as best-effort and must not block a
// command on it.
func CheckChartDrift(rootDir, binaryVersion string) (bool, string, error) {
	vendored, ok, err := vendoredTalmFiles(rootDir)
	if err != nil {
		return false, "", err
	}

	if !ok {
		return false, "", nil
	}

	embedded, err := generated.TalmLibraryFiles()
	if err != nil {
		return false, "", errors.Wrap(err, "loading embedded talm library")
	}

	if generated.HashChartFiles(vendored) == generated.HashChartFiles(embedded) {
		return false, "", nil
	}

	return true, fmt.Sprintf(
		"project's vendored charts/talm/ library differs from the copy built into talm %s; "+
			"run `talm init --update --preset <preset>` to re-sync (or ignore if this is intentional)",
		binaryVersion,
	), nil
}

// presetLockName is the project-root file that records the pristine preset a
// project was generated from. Unlike charts/talm/ (vendored library, never
// operator-edited, content-checked by CheckChartDrift), the preset templates
// land in templates/ and ARE meant to be operator-edited — so they cannot be
// content-checked without false-positiving on every customization. The lock
// instead pins the hash of the preset AS SHIPPED at init time; drift is the
// binary's current preset hash diverging from that pinned baseline, which is
// independent of whatever the operator did to their templates/.
const presetLockName = ".talm-preset.lock"

// presetLockHeader is prepended to the written lock so an operator who opens
// it understands it is machine-managed.
const presetLockHeader = "# Managed by talm. Records the preset this project was generated from and the\n" +
	"# content hash of that preset at init time, so talm can warn when the installed\n" +
	"# binary ships a newer preset. Do not edit by hand; run\n" +
	"# `talm init --update --preset <preset>` to refresh.\n"

// presetLock is the on-disk shape of presetLockName.
type presetLock struct {
	Preset     string `yaml:"preset"`
	PresetHash string `yaml:"presetHash"`
}

// embeddedPresetHash returns the content digest of the named preset as built
// into this binary. PresetFiles() returns every preset keyed by a
// "<preset>/..." path; the subset for one preset is hashed with those keys
// intact, so the same call at init time (WritePresetLock) and at check time
// (CheckPresetDrift) yields the same digest for an unchanged preset. Chart
// metadata is already normalized by PresetFiles, so a pure version bump in
// the preset's Chart.yaml is not seen as content drift — matching the
// library check's contract. Returns an error when the preset is unknown to
// this binary (no files carry its prefix).
func embeddedPresetHash(preset string) (string, error) {
	all, err := generated.PresetFiles()
	if err != nil {
		return "", errors.Wrap(err, "loading embedded preset files")
	}

	prefix := preset + "/"
	subset := make(map[string]string)

	for filePath, content := range all {
		if strings.HasPrefix(filePath, prefix) {
			subset[filePath] = content
		}
	}

	if len(subset) == 0 {
		//nolint:wrapcheck // origin error built in-function with cockroachdb/errors.Newf; nothing upstream to wrap.
		return "", errors.Newf("unknown preset %q: no embedded files carry that prefix", preset)
	}

	return generated.HashChartFiles(subset), nil
}

// WritePresetLock pins the current pristine hash of preset into
// rootDir/.talm-preset.lock. Called after `talm init` (and `init --update`)
// has materialized the preset into the project, so a later binary upgrade
// that changes the preset can be surfaced by CheckPresetDrift. The hash is of
// the EMBEDDED preset, not the project's (possibly operator-edited) copy.
func WritePresetLock(rootDir, preset string) error {
	hash, err := embeddedPresetHash(preset)
	if err != nil {
		return err
	}

	body, err := yaml.Marshal(presetLock{Preset: preset, PresetHash: hash})
	if err != nil {
		return errors.Wrap(err, "marshaling preset lock")
	}

	dest := filepath.Join(rootDir, presetLockName)
	if err := os.WriteFile(dest, append([]byte(presetLockHeader), body...), presetFileMode); err != nil {
		return errors.Wrapf(err, "writing preset lock %q", dest)
	}

	return nil
}

// CheckPresetDrift reports whether the preset built into this talm binary has
// changed since the project was generated, by comparing the binary's current
// preset hash against the baseline pinned in rootDir/.talm-preset.lock at
// init time.
//
// It stays silent — (false, "", nil) — for a project with no lock file (one
// generated before preset pinning existed, or never init'd from a preset):
// there is no baseline to compare, and inventing drift would nag every such
// project. binaryVersion is used only for the operator-facing message.
//
// Crucially this never reads the project's templates/, so operator edits to
// the rendered preset are NOT drift: the baseline is the pristine preset hash
// at init, and the comparison is binary-now vs that baseline.
func CheckPresetDrift(rootDir, binaryVersion string) (bool, string, error) {
	data, err := os.ReadFile(filepath.Join(rootDir, presetLockName))
	if err != nil {
		if os.IsNotExist(err) {
			return false, "", nil
		}

		return false, "", errors.Wrapf(err, "reading preset lock in %q", rootDir)
	}

	var lock presetLock
	if err := yaml.Unmarshal(data, &lock); err != nil {
		return false, "", errors.Wrap(err, "parsing preset lock")
	}

	if lock.Preset == "" || lock.PresetHash == "" {
		return false, "", errors.New("preset lock is missing preset or presetHash")
	}

	current, err := embeddedPresetHash(lock.Preset)
	if err != nil {
		return false, "", err
	}

	if current == lock.PresetHash {
		return false, "", nil
	}

	return true, fmt.Sprintf(
		"project's %s preset differs from the copy built into talm %s; "+
			"run `talm init --update --preset %s` to pull the new preset defaults (your templates/ edits are preserved via the interactive diff)",
		lock.Preset, binaryVersion, lock.Preset,
	), nil
}
