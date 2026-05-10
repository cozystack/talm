package charts

import (
	"strings"
	"testing"
)

// TestPresetFiles_ReturnsChartYamlsWithPlaceholders pins the success
// contract of PresetFiles. Every Chart.yaml in the embedded preset
// charts must come back with `name` and `version` rewritten to the
// `%s` placeholder so the init flow can fmt.Sprintf in the actual
// project name and version. A regression here would silently ship
// charts with a hardcoded `name: cozystack` and a fixed version — the
// init-time placeholder substitution would no-op.
func TestPresetFiles_ReturnsChartYamlsWithPlaceholders(t *testing.T) {
	files, err := PresetFiles()
	if err != nil {
		t.Fatalf("PresetFiles: %v", err)
	}

	if len(files) == 0 {
		t.Fatal("PresetFiles returned empty map; embedded chart tree is missing")
	}

	var foundChart bool
	for path, content := range files {
		if !strings.HasSuffix(path, "Chart.yaml") {
			continue
		}

		foundChart = true
		if !strings.Contains(content, "name: %s") {
			t.Errorf("%s missing `name: %%s` placeholder; init substitution would no-op", path)
		}
		if !strings.Contains(content, "version: %s") {
			t.Errorf("%s missing `version: %%s` placeholder; init substitution would no-op", path)
		}
	}

	if !foundChart {
		t.Error("no Chart.yaml entries in PresetFiles output; the regex rewrite path was never exercised")
	}
}

// TestPresetFiles_SkipsTalmSubdirectoriesUnderPresets pins the
// embedded-tree filtering rule: cozystack/charts/talm/** and
// generic/charts/talm/** are excluded (they're transitively re-
// embedded library copies that would shadow the canonical talm/
// chart at init time), but talm/** itself stays. Without this filter,
// init's preset materialisation would write conflicting Chart.yaml
// files at multiple paths.
func TestPresetFiles_SkipsTalmSubdirectoriesUnderPresets(t *testing.T) {
	files, err := PresetFiles()
	if err != nil {
		t.Fatalf("PresetFiles: %v", err)
	}

	for path := range files {
		if strings.HasPrefix(path, "cozystack/charts/talm/") ||
			strings.HasPrefix(path, "generic/charts/talm/") {
			t.Errorf("file %q must be excluded; preset talm subdirectory leaks shadow the canonical talm/ chart", path)
		}
	}

	var sawTalmRoot bool
	for path := range files {
		if strings.HasPrefix(path, "talm/") {
			sawTalmRoot = true
			break
		}
	}
	if !sawTalmRoot {
		t.Error("expected at least one talm/ entry; the canonical talm chart was filtered out")
	}
}
