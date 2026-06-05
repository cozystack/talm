package charts

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/cockroachdb/errors"
)

const presetGenericName = "generic"

// talmLibraryName is the directory of the talm library chart inside the
// embedded tree (and the name it is vendored under at charts/talm/ in a
// project). It is talm-owned — unlike the preset templates, which the
// operator edits — so it is the only tree the drift check compares.
const talmLibraryName = "talm"

// chartYamlName is the conventional Helm chart metadata filename.
const chartYamlName = "Chart.yaml"

//go:embed all:cozystack all:generic all:talm
var embeddedCharts embed.FS

// chartMetaRegex matches the `name:`/`version:` metadata lines of a
// Chart.yaml. Both are rewritten to a %s placeholder so the init flow can
// substitute the real project name/version, and so the drift check can
// compare two chart trees without a version stamp counting as content.
var chartMetaRegex = regexp.MustCompile(`(name|version): \S+`)

// NormalizeChartMeta rewrites the name/version lines of a Chart.yaml to %s
// placeholders. Files other than Chart.yaml pass through unchanged. base is
// the file's base name (e.g. path.Base(p)). Keeping a single normalizer
// means the init-time substitution and the content-drift comparison treat
// chart metadata identically.
func NormalizeChartMeta(base, content string) string {
	if base != chartYamlName {
		return content
	}

	return chartMetaRegex.ReplaceAllString(content, "$1: %s")
}

// PresetFiles returns a map of file paths to their contents.
// For Chart.yaml files, name and version are replaced with %s placeholders.
func PresetFiles() (map[string]string, error) {
	filesMap := make(map[string]string)

	err := fs.WalkDir(embeddedCharts, ".", func(filePath string, entry fs.DirEntry, err error) error {
		if err != nil {
			// WalkDir surfaces a plain *fs.PathError on failure;
			// wrap with the offending path so a downstream caller
			// reading just the error message can locate the bad file
			// without re-running with extra logging.
			return errors.Wrapf(err, "walking embedded charts at %q", filePath)
		}

		if entry.IsDir() {
			return nil
		}

		// Skip talm subdirectories in preset charts (cozystack/charts/talm, generic/charts/talm)
		// but include files from the main talm chart (talm/templates/_helpers.tpl, etc.)
		if strings.HasPrefix(filePath, "cozystack/charts/talm/") ||
			strings.HasPrefix(filePath, "generic/charts/talm/") {
			return nil
		}

		// Read file content
		data, err := embeddedCharts.ReadFile(filePath)
		if err != nil {
			return errors.Wrapf(err, "reading embedded chart file %q", filePath)
		}

		// For Chart.yaml files, replace name and version with %s.
		content := NormalizeChartMeta(path.Base(filePath), string(data))

		// Use the file path as-is (relative to charts directory)
		filesMap[filePath] = content

		return nil
	})
	if err != nil {
		return nil, errors.Wrap(err, "walking embedded charts")
	}

	return filesMap, nil
}

// TalmLibraryFiles returns the embedded talm library chart keyed by path
// relative to the talm/ root (e.g. "Chart.yaml", "templates/_helpers.tpl"),
// so the keys line up with a project's vendored charts/talm/ tree. Chart.yaml
// metadata is normalized via NormalizeChartMeta, so a version stamp is not
// treated as content. It is the embedded counterpart compared against the
// vendored copy to surface chart drift after a binary upgrade.
func TalmLibraryFiles() (map[string]string, error) {
	filesMap := make(map[string]string)

	err := fs.WalkDir(embeddedCharts, talmLibraryName, func(filePath string, entry fs.DirEntry, err error) error {
		if err != nil {
			return errors.Wrapf(err, "walking embedded talm library at %q", filePath)
		}

		if entry.IsDir() {
			return nil
		}

		data, err := embeddedCharts.ReadFile(filePath)
		if err != nil {
			return errors.Wrapf(err, "reading embedded talm file %q", filePath)
		}

		// Strip the talm/ prefix so keys match a vendored charts/talm/ tree.
		rel := strings.TrimPrefix(filePath, talmLibraryName+"/")
		filesMap[rel] = NormalizeChartMeta(path.Base(filePath), string(data))

		return nil
	})
	if err != nil {
		return nil, errors.Wrap(err, "collecting embedded talm library")
	}

	return filesMap, nil
}

// HashChartFiles returns a deterministic digest of a chart tree described as
// a path→content map. The digest folds in the sorted set of (path, content)
// pairs and is independent of map iteration order. Each path and content is
// length-prefixed so distinct trees cannot collide by a fortunate alignment
// of concatenated bytes. Two trees hash equal iff they carry the same files
// with the same bytes — the signal the drift check relies on.
func HashChartFiles(files map[string]string) string {
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}

	sort.Strings(paths)

	hasher := sha256.New()
	for _, p := range paths {
		// Length-prefix both the path and its content so the boundary
		// between them (and between successive entries) is unambiguous.
		fmt.Fprintf(hasher, "%d:%s%d:", len(p), p, len(files[p]))
		hasher.Write([]byte(files[p]))
	}

	return hex.EncodeToString(hasher.Sum(nil))
}

// AvailablePresets returns a list of available preset chart names.
// The presetGenericName preset is always first if it exists.
func AvailablePresets() ([]string, error) {
	var (
		presets    []string
		hasGeneric bool
	)

	entries, err := embeddedCharts.ReadDir(".")
	if err != nil {
		return nil, errors.Wrap(err, "reading embedded charts root")
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		name := entry.Name()
		// Skip talm as it's a library chart, not a preset
		if name == "talm" {
			continue
		}

		if name == presetGenericName {
			hasGeneric = true
		} else {
			presets = append(presets, name)
		}
	}

	// Put generic first if it exists
	if hasGeneric {
		presets = append([]string{presetGenericName}, presets...)
	}

	return presets, nil
}
