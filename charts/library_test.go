package charts

import (
	"strings"
	"testing"
)

// TestTalmLibraryFiles_NormalizesAndStripsPrefix pins the embedded talm
// library collector. Keys must be relative to the talm/ root (so they
// line up with a project's vendored charts/talm/ tree), the library
// Chart.yaml must come back with its name/version normalized to %s (so a
// version stamp never counts as content), and the helpers template must
// be present verbatim.
func TestTalmLibraryFiles_NormalizesAndStripsPrefix(t *testing.T) {
	files, err := TalmLibraryFiles()
	if err != nil {
		t.Fatalf("TalmLibraryFiles: %v", err)
	}

	if len(files) == 0 {
		t.Fatal("TalmLibraryFiles returned empty map; embedded talm library is missing")
	}

	for path := range files {
		if strings.HasPrefix(path, "talm/") {
			t.Errorf("key %q still carries the talm/ prefix; it would not match a vendored charts/talm/ relative path", path)
		}
	}

	chart, ok := files["Chart.yaml"]
	if !ok {
		t.Fatal("expected Chart.yaml entry keyed relative to the talm/ root")
	}
	if !strings.Contains(chart, "version: %s") {
		t.Errorf("library Chart.yaml not normalized; a version stamp would be treated as content drift:\n%s", chart)
	}

	if _, ok := files["templates/_helpers.tpl"]; !ok {
		t.Error("expected templates/_helpers.tpl in the embedded talm library")
	}
}

// TestHashChartFiles_OrderIndependent pins that the digest depends on the
// set of (path, content) pairs, not on map iteration order. Go map order
// is randomized, so a digest that folded order in would be non-
// deterministic across runs and falsely report drift.
func TestHashChartFiles_OrderIndependent(t *testing.T) {
	a := map[string]string{
		"Chart.yaml":             "name: %s\nversion: %s\n",
		"templates/_helpers.tpl": "{{- define \"x\" -}}{{- end -}}",
	}
	b := map[string]string{
		"templates/_helpers.tpl": "{{- define \"x\" -}}{{- end -}}",
		"Chart.yaml":             "name: %s\nversion: %s\n",
	}

	if HashChartFiles(a) != HashChartFiles(b) {
		t.Error("digest depends on map order; it must be deterministic over the (path, content) set")
	}
}

// TestHashChartFiles_ContentSensitive pins that any change to a file's
// content changes the digest. This is the signal the drift check relies
// on: a stale helpers template must hash differently from a fresh one.
func TestHashChartFiles_ContentSensitive(t *testing.T) {
	base := map[string]string{"templates/_helpers.tpl": "old"}
	changed := map[string]string{"templates/_helpers.tpl": "new"}

	if HashChartFiles(base) == HashChartFiles(changed) {
		t.Error("digest is insensitive to content; real chart drift would go undetected")
	}
}

// TestHashChartFiles_PathBoundaryUnambiguous guards the length-prefixed
// framing: two different file trees must not collide just because their
// concatenated path+content bytes happen to line up. Without framing,
// {"ab": "c"} and {"a": "bc"} would hash identically.
func TestHashChartFiles_PathBoundaryUnambiguous(t *testing.T) {
	left := map[string]string{"ab": "c"}
	right := map[string]string{"a": "bc"}

	if HashChartFiles(left) == HashChartFiles(right) {
		t.Error("path/content boundary is ambiguous; distinct trees collide to the same digest")
	}
}

// TestNormalizeChartMeta_VersionStampDoesNotAffectHash is the core
// correctness guard for the whole drift feature: two library trees that
// differ ONLY in the Chart.yaml version stamp must hash identically once
// normalized. A binary version bump that left charts/talm/ byte-identical
// must not be reported as drift — that false positive is exactly what the
// version-number comparison approach got wrong.
func TestNormalizeChartMeta_VersionStampDoesNotAffectHash(t *testing.T) {
	old := map[string]string{
		"Chart.yaml":             NormalizeChartMeta("Chart.yaml", "name: talm\nversion: 0.27.0\ntype: library\n"),
		"templates/_helpers.tpl": "{{- define \"x\" -}}{{- end -}}",
	}
	fresh := map[string]string{
		"Chart.yaml":             NormalizeChartMeta("Chart.yaml", "name: talm\nversion: 0.30.0\ntype: library\n"),
		"templates/_helpers.tpl": "{{- define \"x\" -}}{{- end -}}",
	}

	if HashChartFiles(old) != HashChartFiles(fresh) {
		t.Error("version-only difference changed the digest; a pure version bump would raise a false drift warning")
	}
}

// TestNormalizeChartMeta_LeavesNonChartYamlUntouched pins that the
// normalizer only rewrites Chart.yaml. A helpers template that happens to
// contain a `version:` line (e.g. inside a rendered manifest snippet) must
// pass through unchanged, or genuine drift in that template would be
// masked.
func TestNormalizeChartMeta_LeavesNonChartYamlUntouched(t *testing.T) {
	const tpl = "version: 1.2.3 # part of a rendered example, not chart metadata"
	if got := NormalizeChartMeta("_helpers.tpl", tpl); got != tpl {
		t.Errorf("NormalizeChartMeta rewrote a non-Chart.yaml file: %q", got)
	}
}

// TestNormalizeChartMeta_PreservesApiAndAppVersion pins that only the
// `name`/`version` metadata is folded to a placeholder. The camelCase
// `apiVersion`/`appVersion` keys must survive verbatim — otherwise a real
// change to those fields would be normalized away and hidden from the drift
// comparison. Guards against a future regex tweak (e.g. adding `(?i)`) that
// would start eating them.
func TestNormalizeChartMeta_PreservesApiAndAppVersion(t *testing.T) {
	const chart = "apiVersion: v2\nname: talm\nversion: 0.1.0\nappVersion: 1.30.0\ntype: library\n"

	got := NormalizeChartMeta("Chart.yaml", chart)

	if !strings.Contains(got, "apiVersion: v2") {
		t.Errorf("apiVersion was rewritten:\n%s", got)
	}
	if !strings.Contains(got, "appVersion: 1.30.0") {
		t.Errorf("appVersion was rewritten:\n%s", got)
	}
	if !strings.Contains(got, "name: %s") || !strings.Contains(got, "version: %s") {
		t.Errorf("name/version not normalized:\n%s", got)
	}
}
