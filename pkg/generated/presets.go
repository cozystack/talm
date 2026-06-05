package generated

import "github.com/cozystack/talm/charts"

// PresetFiles returns a map of file paths to their contents.
// For Chart.yaml files, name and version are replaced with %s placeholders.
func PresetFiles() (map[string]string, error) {
	return charts.PresetFiles()
}

// AvailablePresets returns a list of available preset chart names.
// The "generic" preset is always first if it exists.
func AvailablePresets() ([]string, error) {
	return charts.AvailablePresets()
}

// TalmLibraryFiles returns the embedded talm library chart keyed relative to
// the talm/ root, with Chart.yaml metadata normalized to %s placeholders.
func TalmLibraryFiles() (map[string]string, error) {
	return charts.TalmLibraryFiles()
}

// HashChartFiles returns a deterministic, order-independent digest of a chart
// tree described as a path→content map.
func HashChartFiles(files map[string]string) string {
	return charts.HashChartFiles(files)
}

// NormalizeChartMeta rewrites a Chart.yaml's name/version lines to %s
// placeholders; non-Chart.yaml files pass through unchanged.
func NormalizeChartMeta(base, content string) string {
	return charts.NormalizeChartMeta(base, content)
}
