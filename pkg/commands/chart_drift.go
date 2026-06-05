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

	"github.com/cockroachdb/errors"

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

		// root.FS() keys are already "/"-separated and relative to base, so
		// they match the embedded TalmLibraryFiles output on every platform.
		filesMap[filePath] = generated.NormalizeChartMeta(path.Base(filePath), string(data))

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
