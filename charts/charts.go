package charts

import (
	"embed"
	"io/fs"
	"path"
	"regexp"
	"strings"
)

//go:embed all:cozystack all:generic all:talm
var embeddedCharts embed.FS

// PresetFiles returns a map of file paths to their contents.
// For Chart.yaml files, name and version are replaced with %s placeholders.
func PresetFiles() (map[string]string, error) {
	filesMap := make(map[string]string)
	regex := regexp.MustCompile(`(name|version): \S+`)
	
	err := fs.WalkDir(embeddedCharts, ".", func(filePath string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		
		if d.IsDir() {
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
			return err
		}
		
		content := string(data)
		
		// For Chart.yaml files, replace name and version with %s
		if path.Base(filePath) == "Chart.yaml" {
			content = regex.ReplaceAllString(content, "$1: %s")
		}
		
		// Use the file path as-is (relative to charts directory)
		filesMap[filePath] = content
		
		return nil
	})
	
	if err != nil {
		return nil, err
	}
	
	return filesMap, nil
}

// AvailablePresets returns a list of available preset chart names.
// The "generic" preset is always first if it exists.
func AvailablePresets() ([]string, error) {
	var presets []string
	var hasGeneric bool
	
	entries, err := embeddedCharts.ReadDir(".")
	if err != nil {
		return nil, err
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
		
		if name == "generic" {
			hasGeneric = true
		} else {
			presets = append(presets, name)
		}
	}
	
	// Put generic first if it exists
	if hasGeneric {
		presets = append([]string{"generic"}, presets...)
	}
	
	return presets, nil
}

