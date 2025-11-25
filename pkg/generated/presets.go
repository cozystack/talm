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
