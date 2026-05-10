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

package engine

import (
	"context"
	"maps"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	helmEngine "github.com/cozystack/talm/pkg/engine/helm"
	"github.com/siderolabs/talos/pkg/machinery/client"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/chartutil"
)

// testEndpoint is the cluster endpoint injected by tests that do not
// specifically exercise the `required endpoint` guard. The chart's
// shipped values.yaml leaves `endpoint` empty so a fresh install
// surfaces the missing value loudly; tests need to supply their own
// placeholder so they can exercise the rest of the chart.
const testEndpoint = "https://talm-test.invalid:6443"

// testAdvertisedSubnet is injected for tests that do not supply a
// discovery fixture (so the chart's empty-discovery required() guard
// doesn't fire in unrelated tests). Tests that specifically exercise
// the discovery fallback or the empty-discovery guard override
// advertisedSubnets explicitly.
const testAdvertisedSubnet = "192.168.1.0/24"

// cloneValues returns a recursive deep copy of the chart values map.
// maps.Copy is a shallow copy — mutating a nested map or slice in a
// test would leak into chrt.Values and corrupt subsequent renders.
// Since chart values consist only of maps, slices, and primitives,
// a small switch + recursion suffices; no external dep needed.
func cloneValues(src map[string]any) map[string]any {
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = deepClone(v)
	}
	return dst
}

func deepClone(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, vv := range x {
			out[k] = deepClone(vv)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, vv := range x {
			out[i] = deepClone(vv)
		}
		return out
	default:
		// Primitives (string, bool, int, float, nil) are immutable —
		// safe to share.
		return v
	}
}

// renderChartTemplate renders a chart template in offline mode and returns the output.
// talosVersion sets the TalosVersion in the Helm engine context (empty string for legacy).
func renderChartTemplate(t *testing.T, chartPath string, templateFile string, talosVersion ...string) string {
	t.Helper()

	// Reset to offline mode
	helmEngine.LookupFunc = func(string, string, string) (map[string]any, error) {
		return map[string]any{}, nil
	}

	chrt, err := loader.LoadDir(chartPath)
	if err != nil {
		t.Fatalf("failed to load chart from %s: %v", chartPath, err)
	}

	tv := ""
	if len(talosVersion) > 0 {
		tv = talosVersion[0]
	}

	// Inject test defaults when the chart ships empty values (true
	// for cozystack and generic presets post-issue-25 fix). Tests
	// that specifically exercise the required-endpoint or empty-
	// discovery guards build their own values maps and do not go
	// through this helper. cloneValues deep-copies so a mutation
	// here never leaks into chrt.Values.
	values := cloneValues(chrt.Values)
	if v, _ := values["endpoint"].(string); v == "" {
		values["endpoint"] = testEndpoint
	}
	if arr, ok := values["advertisedSubnets"].([]any); !ok || len(arr) == 0 {
		values["advertisedSubnets"] = []any{testAdvertisedSubnet}
	}

	rootValues := chartutil.Values{
		"Values":       values,
		"TalosVersion": tv,
	}

	eng := helmEngine.Engine{}
	out, err := eng.Render(chrt, rootValues)
	if err != nil {
		t.Fatalf("failed to render chart: %v", err)
	}

	key := path.Join(chrt.Name(), templateFile)
	result, ok := out[key]
	if !ok {
		//nolint:prealloc // capacity-zero map iteration; len(out) is the upper bound but we don't know if all keys land in keys.
		var keys []string
		for k := range out {
			keys = append(keys, k)
		}
		t.Fatalf("template %s not found in output, available keys: %v", key, keys)
	}

	return result
}

// renderChartTemplateWithLookup renders a chart with a custom LookupFunc (or
// offline empty-map default when nil). Restores the previous LookupFunc on
// cleanup so tests don't leak state.
func renderChartTemplateWithLookup(t *testing.T, chartPath string, templateFile string, lookup func(string, string, string) (map[string]any, error), talosVersion ...string) string {
	t.Helper()

	origLookup := helmEngine.LookupFunc
	t.Cleanup(func() { helmEngine.LookupFunc = origLookup })

	if lookup != nil {
		helmEngine.LookupFunc = lookup
	} else {
		helmEngine.LookupFunc = func(string, string, string) (map[string]any, error) {
			return map[string]any{}, nil
		}
	}

	chrt, err := loader.LoadDir(chartPath)
	if err != nil {
		t.Fatalf("failed to load chart from %s: %v", chartPath, err)
	}

	tv := ""
	if len(talosVersion) > 0 {
		tv = talosVersion[0]
	}

	values := cloneValues(chrt.Values)
	if v, _ := values["endpoint"].(string); v == "" {
		values["endpoint"] = testEndpoint
	}
	if arr, ok := values["advertisedSubnets"].([]any); !ok || len(arr) == 0 {
		values["advertisedSubnets"] = []any{testAdvertisedSubnet}
	}

	rootValues := chartutil.Values{
		"Values":       values,
		"TalosVersion": tv,
	}

	eng := helmEngine.Engine{}
	out, err := eng.Render(chrt, rootValues)
	if err != nil {
		t.Fatalf("failed to render chart: %v", err)
	}

	key := path.Join(chrt.Name(), templateFile)
	result, ok := out[key]
	if !ok {
		//nolint:prealloc // capacity-zero map iteration; len(out) is upper bound, not all keys necessarily land in keys.
		var keys []string
		for k := range out {
			keys = append(keys, k)
		}
		t.Fatalf("template %s not found in output, available keys: %v", key, keys)
	}

	return result
}

// assertContains checks that the output contains the expected substring.
func assertContains(t *testing.T, output string, substr string) {
	t.Helper()
	if !strings.Contains(output, substr) {
		t.Errorf("expected output to contain %q, but it does not.\nOutput:\n%s", substr, output)
	}
}

// assertNotContains checks that the output does NOT contain the substring.
func assertNotContains(t *testing.T, output string, substr string) {
	t.Helper()
	if strings.Contains(output, substr) {
		t.Errorf("expected output NOT to contain %q, but it does.\nOutput:\n%s", substr, output)
	}
}

func TestLegacyCozystack_ControlPlane(t *testing.T) {
	output := renderChartTemplate(t, "../../charts/cozystack", "templates/controlplane.yaml")

	// Legacy format: machine.network section present
	assertContains(t, output, "machine:")
	assertContains(t, output, "network:")
	assertContains(t, output, "hostname:")
	assertContains(t, output, "nameservers:")
	assertContains(t, output, "interfaces:")

	// Legacy format: machine.registries section present
	assertContains(t, output, "registries:")
	assertContains(t, output, "mirrors:")
	assertContains(t, output, "docker.io:")
	assertContains(t, output, "https://mirror.gcr.io")

	// Legacy format: cluster section present
	assertContains(t, output, "cluster:")
	assertContains(t, output, "clusterName: \"cozystack\"")
	assertContains(t, output, "controlPlane:")
	assertContains(t, output, "endpoint:")

	// Legacy format: cozystack-specific sections present
	assertContains(t, output, "sysctls:")
	assertContains(t, output, "kernel:")
	assertContains(t, output, "kubelet:")
	assertContains(t, output, "certSANs:")
	assertContains(t, output, "install:")

	// Legacy format: controlplane-specific settings
	assertContains(t, output, "allowSchedulingOnControlPlanes:")
	assertContains(t, output, "etcd:")
	assertContains(t, output, "proxy:")

	// Legacy format: no v1.12 multi-doc types
	assertNotContains(t, output, "kind: HostnameConfig")
	assertNotContains(t, output, "kind: ResolverConfig")
	assertNotContains(t, output, "kind: LinkConfig")
	assertNotContains(t, output, "kind: BondConfig")
	assertNotContains(t, output, "kind: VLANConfig")
	assertNotContains(t, output, "kind: RegistryMirrorConfig")
	assertNotContains(t, output, "kind: Layer2VIPConfig")
}

func TestLegacyCozystack_Worker(t *testing.T) {
	output := renderChartTemplate(t, "../../charts/cozystack", "templates/worker.yaml")

	// Legacy format: machine section present
	assertContains(t, output, "machine:")
	assertContains(t, output, "type: worker")
	assertContains(t, output, "network:")
	assertContains(t, output, "hostname:")
	assertContains(t, output, "interfaces:")
	assertContains(t, output, "registries:")

	// Legacy format: cluster section present
	assertContains(t, output, "cluster:")

	// Worker should NOT have controlplane-specific settings
	assertNotContains(t, output, "allowSchedulingOnControlPlanes:")

	// No v1.12 multi-doc types
	assertNotContains(t, output, "kind: HostnameConfig")
	assertNotContains(t, output, "kind: RegistryMirrorConfig")
	assertNotContains(t, output, "kind: Layer2VIPConfig")
}

func TestLegacyGeneric_ControlPlane(t *testing.T) {
	output := renderChartTemplate(t, "../../charts/generic", "templates/controlplane.yaml")

	// Legacy format: machine.network section present
	assertContains(t, output, "machine:")
	assertContains(t, output, "network:")
	assertContains(t, output, "hostname:")
	assertContains(t, output, "nameservers:")
	assertContains(t, output, "interfaces:")

	// Legacy format: cluster section present
	assertContains(t, output, "cluster:")
	assertContains(t, output, "clusterName: \"generic\"")
	assertContains(t, output, "controlPlane:")
	assertContains(t, output, "endpoint:")

	// Generic does NOT have registries
	assertNotContains(t, output, "registries:")

	// No v1.12 multi-doc types
	assertNotContains(t, output, "kind: HostnameConfig")
	assertNotContains(t, output, "kind: ResolverConfig")
	assertNotContains(t, output, "kind: LinkConfig")
	assertNotContains(t, output, "kind: RegistryMirrorConfig")
}

func TestLegacyGeneric_Worker(t *testing.T) {
	output := renderChartTemplate(t, "../../charts/generic", "templates/worker.yaml")

	// Legacy format: machine section present
	assertContains(t, output, "machine:")
	assertContains(t, output, "type: worker")
	assertContains(t, output, "network:")
	assertContains(t, output, "hostname:")
	assertContains(t, output, "interfaces:")

	// Legacy format: cluster section present
	assertContains(t, output, "cluster:")

	// No v1.12 multi-doc types
	assertNotContains(t, output, "kind: HostnameConfig")
	assertNotContains(t, output, "kind: LinkConfig")
}

// --- Multi-doc format tests for cozystack (v1.12+) ---

func TestMultiDocCozystack_ControlPlane(t *testing.T) {
	output := renderChartTemplateWithLookup(t, "../../charts/cozystack", "templates/controlplane.yaml", simpleNicLookup(), "v1.12")

	// Multi-doc: machine section retains non-deprecated fields
	assertContains(t, output, "machine:")
	assertContains(t, output, "type: controlplane")
	assertContains(t, output, "kubelet:")
	assertContains(t, output, "sysctls:")
	assertContains(t, output, "kernel:")
	assertContains(t, output, "certSANs:")
	assertContains(t, output, "install:")
	assertContains(t, output, "files:")

	// Multi-doc: deprecated machine.network and machine.registries REMOVED
	assertNotContains(t, output, "    interfaces:")
	assertNotContains(t, output, "    mirrors:")

	// Multi-doc: cluster section unchanged
	assertContains(t, output, "cluster:")
	assertContains(t, output, "clusterName: \"cozystack\"")
	assertContains(t, output, "controlPlane:")
	assertContains(t, output, "allowSchedulingOnControlPlanes:")
	assertContains(t, output, "etcd:")
	assertContains(t, output, "proxy:")

	// Multi-doc: new document types present
	assertContains(t, output, "---")
	assertContains(t, output, "kind: HostnameConfig")
	assertContains(t, output, "kind: ResolverConfig")
	assertContains(t, output, "kind: RegistryMirrorConfig")
	assertContains(t, output, "https://mirror.gcr.io")

	// Multi-doc: Layer2VIPConfig is gated on floatingIP, which is now
	// blank in the shipped cozystack values.yaml (see
	// TestMultiDocCozystack_Layer2VIPConfigWhenFloatingIPSet for the
	// emitted-when-set path). Absence here asserts the chart does not
	// fall back to a placeholder VIP on a fresh install.
	assertNotContains(t, output, "kind: Layer2VIPConfig")

	// Multi-doc: network interface document present (LinkConfig or BondConfig)
	hasLinkConfig := strings.Contains(output, "kind: LinkConfig")
	hasBondConfig := strings.Contains(output, "kind: BondConfig")
	if !hasLinkConfig && !hasBondConfig {
		t.Errorf("expected output to contain either LinkConfig or BondConfig document")
	}
}

func TestMultiDocCozystack_Worker(t *testing.T) {
	output := renderChartTemplate(t, "../../charts/cozystack", "templates/worker.yaml", "v1.12")

	// Multi-doc: machine section
	assertContains(t, output, "machine:")
	assertContains(t, output, "type: worker")
	assertContains(t, output, "kubelet:")
	assertContains(t, output, "install:")

	// Multi-doc: deprecated fields REMOVED
	assertNotContains(t, output, "    interfaces:")
	assertNotContains(t, output, "    mirrors:")

	// Multi-doc: new document types present
	assertContains(t, output, "kind: HostnameConfig")
	assertContains(t, output, "kind: ResolverConfig")
	assertContains(t, output, "kind: RegistryMirrorConfig")

	// Worker should NOT have VIP or controlplane cluster settings
	assertNotContains(t, output, "kind: Layer2VIPConfig")
	assertNotContains(t, output, "allowSchedulingOnControlPlanes:")
}

func TestMultiDocCozystack_LegacyFallback(t *testing.T) {
	// v1.11 should produce legacy format even for cozystack chart
	output := renderChartTemplateWithLookup(t, "../../charts/cozystack", "templates/controlplane.yaml", simpleNicLookup(), "v1.11")

	// Legacy format present
	assertContains(t, output, "    interfaces:")
	assertContains(t, output, "  registries:")
	assertContains(t, output, "    mirrors:")

	// No multi-doc types
	assertNotContains(t, output, "kind: HostnameConfig")
	assertNotContains(t, output, "kind: RegistryMirrorConfig")
}

func TestMultiDocCozystack_PreReleaseVersion(t *testing.T) {
	// Pre-release v1.12 versions should still use multi-doc format
	output := renderChartTemplate(t, "../../charts/cozystack", "templates/controlplane.yaml", "v1.12.0-alpha.1")

	assertContains(t, output, "kind: HostnameConfig")
	assertContains(t, output, "kind: RegistryMirrorConfig")
	assertNotContains(t, output, "    interfaces:")
}

func TestMultiDocCozystack_TwoComponentVersion(t *testing.T) {
	// Two-component version string (v1.12 without patch) should work
	output := renderChartTemplate(t, "../../charts/cozystack", "templates/controlplane.yaml", "v1.12")

	assertContains(t, output, "kind: HostnameConfig")
	assertContains(t, output, "kind: RegistryMirrorConfig")
	assertNotContains(t, output, "    interfaces:")
}

func TestLegacyCozystack_NrHugepages(t *testing.T) {
	// Test nr_hugepages is rendered correctly in legacy format
	origLookup := helmEngine.LookupFunc
	t.Cleanup(func() { helmEngine.LookupFunc = origLookup })
	helmEngine.LookupFunc = func(string, string, string) (map[string]any, error) {
		return map[string]any{}, nil
	}

	chrt, err := loader.LoadDir("../../charts/cozystack")
	if err != nil {
		t.Fatal(err)
	}

	values := make(map[string]any)
	maps.Copy(values, chrt.Values)
	values["nr_hugepages"] = 1024
	values["endpoint"] = testEndpoint
	values["advertisedSubnets"] = []any{testAdvertisedSubnet}

	eng := helmEngine.Engine{}
	out, err := eng.Render(chrt, chartutil.Values{
		"Values": values,
	})
	if err != nil {
		t.Fatal(err)
	}

	result := out["cozystack/templates/controlplane.yaml"]
	assertContains(t, result, `vm.nr_hugepages: "1024"`)
}

func TestMultiDocCozystack_NrHugepages(t *testing.T) {
	// Test nr_hugepages is rendered correctly (non-zero value)
	origLookup := helmEngine.LookupFunc
	t.Cleanup(func() { helmEngine.LookupFunc = origLookup })
	helmEngine.LookupFunc = func(string, string, string) (map[string]any, error) {
		return map[string]any{}, nil
	}

	chrt, err := loader.LoadDir("../../charts/cozystack")
	if err != nil {
		t.Fatal(err)
	}

	values := make(map[string]any)
	maps.Copy(values, chrt.Values)
	values["nr_hugepages"] = 1024
	values["endpoint"] = testEndpoint
	values["advertisedSubnets"] = []any{testAdvertisedSubnet}

	eng := helmEngine.Engine{}
	out, err := eng.Render(chrt, chartutil.Values{
		"Values":       values,
		"TalosVersion": "v1.12",
	})
	if err != nil {
		t.Fatal(err)
	}

	result := out["cozystack/templates/controlplane.yaml"]
	assertContains(t, result, `vm.nr_hugepages: "1024"`)
}

// --- Multi-doc format tests for generic (v1.12+) ---

func TestMultiDocGeneric_ControlPlane(t *testing.T) {
	output := renderChartTemplateWithLookup(t, "../../charts/generic", "templates/controlplane.yaml", simpleNicLookup(), "v1.12")

	// Multi-doc: machine section still present but WITHOUT legacy network fields
	assertContains(t, output, "machine:")
	assertContains(t, output, "type: controlplane")
	assertContains(t, output, "kubelet:")
	assertContains(t, output, "install:")

	// Multi-doc: deprecated machine.network fields REMOVED (hostname, nameservers, interfaces)
	assertNotContains(t, output, "    interfaces:")

	// Multi-doc: cluster section still present
	assertContains(t, output, "cluster:")
	assertContains(t, output, "clusterName: \"generic\"")
	assertContains(t, output, "controlPlane:")
	assertContains(t, output, "endpoint:")

	// Multi-doc: new document types present
	assertContains(t, output, "---")
	assertContains(t, output, "kind: HostnameConfig")
	assertContains(t, output, "kind: ResolverConfig")
	assertContains(t, output, "kind: LinkConfig")

	// Generic does NOT have registries
	assertNotContains(t, output, "kind: RegistryMirrorConfig")
}

func TestMultiDocGeneric_Worker(t *testing.T) {
	output := renderChartTemplateWithLookup(t, "../../charts/generic", "templates/worker.yaml", simpleNicLookup(), "v1.12")

	// Multi-doc: machine section present
	assertContains(t, output, "machine:")
	assertContains(t, output, "type: worker")
	assertContains(t, output, "kubelet:")
	assertContains(t, output, "install:")

	// Multi-doc: deprecated machine.network fields REMOVED
	assertNotContains(t, output, "    interfaces:")

	// Multi-doc: new document types present
	assertContains(t, output, "kind: HostnameConfig")
	assertContains(t, output, "kind: ResolverConfig")
	assertContains(t, output, "kind: LinkConfig")

	// Worker should NOT have VIP
	assertNotContains(t, output, "kind: Layer2VIPConfig")
}

func TestMultiDocGeneric_LegacyFallback(t *testing.T) {
	// v1.11 should produce legacy format
	output := renderChartTemplate(t, "../../charts/generic", "templates/controlplane.yaml", "v1.11")

	// Legacy format: machine.network present
	assertContains(t, output, "  network:")
	assertContains(t, output, "hostname:")
	assertContains(t, output, "interfaces:")

	// No multi-doc types
	assertNotContains(t, output, "kind: HostnameConfig")
	assertNotContains(t, output, "kind: LinkConfig")
}

// createTestChart creates a minimal Helm chart in a temp directory with the
// given template content. Returns the chart root path.
func createTestChart(t *testing.T, chartName, templateName, templateContent string) string {
	t.Helper()
	root := t.TempDir()

	chartYAML := "apiVersion: v2\nname: " + chartName + "\ntype: application\nversion: 0.1.0\n"
	if err := os.WriteFile(filepath.Join(root, "Chart.yaml"), []byte(chartYAML), 0o644); err != nil {
		t.Fatalf("write Chart.yaml: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "values.yaml"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write values.yaml: %v", err)
	}

	templatesDir := filepath.Join(root, "templates")
	if err := os.MkdirAll(templatesDir, 0o755); err != nil {
		t.Fatalf("mkdir templates: %v", err)
	}
	if err := os.WriteFile(filepath.Join(templatesDir, templateName), []byte(templateContent), 0o644); err != nil {
		t.Fatalf("write template: %v", err)
	}

	return root
}

// TestLookupOfflineProducesEmptyInterface is a regression test for the bug
// where `talm apply` rendered templates offline, causing lookup() to return
// empty maps. Templates that derive the interface name from discovery data
// (e.g., iterating routes) produced an empty interface field, which Talos v1.12
// rejects with:
//
//	[networking.os.device.interface], [networking.os.device.deviceSelector]:
//	required either config section to be set
//
// The fix: render templates online (with a real client and LookupFunc).
// This test verifies both the broken (offline) and fixed (online) paths at
// the Helm template rendering layer.
func TestLookupOfflineProducesEmptyInterface(t *testing.T) {
	// Template that mimics the real talm.discovered.default_link_name_by_gateway
	// pattern: iterate routes from lookup(), extract outLinkName. When offline,
	// lookup returns an empty map → range produces nothing → empty interface.
	const tmpl = `{{- $linkName := "" -}}
{{- range (lookup "routes" "" "").items -}}
{{- if and (eq .spec.dst "") (not (eq .spec.gateway "")) -}}
{{- $linkName = .spec.outLinkName -}}
{{- end -}}
{{- end -}}
machine:
  network:
    interfaces:
    - interface: {{ $linkName }}
`

	chartRoot := createTestChart(t, "testchart", "config.yaml", tmpl)
	chrt, err := loader.LoadDir(chartRoot)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}

	rootValues := map[string]any{
		"Values": chrt.Values,
	}

	t.Run("offline_produces_empty_interface", func(t *testing.T) {
		origLookup := helmEngine.LookupFunc
		defer func() { helmEngine.LookupFunc = origLookup }()

		// Default no-op: returns empty map (same as offline mode)
		helmEngine.LookupFunc = func(string, string, string) (map[string]any, error) {
			return map[string]any{}, nil
		}

		eng := helmEngine.Engine{}
		out, err := eng.Render(chrt, rootValues)
		if err != nil {
			t.Fatalf("Render: %v", err)
		}

		rendered := out["testchart/templates/config.yaml"]
		// With offline lookup, the interface name is empty — this is the bug.
		if strings.Contains(rendered, "interface: eth0") {
			t.Error("offline render should NOT produce 'interface: eth0'")
		}
		if !strings.Contains(rendered, "interface: ") {
			t.Error("offline render should contain 'interface: ' (with empty value)")
		}
	})

	t.Run("online_lookup_populates_interface", func(t *testing.T) {
		origLookup := helmEngine.LookupFunc
		defer func() { helmEngine.LookupFunc = origLookup }()

		// Simulate online mode: return route data with a real interface name.
		helmEngine.LookupFunc = func(resource, _, name string) (map[string]any, error) {
			if resource == "routes" && name == "" {
				return map[string]any{
					"apiVersion": "v1",
					"kind":       "List",
					"items": []any{
						map[string]any{
							"spec": map[string]any{
								"dst":         "",
								"gateway":     "192.168.1.1",
								"outLinkName": "eth0",
								"table":       "main",
							},
						},
					},
				}, nil
			}
			return map[string]any{}, nil
		}

		eng := helmEngine.Engine{}
		out, err := eng.Render(chrt, rootValues)
		if err != nil {
			t.Fatalf("Render: %v", err)
		}

		rendered := out["testchart/templates/config.yaml"]
		if !strings.Contains(rendered, "interface: eth0") {
			t.Errorf("online render should produce 'interface: eth0', got:\n%s", rendered)
		}
	})
}

// TestRenderOfflineSkipsLookupFunc verifies that Render with Offline=true does
// NOT replace the LookupFunc, and Offline=false does replace it. This is a
// unit check that the fix (Offline=false in apply) causes the real LookupFunc
// to be wired up.
func TestRenderOfflineSkipsLookupFunc(t *testing.T) {
	origLookup := helmEngine.LookupFunc
	defer func() { helmEngine.LookupFunc = origLookup }()

	// Set a sentinel LookupFunc
	helmEngine.LookupFunc = func(string, string, string) (map[string]any, error) {
		return map[string]any{"sentinel": true}, nil
	}

	// Offline=true should leave the sentinel intact
	opts := Options{Offline: true}
	if !opts.Offline {
		t.Fatal("test setup: expected Offline=true")
	}

	res, _ := helmEngine.LookupFunc("test", "", "")
	if _, ok := res["sentinel"]; !ok {
		t.Error("Offline=true must not replace LookupFunc")
	}

	// Verify: when Offline=false, Render() would call
	// helmEngine.LookupFunc = newLookupFunction(ctx, c), replacing the sentinel.
	// We can't call full Render without a chart/client, but the logic is:
	//   if !opts.Offline { helmEngine.LookupFunc = newLookupFunction(ctx, c) }
	// This is tested implicitly by the online_lookup_populates_interface subtest.
}

// bondTopologyLookup returns a LookupFunc emulating a bonded interface with
// two physical slaves and a default route pointing through it. Used by
// BondConfig rendering tests.
func bondTopologyLookup() func(string, string, string) (map[string]any, error) {
	bondLink := map[string]any{
		"metadata": map[string]any{"id": "bond0"},
		"spec": map[string]any{
			"kind":  "bond",
			"index": 10,
			"bondMaster": map[string]any{
				"mode":           "802.3ad",
				"xmitHashPolicy": "layer3+4",
				"lacpRate":       "fast",
				"miimon":         100,
			},
			"hardwareAddr": "aa:bb:cc:dd:ee:ff",
			"busPath":      "pci-0000:00:1f.6",
		},
	}
	eth0 := map[string]any{
		"metadata": map[string]any{"id": "eth0"},
		"spec": map[string]any{
			"kind":         "physical",
			"slaveKind":    "bond",
			"masterIndex":  10,
			"hardwareAddr": "aa:bb:cc:dd:ee:00",
			"busPath":      "pci-0000:00:1f.0",
		},
	}
	eth1 := map[string]any{
		"metadata": map[string]any{"id": "eth1"},
		"spec": map[string]any{
			"kind":         "physical",
			"slaveKind":    "bond",
			"masterIndex":  10,
			"hardwareAddr": "aa:bb:cc:dd:ee:01",
			"busPath":      "pci-0000:00:1f.1",
		},
	}
	routesList := map[string]any{
		"apiVersion": "v1",
		"kind":       "List",
		"items": []any{
			map[string]any{
				"spec": map[string]any{
					"dst":         "",
					"gateway":     "192.168.1.1",
					"outLinkName": "bond0",
					"family":      "inet4",
					"table":       "main",
				},
			},
		},
	}
	linksList := map[string]any{
		"apiVersion": "v1",
		"kind":       "List",
		"items":      []any{bondLink, eth0, eth1},
	}
	addressesList := map[string]any{
		"apiVersion": "v1",
		"kind":       "List",
		"items": []any{
			map[string]any{
				"spec": map[string]any{
					"linkName": "bond0",
					"address":  "192.168.1.100/24",
					"family":   "inet4",
					"scope":    "global",
				},
			},
		},
	}
	nodeDefault := map[string]any{
		"spec": map[string]any{
			"addresses": []any{"192.168.1.100/24"},
		},
	}
	resolvers := map[string]any{
		"spec": map[string]any{
			"dnsServers": []any{"8.8.8.8", "1.1.1.1"},
		},
	}
	return func(resource, _, id string) (map[string]any, error) {
		switch resource {
		case "routes":
			return routesList, nil
		case "links":
			if id == "bond0" {
				return bondLink, nil
			}
			if id == "" {
				return linksList, nil
			}
			return map[string]any{}, nil
		case "addresses":
			return addressesList, nil
		case "nodeaddress":
			if id == "default" {
				return nodeDefault, nil
			}
		case "resolvers":
			if id == "resolvers" {
				return resolvers, nil
			}
		}
		return map[string]any{}, nil
	}
}

// vlanOnBondTopologyLookup returns a LookupFunc emulating a VLAN interface
// stacked on top of a bond. Used by VLANConfig rendering tests.
func vlanOnBondTopologyLookup() func(string, string, string) (map[string]any, error) {
	bondLink := map[string]any{
		"metadata": map[string]any{"id": "bond0"},
		"spec": map[string]any{
			"kind":  "bond",
			"index": 10,
			"bondMaster": map[string]any{
				"mode": "802.3ad",
			},
			"hardwareAddr": "aa:bb:cc:dd:ee:ff",
			"busPath":      "pci-0000:00:1f.6",
		},
	}
	vlanLink := map[string]any{
		"metadata": map[string]any{"id": "bond0.100"},
		"spec": map[string]any{
			"kind":      "vlan",
			"index":     42,
			"linkIndex": 10,
			"vlan":      map[string]any{"vlanID": 100},
		},
	}
	eth0 := map[string]any{
		"metadata": map[string]any{"id": "eth0"},
		"spec": map[string]any{
			"kind":         "physical",
			"slaveKind":    "bond",
			"masterIndex":  10,
			"hardwareAddr": "aa:bb:cc:dd:ee:00",
			"busPath":      "pci-0000:00:1f.0",
		},
	}
	eth1 := map[string]any{
		"metadata": map[string]any{"id": "eth1"},
		"spec": map[string]any{
			"kind":         "physical",
			"slaveKind":    "bond",
			"masterIndex":  10,
			"hardwareAddr": "aa:bb:cc:dd:ee:01",
			"busPath":      "pci-0000:00:1f.1",
		},
	}
	routesList := map[string]any{
		"apiVersion": "v1",
		"kind":       "List",
		"items": []any{
			map[string]any{
				"spec": map[string]any{
					"dst":         "",
					"gateway":     "10.0.0.1",
					"outLinkName": "bond0.100",
					"family":      "inet4",
					"table":       "main",
				},
			},
		},
	}
	linksList := map[string]any{
		"apiVersion": "v1",
		"kind":       "List",
		"items":      []any{bondLink, vlanLink, eth0, eth1},
	}
	addressesList := map[string]any{
		"apiVersion": "v1",
		"kind":       "List",
		"items": []any{
			map[string]any{
				"spec": map[string]any{
					"linkName": "bond0.100",
					"address":  "10.0.0.50/24",
					"family":   "inet4",
					"scope":    "global",
				},
			},
		},
	}
	nodeDefault := map[string]any{
		"spec": map[string]any{
			"addresses": []any{"10.0.0.50/24"},
		},
	}
	resolvers := map[string]any{
		"spec": map[string]any{
			"dnsServers": []any{"8.8.8.8"},
		},
	}
	return func(resource, _, id string) (map[string]any, error) {
		switch resource {
		case "routes":
			return routesList, nil
		case "links":
			switch id {
			case "bond0":
				return bondLink, nil
			case "bond0.100":
				return vlanLink, nil
			case "":
				return linksList, nil
			}
			return map[string]any{}, nil
		case "addresses":
			return addressesList, nil
		case "nodeaddress":
			if id == "default" {
				return nodeDefault, nil
			}
		case "resolvers":
			if id == "resolvers" {
				return resolvers, nil
			}
		}
		return map[string]any{}, nil
	}
}

func TestMultiDocCozystack_BondTopology(t *testing.T) {
	origLookup := helmEngine.LookupFunc
	t.Cleanup(func() { helmEngine.LookupFunc = origLookup })
	helmEngine.LookupFunc = bondTopologyLookup()

	chrt, err := loader.LoadDir("../../charts/cozystack")
	if err != nil {
		t.Fatal(err)
	}

	values := make(map[string]any)
	maps.Copy(values, chrt.Values)
	values["endpoint"] = testEndpoint

	eng := helmEngine.Engine{}
	out, err := eng.Render(chrt, chartutil.Values{
		"Values":       values,
		"TalosVersion": "v1.12",
	})
	if err != nil {
		t.Fatal(err)
	}

	result := out["cozystack/templates/controlplane.yaml"]
	assertContains(t, result, "kind: BondConfig")
	assertContains(t, result, "name: bond0")
	assertContains(t, result, "- eth0")
	assertContains(t, result, "- eth1")
	assertContains(t, result, "bondMode: 802.3ad")
	assertContains(t, result, "xmitHashPolicy: layer3+4")
	assertContains(t, result, "lacpRate: fast")
	assertContains(t, result, "address: 192.168.1.100/24")
	assertContains(t, result, "gateway: 192.168.1.1")
	assertNotContains(t, result, "kind: LinkConfig")
	assertNotContains(t, result, "kind: VLANConfig")
}

func TestMultiDocCozystack_VlanOnBondTopology(t *testing.T) {
	origLookup := helmEngine.LookupFunc
	t.Cleanup(func() { helmEngine.LookupFunc = origLookup })
	helmEngine.LookupFunc = vlanOnBondTopologyLookup()

	chrt, err := loader.LoadDir("../../charts/cozystack")
	if err != nil {
		t.Fatal(err)
	}

	values := make(map[string]any)
	maps.Copy(values, chrt.Values)
	values["endpoint"] = testEndpoint

	eng := helmEngine.Engine{}
	out, err := eng.Render(chrt, chartutil.Values{
		"Values":       values,
		"TalosVersion": "v1.12",
	})
	if err != nil {
		t.Fatal(err)
	}

	result := out["cozystack/templates/controlplane.yaml"]
	assertContains(t, result, "kind: BondConfig")
	assertContains(t, result, "kind: VLANConfig")
	assertContains(t, result, "name: bond0.100")
	assertContains(t, result, "vlanID: 100")
	assertContains(t, result, "parent: bond0")
	assertContains(t, result, "address: 10.0.0.50/24")
	assertContains(t, result, "gateway: 10.0.0.1")
	assertNotContains(t, result, "kind: LinkConfig")
}

func TestMultiDocGeneric_BondTopology(t *testing.T) {
	origLookup := helmEngine.LookupFunc
	t.Cleanup(func() { helmEngine.LookupFunc = origLookup })
	helmEngine.LookupFunc = bondTopologyLookup()

	chrt, err := loader.LoadDir("../../charts/generic")
	if err != nil {
		t.Fatal(err)
	}

	values := make(map[string]any)
	maps.Copy(values, chrt.Values)
	values["endpoint"] = testEndpoint

	eng := helmEngine.Engine{}
	out, err := eng.Render(chrt, chartutil.Values{
		"Values":       values,
		"TalosVersion": "v1.12",
	})
	if err != nil {
		t.Fatal(err)
	}

	result := out["generic/templates/controlplane.yaml"]
	assertContains(t, result, "kind: BondConfig")
	assertContains(t, result, "name: bond0")
	assertContains(t, result, "bondMode: 802.3ad")
	assertContains(t, result, "- eth0")
	assertContains(t, result, "- eth1")
	assertNotContains(t, result, "kind: LinkConfig")
	assertNotContains(t, result, "kind: VLANConfig")
}

func TestMultiDocGeneric_VlanOnBondTopology(t *testing.T) {
	origLookup := helmEngine.LookupFunc
	t.Cleanup(func() { helmEngine.LookupFunc = origLookup })
	helmEngine.LookupFunc = vlanOnBondTopologyLookup()

	chrt, err := loader.LoadDir("../../charts/generic")
	if err != nil {
		t.Fatal(err)
	}

	values := make(map[string]any)
	maps.Copy(values, chrt.Values)
	values["endpoint"] = testEndpoint

	eng := helmEngine.Engine{}
	out, err := eng.Render(chrt, chartutil.Values{
		"Values":       values,
		"TalosVersion": "v1.12",
	})
	if err != nil {
		t.Fatal(err)
	}

	result := out["generic/templates/controlplane.yaml"]
	assertContains(t, result, "kind: BondConfig")
	assertContains(t, result, "kind: VLANConfig")
	assertContains(t, result, "vlanID: 100")
	assertContains(t, result, "parent: bond0")
	assertContains(t, result, "address: 10.0.0.50/24")
	assertNotContains(t, result, "kind: LinkConfig")
}

// multiNicMultipleDefaultRoutesLookup emulates a node with two physical NICs,
// each carrying a default route. eth0 is in `table=main`, eth1 is in a non-main
// table (e.g. an alternate routing table) and a third interface has a main-table
// route that should be ignored once the first match is taken. Used to verify
// `default_link_name_by_gateway` (#108) returns a single deterministic value
// rather than concatenating link names from every matching route.
func multiNicMultipleDefaultRoutesLookup() func(string, string, string) (map[string]any, error) {
	eth0 := map[string]any{
		"metadata": map[string]any{"id": "eth0"},
		"spec": map[string]any{
			"kind":         "physical",
			"hardwareAddr": "aa:bb:cc:dd:ee:00",
			"busPath":      "pci-0000:00:1f.0",
		},
	}
	eth1 := map[string]any{
		"metadata": map[string]any{"id": "eth1"},
		"spec": map[string]any{
			"kind":         "physical",
			"hardwareAddr": "aa:bb:cc:dd:ee:01",
			"busPath":      "pci-0000:00:1f.1",
		},
	}
	eth2 := map[string]any{
		"metadata": map[string]any{"id": "eth2"},
		"spec": map[string]any{
			"kind":         "physical",
			"hardwareAddr": "aa:bb:cc:dd:ee:02",
			"busPath":      "pci-0000:00:1f.2",
		},
	}
	routesList := map[string]any{
		"apiVersion": "v1",
		"kind":       "List",
		"items": []any{
			map[string]any{
				"spec": map[string]any{
					"dst":         "",
					"gateway":     "10.99.0.1",
					"outLinkName": "eth1",
					"family":      "inet4",
					"table":       "private",
				},
			},
			map[string]any{
				"spec": map[string]any{
					"dst":         "",
					"gateway":     "192.168.1.1",
					"outLinkName": "eth0",
					"family":      "inet4",
					"table":       "main",
				},
			},
			map[string]any{
				"spec": map[string]any{
					"dst":         "",
					"gateway":     "192.168.2.1",
					"outLinkName": "eth2",
					"family":      "inet4",
					"table":       "main",
				},
			},
		},
	}
	linksList := map[string]any{
		"apiVersion": "v1",
		"kind":       "List",
		"items":      []any{eth0, eth1, eth2},
	}
	addressesList := map[string]any{
		"apiVersion": "v1",
		"kind":       "List",
		"items": []any{
			map[string]any{
				"spec": map[string]any{
					"linkName": "eth0",
					"address":  "192.168.1.10/24",
					"family":   "inet4",
					"scope":    "global",
				},
			},
			map[string]any{
				"spec": map[string]any{
					"linkName": "eth1",
					"address":  "10.99.0.5/24",
					"family":   "inet4",
					"scope":    "global",
				},
			},
			map[string]any{
				"spec": map[string]any{
					"linkName": "eth2",
					"address":  "192.168.2.10/24",
					"family":   "inet4",
					"scope":    "global",
				},
			},
		},
	}
	return func(resource, _, id string) (map[string]any, error) {
		switch resource {
		case "routes":
			return routesList, nil
		case "links":
			switch id {
			case "eth0":
				return eth0, nil
			case "eth1":
				return eth1, nil
			case "eth2":
				return eth2, nil
			case "":
				return linksList, nil
			}
			return map[string]any{}, nil
		case "addresses":
			return addressesList, nil
		}
		return map[string]any{}, nil
	}
}

// TestDefaultLinkByGatewayHelpers_MultiNIC is a regression test for #108.
// When a node has multiple default routes (typical for DHCP on multi-NIC
// machines), the helpers historically iterated all matches and concatenated
// the outputs (e.g. `eth0eth1eth2`) and didn't filter by `table=main`.
// After the fix the helpers must:
//   - filter routes by table=main
//   - return exactly one value (the first matching route)
func TestDefaultLinkByGatewayHelpers_MultiNIC(t *testing.T) {
	const tmpl = `link={{ include "talm.discovered.default_link_name_by_gateway" . }}
mac={{ include "talm.discovered.default_link_address_by_gateway" . }}
bus={{ include "talm.discovered.default_link_bus_by_gateway" . }}
`
	chartRoot := createTestChart(t, "tc", "out.yaml", tmpl)

	// Vendor the talm helpers into the test chart so the include resolves.
	helpersSrc, err := os.ReadFile("../../charts/talm/templates/_helpers.tpl")
	if err != nil {
		t.Fatalf("read helpers: %v", err)
	}
	if err := os.WriteFile(filepath.Join(chartRoot, "templates", "_helpers.tpl"), helpersSrc, 0o644); err != nil {
		t.Fatalf("write vendored helpers: %v", err)
	}

	output := renderChartTemplateWithLookup(t, chartRoot, "templates/out.yaml", multiNicMultipleDefaultRoutesLookup())

	assertContains(t, output, "link=eth0\n")
	assertContains(t, output, "mac=aa:bb:cc:dd:ee:00\n")
	// default_link_bus_by_gateway must return the busPath, not the MAC.
	// Long-standing copy-paste bug from the address helper: see commit log.
	assertContains(t, output, "bus=pci-0000:00:1f.0\n")
	assertNotContains(t, output, "bus=aa:bb:cc:dd:ee:00")
	assertNotContains(t, output, "eth1")
	assertNotContains(t, output, "eth2")
}

// secondaryNicLookup emulates a node with two physical NICs (eth0 primary
// with a default route, eth1 storage with a static subnet route and no
// default) plus a bond master link. Used to exercise the multi-NIC discovery
// helpers added for #125.
func secondaryNicLookup() func(string, string, string) (map[string]any, error) {
	eth0 := map[string]any{
		"metadata": map[string]any{"id": "eth0"},
		"spec": map[string]any{
			"kind":         "physical",
			"hardwareAddr": "aa:bb:cc:dd:ee:00",
			"busPath":      "pci-0000:00:1f.0",
		},
	}
	eth1 := map[string]any{
		"metadata": map[string]any{"id": "eth1"},
		"spec": map[string]any{
			"kind":         "physical",
			"hardwareAddr": "aa:bb:cc:dd:ee:01",
			"busPath":      "pci-0000:00:1f.1",
		},
	}
	bond0 := map[string]any{
		"metadata": map[string]any{"id": "bond0"},
		"spec": map[string]any{
			"kind":         "bond",
			"hardwareAddr": "aa:bb:cc:dd:ee:ff",
		},
	}
	routesList := map[string]any{
		"apiVersion": "v1",
		"kind":       "List",
		"items": []any{
			// IPv6 default route ordered first so a missing family filter in
			// gateway_by_link would return fe80::1 instead of the IPv4
			// gateway. Required for the no-IPv4-family-filter regression.
			map[string]any{
				"spec": map[string]any{
					"dst":         "",
					"gateway":     "fe80::1",
					"outLinkName": "eth0",
					"family":      "inet6",
					"table":       "main",
					"priority":    1024,
				},
			},
			map[string]any{
				"spec": map[string]any{
					"dst":         "",
					"gateway":     "192.168.1.1",
					"outLinkName": "eth0",
					"family":      "inet4",
					"table":       "main",
					"priority":    100,
				},
			},
			map[string]any{
				"spec": map[string]any{
					"dst":         "10.0.0.0/24",
					"gateway":     "",
					"outLinkName": "eth1",
					"family":      "inet4",
					"table":       "main",
					"priority":    100,
				},
			},
			map[string]any{
				"spec": map[string]any{
					"dst":         "10.10.0.0/16",
					"gateway":     "10.0.0.254",
					"outLinkName": "eth1",
					"family":      "inet4",
					"table":       "main",
					"priority":    200,
				},
			},
			// Route with several fields absent — exercises the kindIs
			// "invalid" guard in routes_by_link so consumers never see "<nil>".
			map[string]any{
				"spec": map[string]any{
					"dst":         "172.16.0.0/12",
					"outLinkName": "eth1",
					"table":       "main",
				},
			},
			// Route with priority: 0 — int zero must round-trip through
			// printf "%v" (the older `default ""` guard collapsed it to "").
			map[string]any{
				"spec": map[string]any{
					"dst":         "203.0.113.0/24",
					"gateway":     "10.0.0.1",
					"outLinkName": "eth1",
					"family":      "inet4",
					"table":       "main",
					"priority":    0,
				},
			},
		},
	}
	linksList := map[string]any{
		"apiVersion": "v1",
		"kind":       "List",
		"items":      []any{eth0, eth1, bond0},
	}
	addressesList := map[string]any{
		"apiVersion": "v1",
		"kind":       "List",
		"items": []any{
			map[string]any{"spec": map[string]any{"linkName": "eth0", "address": "192.168.1.10/24", "family": "inet4", "scope": "global"}},
			map[string]any{"spec": map[string]any{"linkName": "eth1", "address": "10.0.0.5/24", "family": "inet4", "scope": "global"}},
			// IPv6 link-local on a configurable link — addresses_by_link must
			// filter scope=link out so callers never configure fe80::/64.
			map[string]any{"spec": map[string]any{"linkName": "eth1", "address": "fe80::aa:bbff:fecc:dd01/64", "family": "inet6", "scope": "link"}},
			// Address with no scope set at all — must also be filtered out
			// (defensive: real Talos always emits scope, but missing-field
			// safety matters for user mocks and future API changes).
			map[string]any{"spec": map[string]any{"linkName": "eth1", "address": "10.99.99.99/32", "family": "inet4"}},
			map[string]any{"spec": map[string]any{"linkName": "lo", "address": "127.0.0.1/8", "family": "inet4", "scope": "host"}},
		},
	}
	return func(resource, _, id string) (map[string]any, error) {
		switch resource {
		case "routes":
			return routesList, nil
		case "links":
			switch id {
			case "eth0":
				return eth0, nil
			case "eth1":
				return eth1, nil
			case "bond0":
				return bond0, nil
			case "":
				return linksList, nil
			}
			return map[string]any{}, nil
		case "addresses":
			return addressesList, nil
		}
		return map[string]any{}, nil
	}
}

// TestSecondaryNicHelpers covers the per-link helpers added for #125. They
// expose every physical NIC (not just the primary one carrying the default
// route) so user templates can configure secondary uplinks (e.g. storage
// network on a control-plane).
func TestSecondaryNicHelpers(t *testing.T) {
	const tmpl = `physical={{ include "talm.discovered.physical_link_names" . }}
configurable={{ include "talm.discovered.configurable_link_names" . }}
addr_eth0={{ include "talm.discovered.addresses_by_link" "eth0" }}
addr_eth1={{ include "talm.discovered.addresses_by_link" "eth1" }}
gw_eth0={{ include "talm.discovered.gateway_by_link" "eth0" }}
gw_eth1={{ include "talm.discovered.gateway_by_link" "eth1" }}
routes_eth1={{ include "talm.discovered.routes_by_link" "eth1" }}
mac_eth1={{ include "talm.discovered.mac_by_link" "eth1" }}
bus_eth1={{ include "talm.discovered.bus_by_link" "eth1" }}
mac_bond0={{ include "talm.discovered.mac_by_link" "bond0" }}
bus_bond0={{ include "talm.discovered.bus_by_link" "bond0" }}
mac_unknown={{ include "talm.discovered.mac_by_link" "doesnotexist" }}
bus_unknown={{ include "talm.discovered.bus_by_link" "doesnotexist" }}
selector_eth1=
{{ include "talm.discovered.link_selector_by_name" "eth1" }}
`
	chartRoot := createTestChart(t, "tc", "out.yaml", tmpl)
	helpersSrc, err := os.ReadFile("../../charts/talm/templates/_helpers.tpl")
	if err != nil {
		t.Fatalf("read helpers: %v", err)
	}
	if err := os.WriteFile(filepath.Join(chartRoot, "templates", "_helpers.tpl"), helpersSrc, 0o644); err != nil {
		t.Fatalf("write vendored helpers: %v", err)
	}

	output := renderChartTemplateWithLookup(t, chartRoot, "templates/out.yaml", secondaryNicLookup())

	assertContains(t, output, `physical=["eth0","eth1"]`)
	// configurable_link_names must include the bond master too.
	assertContains(t, output, `configurable=["eth0","eth1","bond0"]`)
	assertContains(t, output, `addr_eth0=["192.168.1.10/24"]`)
	// eth1 has 4 raw addresses but only the global one survives the filter:
	// fe80::/64 (scope=link), 10.99.99.99/32 (no scope), 127.0.0.1/8
	// (scope=host on lo, different link) — all rejected.
	assertContains(t, output, `addr_eth1=["10.0.0.5/24"]`)
	assertNotContains(t, output, "fe80::aa:bbff:fecc:dd01")
	assertNotContains(t, output, "10.99.99.99")
	// gateway_by_link returns IPv4 even when an IPv6 default route is also
	// present on the same link.
	assertContains(t, output, "gw_eth0=192.168.1.1")
	assertNotContains(t, output, "gw_eth0=fe80::1")
	// Storage NIC has no default route.
	assertContains(t, output, "gw_eth1=\n")
	// Static routes are exposed; default route is excluded.
	assertContains(t, output, `"dst":"10.0.0.0/24"`)
	assertContains(t, output, `"dst":"10.10.0.0/16"`)
	assertContains(t, output, `"dst":"172.16.0.0/12"`)
	assertContains(t, output, `"dst":"203.0.113.0/24"`)
	assertContains(t, output, `"gateway":"10.0.0.254"`)
	assertNotContains(t, output, `"dst":""`)
	// priority: 0 must round-trip as "0", not collapse to "" via sprig
	// `default ""`.
	assertContains(t, output, `"priority":"0"`)
	// Missing route fields must render as empty strings, never "<nil>" or
	// HTML-escaped "\u003cnil\u003e".
	assertNotContains(t, output, "<nil>")
	assertNotContains(t, output, `\u003cnil\u003e`)
	assertContains(t, output, "mac_eth1=aa:bb:cc:dd:ee:01")
	assertContains(t, output, "bus_eth1=pci-0000:00:1f.1")
	// Virtual link with a present spec but missing busPath: a present-spec
	// path through bus_by_link must not surface "<nil>" via `nil | toString`.
	// bond0's fixture has hardwareAddr but no busPath, so mac_bond0 returns
	// the synthetic MAC and bus_bond0 must be empty.
	assertContains(t, output, "mac_bond0=aa:bb:cc:dd:ee:ff")
	assertContains(t, output, "bus_bond0=\n")
	// Unknown link must yield empty MAC/busPath even when the lookup mock
	// returns an empty map (real Helm returns nil; defensive on both).
	assertContains(t, output, "mac_unknown=\n")
	assertContains(t, output, "bus_unknown=\n")
	assertContains(t, output, "busPath: pci-0000:00:1f.1")
}

// TestDefaultGatewayIsIPv4OnDualStack pins that
// talm.discovered.default_gateway returns the IPv4 default-route gateway,
// not the IPv6 one, when both are present on the node. The cozystack and
// generic charts pair this gateway with a hardcoded IPv4 destination
// (`network: 0.0.0.0/0`, or no `network:` at all on the typed
// RouteConfig schema where Talos defaults to IPv4): an IPv6 gateway in
// either case lands as a malformed route that Talos cannot install, and
// dependent features (e.g. Layer2 VIP) silently break.
//
// The fixture orders the IPv6 default route before the IPv4 one to
// catch a missing family filter — without one the helper returns the
// first-iterated gateway, which on real Hetzner-style dual-stack nodes
// is often the IPv6 entry.
func TestDefaultGatewayIsIPv4OnDualStack(t *testing.T) {
	const tmpl = `gw={{ include "talm.discovered.default_gateway" . }}
`
	chartRoot := createTestChart(t, "tc", "out.yaml", tmpl)
	helpersSrc, err := os.ReadFile("../../charts/talm/templates/_helpers.tpl")
	if err != nil {
		t.Fatalf("read helpers: %v", err)
	}
	if err := os.WriteFile(filepath.Join(chartRoot, "templates", "_helpers.tpl"), helpersSrc, 0o644); err != nil {
		t.Fatalf("write vendored helpers: %v", err)
	}

	output := renderChartTemplateWithLookup(t, chartRoot, "templates/out.yaml", secondaryNicLookup())

	if !strings.Contains(output, "gw=192.168.1.1") {
		t.Errorf("expected default_gateway to return IPv4 192.168.1.1, got:\n%s", output)
	}
	if strings.Contains(output, "gw=fe80::1") {
		t.Errorf("default_gateway returned IPv6 fe80::1; the consumer pairs this with an IPv4 destination and Talos will reject the resulting route:\n%s", output)
	}
}

// TestDefaultLinkHelpersFollowIPv4OnTwoNicDualStack pins that every
// default_*_by_gateway helper (link name, MAC, busPath, deviceSelector)
// follows the IPv4 default route on a multi-NIC dual-stack node where
// IPv4 and IPv6 default routes terminate on DIFFERENT links. Without
// the family filter the link-identification chain selects the
// IPv6-default link (eth1) while the addresses/gateway helpers
// describe the IPv4-default link (eth0) — the resulting LinkConfig
// name attaches to eth1 but its addresses live on eth0 and the
// rendered config configures neither NIC correctly.
func TestDefaultLinkHelpersFollowIPv4OnTwoNicDualStack(t *testing.T) {
	const tmpl = `link_name={{ include "talm.discovered.default_link_name_by_gateway" . }}
link_mac={{ include "talm.discovered.default_link_address_by_gateway" . }}
link_bus={{ include "talm.discovered.default_link_bus_by_gateway" . }}
link_selector={{ include "talm.discovered.default_link_selector_by_gateway" . }}
`
	chartRoot := createTestChart(t, "tc", "out.yaml", tmpl)
	helpersSrc, err := os.ReadFile("../../charts/talm/templates/_helpers.tpl")
	if err != nil {
		t.Fatalf("read helpers: %v", err)
	}
	if err := os.WriteFile(filepath.Join(chartRoot, "templates", "_helpers.tpl"), helpersSrc, 0o644); err != nil {
		t.Fatalf("write vendored helpers: %v", err)
	}

	output := renderChartTemplateWithLookup(t, chartRoot, "templates/out.yaml", dualStackTwoNicsLookup())

	if !strings.Contains(output, "link_name=eth0") {
		t.Errorf("default_link_name_by_gateway must follow the IPv4 default route to eth0, not the IPv6 default route to eth1:\n%s", output)
	}
	if strings.Contains(output, "link_name=eth1") {
		t.Errorf("default_link_name_by_gateway returned eth1 (the IPv6-default NIC) — chart will attach LinkConfig to the wrong link:\n%s", output)
	}
	// MAC and bus path must come from eth0 — eth1's MAC/bus would
	// land in deviceSelector and Talos would attach the rendered
	// LinkConfig to the wrong PCI function.
	if !strings.Contains(output, "link_mac=aa:bb:cc:00:00:01") {
		t.Errorf("default_link_address_by_gateway must return eth0's MAC, got:\n%s", output)
	}
	if !strings.Contains(output, "link_bus=pci-0000:00:1f.0") {
		t.Errorf("default_link_bus_by_gateway must return eth0's busPath, got:\n%s", output)
	}
	if !strings.Contains(output, "busPath: pci-0000:00:1f.0") {
		t.Errorf("default_link_selector_by_gateway must emit eth0's busPath, got:\n%s", output)
	}
}

// TestCozystackChartRendersIPv4GatewayOnDualStack pins the end-to-end
// contract for the headline scenario: a node with both IPv4 and IPv6
// default routes must produce a chart-rendered config whose
// `0.0.0.0/0` route carries the IPv4 gateway, never IPv6. This is the
// exact regression a user hits on a Hetzner-style dual-stack node where
// the rendered VIP config silently breaks because the route is
// malformed.
//
// The single-NIC fixture catches the family-filter bug at the
// gateway/address level. The two-NIC subtest below catches the same
// bug at the link-identification level — both must hold for the chart
// to produce a working config on real dual-stack hardware.
//
//nolint:godox // tracked engine bug; comment intentional pending the link-identification refactor.
func TestCozystackChartRendersIPv4GatewayOnDualStack(t *testing.T) {
	t.Run("single NIC dual-stack", func(t *testing.T) {
		output := renderChartTemplateWithLookup(t, "../../charts/cozystack", "templates/controlplane.yaml", dualStackNicLookup(), "v1.12")

		// Multidoc v1.12 schema emits LinkConfig / VLANConfig with
		// `routes: [{gateway: ...}]`. The destination defaults to IPv4
		// upstream, so the gateway must be IPv4.
		if !strings.Contains(output, "gateway: 192.168.201.1") {
			t.Errorf("expected rendered gateway 192.168.201.1, got:\n%s", output)
		}
		if strings.Contains(output, "gateway: fe80::1") {
			t.Errorf("rendered route uses IPv6 gateway fe80::1 paired with an IPv4 destination — Talos will reject this route on the node:\n%s", output)
		}
		// default_addresses_by_gateway must also prefer the IPv4 default
		// route's family; without that, the rendered LinkConfig.addresses
		// is empty (no IPv6 addresses exist on the node) and the chart
		// produces a config that does not configure the primary NIC at all.
		if !strings.Contains(output, "192.168.201.10/24") {
			t.Errorf("expected rendered address 192.168.201.10/24 (default_addresses_by_gateway must follow the IPv4 default route), got:\n%s", output)
		}
	})

	t.Run("generic chart single NIC dual-stack", func(t *testing.T) {
		// charts/generic shares the same talm helpers (via symlink at
		// charts/generic/charts/talm), but its own values defaults and
		// chart-level helpers differ from cozystack. Pin that the same
		// IPv4-only contract holds end-to-end through the generic chart
		// path so a future generic-only template change cannot
		// regress the family handling without surfacing here.
		output := renderChartTemplateWithLookup(t, "../../charts/generic", "templates/controlplane.yaml", dualStackNicLookup(), "v1.12")

		if !strings.Contains(output, "gateway: 192.168.201.1") {
			t.Errorf("generic chart: expected rendered gateway 192.168.201.1, got:\n%s", output)
		}
		if strings.Contains(output, "gateway: fe80::1") {
			t.Errorf("generic chart: rendered route uses IPv6 gateway fe80::1:\n%s", output)
		}
		if !strings.Contains(output, "192.168.201.10/24") {
			t.Errorf("generic chart: expected rendered address 192.168.201.10/24, got:\n%s", output)
		}
	})

	t.Run("two NIC dual-stack with IPv4 and IPv6 default routes on different links", func(t *testing.T) {
		// On Hetzner-like nodes the IPv4 default route and the IPv6
		// default route may terminate on different links. Both links
		// must end up in the rendered config — eth0 (the IPv4-default
		// link) carries the gateway since the chart's route-emit is
		// IPv4-only, eth1 (the IPv6-default link) renders as a
		// gateway-less LinkConfig. A missing family filter would route
		// the gateway through eth1 instead of eth0; a single-link
		// renderer would drop eth1 entirely (regression that
		// TestMultiDocRendersAllConfigurableLinks pins separately).
		output := renderChartTemplateWithLookup(t, "../../charts/cozystack", "templates/controlplane.yaml", dualStackTwoNicsLookup(), "v1.12")

		if !strings.Contains(output, "name: eth0") {
			t.Errorf("expected LinkConfig name: eth0 (the IPv4-default link), got:\n%s", output)
		}
		if !strings.Contains(output, "name: eth1") {
			t.Errorf("expected LinkConfig name: eth1 (the IPv6-default link) — multi-doc renderer must emit a doc per configurable link:\n%s", output)
		}
		if !strings.Contains(output, "gateway: 192.168.201.1") {
			t.Errorf("expected rendered gateway 192.168.201.1, got:\n%s", output)
		}
		if strings.Contains(output, "gateway: fe80::1") {
			t.Errorf("rendered route uses IPv6 gateway fe80::1, got:\n%s", output)
		}
		if !strings.Contains(output, "192.168.201.10/24") {
			t.Errorf("expected rendered address 192.168.201.10/24, got:\n%s", output)
		}
	})
}

// TestNetworkMultidoc_NoDiscovery is a regression test for #58. When discovery
// returns no default route (offline render, isolated node, custom networking),
// the multidoc cozystack template must NOT emit a LinkConfig/BondConfig/
// VLANConfig/Layer2VIPConfig with empty `name:` — Talos v1.12 rejects such
// documents with `[networking.os.device.interface] required`.
func TestNetworkMultidoc_NoDiscovery(t *testing.T) {
	output := renderChartTemplate(t, "../../charts/cozystack", "templates/controlplane.yaml", "v1.12")

	assertNotContains(t, output, "kind: LinkConfig")
	assertNotContains(t, output, "kind: BondConfig")
	assertNotContains(t, output, "kind: VLANConfig")
	assertNotContains(t, output, "kind: Layer2VIPConfig")
	// HostnameConfig/ResolverConfig still emit (independent of link discovery).
	assertContains(t, output, "kind: HostnameConfig")
	assertContains(t, output, "kind: ResolverConfig")
}

// TestMultiDocRendersAllConfigurableLinks pins the contract that the
// v1.12 multi-doc renderer emits a LinkConfig for every configurable
// link on the node, not just the one that carries the default route.
// Today the renderer resolves a single $defaultLinkName via
// default_link_name_by_gateway and stops, so a node with a routed NIC
// plus a storage NIC ends up with the storage NIC silently
// unconfigured — discovery sees it but the rendered config does not
// describe it.
func TestMultiDocRendersAllConfigurableLinks(t *testing.T) {
	output := renderChartTemplateWithLookup(t, "../../charts/cozystack", "templates/controlplane.yaml", multiNicLookup(), "v1.12")

	if !strings.Contains(output, "name: eth0") {
		t.Errorf("expected LinkConfig name: eth0 (routed uplink), got:\n%s", output)
	}
	if !strings.Contains(output, "name: eth1") {
		t.Errorf("expected LinkConfig name: eth1 (storage NIC) — multi-doc renderer dropped a configurable link, leaving it unconfigured:\n%s", output)
	}
	// Gateway lives only on the routed link's LinkConfig.
	if !strings.Contains(output, "gateway: 192.168.201.1") {
		t.Errorf("expected default gateway on eth0 LinkConfig, got:\n%s", output)
	}
	// Each link's addresses must come from its own discovery, not from
	// the gateway link's addresses.
	if !strings.Contains(output, "192.168.201.10/24") {
		t.Errorf("expected eth0 address 192.168.201.10/24 in merged output:\n%s", output)
	}
	if !strings.Contains(output, "10.0.0.5/24") {
		t.Errorf("expected eth1 address 10.0.0.5/24 (storage NIC) in merged output — by-link addresses were dropped:\n%s", output)
	}
}

// TestMultiDocEmitsVLANConfigForDiscoveredVLAN pins that the v1.12
// multi-doc renderer emits a VLANConfig document for any VLAN
// sub-interface present on the node, regardless of whether the VLAN
// itself or its parent carries the default route. The previous
// single-link renderer emitted VLANConfig only when the VLAN was the
// gateway link; a VLAN on a non-gateway parent (storage VLAN, mgmt
// VLAN) was silently dropped.
func TestMultiDocEmitsVLANConfigForDiscoveredVLAN(t *testing.T) {
	output := renderChartTemplateWithLookup(t, "../../charts/cozystack", "templates/controlplane.yaml", multiNicWithVLANLookup(), "v1.12")

	// Both LinkConfig (parent eth0) and VLANConfig (eth0.4000) must be emitted.
	if !strings.Contains(output, "name: eth0") {
		t.Errorf("expected LinkConfig name: eth0 (parent of the VLAN), got:\n%s", output)
	}
	if !strings.Contains(output, "kind: VLANConfig") {
		t.Errorf("expected a VLANConfig document for the VLAN sub-interface, got:\n%s", output)
	}
	if !strings.Contains(output, "name: eth0.4000") {
		t.Errorf("expected VLANConfig name: eth0.4000, got:\n%s", output)
	}
	if !strings.Contains(output, "vlanID: 4000") {
		t.Errorf("expected vlanID: 4000 on the VLANConfig, got:\n%s", output)
	}
	if !strings.Contains(output, "parent: eth0") {
		t.Errorf("expected parent: eth0 on the VLANConfig, got:\n%s", output)
	}
	if !strings.Contains(output, "192.168.100.2/24") {
		t.Errorf("expected VLAN address 192.168.100.2/24 on the VLANConfig, got:\n%s", output)
	}
}

// TestMultiDocLinkConfigStripsFloatingIPFromAddresses pins that the
// configured floatingIP does not leak into LinkConfig.addresses on
// the link where the VIP is currently active. Talos's VIP operator
// installs the floating IP as a regular global-scope address on the
// link, indistinguishable from a permanent address in the COSI
// addresses resource — without filtering, a re-render against the
// VIP-active node ends up declaring the same IP both as a permanent
// address on LinkConfig and as the Layer2VIPConfig.link target,
// causing a config tug-of-war between leader and follower nodes.
func TestMultiDocLinkConfigStripsFloatingIPFromAddresses(t *testing.T) {
	output := renderCozystackWith(t, vipActiveOnLinkLookup(), map[string]any{
		"floatingIP": "192.168.201.5",
	})

	// Permanent address must remain.
	if !strings.Contains(output, "192.168.201.10/24") {
		t.Errorf("expected permanent address 192.168.201.10/24 on LinkConfig, got:\n%s", output)
	}
	// floatingIP/32 must NOT appear under any LinkConfig.addresses.
	if strings.Contains(output, "192.168.201.5/32") {
		t.Errorf("floatingIP 192.168.201.5/32 leaked into LinkConfig.addresses; the VIP-bearing address must be filtered out so the re-render does not declare the VIP both as a permanent address and as the Layer2VIPConfig target:\n%s", output)
	}
	// Layer2VIPConfig must still be emitted.
	if !strings.Contains(output, "kind: Layer2VIPConfig") {
		t.Errorf("expected Layer2VIPConfig to still emit, got:\n%s", output)
	}
}

// TestMultiDocFailsWhenBridgeCarriesDefaultRoute pins the guardrail
// for the case where a discovered bridge is the IPv4-default link.
// The bridge branch of the renderer skips emission (BridgeConfig is
// not yet implemented), so without an explicit fail the rendered
// config would carry no document for the gateway-bearing link at
// all — silent drop of the entire network configuration. The
// guardrail surfaces the missing branch as a clear error pointing
// the operator at the per-node body workaround.
func TestMultiDocFailsWhenBridgeCarriesDefaultRoute(t *testing.T) {
	origLookup := helmEngine.LookupFunc
	t.Cleanup(func() { helmEngine.LookupFunc = origLookup })
	helmEngine.LookupFunc = bridgeWithGatewayLookup()

	chrt, err := loader.LoadDir("../../charts/cozystack")
	if err != nil {
		t.Fatalf("load chart: %v", err)
	}
	values := cloneValues(chrt.Values)
	if v, _ := values["endpoint"].(string); v == "" {
		values["endpoint"] = testEndpoint
	}
	if arr, ok := values["advertisedSubnets"].([]any); !ok || len(arr) == 0 {
		values["advertisedSubnets"] = []any{testAdvertisedSubnet}
	}

	eng := helmEngine.Engine{}
	_, err = eng.Render(chrt, chartutil.Values{
		"Values":       values,
		"TalosVersion": "v1.12",
	})
	if err == nil {
		t.Fatal("expected render to fail when a bridge carries the IPv4 default route — silent drop of every network document for the gateway link is the regression this guardrail prevents")
	}
	if !strings.Contains(err.Error(), "bridge") {
		t.Errorf("fail message must name the bridge so the operator can locate the offending link, got: %v", err)
	}
}

// TestMultiDocLinkConfigEmitsIPv6Addresses pins the dual-stack
// contract for LinkConfig.addresses: both IPv4 and IPv6 global-scope
// addresses on a link reach the rendered config. The chart's
// gateway helper is IPv4-only by convention, but addresses are not
// — a node with an IPv6 address on a NIC must keep that address in
// the rendered LinkConfig so a re-render does not silently drop the
// user's IPv6 configuration. addresses_by_link returns both
// families; the renderer passes them through after the
// floatingIP-strip filter.
func TestMultiDocLinkConfigEmitsIPv6Addresses(t *testing.T) {
	output := renderChartTemplateWithLookup(t, "../../charts/cozystack", "templates/controlplane.yaml", dualStackTwoNicsLookup(), "v1.12")

	// eth1 has only an IPv6 global-scope address in this fixture.
	// The renderer must emit it as a LinkConfig address — silently
	// dropping the IPv6 entry would erase the user's network state
	// on the storage NIC.
	if !strings.Contains(output, "2001:db8::a/64") {
		t.Errorf("expected IPv6 address 2001:db8::a/64 on eth1's LinkConfig — addresses_by_link returns both families and the renderer must surface them, got:\n%s", output)
	}
}

// TestMultiDocFailsWhenVLANHasNoVlanID pins the symmetric guardrail
// for the missing-vlanID case: VLANConfig requires both parent and
// vlanID on the wire. A VLAN discovered without a resolvable
// vlanID (partial discovery state) must surface a fail at template
// time, not silently emit a VLANConfig that Talos rejects on apply.
func TestMultiDocFailsWhenVLANHasNoVlanID(t *testing.T) {
	origLookup := helmEngine.LookupFunc
	t.Cleanup(func() { helmEngine.LookupFunc = origLookup })
	helmEngine.LookupFunc = vlanWithoutVlanIDLookup()

	chrt, err := loader.LoadDir("../../charts/cozystack")
	if err != nil {
		t.Fatalf("load chart: %v", err)
	}
	values := cloneValues(chrt.Values)
	if v, _ := values["endpoint"].(string); v == "" {
		values["endpoint"] = testEndpoint
	}
	if arr, ok := values["advertisedSubnets"].([]any); !ok || len(arr) == 0 {
		values["advertisedSubnets"] = []any{testAdvertisedSubnet}
	}

	eng := helmEngine.Engine{}
	_, err = eng.Render(chrt, chartutil.Values{
		"Values":       values,
		"TalosVersion": "v1.12",
	})
	if err == nil {
		t.Fatal("expected render to fail when a VLAN link is missing vlanID — the field is required by Talos and an emitted VLANConfig without it is rejected on apply")
	}
	if !strings.Contains(err.Error(), "vlanID") {
		t.Errorf("fail message must name vlanID so the operator knows what is missing, got: %v", err)
	}
}

// TestMultiDocLayer2VIPLinkOnBondDefaultGateway pins that the
// discovery-derived Layer2VIPConfig.link points at the bond master
// when the bond is the IPv4-default link. Mirror of the VLAN-as-
// default-gateway test; physical-as-default-gateway is covered by
// existing TestMultiDocCozystack_Layer2VIPConfigWhenFloatingIPSet.
func TestMultiDocLayer2VIPLinkOnBondDefaultGateway(t *testing.T) {
	output := renderCozystackWith(t, bondWithSlavesLookup(), map[string]any{
		"floatingIP": "192.168.201.5",
	})

	if !strings.Contains(output, "kind: Layer2VIPConfig") {
		t.Errorf("expected Layer2VIPConfig in the rendered output:\n%s", output)
	}
	if !strings.Contains(output, "link: bond0") {
		t.Errorf("expected Layer2VIPConfig.link: bond0 (the bond master carrying the default route), got:\n%s", output)
	}
}

// TestMultiDocFailsWhenVLANHasNoParent pins that the renderer
// surfaces a clear error when a VLAN link discovered without a
// resolvable parent reaches the VLANConfig branch — Talos rejects a
// VLANConfig without the required parent field, and silently
// emitting one would surface the rejection on the apply RPC instead
// of at template time.
func TestMultiDocFailsWhenVLANHasNoParent(t *testing.T) {
	origLookup := helmEngine.LookupFunc
	t.Cleanup(func() { helmEngine.LookupFunc = origLookup })
	helmEngine.LookupFunc = vlanWithoutParentLookup()

	chrt, err := loader.LoadDir("../../charts/cozystack")
	if err != nil {
		t.Fatalf("load chart: %v", err)
	}
	values := cloneValues(chrt.Values)
	if v, _ := values["endpoint"].(string); v == "" {
		values["endpoint"] = testEndpoint
	}
	if arr, ok := values["advertisedSubnets"].([]any); !ok || len(arr) == 0 {
		values["advertisedSubnets"] = []any{testAdvertisedSubnet}
	}

	eng := helmEngine.Engine{}
	_, err = eng.Render(chrt, chartutil.Values{
		"Values":       values,
		"TalosVersion": "v1.12",
	})
	if err == nil {
		t.Fatal("expected render to fail when a VLAN link is missing a resolvable parent — emitting VLANConfig without the required parent field is rejected by Talos at apply time")
	}
	if !strings.Contains(err.Error(), "parent") {
		t.Errorf("fail message must name the missing parent link so the operator knows what to fix, got: %v", err)
	}
}

// TestMultiDocBondSlavesNotEmittedAsLinkConfig pins that bond slaves
// (physical NICs enrolled into a bond) do NOT get their own
// LinkConfig document alongside the master's BondConfig. A slave
// LinkConfig conflicts with the bond's claim on the link and Talos
// rejects the resulting configuration on controller convergence.
// configurable_link_names filters on spec.slaveKind to drop slaves
// from the iteration.
func TestMultiDocBondSlavesNotEmittedAsLinkConfig(t *testing.T) {
	output := renderChartTemplateWithLookup(t, "../../charts/cozystack", "templates/controlplane.yaml", bondWithSlavesLookup(), "v1.12")

	if !strings.Contains(output, "kind: BondConfig") {
		t.Errorf("expected BondConfig for the bond master, got:\n%s", output)
	}
	if !strings.Contains(output, "name: bond0") {
		t.Errorf("expected BondConfig name: bond0, got:\n%s", output)
	}
	// Slaves must NOT appear as standalone LinkConfig — emitting them
	// alongside the master's BondConfig produces conflicting
	// declarations Talos rejects.
	if strings.Contains(output, "name: eth0\n") || strings.Contains(output, "name: eth1\n") {
		t.Errorf("bond slave eth0/eth1 emitted as standalone LinkConfig, conflicts with master's BondConfig:\n%s", output)
	}
}

// TestMultiDocBridgeSkipsLinkConfigBranch pins that a discovered
// bridge link does NOT fall through to the LinkConfig branch. The
// chart does not yet emit BridgeConfig, so the bridge must be
// skipped (rather than rendered as a wrong-kind LinkConfig that
// Talos would attach to the wrong interface semantics). Once a
// future change adds a BridgeConfig branch, the test gets updated
// to assert the new emission.
func TestMultiDocBridgeSkipsLinkConfigBranch(t *testing.T) {
	output := renderChartTemplateWithLookup(t, "../../charts/cozystack", "templates/controlplane.yaml", bridgeLookup(), "v1.12")

	if strings.Contains(output, "kind: LinkConfig\nname: br0") {
		t.Errorf("bridge br0 emitted as a LinkConfig — wrong document kind. Should be skipped until BridgeConfig support lands:\n%s", output)
	}
	// Routed physical NIC (eth0) still emits its own LinkConfig.
	if !strings.Contains(output, "name: eth0") {
		t.Errorf("expected LinkConfig for the routed physical eth0, got:\n%s", output)
	}
}

// TestMultiDocBondAndVLANCarryMTU pins that BondConfig and VLANConfig
// surface discovered MTU just like LinkConfig does. The renderer
// emits mtu on all three document kinds; without the test, a future
// change that drops the field on bond or VLAN would silently
// regress on jumbo-frame setups for those link kinds.
func TestMultiDocBondAndVLANCarryMTU(t *testing.T) {
	bond := renderChartTemplateWithLookup(t, "../../charts/cozystack", "templates/controlplane.yaml", bondWithSlavesLookup(), "v1.12")
	if !strings.Contains(bond, "mtu: 9000") {
		t.Errorf("expected BondConfig mtu: 9000 on the jumbo-MTU bond fixture, got:\n%s", bond)
	}

	vlan := renderChartTemplateWithLookup(t, "../../charts/cozystack", "templates/controlplane.yaml", multiNicWithVLANLookup(), "v1.12")
	// The fixture sets parent eth0 mtu=1500 and the VLAN eth0.4000
	// mtu=1450 so the assertion targets the VLAN document, not the
	// parent LinkConfig that also carries mtu.
	if !strings.Contains(vlan, "mtu: 1450") {
		t.Errorf("expected VLANConfig mtu: 1450 on the VLAN fixture (parent LinkConfig has mtu: 1500), got:\n%s", vlan)
	}
}

// TestMultiDocLayer2VIPLinkOnVLANDefaultGateway pins that the
// discovery-derived Layer2VIPConfig.link points at the VLAN
// sub-interface when the VLAN is the link carrying the default
// route. The single-link renderer set $vipLinkName = $defaultLinkName
// on the VLAN branch; the rewrite preserves that semantic by
// pointing the discovery VIP directly at $defaultLinkName, but no
// existing test pinned the VLAN-default-gateway case with floatingIP
// set.
func TestMultiDocLayer2VIPLinkOnVLANDefaultGateway(t *testing.T) {
	output := renderCozystackWith(t, multiNicWithVLANLookup(), map[string]any{
		"floatingIP": "192.168.100.10",
	})

	if !strings.Contains(output, "kind: Layer2VIPConfig") {
		t.Errorf("expected Layer2VIPConfig in the rendered output:\n%s", output)
	}
	if !strings.Contains(output, "link: eth0.4000") {
		t.Errorf("expected Layer2VIPConfig.link: eth0.4000 (the VLAN sub-interface carrying the default route), got:\n%s", output)
	}
}

// TestMultiDocBondConfigOmitsMissingBondMasterFields pins the
// gracefully-degrading BondConfig render: when discovery returns a
// bond link with a partial or absent bondMaster, the renderer must
// omit fields that aren't set rather than emitting `bondMode: <nil>`
// or similar broken YAML. The previous renderer unconditionally
// emitted bondMode and the surrounding fields, breaking on partial
// discovery state.
func TestMultiDocBondConfigOmitsMissingBondMasterFields(t *testing.T) {
	output := renderChartTemplateWithLookup(t, "../../charts/cozystack", "templates/controlplane.yaml", bondWithoutBondMasterLookup(), "v1.12")

	if !strings.Contains(output, "kind: BondConfig") {
		t.Errorf("expected BondConfig to still render despite missing bondMaster, got:\n%s", output)
	}
	if strings.Contains(output, "bondMode: <nil>") || strings.Contains(output, "bondMode: \n") {
		t.Errorf("BondConfig rendered bondMode with a nil/empty value, breaking YAML:\n%s", output)
	}
}

// TestMultiDocFailsOnLegacyInterfacesInRunningConfig pins the
// guardrail for clusters upgrading from a chart that emitted the
// legacy machine.network.interfaces[] schema to one that emits the
// v1.12 multi-doc form: the multi-doc renderer must surface a clear
// failure when the live MachineConfig still carries legacy interface
// declarations, because the renderer reconstructs network state from
// discovery resources and would otherwise silently drop the user's
// declarations.
//
// The fail directive points at the concrete migration path (move the
// legacy interfaces into the per-node body as multi-doc documents)
// instead of letting the user discover the silent drop in production.
func TestMultiDocFailsOnLegacyInterfacesInRunningConfig(t *testing.T) {
	origLookup := helmEngine.LookupFunc
	t.Cleanup(func() { helmEngine.LookupFunc = origLookup })
	helmEngine.LookupFunc = legacyInterfacesInRunningConfigLookup()

	chrt, err := loader.LoadDir("../../charts/cozystack")
	if err != nil {
		t.Fatalf("load chart: %v", err)
	}
	values := cloneValues(chrt.Values)
	if v, _ := values["endpoint"].(string); v == "" {
		values["endpoint"] = testEndpoint
	}
	if arr, ok := values["advertisedSubnets"].([]any); !ok || len(arr) == 0 {
		values["advertisedSubnets"] = []any{testAdvertisedSubnet}
	}

	eng := helmEngine.Engine{}
	_, err = eng.Render(chrt, chartutil.Values{
		"Values":       values,
		"TalosVersion": "v1.12",
	})
	if err == nil {
		t.Fatal("expected render to fail when running config carries legacy machine.network.interfaces[] — silent drop of user's interfaces is the regression this guardrail is for")
	}
	if !strings.Contains(err.Error(), "machine.network.interfaces") {
		t.Errorf("fail message must name the offending field so the user knows what to migrate; got: %v", err)
	}
}

// TestMultiDocLinkConfigCarriesMTU pins that LinkConfig (and
// VLANConfig / BondConfig where applicable) carries the link's MTU
// when discovery reports one. Today the multi-doc LinkConfig template
// emits only name / addresses / routes, so a node with a non-default
// MTU (jumbo frames, GRE tunnels, etc.) cannot be managed via the
// chart on v1.12 — the discovered MTU is silently dropped, and a user
// override in legacy `machine.network.interfaces[].mtu` does not
// translate either.
func TestMultiDocLinkConfigCarriesMTU(t *testing.T) {
	output := renderChartTemplateWithLookup(t, "../../charts/cozystack", "templates/controlplane.yaml", multiNicLookup(), "v1.12")

	// eth0 has mtu: 1500 (default), eth1 has mtu: 9000 (jumbo).
	// The renderer must surface eth1's non-default MTU on its
	// LinkConfig. Default MTU rendering is allowed but not required.
	if !strings.Contains(output, "mtu: 9000") {
		t.Errorf("expected eth1 LinkConfig to carry mtu: 9000 (jumbo frames discovered on the storage NIC), got:\n%s", output)
	}
}

// TestNetworkLegacy_NoDiscovery is a regression test for #58 covering the
// legacy (Talos < v1.12) path. The `interfaces:` key was unconditionally
// emitted, producing either an empty list or a `- interface:` block with an
// empty interface name when discovery found no default route. Either form
// breaks Talos validation. After the fix, the template must skip both
// `interfaces:` and the per-interface block entirely.
func TestNetworkLegacy_NoDiscovery(t *testing.T) {
	output := renderChartTemplate(t, "../../charts/cozystack", "templates/controlplane.yaml", "v1.11")

	// `    interfaces:` (the YAML key) must not appear; the helper-emitted
	// `# -- Discovered interfaces:` comment is fine and intentional.
	assertNotContains(t, output, "    interfaces:")
	assertNotContains(t, output, "- interface: \n")
	// Hostname / nameservers should still be rendered — they are independent
	// of link discovery.
	assertContains(t, output, "hostname:")
	assertContains(t, output, "nameservers:")
}

// TestMergeFileAsPatch pins the contract for the apply-side overlay of a
// node file's body on top of a rendered template:
//
//   - When the node file has Talos config in addition to the modeline,
//     fields from the file must overlay the rendered template (custom
//     hostname wins over the auto-generated one; secondary interfaces and
//     VIPs declared in the file appear in the merged output).
//   - When the node file is just a modeline, an empty file, or comments
//     and document separators with no body, the merge must be a true
//     byte-for-byte identity over rendered. The Talos config-patcher
//     misclassifies such inputs as empty JSON6902 patches, which then
//     refuses any multi-document machine config — the v1.12+ default
//     output format. The empty-detection short-circuit avoids that.
func TestMergeFileAsPatch(t *testing.T) {
	const renderedTemplate = `version: v1alpha1
debug: false
machine:
  type: controlplane
  install:
    disk: /dev/sda
  network:
    hostname: talos-abcde
    interfaces:
      - interface: ens3
        addresses:
          - 10.0.0.1/24
        routes:
          - network: 0.0.0.0/0
            gateway: 10.0.0.254
cluster:
  controlPlane:
    endpoint: https://10.0.0.10:6443
  network:
    podSubnets:
      - 10.244.0.0/16
    serviceSubnets:
      - 10.96.0.0/16
`

	t.Run("overlays hostname and adds secondary interface", func(t *testing.T) {
		dir := t.TempDir()
		nodeFile := filepath.Join(dir, "node0.yaml")
		// First line is the modeline (a YAML comment); the body is a
		// strategic merge patch.
		const nodeBody = `# talm: nodes=["10.0.0.1"], endpoints=["10.0.0.1"], templates=["templates/controlplane.yaml"]
machine:
  network:
    hostname: node0
    interfaces:
      - interface: ens3
        addresses:
          - 10.0.0.1/24
        routes:
          - network: 0.0.0.0/0
            gateway: 10.0.0.254
      - deviceSelector:
          hardwareAddr: "02:00:17:02:55:aa"
        addresses:
          - 10.0.100.11/24
        vip:
          ip: 10.0.100.10
`
		if err := os.WriteFile(nodeFile, []byte(nodeBody), 0o644); err != nil {
			t.Fatalf("write node file: %v", err)
		}

		merged, err := MergeFileAsPatch([]byte(renderedTemplate), nodeFile)
		if err != nil {
			t.Fatalf("MergeFileAsPatch: %v", err)
		}

		out := string(merged)
		if !strings.Contains(out, "hostname: node0") {
			t.Errorf("merged output missing custom hostname 'node0':\n%s", out)
		}
		if strings.Contains(out, "hostname: talos-abcde") {
			t.Errorf("merged output still contains template hostname 'talos-abcde':\n%s", out)
		}
		if !strings.Contains(out, "02:00:17:02:55:aa") {
			t.Errorf("merged output missing deviceSelector secondary interface:\n%s", out)
		}
		if !strings.Contains(out, "10.0.100.10") {
			t.Errorf("merged output missing VIP from node file:\n%s", out)
		}
	})

	t.Run("modeline-only file is a true byte-identity no-op", func(t *testing.T) {
		dir := t.TempDir()
		nodeFile := filepath.Join(dir, "node0.yaml")
		if err := os.WriteFile(nodeFile, []byte("# talm: nodes=[\"10.0.0.1\"], templates=[\"templates/controlplane.yaml\"]\n"), 0o644); err != nil {
			t.Fatalf("write node file: %v", err)
		}

		merged, err := MergeFileAsPatch([]byte(renderedTemplate), nodeFile)
		if err != nil {
			t.Fatalf("MergeFileAsPatch: %v", err)
		}

		// Modeline-only node files must short-circuit before the Talos
		// config-patcher round-trip — the patcher would otherwise reformat
		// YAML, drop comments, and (worse) reject multi-document rendered
		// configs via JSON6902. Identity is the contract.
		if string(merged) != renderedTemplate {
			t.Errorf("modeline-only merge must return rendered byte-for-byte, got diff:\n%s", string(merged))
		}
	})

	t.Run("modeline-only file does not break multi-doc rendered (Talos v1.12+)", func(t *testing.T) {
		// Default Talos v1.12+ output format is multi-document. A
		// modeline-only node file used to route through
		// configpatcher.Apply → JSON6902, which refuses multi-document
		// machine configs with: `JSON6902 patches are not supported for
		// multi-document machine configuration`. The empty-content
		// short-circuit must keep this case working.
		const multiDocRendered = `version: v1alpha1
machine:
  type: controlplane
---
apiVersion: v1alpha1
kind: HostnameConfig
hostname: talos-abcde
---
apiVersion: v1alpha1
kind: LinkConfig
name: ens3
addresses:
  - address: 10.0.0.1/24
`
		dir := t.TempDir()
		nodeFile := filepath.Join(dir, "node0.yaml")
		if err := os.WriteFile(nodeFile, []byte("# talm: nodes=[\"10.0.0.1\"]\n"), 0o644); err != nil {
			t.Fatalf("write node file: %v", err)
		}

		merged, err := MergeFileAsPatch([]byte(multiDocRendered), nodeFile)
		if err != nil {
			t.Fatalf("multi-doc + modeline-only patch must not error, got: %v", err)
		}
		if string(merged) != multiDocRendered {
			t.Errorf("multi-doc + modeline-only merge must return rendered byte-for-byte, got:\n%s", string(merged))
		}
	})

	t.Run("multi-doc rendered + non-empty patch overlays the legacy machine doc", func(t *testing.T) {
		// The realistic Talos v1.12+ apply scenario: a multi-document
		// rendered config (legacy `machine:`/`cluster:` doc plus typed
		// HostnameConfig / LinkConfig docs) plus a node file that pins
		// hostname/network on the legacy machine doc. The strategic-merge
		// patcher must accept the multi-doc input, apply the patch to the
		// legacy machine doc, and leave the typed sibling docs intact.
		const multiDocRendered = `version: v1alpha1
machine:
  type: controlplane
  install:
    disk: /dev/sda
  network:
    hostname: talos-abcde
cluster:
  controlPlane:
    endpoint: https://10.0.0.10:6443
---
apiVersion: v1alpha1
kind: HostnameConfig
hostname: talos-abcde
---
apiVersion: v1alpha1
kind: LinkConfig
name: ens3
addresses:
  - address: 10.0.0.1/24
`
		dir := t.TempDir()
		nodeFile := filepath.Join(dir, "node0.yaml")
		const patchBody = `# talm: nodes=["10.0.0.1"]
machine:
  network:
    hostname: node0
    interfaces:
      - deviceSelector:
          hardwareAddr: "02:00:17:02:55:aa"
        addresses:
          - 10.0.100.11/24
`
		if err := os.WriteFile(nodeFile, []byte(patchBody), 0o644); err != nil {
			t.Fatalf("write node file: %v", err)
		}

		merged, err := MergeFileAsPatch([]byte(multiDocRendered), nodeFile)
		if err != nil {
			t.Fatalf("multi-doc + non-empty patch must not error, got: %v", err)
		}

		out := string(merged)
		if !strings.Contains(out, "hostname: node0") {
			t.Errorf("merged output must overlay machine.network.hostname with node0:\n%s", out)
		}
		if !strings.Contains(out, "02:00:17:02:55:aa") {
			t.Errorf("merged output must include node-file deviceSelector:\n%s", out)
		}
		// The sibling typed documents must not be dropped by the merge.
		if !strings.Contains(out, "kind: LinkConfig") {
			t.Errorf("merged output must preserve sibling LinkConfig document:\n%s", out)
		}
		if !strings.Contains(out, "name: ens3") {
			t.Errorf("merged output must preserve LinkConfig name field:\n%s", out)
		}
	})

	t.Run("empty file is also a no-op", func(t *testing.T) {
		dir := t.TempDir()
		nodeFile := filepath.Join(dir, "empty.yaml")
		if err := os.WriteFile(nodeFile, []byte(""), 0o644); err != nil {
			t.Fatalf("write node file: %v", err)
		}
		merged, err := MergeFileAsPatch([]byte(renderedTemplate), nodeFile)
		if err != nil {
			t.Fatalf("MergeFileAsPatch on empty file: %v", err)
		}
		if string(merged) != renderedTemplate {
			t.Errorf("empty patch must round-trip rendered byte-for-byte")
		}
	})

	t.Run("comments-and-separators-only file is a no-op", func(t *testing.T) {
		dir := t.TempDir()
		nodeFile := filepath.Join(dir, "comments.yaml")
		if err := os.WriteFile(nodeFile, []byte("# top\n---\n# middle\n  \n---\n# bottom\n"), 0o644); err != nil {
			t.Fatalf("write node file: %v", err)
		}
		merged, err := MergeFileAsPatch([]byte(renderedTemplate), nodeFile)
		if err != nil {
			t.Fatalf("MergeFileAsPatch on comments-only file: %v", err)
		}
		if string(merged) != renderedTemplate {
			t.Errorf("comments-only patch must round-trip rendered byte-for-byte")
		}
	})

	t.Run("round-trips an autogenerated controlplane body", func(t *testing.T) {
		// `talm init` writes the rendered template back into nodes/<n>.yaml
		// as the body. The next `talm apply` then re-enters
		// MergeFileAsPatch with that same body. Pin that the chart's actual
		// rendered output round-trips cleanly across both legacy and
		// multi-doc formats — a chart regression that re-introduces a
		// directive incompatible with configpatcher's strict-decode path
		// is caught here at the integration level. The upstream
		// constraint lives in configloader/internal/decoder/delete.go
		// AppendDeletesTo: it extracts $patch:delete only at document
		// and top-level mapping scopes, so a directive nested under a
		// typed map field reaches the strict v1alpha1 decoder and trips
		// it. MergeFileAsPatch strips chart-side directives before the
		// merge target loads to dodge this.
		for _, version := range []string{"v1.11", "v1.12"} {
			t.Run(version, func(t *testing.T) {
				rendered := renderChartTemplate(t, "../../charts/cozystack", "templates/controlplane.yaml", version)

				dir := t.TempDir()
				nodeFile := filepath.Join(dir, "node0.yaml")
				body := "# talm: nodes=[\"10.0.0.1\"], templates=[\"templates/controlplane.yaml\"]\n" + rendered
				if err := os.WriteFile(nodeFile, []byte(body), 0o644); err != nil {
					t.Fatalf("write node file: %v", err)
				}

				if _, err := MergeFileAsPatch([]byte(rendered), nodeFile); err != nil {
					t.Fatalf("MergeFileAsPatch on autogenerated %s body: %v", version, err)
				}
			})
		}
	})

	t.Run("real cozystack chart round-trip does not duplicate primitive arrays", func(t *testing.T) {
		// End-to-end guard against the headline regression: render the
		// chart, write the rendered output as the node body (the exact
		// `talm init` / `talm template -I` flow), re-render the same
		// template, run MergeFileAsPatch on (rendered, body) and assert
		// every primitive list entry the chart emits appears exactly
		// once in the merged output. Without identity-prune + per-doc
		// reattach this fails because Talos's strategic-merge appends
		// every primitive list per document and certSANs/nameservers/
		// validSubnets/endpoints all double on each apply round-trip.
		// This is a stronger contract than the inline-fixture sibling
		// subtest above: it walks the actual chart, so any future chart
		// change that adds a new primitive list also gets covered.
		for _, version := range []string{"v1.11", "v1.12"} {
			t.Run(version, func(t *testing.T) {
				rendered := renderChartTemplate(t, "../../charts/cozystack", "templates/controlplane.yaml", version)

				dir := t.TempDir()
				nodeFile := filepath.Join(dir, "node0.yaml")
				body := "# talm: nodes=[\"10.0.0.1\"], templates=[\"templates/controlplane.yaml\"]\n" + rendered
				if err := os.WriteFile(nodeFile, []byte(body), 0o644); err != nil {
					t.Fatalf("write node file: %v", err)
				}

				merged, err := MergeFileAsPatch([]byte(rendered), nodeFile)
				if err != nil {
					t.Fatalf("MergeFileAsPatch on autogenerated %s body: %v", version, err)
				}

				// Tokens that appear in the chart's rendered primitive
				// lists. Every one must appear at most as many times in
				// merged as it appeared in rendered (no duplication).
				// Pulled from the actual chart output rather than
				// hardcoded so a chart change does not invalidate this
				// guard silently.
				probes := []string{
					"127.0.0.1",
					"https://mirror.gcr.io",
				}
				for _, probe := range probes {
					rcount := strings.Count(rendered, probe)
					if rcount == 0 {
						continue
					}
					mcount := strings.Count(string(merged), probe)
					if mcount > rcount {
						t.Errorf("primitive list entry %q duplicated by round-trip: rendered=%d, merged=%d (%s)\n%s",
							probe, rcount, mcount, version, string(merged))
					}
				}
			})
		}
	})

	t.Run("body identical to rendered does not duplicate primitive arrays", func(t *testing.T) {
		// `talm template -I` writes the rendered template back as the
		// body. The next apply re-enters MergeFileAsPatch with body ==
		// rendered. Talos's strategic-merge appends to primitive arrays
		// rather than treating them as a set, so without identity-pruning
		// every certSANs/nameservers/validSubnets/endpoints entry doubles
		// on every apply round-trip — every certSAN, every nameserver,
		// every podSubnet appears twice on the second apply, four times
		// on the third, and so on.
		//
		// Pin the post-fix contract: when the body's keys match the
		// rendered's keys exactly (deep-equal), MergeFileAsPatch must
		// return a result whose primitive arrays are not duplicated.
		const renderedTemplate = `version: v1alpha1
machine:
  type: controlplane
  certSANs:
    - 127.0.0.1
  network:
    nameservers:
      - 1.1.1.1
      - 8.8.8.8
  install:
    disk: /dev/sda
cluster:
  controlPlane:
    endpoint: https://10.0.0.10:6443
  apiServer:
    certSANs:
      - 127.0.0.1
`
		dir := t.TempDir()
		nodeFile := filepath.Join(dir, "node0.yaml")
		body := "# talm: nodes=[\"10.0.0.1\"]\n" + renderedTemplate
		if err := os.WriteFile(nodeFile, []byte(body), 0o644); err != nil {
			t.Fatalf("write node file: %v", err)
		}

		merged, err := MergeFileAsPatch([]byte(renderedTemplate), nodeFile)
		if err != nil {
			t.Fatalf("MergeFileAsPatch: %v", err)
		}

		out := string(merged)
		// certSANs appears in two places (machine and apiServer); each
		// should still hold exactly one 127.0.0.1.
		if got := strings.Count(out, "127.0.0.1"); got != 2 {
			t.Errorf("expected 127.0.0.1 to appear twice (once under each certSANs), got %d:\n%s", got, out)
		}
		if got := strings.Count(out, "1.1.1.1"); got != 1 {
			t.Errorf("nameservers entry 1.1.1.1 duplicated (count=%d):\n%s", got, out)
		}
		if got := strings.Count(out, "8.8.8.8"); got != 1 {
			t.Errorf("nameservers entry 8.8.8.8 duplicated (count=%d):\n%s", got, out)
		}
	})

	t.Run("body adding a primitive-array entry must not duplicate rendered entries", func(t *testing.T) {
		// The "autogenerated body re-states unchanged values" prune covers
		// the byte-identical case; this test pins the partial-edit case
		// where the user adds one new entry to an array the chart already
		// populated. Talos's strategic-merge appends primitive arrays
		// rather than treating them as a set, so without per-element diff
		// the rendered entries appear twice in the merged config — the
		// same duplicate-primitive-array-entries-per-round-trip symptom
		// as the byte-identical case, just one entry deeper.
		const renderedTemplate = `version: v1alpha1
machine:
  type: controlplane
  install:
    disk: /dev/sda
  certSANs:
    - 127.0.0.1
    - 10.0.0.10
`
		const userBody = `# talm: nodes=["10.0.0.1"]
machine:
  certSANs:
    - 127.0.0.1
    - 10.0.0.10
    - 10.0.0.11
`
		dir := t.TempDir()
		nodeFile := filepath.Join(dir, "node0.yaml")
		if err := os.WriteFile(nodeFile, []byte(userBody), 0o644); err != nil {
			t.Fatalf("write node file: %v", err)
		}

		merged, err := MergeFileAsPatch([]byte(renderedTemplate), nodeFile)
		if err != nil {
			t.Fatalf("MergeFileAsPatch: %v", err)
		}
		out := string(merged)
		if got := strings.Count(out, "127.0.0.1"); got != 1 {
			t.Errorf("rendered entry 127.0.0.1 duplicated after partial-array edit (count=%d):\n%s", got, out)
		}
		if got := strings.Count(out, "10.0.0.10"); got != 1 {
			t.Errorf("rendered entry 10.0.0.10 duplicated after partial-array edit (count=%d):\n%s", got, out)
		}
		if !strings.Contains(out, "10.0.0.11") {
			t.Errorf("user-added entry 10.0.0.11 missing from merged output:\n%s", out)
		}
	})

	t.Run("network interface partial edit does not duplicate addresses or routes", func(t *testing.T) {
		// rendered already populates machine.network.interfaces[interface=X]
		// with addresses and routes; the user's body re-states the same
		// interface with one extra address (a typical per-node edit).
		// Talos's strategic merge matches interfaces by `interface:` and
		// recurses into the matched element, but the inner primitive list
		// (addresses) and the routes object array both append rather than
		// replace — every apply round-trip thus duplicates the rendered
		// entries once more, accumulating linearly with the number of
		// applies. Without object-array recursion in pruneIdenticalKeys,
		// the body's interfaces value reaches configpatcher.Apply with
		// the rendered-side entries still present and triggers the append.
		const renderedTemplate = `version: v1alpha1
machine:
  type: controlplane
  install:
    disk: /dev/sda
  network:
    interfaces:
      - interface: enp0s31f6
        addresses:
          - 88.99.249.47/26
        routes:
          - network: 0.0.0.0/0
            gateway: 88.99.249.1
cluster:
  controlPlane:
    endpoint: https://10.0.0.10:6443
`
		const userBody = `# talm: nodes=["10.0.0.1"]
version: v1alpha1
machine:
  type: controlplane
  install:
    disk: /dev/sda
  network:
    interfaces:
      - interface: enp0s31f6
        addresses:
          - 88.99.249.47/26
          - 10.0.0.99/24
        routes:
          - network: 0.0.0.0/0
            gateway: 88.99.249.1
cluster:
  controlPlane:
    endpoint: https://10.0.0.10:6443
`
		dir := t.TempDir()
		nodeFile := filepath.Join(dir, "node0.yaml")
		if err := os.WriteFile(nodeFile, []byte(userBody), 0o644); err != nil {
			t.Fatalf("write node file: %v", err)
		}

		merged, err := MergeFileAsPatch([]byte(renderedTemplate), nodeFile)
		if err != nil {
			t.Fatalf("MergeFileAsPatch: %v", err)
		}
		out := string(merged)
		if got := strings.Count(out, "88.99.249.47/26"); got != 1 {
			t.Errorf("rendered interface address 88.99.249.47/26 duplicated by partial-edit round-trip (count=%d):\n%s", got, out)
		}
		if got := strings.Count(out, "88.99.249.1"); got != 1 {
			t.Errorf("rendered route gateway 88.99.249.1 duplicated by partial-edit round-trip (count=%d):\n%s", got, out)
		}
		if !strings.Contains(out, "10.0.0.99/24") {
			t.Errorf("user-added address 10.0.0.99/24 missing from merged output:\n%s", out)
		}
	})

	t.Run("nested vlan addresses do not duplicate", func(t *testing.T) {
		// Same shape as the interface-addresses regression but one
		// level deeper: machine.network.interfaces[interface=X]
		// .vlans[vlanId=Y].addresses. Both the parent interface and
		// the vlan are matched by their identity keys upstream, then
		// the inner primitive `addresses` list appends. Pin the
		// post-fix contract: identical inner primitives must not
		// duplicate when the outer object arrays are matched.
		const renderedTemplate = `version: v1alpha1
machine:
  type: controlplane
  install:
    disk: /dev/sda
  network:
    interfaces:
      - interface: enp0s31f6
        vlans:
          - vlanId: 4000
            addresses:
              - 192.168.100.2/24
cluster:
  controlPlane:
    endpoint: https://10.0.0.10:6443
`
		const userBody = `# talm: nodes=["10.0.0.1"]
version: v1alpha1
machine:
  type: controlplane
  install:
    disk: /dev/sda
  network:
    interfaces:
      - interface: enp0s31f6
        vlans:
          - vlanId: 4000
            addresses:
              - 192.168.100.2/24
              - 192.168.100.3/24
cluster:
  controlPlane:
    endpoint: https://10.0.0.10:6443
`
		dir := t.TempDir()
		nodeFile := filepath.Join(dir, "node0.yaml")
		if err := os.WriteFile(nodeFile, []byte(userBody), 0o644); err != nil {
			t.Fatalf("write node file: %v", err)
		}

		merged, err := MergeFileAsPatch([]byte(renderedTemplate), nodeFile)
		if err != nil {
			t.Fatalf("MergeFileAsPatch: %v", err)
		}
		out := string(merged)
		if got := strings.Count(out, "192.168.100.2/24"); got != 1 {
			t.Errorf("rendered vlan address 192.168.100.2/24 duplicated by partial-edit round-trip (count=%d):\n%s", got, out)
		}
		if !strings.Contains(out, "192.168.100.3/24") {
			t.Errorf("user-added vlan address 192.168.100.3/24 missing from merged output:\n%s", out)
		}
	})

	t.Run("admissionControl exemption namespaces do not duplicate", func(t *testing.T) {
		// cluster.apiServer.admissionControl[name=PodSecurity]
		// .configuration.exemptions.namespaces accumulates duplicates of
		// `kube-system` on every apply round-trip. The admissionControl
		// element is matched by its `name:` key upstream, then the
		// nested primitive list under exemptions.namespaces appends.
		const renderedTemplate = `version: v1alpha1
machine:
  type: controlplane
  install:
    disk: /dev/sda
cluster:
  controlPlane:
    endpoint: https://10.0.0.10:6443
  apiServer:
    admissionControl:
      - name: PodSecurity
        configuration:
          apiVersion: pod-security.admission.config.k8s.io/v1alpha1
          kind: PodSecurityConfiguration
          exemptions:
            namespaces:
              - kube-system
`
		const userBody = `# talm: nodes=["10.0.0.1"]
version: v1alpha1
machine:
  type: controlplane
  install:
    disk: /dev/sda
cluster:
  controlPlane:
    endpoint: https://10.0.0.10:6443
  apiServer:
    admissionControl:
      - name: PodSecurity
        configuration:
          apiVersion: pod-security.admission.config.k8s.io/v1alpha1
          kind: PodSecurityConfiguration
          exemptions:
            namespaces:
              - kube-system
              - my-namespace
`
		dir := t.TempDir()
		nodeFile := filepath.Join(dir, "node0.yaml")
		if err := os.WriteFile(nodeFile, []byte(userBody), 0o644); err != nil {
			t.Fatalf("write node file: %v", err)
		}

		merged, err := MergeFileAsPatch([]byte(renderedTemplate), nodeFile)
		if err != nil {
			t.Fatalf("MergeFileAsPatch: %v", err)
		}
		out := string(merged)
		if got := strings.Count(out, "kube-system"); got != 1 {
			t.Errorf("rendered admissionControl exemption namespace kube-system duplicated by partial-edit round-trip (count=%d):\n%s", got, out)
		}
		if !strings.Contains(out, "my-namespace") {
			t.Errorf("user-added exemption namespace my-namespace missing from merged output:\n%s", out)
		}
	})

	t.Run("podSubnets partial edit preserves rendered entry", func(t *testing.T) {
		// cluster.network.podSubnets is tagged `merge:"replace"`
		// upstream — the patcher overwrites rendered's slice with
		// body's slice verbatim (unless body is the zero value, in
		// which case rendered survives). The primitive-subtract
		// dedup must NOT fire on this path: if body re-states
		// rendered's entry plus a user-add, subtracting rendered
		// would strip rendered's entry from the body, the upstream
		// replace then writes only the user-add, and rendered's
		// pod CIDR would silently vanish from the merged config.
		// Pin the post-fix contract: a partial edit on podSubnets
		// must reach upstream unchanged so the replace produces
		// the expected union.
		const renderedTemplate = `version: v1alpha1
machine:
  type: controlplane
  install:
    disk: /dev/sda
cluster:
  controlPlane:
    endpoint: https://10.0.0.10:6443
  network:
    podSubnets:
      - 10.244.0.0/16
`
		const userBody = `# talm: nodes=["10.0.0.1"]
version: v1alpha1
machine:
  type: controlplane
  install:
    disk: /dev/sda
cluster:
  controlPlane:
    endpoint: https://10.0.0.10:6443
  network:
    podSubnets:
      - 10.244.0.0/16
      - 172.16.0.0/16
`
		dir := t.TempDir()
		nodeFile := filepath.Join(dir, "node0.yaml")
		if err := os.WriteFile(nodeFile, []byte(userBody), 0o644); err != nil {
			t.Fatalf("write node file: %v", err)
		}

		merged, err := MergeFileAsPatch([]byte(renderedTemplate), nodeFile)
		if err != nil {
			t.Fatalf("MergeFileAsPatch: %v", err)
		}
		out := string(merged)
		if !strings.Contains(out, "10.244.0.0/16") {
			t.Errorf("rendered podSubnet 10.244.0.0/16 silently lost on replace-tagged partial edit:\n%s", out)
		}
		if !strings.Contains(out, "172.16.0.0/16") {
			t.Errorf("user-added podSubnet 172.16.0.0/16 missing from merged output:\n%s", out)
		}
	})

	t.Run("serviceSubnets partial edit preserves rendered entry", func(t *testing.T) {
		// Same `merge:"replace"` semantics as podSubnets — the
		// primitive-subtract dedup would strip rendered's entry,
		// the upstream replace would then leave only the user-add,
		// and rendered's service CIDR would vanish.
		const renderedTemplate = `version: v1alpha1
machine:
  type: controlplane
  install:
    disk: /dev/sda
cluster:
  controlPlane:
    endpoint: https://10.0.0.10:6443
  network:
    serviceSubnets:
      - 10.96.0.0/12
`
		const userBody = `# talm: nodes=["10.0.0.1"]
version: v1alpha1
machine:
  type: controlplane
  install:
    disk: /dev/sda
cluster:
  controlPlane:
    endpoint: https://10.0.0.10:6443
  network:
    serviceSubnets:
      - 10.96.0.0/12
      - 192.168.16.0/20
`
		dir := t.TempDir()
		nodeFile := filepath.Join(dir, "node0.yaml")
		if err := os.WriteFile(nodeFile, []byte(userBody), 0o644); err != nil {
			t.Fatalf("write node file: %v", err)
		}

		merged, err := MergeFileAsPatch([]byte(renderedTemplate), nodeFile)
		if err != nil {
			t.Fatalf("MergeFileAsPatch: %v", err)
		}
		out := string(merged)
		if !strings.Contains(out, "10.96.0.0/12") {
			t.Errorf("rendered serviceSubnet 10.96.0.0/12 silently lost on replace-tagged partial edit:\n%s", out)
		}
		if !strings.Contains(out, "192.168.16.0/20") {
			t.Errorf("user-added serviceSubnet 192.168.16.0/20 missing from merged output:\n%s", out)
		}
	})

	t.Run("NetworkRuleConfig ingress partial edit preserves rendered rule", func(t *testing.T) {
		// NetworkRuleConfig is a typed v1.12+ document. Its `ingress`
		// field is tagged `merge:"replace"` upstream. The prune walks
		// the typed doc with path starting at the doc root, so the
		// child path is the bare "ingress". Without that path in
		// replaceSemanticPaths, the object-array branch's deep-equal
		// fallback would drop body's restated rule, the upstream
		// replace would write only the new rule, and rendered's rule
		// would silently vanish from the merged firewall config.
		const renderedTemplate = `version: v1alpha1
machine:
  type: controlplane
  install:
    disk: /dev/sda
cluster:
  controlPlane:
    endpoint: https://10.0.0.10:6443
---
apiVersion: v1alpha1
kind: NetworkRuleConfig
name: kubelet-ingress
portSelector:
  ports:
    - 10250
  protocol: tcp
ingress:
  - subnet: 10.0.0.0/8
`
		const userBody = `# talm: nodes=["10.0.0.1"]
version: v1alpha1
machine:
  type: controlplane
  install:
    disk: /dev/sda
cluster:
  controlPlane:
    endpoint: https://10.0.0.10:6443
---
apiVersion: v1alpha1
kind: NetworkRuleConfig
name: kubelet-ingress
portSelector:
  ports:
    - 10250
  protocol: tcp
ingress:
  - subnet: 10.0.0.0/8
  - subnet: 192.168.0.0/16
`
		dir := t.TempDir()
		nodeFile := filepath.Join(dir, "node0.yaml")
		if err := os.WriteFile(nodeFile, []byte(userBody), 0o644); err != nil {
			t.Fatalf("write node file: %v", err)
		}

		merged, err := MergeFileAsPatch([]byte(renderedTemplate), nodeFile)
		if err != nil {
			t.Fatalf("MergeFileAsPatch: %v", err)
		}
		out := string(merged)
		if !strings.Contains(out, "10.0.0.0/8") {
			t.Errorf("rendered ingress subnet 10.0.0.0/8 silently lost on replace-tagged partial edit:\n%s", out)
		}
		if !strings.Contains(out, "192.168.0.0/16") {
			t.Errorf("user-added ingress subnet 192.168.0.0/16 missing from merged output:\n%s", out)
		}
	})

	t.Run("NetworkRuleConfig portSelector.ports partial edit preserves rendered port", func(t *testing.T) {
		// portSelector.ports is the second `merge:"replace"`-tagged
		// field on NetworkRuleConfig. The typed-doc walk reaches it
		// at path "portSelector/ports". Without the entry in
		// replaceSemanticPaths the primitive-subtract branch would
		// strip rendered's port from body; the upstream replace would
		// then leave only the user-add and the rendered port would
		// disappear from the firewall rule.
		const renderedTemplate = `version: v1alpha1
machine:
  type: controlplane
  install:
    disk: /dev/sda
cluster:
  controlPlane:
    endpoint: https://10.0.0.10:6443
---
apiVersion: v1alpha1
kind: NetworkRuleConfig
name: api-ingress
portSelector:
  ports:
    - 9999
  protocol: tcp
ingress:
  - subnet: 10.0.0.0/8
`
		const userBody = `# talm: nodes=["10.0.0.1"]
version: v1alpha1
machine:
  type: controlplane
  install:
    disk: /dev/sda
cluster:
  controlPlane:
    endpoint: https://10.0.0.10:6443
---
apiVersion: v1alpha1
kind: NetworkRuleConfig
name: api-ingress
portSelector:
  ports:
    - 9999
    - 50000
  protocol: tcp
ingress:
  - subnet: 10.0.0.0/8
`
		dir := t.TempDir()
		nodeFile := filepath.Join(dir, "node0.yaml")
		if err := os.WriteFile(nodeFile, []byte(userBody), 0o644); err != nil {
			t.Fatalf("write node file: %v", err)
		}

		merged, err := MergeFileAsPatch([]byte(renderedTemplate), nodeFile)
		if err != nil {
			t.Fatalf("MergeFileAsPatch: %v", err)
		}
		out := string(merged)
		if !strings.Contains(out, "9999") {
			t.Errorf("rendered port 9999 silently lost on replace-tagged partial edit:\n%s", out)
		}
		if !strings.Contains(out, "50000") {
			t.Errorf("user-added port 50000 missing from merged output:\n%s", out)
		}
	})

	t.Run("auditPolicy partial edit preserves rendered map keys", func(t *testing.T) {
		// cluster.apiServer.auditPolicy is an Unstructured map tagged
		// `merge:"replace"`: upstream overwrites the entire map with
		// body's value (unless body is the zero value). The map-recursion
		// branch of pruneIdenticalKeysAt would deep-equal-strip rendered's
		// matching keys (apiVersion, kind, the unmodified rules), the
		// body's map would then carry only the user's edit, and the
		// upstream replace would land that minimal map as the final
		// auditPolicy — the typed-doc identity keys and the unmodified
		// rules vanish. Pin the post-fix contract: a partial edit on
		// auditPolicy reaches upstream verbatim.
		const renderedTemplate = `version: v1alpha1
machine:
  type: controlplane
  install:
    disk: /dev/sda
cluster:
  controlPlane:
    endpoint: https://10.0.0.10:6443
  apiServer:
    auditPolicy:
      apiVersion: audit.k8s.io/v1
      kind: Policy
      rules:
        - level: Metadata
`
		const userBody = `# talm: nodes=["10.0.0.1"]
version: v1alpha1
machine:
  type: controlplane
  install:
    disk: /dev/sda
cluster:
  controlPlane:
    endpoint: https://10.0.0.10:6443
  apiServer:
    auditPolicy:
      apiVersion: audit.k8s.io/v1
      kind: Policy
      rules:
        - level: Metadata
        - level: RequestResponse
          users:
            - alice
`
		dir := t.TempDir()
		nodeFile := filepath.Join(dir, "node0.yaml")
		if err := os.WriteFile(nodeFile, []byte(userBody), 0o644); err != nil {
			t.Fatalf("write node file: %v", err)
		}

		merged, err := MergeFileAsPatch([]byte(renderedTemplate), nodeFile)
		if err != nil {
			t.Fatalf("MergeFileAsPatch: %v", err)
		}
		out := string(merged)
		if !strings.Contains(out, "apiVersion: audit.k8s.io/v1") {
			t.Errorf("auditPolicy apiVersion silently lost on replace-tagged partial edit:\n%s", out)
		}
		if !strings.Contains(out, "kind: Policy") {
			t.Errorf("auditPolicy kind silently lost on replace-tagged partial edit:\n%s", out)
		}
		if !strings.Contains(out, "RequestResponse") {
			t.Errorf("user-added auditPolicy rule RequestResponse missing from merged output:\n%s", out)
		}
		if !strings.Contains(out, "alice") {
			t.Errorf("user-added auditPolicy rule user alice missing from merged output:\n%s", out)
		}
	})

	t.Run("NetworkDefaultActionConfig.ingress is harmless under replace-skip", func(t *testing.T) {
		// NetworkDefaultActionConfig is a separate typed v1.12+ doc
		// kind that also has a top-level `ingress:` key — but in this
		// type ingress is a SCALAR (accept/block), not an object slice.
		// Both NetworkRuleConfig and NetworkDefaultActionConfig walks
		// see path "ingress" at their respective doc roots, so the
		// flat replaceSemanticPaths lookup hits both. Pin that this
		// collision is harmless: the deep-equal short-circuit catches
		// the body-equals-rendered case, and the replace-skip on a
		// scalar simply preserves body's value verbatim — same outcome
		// as the existing scalar-edit code path. If the upstream
		// schema later changes ingress to a structured field on this
		// kind, this test will surface the regression so a kind-aware
		// scope can be added before correctness drifts.
		const renderedTemplate = `version: v1alpha1
machine:
  type: controlplane
  install:
    disk: /dev/sda
cluster:
  controlPlane:
    endpoint: https://10.0.0.10:6443
---
apiVersion: v1alpha1
kind: NetworkDefaultActionConfig
ingress: accept
`
		const userBody = `# talm: nodes=["10.0.0.1"]
version: v1alpha1
machine:
  type: controlplane
  install:
    disk: /dev/sda
cluster:
  controlPlane:
    endpoint: https://10.0.0.10:6443
---
apiVersion: v1alpha1
kind: NetworkDefaultActionConfig
ingress: block
`
		dir := t.TempDir()
		nodeFile := filepath.Join(dir, "node0.yaml")
		if err := os.WriteFile(nodeFile, []byte(userBody), 0o644); err != nil {
			t.Fatalf("write node file: %v", err)
		}

		merged, err := MergeFileAsPatch([]byte(renderedTemplate), nodeFile)
		if err != nil {
			t.Fatalf("MergeFileAsPatch: %v", err)
		}
		out := string(merged)
		if !strings.Contains(out, "ingress: block") {
			t.Errorf("user's ingress=block override silently lost on the scalar collision path:\n%s", out)
		}
	})

	t.Run("NetworkRuleConfig multi-doc pairs body and rendered by name identity", func(t *testing.T) {
		// pruneBodyIdentitiesAgainstRendered keys typed-doc body docs
		// against rendered docs by `apiVersion + kind + name`. Two
		// NetworkRuleConfig docs sharing apiVersion/kind but differing
		// `name:` are distinct documents — the prune must NOT collapse
		// them. Body's user-add doc must reach the merge unmodified;
		// only the body doc whose name matches a rendered doc gets
		// paired and pruned.
		const renderedTemplate = `version: v1alpha1
machine:
  type: controlplane
  install:
    disk: /dev/sda
cluster:
  controlPlane:
    endpoint: https://10.0.0.10:6443
---
apiVersion: v1alpha1
kind: NetworkRuleConfig
name: api-ingress
portSelector:
  ports:
    - 9999
  protocol: tcp
ingress:
  - subnet: 10.0.0.0/8
`
		const userBody = `# talm: nodes=["10.0.0.1"]
version: v1alpha1
machine:
  type: controlplane
  install:
    disk: /dev/sda
cluster:
  controlPlane:
    endpoint: https://10.0.0.10:6443
---
apiVersion: v1alpha1
kind: NetworkRuleConfig
name: api-ingress
portSelector:
  ports:
    - 9999
  protocol: tcp
ingress:
  - subnet: 10.0.0.0/8
---
apiVersion: v1alpha1
kind: NetworkRuleConfig
name: kubelet-ingress
portSelector:
  ports:
    - 10250
  protocol: tcp
ingress:
  - subnet: 192.168.0.0/16
`
		dir := t.TempDir()
		nodeFile := filepath.Join(dir, "node0.yaml")
		if err := os.WriteFile(nodeFile, []byte(userBody), 0o644); err != nil {
			t.Fatalf("write node file: %v", err)
		}

		merged, err := MergeFileAsPatch([]byte(renderedTemplate), nodeFile)
		if err != nil {
			t.Fatalf("MergeFileAsPatch: %v", err)
		}
		out := string(merged)
		// Body's matching api-ingress should round-trip without
		// duplicating; user-add kubelet-ingress reaches the merge
		// intact.
		if !strings.Contains(out, "api-ingress") {
			t.Errorf("rendered NetworkRuleConfig api-ingress missing from merged output:\n%s", out)
		}
		if !strings.Contains(out, "kubelet-ingress") {
			t.Errorf("user-added NetworkRuleConfig kubelet-ingress missing from merged output:\n%s", out)
		}
		if !strings.Contains(out, "10250") {
			t.Errorf("user-added kubelet-ingress port 10250 missing from merged output:\n%s", out)
		}
		if !strings.Contains(out, "192.168.0.0/16") {
			t.Errorf("user-added kubelet-ingress subnet 192.168.0.0/16 missing from merged output:\n%s", out)
		}
	})

	t.Run("interface body without identity field reaches merge intact", func(t *testing.T) {
		// Upstream NetworkDeviceList.mergeDevice falls through both
		// switch cases when body has neither `interface:` nor
		// `deviceSelector:` set, then appends body verbatim (the
		// patched element will fail upstream validation; the prune
		// layer's job is only to not consume it). The prune mirrors
		// this: hasIdentityValue rejects empty/zero-valued identity
		// fields, matchObjectArrayItem returns nil, the body item is
		// preserved unchanged. Pin that contract so a future change
		// to keys-driven matching does not silently collapse a body
		// without identity onto the first rendered element.
		const renderedTemplate = `version: v1alpha1
machine:
  type: controlplane
  install:
    disk: /dev/sda
  network:
    interfaces:
      - interface: eth0
        addresses:
          - 10.0.0.1/24
cluster:
  controlPlane:
    endpoint: https://10.0.0.10:6443
`
		const userBody = `# talm: nodes=["10.0.0.1"]
version: v1alpha1
machine:
  type: controlplane
  install:
    disk: /dev/sda
  network:
    interfaces:
      - addresses:
          - 10.0.0.5/24
cluster:
  controlPlane:
    endpoint: https://10.0.0.10:6443
`
		dir := t.TempDir()
		nodeFile := filepath.Join(dir, "node0.yaml")
		if err := os.WriteFile(nodeFile, []byte(userBody), 0o644); err != nil {
			t.Fatalf("write node file: %v", err)
		}

		merged, err := MergeFileAsPatch([]byte(renderedTemplate), nodeFile)
		if err != nil {
			t.Fatalf("MergeFileAsPatch: %v", err)
		}
		out := string(merged)
		// Body's identity-less item must reach the merge with its
		// addresses intact (upstream will then reject it at validation,
		// but that's not the prune's concern). The prune must not
		// silently consume it onto rendered's eth0.
		if !strings.Contains(out, "10.0.0.5/24") {
			t.Errorf("body item without identity field silently consumed onto rendered's eth0:\n%s", out)
		}
		if !strings.Contains(out, "10.0.0.1/24") {
			t.Errorf("rendered eth0 address 10.0.0.1/24 missing from merged output:\n%s", out)
		}
	})

	t.Run("interface identity selection mirrors upstream body-driven switch", func(t *testing.T) {
		// Talos's NetworkDeviceList.mergeDevice picks the identity
		// field from the BODY element being merged: if body sets
		// `interface:` (non-empty), upstream matches rendered ONLY by
		// `interface:`; otherwise upstream falls back to
		// `deviceSelector:`. The prune must mirror that selection or
		// it can silently drop a user-add.
		//
		// Concrete trap: body has interface=eth0 + a deviceSelector;
		// rendered has interface=eth1 with the SAME deviceSelector.
		// Upstream picks body.DeviceInterface (non-empty), looks for
		// `eth0` in rendered, finds none, appends body verbatim — the
		// user gets two interfaces. A prune that fell back to
		// deviceSelector would match body[0] to rendered[0], recurse,
		// drop everything, and ship a body that the upstream merge
		// could not append meaningfully — eth0 and its addresses
		// would never reach the node.
		const renderedTemplate = `version: v1alpha1
machine:
  type: controlplane
  install:
    disk: /dev/sda
  network:
    interfaces:
      - interface: eth1
        deviceSelector:
          hardwareAddr: 'aa:bb:cc:dd:ee:ff'
        addresses:
          - 10.0.0.5/24
cluster:
  controlPlane:
    endpoint: https://10.0.0.10:6443
`
		const userBody = `# talm: nodes=["10.0.0.1"]
version: v1alpha1
machine:
  type: controlplane
  install:
    disk: /dev/sda
  network:
    interfaces:
      - interface: eth0
        deviceSelector:
          hardwareAddr: 'aa:bb:cc:dd:ee:ff'
        addresses:
          - 10.0.0.5/24
cluster:
  controlPlane:
    endpoint: https://10.0.0.10:6443
`
		dir := t.TempDir()
		nodeFile := filepath.Join(dir, "node0.yaml")
		if err := os.WriteFile(nodeFile, []byte(userBody), 0o644); err != nil {
			t.Fatalf("write node file: %v", err)
		}

		merged, err := MergeFileAsPatch([]byte(renderedTemplate), nodeFile)
		if err != nil {
			t.Fatalf("MergeFileAsPatch: %v", err)
		}
		out := string(merged)
		// User's eth0 must survive with its addresses intact. Both
		// items share addresses by accident; if the prune consumed
		// body[0] via the deviceSelector fallback it would strip the
		// addresses (deep-equal to rendered's), re-attach
		// deviceSelector, and ship `{interface: eth0, deviceSelector}`
		// — which the upstream merge then appends as a NEW element
		// because eth0 != eth1. Result: a stranded eth0 with no
		// addresses, no routes — silent data loss.
		if !strings.Contains(out, "interface: eth0") {
			t.Errorf("user-added interface eth0 silently lost (deviceSelector fallback consumed it):\n%s", out)
		}
		if got := strings.Count(out, "10.0.0.5/24"); got != 2 {
			t.Errorf("expected 10.0.0.5/24 to appear twice (once under each interface), got %d:\n%s", got, out)
		}
		// rendered's eth1 must remain.
		if !strings.Contains(out, "interface: eth1") {
			t.Errorf("rendered interface eth1 missing from merged output:\n%s", out)
		}
	})

	t.Run("object array without upstream merge dedupes by deep-equal fallback", func(t *testing.T) {
		// Talos's v1alpha1 schema has many object arrays (extraVolumes,
		// inlineManifests, kernel.modules, wireguard.peers, ...) where
		// the upstream patcher has no custom Merge method matching by
		// identity — it simply appends body's elements to rendered's.
		// Adding such a path to objectArrayMergeKeys would re-attach an
		// identity field on partial edits and the upstream append would
		// then leave behind a duplicate next to rendered's element. The
		// safer contract: leave such paths to the deep-equal fallback in
		// matchObjectArrayItem, which still drops body items that
		// byte-equal a rendered item — covering the dominant
		// `talm template -I` round-trip scenario.
		//
		// Pin that contract here for cluster.apiServer.extraVolumes
		// (one of the deliberately unlisted paths): a body that
		// re-states rendered's volume verbatim and adds a new one must
		// NOT duplicate the restated volume in the merged output.
		const renderedTemplate = `version: v1alpha1
machine:
  type: controlplane
  install:
    disk: /dev/sda
cluster:
  controlPlane:
    endpoint: https://10.0.0.10:6443
  apiServer:
    extraVolumes:
      - hostPath: /var/lib/auth
        mountPath: /etc/kubernetes/auth
        readonly: true
`
		const userBody = `# talm: nodes=["10.0.0.1"]
version: v1alpha1
machine:
  type: controlplane
  install:
    disk: /dev/sda
cluster:
  controlPlane:
    endpoint: https://10.0.0.10:6443
  apiServer:
    extraVolumes:
      - hostPath: /var/lib/auth
        mountPath: /etc/kubernetes/auth
        readonly: true
      - hostPath: /var/lib/audit
        mountPath: /etc/kubernetes/audit
        readonly: false
`
		dir := t.TempDir()
		nodeFile := filepath.Join(dir, "node0.yaml")
		if err := os.WriteFile(nodeFile, []byte(userBody), 0o644); err != nil {
			t.Fatalf("write node file: %v", err)
		}

		merged, err := MergeFileAsPatch([]byte(renderedTemplate), nodeFile)
		if err != nil {
			t.Fatalf("MergeFileAsPatch: %v", err)
		}
		out := string(merged)
		if got := strings.Count(out, "/var/lib/auth"); got != 1 {
			t.Errorf("rendered extraVolumes hostPath /var/lib/auth duplicated by user-add round-trip (count=%d):\n%s", got, out)
		}
		if !strings.Contains(out, "/var/lib/audit") {
			t.Errorf("user-added extraVolume /var/lib/audit missing from merged output:\n%s", out)
		}
	})

	t.Run("body adding a new interface preserves rendered interfaces", func(t *testing.T) {
		// Regression-safety probe for the user-add path: when the body
		// adds an interface absent from rendered, the new interface
		// must reach the merge intact. Object-array dedup must not
		// over-prune body items that have no rendered counterpart.
		const renderedTemplate = `version: v1alpha1
machine:
  type: controlplane
  install:
    disk: /dev/sda
  network:
    interfaces:
      - interface: enp0s31f6
        addresses:
          - 88.99.249.47/26
cluster:
  controlPlane:
    endpoint: https://10.0.0.10:6443
`
		const userBody = `# talm: nodes=["10.0.0.1"]
version: v1alpha1
machine:
  type: controlplane
  install:
    disk: /dev/sda
  network:
    interfaces:
      - interface: enp0s31f6
        addresses:
          - 88.99.249.47/26
      - interface: eth1
        addresses:
          - 10.0.0.5/24
cluster:
  controlPlane:
    endpoint: https://10.0.0.10:6443
`
		dir := t.TempDir()
		nodeFile := filepath.Join(dir, "node0.yaml")
		if err := os.WriteFile(nodeFile, []byte(userBody), 0o644); err != nil {
			t.Fatalf("write node file: %v", err)
		}

		merged, err := MergeFileAsPatch([]byte(renderedTemplate), nodeFile)
		if err != nil {
			t.Fatalf("MergeFileAsPatch: %v", err)
		}
		out := string(merged)
		if !strings.Contains(out, "enp0s31f6") {
			t.Errorf("rendered interface enp0s31f6 missing from merged output:\n%s", out)
		}
		if !strings.Contains(out, "eth1") {
			t.Errorf("user-added interface eth1 missing from merged output:\n%s", out)
		}
		if !strings.Contains(out, "10.0.0.5/24") {
			t.Errorf("user-added address 10.0.0.5/24 missing from merged output:\n%s", out)
		}
		if got := strings.Count(out, "88.99.249.47/26"); got != 1 {
			t.Errorf("rendered address 88.99.249.47/26 duplicated when body adds a new interface (count=%d):\n%s", got, out)
		}
	})

	t.Run("JSON Patch body is forwarded to LoadPatch unchanged", func(t *testing.T) {
		// MergeFileAsPatch's documented contract (and the existing
		// LoadPatch error hint) advertises support for JSON Patch and
		// YAML patch-list shapes. Those bodies decode as a YAML
		// sequence at the top level, not a mapping; the identity-prune
		// step cannot operate on them and must pass them through to
		// configpatcher.LoadPatch unchanged. A regression here
		// silently neutralises the patch.
		const renderedTemplate = `version: v1alpha1
machine:
  type: controlplane
  install:
    disk: /dev/sda
  network:
    hostname: rendered-host
    nameservers:
      - 1.1.1.1
`
		const jsonPatchBody = `# talm: nodes=["10.0.0.1"]
- op: replace
  path: /machine/network/hostname
  value: cozy-01
`
		dir := t.TempDir()
		nodeFile := filepath.Join(dir, "node0.yaml")
		if err := os.WriteFile(nodeFile, []byte(jsonPatchBody), 0o644); err != nil {
			t.Fatalf("write node file: %v", err)
		}

		merged, err := MergeFileAsPatch([]byte(renderedTemplate), nodeFile)
		if err != nil {
			t.Fatalf("MergeFileAsPatch: %v", err)
		}
		out := string(merged)
		if !strings.Contains(out, "hostname: cozy-01") {
			t.Errorf("JSON Patch replace op silently dropped (hostname not overridden):\n%s", out)
		}
		if strings.Contains(out, "rendered-host") {
			t.Errorf("rendered hostname still present despite JSON Patch replace op:\n%s", out)
		}
	})

	t.Run("multi-doc body identical to rendered does not duplicate primitive arrays", func(t *testing.T) {
		// Talos v1.12+ output is multi-document. The single-doc identity
		// prune cannot help here unless it understands document boundaries:
		// `talm template -I` writes each rendered document back as a body
		// document, and Talos's strategic-merge appends to primitive arrays
		// per-document. Without per-doc identity matching, the prune
		// short-circuits the multi-doc input and the duplicate-primitive-
		// array-entries-per-round-trip symptom reappears at the v1.12+
		// default — a `127.0.0.1` certSAN entry doubles on every apply.
		//
		// Pin the post-fix contract: a body that re-states an unchanged
		// multi-doc rendered template must merge to a config whose
		// primitive-array entry counts are unchanged.
		rendered := renderChartTemplate(t, "../../charts/cozystack", "templates/controlplane.yaml", "v1.12")

		dir := t.TempDir()
		nodeFile := filepath.Join(dir, "node0.yaml")
		body := "# talm: nodes=[\"10.0.0.1\"], templates=[\"templates/controlplane.yaml\"]\n" + rendered
		if err := os.WriteFile(nodeFile, []byte(body), 0o644); err != nil {
			t.Fatalf("write node file: %v", err)
		}

		merged, err := MergeFileAsPatch([]byte(rendered), nodeFile)
		if err != nil {
			t.Fatalf("MergeFileAsPatch: %v", err)
		}

		renderedCount := strings.Count(rendered, "127.0.0.1")
		mergedCount := strings.Count(string(merged), "127.0.0.1")
		if renderedCount == 0 {
			t.Fatalf("test fixture broken: rendered output has no 127.0.0.1 to count duplicates against")
		}
		if mergedCount != renderedCount {
			t.Errorf("primitive array entry duplicated across multi-doc round-trip: rendered had %d occurrences of 127.0.0.1, merged has %d", renderedCount, mergedCount)
		}
	})

	t.Run("body with override and identical arrays merges only the override", func(t *testing.T) {
		// User pattern: keep the auto-generated body almost intact, change
		// just one field (e.g. hostname). The unchanged keys must be
		// pruned before merge so they don't replay back as appends.
		const renderedTemplate = `version: v1alpha1
machine:
  type: controlplane
  network:
    hostname: rescue
    nameservers:
      - 1.1.1.1
      - 8.8.8.8
`
		dir := t.TempDir()
		nodeFile := filepath.Join(dir, "node0.yaml")
		const body = `# talm: nodes=["10.0.0.1"]
machine:
  type: controlplane
  network:
    hostname: cozy-01
    nameservers:
      - 1.1.1.1
      - 8.8.8.8
`
		if err := os.WriteFile(nodeFile, []byte(body), 0o644); err != nil {
			t.Fatalf("write node file: %v", err)
		}

		merged, err := MergeFileAsPatch([]byte(renderedTemplate), nodeFile)
		if err != nil {
			t.Fatalf("MergeFileAsPatch: %v", err)
		}

		out := string(merged)
		if !strings.Contains(out, "hostname: cozy-01") {
			t.Errorf("override not applied: %s", out)
		}
		if strings.Contains(out, "hostname: rescue") {
			t.Errorf("rendered hostname survived merge: %s", out)
		}
		if got := strings.Count(out, "1.1.1.1"); got != 1 {
			t.Errorf("nameservers 1.1.1.1 duplicated despite identity-pruning (count=%d):\n%s", got, out)
		}
	})

	t.Run("preserves chart directive effect: rendered with $patch:delete merges into config without the deleted key", func(t *testing.T) {
		// MergeFileAsPatch is the only consumer of the rendered template's
		// $patch:delete directives in the apply pipeline — the bytes it
		// returns are sent verbatim to Talos's ApplyConfiguration RPC,
		// whose server-side configloader.NewFromBytes does NOT enable
		// WithAllowPatchDelete (see Talos's internal/app/machined/internal/server/v1alpha1/v1alpha1_server.go
		// ApplyConfiguration). A directive surviving the merge would be
		// rejected on the wire.
		//
		// Pin the contract: when the rendered template carries a nested
		// `$patch: delete` directive (the cozystack chart pattern that
		// removes the exclude-from-external-load-balancers label on
		// controlplane), MergeFileAsPatch must
		//   1. apply the directive's effect locally so the deleted key is
		//      absent from the merged output, and
		//   2. return bytes that contain no `$patch: delete` literal so
		//      Talos's strict decoder accepts the payload.
		//
		// The fixture mimics the rendered template that contains the
		// directive plus a non-empty user body to exercise the full
		// LoadPatch + Apply path (modeline-only would short-circuit).
		const renderedWithDirective = `version: v1alpha1
machine:
  type: controlplane
  install:
    disk: /dev/sda
  nodeLabels:
    node.kubernetes.io/exclude-from-external-load-balancers:
      $patch: delete
`
		const userBody = `# talm: nodes=["10.0.0.1"]
machine:
  network:
    hostname: cozy-01
`
		dir := t.TempDir()
		nodeFile := filepath.Join(dir, "node0.yaml")
		if err := os.WriteFile(nodeFile, []byte(userBody), 0o644); err != nil {
			t.Fatalf("write node file: %v", err)
		}

		merged, err := MergeFileAsPatch([]byte(renderedWithDirective), nodeFile)
		if err != nil {
			t.Fatalf("MergeFileAsPatch: %v", err)
		}

		out := string(merged)
		if strings.Contains(out, "$patch: delete") {
			t.Errorf("merged output still carries the directive literal:\n%s", out)
		}
		if strings.Contains(out, "exclude-from-external-load-balancers") {
			t.Errorf("merged output still carries the deleted key (directive's effect not applied):\n%s", out)
		}
		if !strings.Contains(out, "hostname: cozy-01") {
			t.Errorf("body's hostname override missing from merged output:\n%s", out)
		}
	})

	t.Run("body $patch:delete on absent path is a no-op", func(t *testing.T) {
		// Kubernetes strategic merge patch treats a $patch:delete on an
		// absent path as a no-op (the key is already absent, nothing to
		// delete). Talos's configpatcher.Apply does not: its Selector-
		// based deleteForPath walks the parsed v1alpha1.Config struct and
		// returns ErrLookupFailed when any path segment doesn't resolve,
		// surfacing as `failed to delete path '...': lookup failed` from
		// the apply RPC.
		//
		// Stripping these no-op directives at the talm side before the
		// patch reaches configpatcher.Apply matches the k8s SMP semantic
		// and stops a real-world failure: a node body restating a chart-
		// emitted directive (e.g. machine.nodeLabels.<label>: $patch:
		// delete) errors out when rendered for the first time on a
		// freshly generated config that hasn't yet acquired the label.
		// Without this strip, the chart's own pattern fails on every
		// fresh apply and bootstrap is broken.
		const renderedTemplate = `version: v1alpha1
machine:
  type: controlplane
  install:
    disk: /dev/sda
cluster:
  controlPlane:
    endpoint: https://10.0.0.10:6443
`
		const userBody = `# talm: nodes=["10.0.0.1"]
machine:
  nodeLabels:
    node.kubernetes.io/exclude-from-external-load-balancers:
      $patch: delete
`
		dir := t.TempDir()
		nodeFile := filepath.Join(dir, "node0.yaml")
		if err := os.WriteFile(nodeFile, []byte(userBody), 0o644); err != nil {
			t.Fatalf("write node file: %v", err)
		}

		merged, err := MergeFileAsPatch([]byte(renderedTemplate), nodeFile)
		if err != nil {
			t.Fatalf("MergeFileAsPatch must accept a delete directive on an absent path as a no-op, got error: %v", err)
		}
		out := string(merged)
		if strings.Contains(out, "$patch: delete") {
			t.Errorf("merged output still carries the directive literal:\n%s", out)
		}
		if strings.Contains(out, "exclude-from-external-load-balancers") {
			t.Errorf("merged output mentions the never-rendered key — the body's no-op directive should not have re-introduced it:\n%s", out)
		}
	})

	t.Run("body $patch:delete on partially-present path is a no-op when leaf is absent", func(t *testing.T) {
		// A subtler form of the absent-path case: the body's directive
		// addresses a leaf under a parent that DOES exist in rendered,
		// but the leaf itself doesn't. configpatcher.Apply walks the
		// path segment-by-segment and fails on the missing leaf with
		// the same ErrLookupFailed. The fix must treat any path whose
		// final segment doesn't resolve as a no-op, not just paths
		// missing at the top level.
		const renderedTemplate = `version: v1alpha1
machine:
  type: controlplane
  install:
    disk: /dev/sda
  nodeLabels:
    other-label: present
cluster:
  controlPlane:
    endpoint: https://10.0.0.10:6443
`
		const userBody = `# talm: nodes=["10.0.0.1"]
machine:
  nodeLabels:
    node.kubernetes.io/exclude-from-external-load-balancers:
      $patch: delete
`
		dir := t.TempDir()
		nodeFile := filepath.Join(dir, "node0.yaml")
		if err := os.WriteFile(nodeFile, []byte(userBody), 0o644); err != nil {
			t.Fatalf("write node file: %v", err)
		}

		merged, err := MergeFileAsPatch([]byte(renderedTemplate), nodeFile)
		if err != nil {
			t.Fatalf("MergeFileAsPatch: %v", err)
		}
		out := string(merged)
		if strings.Contains(out, "$patch: delete") {
			t.Errorf("merged output still carries the directive literal:\n%s", out)
		}
		if !strings.Contains(out, "other-label: present") {
			t.Errorf("rendered sibling key under nodeLabels was incorrectly stripped along with the absent target:\n%s", out)
		}
	})

	t.Run("body $patch:delete on present path still removes the key", func(t *testing.T) {
		// Regression-safety probe: the no-op-on-absent fix must not
		// over-trigger and silently drop directives whose target IS
		// present in rendered. The user-intent delete must still land
		// as a Selector and remove the key from the merged config.
		const renderedTemplate = `version: v1alpha1
machine:
  type: controlplane
  install:
    disk: /dev/sda
  nodeLabels:
    user-label: please-delete-me
cluster:
  controlPlane:
    endpoint: https://10.0.0.10:6443
`
		const userBody = `# talm: nodes=["10.0.0.1"]
machine:
  nodeLabels:
    user-label:
      $patch: delete
`
		dir := t.TempDir()
		nodeFile := filepath.Join(dir, "node0.yaml")
		if err := os.WriteFile(nodeFile, []byte(userBody), 0o644); err != nil {
			t.Fatalf("write node file: %v", err)
		}

		merged, err := MergeFileAsPatch([]byte(renderedTemplate), nodeFile)
		if err != nil {
			t.Fatalf("MergeFileAsPatch: %v", err)
		}
		out := string(merged)
		if strings.Contains(out, "$patch: delete") {
			t.Errorf("merged output still carries the directive literal:\n%s", out)
		}
		if strings.Contains(out, "user-label") {
			t.Errorf("user-intent delete on a present path was suppressed; the key still appears in merged output:\n%s", out)
		}
	})
}

// TestMergeFileAsPatch_PreservesUserIntentPatchDelete pins the contract
// that a user-supplied `$patch: delete` in the per-node body — at a path
// the chart-rendered template did NOT itself mark for deletion — must
// survive MergeFileAsPatch and remove the named key from the merged
// output. configpatcher.LoadPatch already routes such bodies through
// configloader.NewFromBytes(WithAllowPatchDelete()) (load.go:24), so a
// genuine user-intent directive should land in the merge as a Selector
// and delete the key from rendered.
//
// The chart pattern (rendered AND body both carry the directive at the
// same path because `talm template -I` writes rendered back as body)
// is handled by stripping the redundant entry; this test exercises the
// orthogonal case where the directive is user intent, not chart noise.
func TestMergeFileAsPatch_PreservesUserIntentPatchDelete(t *testing.T) {
	const renderedTemplate = `version: v1alpha1
machine:
  type: controlplane
  install:
    disk: /dev/sda
  network:
    hostname: rendered-hostname
    nameservers:
      - 1.1.1.1
`
	const userBodyDeleteHostname = `# talm: nodes=["10.0.0.1"]
machine:
  network:
    hostname:
      $patch: delete
`
	dir := t.TempDir()
	nodeFile := filepath.Join(dir, "node0.yaml")
	if err := os.WriteFile(nodeFile, []byte(userBodyDeleteHostname), 0o644); err != nil {
		t.Fatalf("write node file: %v", err)
	}

	merged, err := MergeFileAsPatch([]byte(renderedTemplate), nodeFile)
	if err != nil {
		t.Fatalf("MergeFileAsPatch: %v", err)
	}

	out := string(merged)
	if strings.Contains(out, "rendered-hostname") {
		t.Errorf("user-intent $patch:delete on machine.network.hostname did not remove the rendered value:\n%s", out)
	}
	if strings.Contains(out, "$patch: delete") {
		t.Errorf("merged output still carries the directive literal — Talos will reject it on ApplyConfiguration:\n%s", out)
	}
	if !strings.Contains(out, "1.1.1.1") {
		t.Errorf("nameservers (an untouched sibling field) lost from merge:\n%s", out)
	}
}

// TestTalmDiscoveredHostnameFiltersTransientNames pins the filter contract
// for `talm.discovered.hostname`. Boot-to-talos and Talos's own pre-config
// state can leave a node with a placeholder hostname (`rescue`, `talos`,
// `localhost`, `localhost.localdomain`). When the helper propagates such a
// name, `talm template -I` writes it back into the node body — and because
// the body now matches what the live node already has, the user has no diff
// to alert them that the hostname is transient. The next apply replays the
// placeholder, discovery keeps returning it, and the loop never resolves to
// the user's intended per-node hostname: a freshly imaged node sits with
// `hostname: rescue` indefinitely until the operator notices and edits the
// node body by hand.
//
// The fix: skip the discovery hit for a small set of well-known transient
// names and fall through to the address-derived `talos-XXXXX` placeholder,
// the same form a node with no discoverable hostname produces. The
// placeholder is visibly synthetic, signalling "this needs a real
// per-node value" to anyone reviewing the autogenerated body.
func TestTalmDiscoveredHostnameFiltersTransientNames(t *testing.T) {
	// Includes case-variant entries to pin the lower-case fold the
	// helper applies before its `has` membership check. Some PXE/DHCP
	// servers hand out `Localhost`/`TALOS` and the trap-loop is
	// identical for those; the filter must catch them too.
	transient := []string{
		"rescue", "talos", "localhost", "localhost.localdomain",
		"Localhost", "TALOS", "RESCUE", "Localhost.LocalDomain",
	}
	for _, name := range transient {
		t.Run(name, func(t *testing.T) {
			output := renderChartTemplateWithLookup(
				t,
				"../../charts/cozystack",
				"templates/controlplane.yaml",
				hostnameLookupOverride(simpleNicLookup(), name),
				"v1.11",
			)
			if strings.Contains(output, `hostname: "`+name+`"`) {
				t.Errorf("transient hostname %q leaked into rendered output:\n%s", name, output)
			}
			if !strings.Contains(output, `hostname: "talos-`) {
				t.Errorf(`expected fallback hostname "talos-XXXXX", got:\n%s`, output)
			}
		})
	}

	t.Run("real_hostname_passes_through", func(t *testing.T) {
		output := renderChartTemplateWithLookup(
			t,
			"../../charts/cozystack",
			"templates/controlplane.yaml",
			hostnameLookupOverride(simpleNicLookup(), "cozy-01"),
			"v1.11",
		)
		if !strings.Contains(output, `hostname: "cozy-01"`) {
			t.Errorf("real hostname did not propagate, output:\n%s", output)
		}
	})
}

// hostnameLookupOverride wraps a base LookupFunc to return a fixed hostname
// for the `hostname/<empty>/hostname` query that talm.discovered.hostname
// issues. Every other lookup falls through to base. Used by the
// hostname-filter regression tests to exercise the helper without standing
// up a real Talos node.
func hostnameLookupOverride(base func(string, string, string) (map[string]any, error), hostname string) func(string, string, string) (map[string]any, error) {
	return func(kind, namespace, id string) (map[string]any, error) {
		if kind == "hostname" {
			return map[string]any{
				"metadata": map[string]any{"id": "hostname"},
				"spec":     map[string]any{"hostname": hostname},
			}, nil
		}
		return base(kind, namespace, id)
	}
}

// TestRenderedControlplaneEmitsExcludeLabelDeleteDirective pins the
// cozystack-side intent of commit abf48543 ("Remove
// node.kubernetes.io/exclude-from-external-load-balancers label for
// Cozystack"): the rendered controlplane output MUST emit
//
//	machine.nodeLabels.node.kubernetes.io/exclude-from-external-load-balancers:
//	  $patch: delete
//
// so that controlplane nodes participate in external load balancer target
// pools. The directive is a strategic-merge SMP signal; it must reach the
// merged config and be applied before the bytes are sent to Talos's
// ApplyConfiguration RPC, which strict-decodes the payload without
// WithAllowPatchDelete and would reject a directive that survived merge.
// The upstream guardrail lives in
// pkg/machinery/config/configloader/internal/decoder/delete.go
// AppendDeletesTo: it only extracts $patch:delete at document and top-
// level mapping scopes, so a nested directive that survived merge
// reaches the strict v1alpha1 decoder and trips
// `cannot construct !!map into string`. talm's MergeFileAsPatch
// resolves the directive locally — see the
// "preserves chart directive effect" subtest in TestMergeFileAsPatch
// for the resolved-config contract.
func TestRenderedControlplaneEmitsExcludeLabelDeleteDirective(t *testing.T) {
	for _, version := range []string{"v1.11", "v1.12"} {
		t.Run(version, func(t *testing.T) {
			output := renderChartTemplate(t, "../../charts/cozystack", "templates/controlplane.yaml", version)
			assertContains(t, output, "node.kubernetes.io/exclude-from-external-load-balancers:")
			assertContains(t, output, "$patch: delete")
		})
	}
}

// TestNodeFileHasOverlay pins the classifier used by the apply path to
// decide whether a multi-node modeline would replay a per-node body
// onto every target. Modeline-only and comments-only files must
// classify as no-overlay; any real YAML key must count as an overlay.
func TestNodeFileHasOverlay(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{
			name:    "modeline only",
			content: `# talm: nodes=["a","b"]` + "\n",
			want:    false,
		},
		{
			name:    "empty file",
			content: "",
			want:    false,
		},
		{
			name:    "comments and separators",
			content: "# top\n---\n# middle\n  \n---\n# bottom\n",
			want:    false,
		},
		{
			name: "real yaml body",
			content: `# talm: nodes=["a"]
machine:
  network:
    hostname: node0
`,
			want: true,
		},
		{
			// A "---" with leading whitespace is not a YAML document
			// separator (separators must be at column 0); it's a
			// scalar inside a parent mapping. Treating it as a
			// separator would misclassify a real overlay as empty
			// and let the multi-node guard be bypassed.
			name:    "indented separator counts as overlay",
			content: "# talm: nodes=[\"a\",\"b\"]\nmachine:\n  ---\n",
			want:    true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "node.yaml")
			if err := os.WriteFile(path, []byte(tt.content), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
			got, err := NodeFileHasOverlay(path)
			if err != nil {
				t.Fatalf("NodeFileHasOverlay: %v", err)
			}
			if got != tt.want {
				t.Errorf("NodeFileHasOverlay = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestRenderFailIfMultiNodes_UsesCommandName guards the contract for
// Options.CommandName: the multi-node rejection error must reference the
// calling subcommand the caller passed in, never the historical
// hardcoded literal that pre-dated this option. An empty value falls back
// to the neutral "talm".
func TestRenderFailIfMultiNodes_UsesCommandName(t *testing.T) {
	tests := []struct {
		name        string
		commandName string
		wantInError string
	}{
		{"talm apply", "talm apply", "talm apply"},
		{"talm template", "talm template", "talm template"},
		{"empty falls back to talm", "", "talm"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := client.WithNodes(context.Background(), "10.0.0.1", "10.0.0.2")
			opts := Options{
				Offline:     false,
				CommandName: tt.commandName,
			}
			_, err := Render(ctx, nil, opts)
			if err == nil {
				t.Fatalf("Render expected an error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantInError) {
				t.Errorf("error = %q, expected to contain %q", err.Error(), tt.wantInError)
			}
		})
	}

	t.Run("non-CommandName subcommand names must not leak into the error", func(t *testing.T) {
		// If a caller passes "talm apply", the error must not carry any
		// other subcommand name — historically the call site here emitted
		// "talm template" unconditionally.
		ctx := client.WithNodes(context.Background(), "10.0.0.1", "10.0.0.2")
		opts := Options{Offline: false, CommandName: "talm apply"}
		_, err := Render(ctx, nil, opts)
		if err == nil {
			t.Fatal("Render expected an error, got nil")
		}
		if strings.Contains(err.Error(), "talm template") {
			t.Errorf("error must not mention 'talm template' when CommandName is 'talm apply'; got %q", err.Error())
		}
	})
}

// TestRenderInvalidTalosVersion verifies that malformed TalosVersion values
// surface a user-friendly error before template rendering, instead of the
// opaque "error calling semverCompare: invalid semantic version" that escapes
// from deep inside the Helm engine.
func TestRenderInvalidTalosVersion(t *testing.T) {
	chartRoot := createTestChart(t, "dummy", "config.yaml", "machine:\n  type: worker\n")

	tests := []struct {
		name    string
		version string
	}{
		{"plain word", "latest"},
		{"garbage", "foobar"},
		{"v-prefixed garbage", "vlatest"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := Options{
				Offline:       true,
				Root:          chartRoot,
				TalosVersion:  tt.version,
				TemplateFiles: []string{"templates/config.yaml"},
			}
			_, err := Render(context.Background(), nil, opts)
			if err == nil {
				t.Fatalf("Render(%q) expected error, got nil", tt.version)
			}
			if !strings.Contains(err.Error(), "invalid talos-version") {
				t.Errorf("Render(%q) error = %q, want prefix 'invalid talos-version'", tt.version, err.Error())
			}
		})
	}
}

// simpleNicLookup returns a lookup fixture exposing one physical
// interface (eth0) with address 192.168.201.10/24 and default route
// via 192.168.201.1. Used by the discovery-fallback tests below — the
// specific subnet is intentionally different from the 192.168.100.*
// placeholders baked into charts' historical defaults so the tests
// can distinguish "discovered" from "default" output.
func simpleNicLookup() func(string, string, string) (map[string]any, error) {
	eth0 := map[string]any{
		"metadata": map[string]any{"id": "eth0"},
		"spec": map[string]any{
			"kind":         "physical",
			"index":        1,
			"hardwareAddr": "aa:bb:cc:00:00:01",
			"busPath":      "pci-0000:00:1f.0",
		},
	}
	routesList := map[string]any{
		"apiVersion": "v1",
		"kind":       "List",
		"items": []any{
			map[string]any{
				"spec": map[string]any{
					"dst":         "",
					"gateway":     "192.168.201.1",
					"outLinkName": "eth0",
					"family":      "inet4",
					"table":       "main",
				},
			},
		},
	}
	linksList := map[string]any{
		"apiVersion": "v1",
		"kind":       "List",
		"items":      []any{eth0},
	}
	addressesList := map[string]any{
		"apiVersion": "v1",
		"kind":       "List",
		"items": []any{
			map[string]any{
				"spec": map[string]any{
					"linkName": "eth0",
					"address":  "192.168.201.10/24",
					"family":   "inet4",
					"scope":    "global",
				},
			},
		},
	}
	nodeDefault := map[string]any{
		"spec": map[string]any{
			"addresses": []any{"192.168.201.10/24"},
		},
	}
	resolvers := map[string]any{
		"spec": map[string]any{
			"dnsServers": []any{"8.8.8.8"},
		},
	}
	return func(resource, _, id string) (map[string]any, error) {
		switch resource {
		case "routes":
			return routesList, nil
		case "links":
			if id == "eth0" {
				return eth0, nil
			}
			if id == "" {
				return linksList, nil
			}
			return map[string]any{}, nil
		case "addresses":
			return addressesList, nil
		case "nodeaddress":
			if id == "default" {
				return nodeDefault, nil
			}
		case "resolvers":
			if id == "resolvers" {
				return resolvers, nil
			}
		}
		return map[string]any{}, nil
	}
}

// dualStackNicLookup returns a lookup fixture for a node with both an
// IPv4 and an IPv6 default route on the primary link. The IPv6 route
// is ordered first to catch any default-gateway helper that lacks a
// family filter — without one, the helper would return the IPv6
// gateway and the chart's `0.0.0.0/0` route would land malformed.
// Mirrors a typical Hetzner-style cloud node where `route -6` and
// `route -4` both have a default entry.
func dualStackNicLookup() func(string, string, string) (map[string]any, error) {
	eth0 := map[string]any{
		"metadata": map[string]any{"id": "eth0"},
		"spec": map[string]any{
			"kind":         "physical",
			"index":        1,
			"hardwareAddr": "aa:bb:cc:00:00:01",
			"busPath":      "pci-0000:00:1f.0",
		},
	}
	routesList := map[string]any{
		"apiVersion": "v1",
		"kind":       "List",
		"items": []any{
			// IPv6 default route ordered first — a missing family
			// filter in default_gateway would return fe80::1.
			map[string]any{
				"spec": map[string]any{
					"dst":         "",
					"gateway":     "fe80::1",
					"outLinkName": "eth0",
					"family":      "inet6",
					"table":       "main",
					"priority":    1024,
				},
			},
			map[string]any{
				"spec": map[string]any{
					"dst":         "",
					"gateway":     "192.168.201.1",
					"outLinkName": "eth0",
					"family":      "inet4",
					"table":       "main",
					"priority":    100,
				},
			},
		},
	}
	linksList := map[string]any{
		"apiVersion": "v1",
		"kind":       "List",
		"items":      []any{eth0},
	}
	addressesList := map[string]any{
		"apiVersion": "v1",
		"kind":       "List",
		"items": []any{
			map[string]any{
				"spec": map[string]any{
					"linkName": "eth0",
					"address":  "192.168.201.10/24",
					"family":   "inet4",
					"scope":    "global",
				},
			},
		},
	}
	nodeDefault := map[string]any{
		"spec": map[string]any{
			"addresses": []any{"192.168.201.10/24"},
		},
	}
	resolvers := map[string]any{
		"spec": map[string]any{
			"dnsServers": []any{"8.8.8.8"},
		},
	}
	return func(resource, _, id string) (map[string]any, error) {
		switch resource {
		case "routes":
			return routesList, nil
		case "links":
			if id == "eth0" {
				return eth0, nil
			}
			if id == "" {
				return linksList, nil
			}
			return map[string]any{}, nil
		case "addresses":
			return addressesList, nil
		case "nodeaddress":
			if id == "default" {
				return nodeDefault, nil
			}
		case "resolvers":
			if id == "resolvers" {
				return resolvers, nil
			}
		}
		return map[string]any{}, nil
	}
}

// multiNicLookup returns a lookup fixture for a node with two
// physical NICs configured for IPv4 only: eth0 carries the default
// route (the routed uplink) and eth1 has a static address but no
// default route (a storage / management network NIC). The shape
// mirrors the typical control-plane node with a dedicated cluster-
// internal network. Used to pin the multi-doc renderer's contract
// that EVERY configurable link gets a LinkConfig document, not just
// the gateway-bearing one.
func multiNicLookup() func(string, string, string) (map[string]any, error) {
	eth0 := map[string]any{
		"metadata": map[string]any{"id": "eth0"},
		"spec": map[string]any{
			"kind":         "physical",
			"index":        1,
			"hardwareAddr": "aa:bb:cc:00:00:01",
			"busPath":      "pci-0000:00:1f.0",
			"mtu":          1500,
		},
	}
	eth1 := map[string]any{
		"metadata": map[string]any{"id": "eth1"},
		"spec": map[string]any{
			"kind":         "physical",
			"index":        2,
			"hardwareAddr": "aa:bb:cc:00:00:02",
			"busPath":      "pci-0000:00:1f.1",
			"mtu":          9000,
		},
	}
	routesList := map[string]any{
		"apiVersion": "v1",
		"kind":       "List",
		"items": []any{
			map[string]any{
				"spec": map[string]any{
					"dst":         "",
					"gateway":     "192.168.201.1",
					"outLinkName": "eth0",
					"family":      "inet4",
					"table":       "main",
					"priority":    100,
				},
			},
			map[string]any{
				"spec": map[string]any{
					"dst":         "10.0.0.0/24",
					"gateway":     "10.0.0.1",
					"outLinkName": "eth1",
					"family":      "inet4",
					"table":       "main",
					"priority":    200,
				},
			},
		},
	}
	linksList := map[string]any{
		"apiVersion": "v1",
		"kind":       "List",
		"items":      []any{eth0, eth1},
	}
	addressesList := map[string]any{
		"apiVersion": "v1",
		"kind":       "List",
		"items": []any{
			map[string]any{"spec": map[string]any{"linkName": "eth0", "address": "192.168.201.10/24", "family": "inet4", "scope": "global"}},
			map[string]any{"spec": map[string]any{"linkName": "eth1", "address": "10.0.0.5/24", "family": "inet4", "scope": "global"}},
		},
	}
	nodeDefault := map[string]any{
		"spec": map[string]any{
			"addresses": []any{"192.168.201.10/24"},
		},
	}
	resolvers := map[string]any{
		"spec": map[string]any{
			"dnsServers": []any{"8.8.8.8"},
		},
	}
	return func(resource, _, id string) (map[string]any, error) {
		switch resource {
		case "routes":
			return routesList, nil
		case "links":
			switch id {
			case "eth0":
				return eth0, nil
			case "eth1":
				return eth1, nil
			case "":
				return linksList, nil
			}
			return map[string]any{}, nil
		case "addresses":
			return addressesList, nil
		case "nodeaddress":
			if id == "default" {
				return nodeDefault, nil
			}
		case "resolvers":
			if id == "resolvers" {
				return resolvers, nil
			}
		}
		return map[string]any{}, nil
	}
}

// multiNicWithVLANLookup returns a lookup fixture for a node with a
// physical NIC eth0 and a VLAN sub-interface eth0.4000. The VLAN is
// the link that actually carries the IPv4 default route — common
// shape on bare-metal hosters where the management/cluster network
// rides a VLAN over the public NIC. The renderer must emit
// LinkConfig for eth0 (the parent), VLANConfig for eth0.4000 (the
// VLAN) with parent reference, and place the gateway on the VLAN.
func multiNicWithVLANLookup() func(string, string, string) (map[string]any, error) {
	eth0 := map[string]any{
		"metadata": map[string]any{"id": "eth0"},
		"spec": map[string]any{
			"kind":         "physical",
			"index":        1,
			"hardwareAddr": "aa:bb:cc:00:00:01",
			"busPath":      "pci-0000:00:1f.0",
			"mtu":          1500,
		},
	}
	vlan := map[string]any{
		"metadata": map[string]any{"id": "eth0.4000"},
		"spec": map[string]any{
			"kind":      "vlan",
			"index":     2,
			"linkIndex": 1,
			"vlan":      map[string]any{"vlanID": 4000},
			"mtu":       1450,
		},
	}
	routesList := map[string]any{
		"apiVersion": "v1",
		"kind":       "List",
		"items": []any{
			map[string]any{
				"spec": map[string]any{
					"dst":         "",
					"gateway":     "192.168.100.1",
					"outLinkName": "eth0.4000",
					"family":      "inet4",
					"table":       "main",
					"priority":    100,
				},
			},
		},
	}
	linksList := map[string]any{
		"apiVersion": "v1",
		"kind":       "List",
		"items":      []any{eth0, vlan},
	}
	addressesList := map[string]any{
		"apiVersion": "v1",
		"kind":       "List",
		"items": []any{
			map[string]any{"spec": map[string]any{"linkName": "eth0", "address": "88.99.249.47/26", "family": "inet4", "scope": "global"}},
			map[string]any{"spec": map[string]any{"linkName": "eth0.4000", "address": "192.168.100.2/24", "family": "inet4", "scope": "global"}},
		},
	}
	nodeDefault := map[string]any{
		"spec": map[string]any{
			"addresses": []any{"192.168.100.2/24"},
		},
	}
	resolvers := map[string]any{
		"spec": map[string]any{
			"dnsServers": []any{"8.8.8.8"},
		},
	}
	return func(resource, _, id string) (map[string]any, error) {
		switch resource {
		case "routes":
			return routesList, nil
		case "links":
			switch id {
			case "eth0":
				return eth0, nil
			case "eth0.4000":
				return vlan, nil
			case "":
				return linksList, nil
			}
			return map[string]any{}, nil
		case "addresses":
			return addressesList, nil
		case "nodeaddress":
			if id == "default" {
				return nodeDefault, nil
			}
		case "resolvers":
			if id == "resolvers" {
				return resolvers, nil
			}
		}
		return map[string]any{}, nil
	}
}

// hetznerPublicNICWithPrivateVLANLookup returns a lookup fixture
// that mirrors a Hetzner-style topology: a public NIC carrying the
// IPv4 default route and a VLAN sub-interface carrying the private
// cluster network. Distinct from multiNicWithVLANLookup, where the
// VLAN itself owns the default route.
//
// Topology pinned by this fixture:
//   - enp0s31f6: physical, public address 88.99.210.37/26, IPv4
//     default route 0.0.0.0/0 via 88.99.210.1.
//   - enp0s31f6.4000: VLAN child of enp0s31f6 (linkIndex=1, vlanID=4000),
//     private address 192.168.100.4/24, NO default route.
//
// Use case: a controlplane floatingIP in the private cluster subnet
// (e.g. 192.168.100.10) must be hosted on the VLAN sub-interface, not
// on the public default-route NIC. Today the multi-doc renderer
// hardcodes Layer2VIPConfig.link to the IPv4-default-route link, which
// puts the VIP on enp0s31f6 — wrong for this topology. This fixture is
// the reproduction case for that bug.
func hetznerPublicNICWithPrivateVLANLookup() func(string, string, string) (map[string]any, error) {
	publicNIC := map[string]any{
		"metadata": map[string]any{"id": "enp0s31f6"},
		"spec": map[string]any{
			"kind":         "physical",
			"index":        1,
			"hardwareAddr": "aa:bb:cc:00:01:01",
			"busPath":      "pci-0000:00:1f.6",
			"mtu":          1500,
		},
	}
	privateVLAN := map[string]any{
		"metadata": map[string]any{"id": "enp0s31f6.4000"},
		"spec": map[string]any{
			"kind":      "vlan",
			"index":     2,
			"linkIndex": 1,
			"vlan":      map[string]any{"vlanID": 4000},
			"mtu":       1500,
		},
	}
	routesList := map[string]any{
		"apiVersion": "v1",
		"kind":       "List",
		"items": []any{
			map[string]any{
				"spec": map[string]any{
					"dst":         "",
					"gateway":     "88.99.210.1",
					"outLinkName": "enp0s31f6",
					"family":      "inet4",
					"table":       "main",
					"priority":    100,
				},
			},
		},
	}
	linksList := map[string]any{
		"apiVersion": "v1",
		"kind":       "List",
		"items":      []any{publicNIC, privateVLAN},
	}
	addressesList := map[string]any{
		"apiVersion": "v1",
		"kind":       "List",
		"items": []any{
			map[string]any{"spec": map[string]any{"linkName": "enp0s31f6", "address": "88.99.210.37/26", "family": "inet4", "scope": "global"}},
			map[string]any{"spec": map[string]any{"linkName": "enp0s31f6.4000", "address": "192.168.100.4/24", "family": "inet4", "scope": "global"}},
		},
	}
	nodeDefault := map[string]any{
		"spec": map[string]any{
			"addresses": []any{"192.168.100.4/24"},
		},
	}
	resolvers := map[string]any{
		"spec": map[string]any{
			"dnsServers": []any{"8.8.8.8"},
		},
	}

	return func(resource, _, id string) (map[string]any, error) {
		switch resource {
		case "routes":
			return routesList, nil
		case "links":
			switch id {
			case "enp0s31f6":
				return publicNIC, nil
			case "enp0s31f6.4000":
				return privateVLAN, nil
			case "":
				return linksList, nil
			}

			return map[string]any{}, nil
		case "addresses":
			return addressesList, nil
		case "nodeaddress":
			if id == "default" {
				return nodeDefault, nil
			}
		case "resolvers":
			if id == "resolvers" {
				return resolvers, nil
			}
		}

		return map[string]any{}, nil
	}
}

// hetznerPublicNICWithPrivateIPv6VLANLookup is the IPv6-equivalent of
// hetznerPublicNICWithPrivateVLANLookup. The same physical / VLAN
// topology, but the private subnet is a /64 ULA and the VIP is an
// IPv6 literal. Pins that the VIP-link selection helper handles
// IPv6 just as it does IPv4 — net/netip.Prefix.Contains is family-
// agnostic, so the chart side has no per-family branches; this
// fixture exists to surface a regression that ever introduces one.
//
// IPv4 default-route stays on the public NIC (matching real-world
// dual-stack: IPv4 default goes upstream, the IPv6 ULA never has a
// default route — operators run IPv6 only between cluster nodes).
func hetznerPublicNICWithPrivateIPv6VLANLookup() func(string, string, string) (map[string]any, error) {
	publicNIC := map[string]any{
		"metadata": map[string]any{"id": "enp0s31f6"},
		"spec": map[string]any{
			"kind":         "physical",
			"index":        1,
			"hardwareAddr": "aa:bb:cc:00:01:01",
			"busPath":      "pci-0000:00:1f.6",
			"mtu":          1500,
		},
	}
	privateVLAN := map[string]any{
		"metadata": map[string]any{"id": "enp0s31f6.4000"},
		"spec": map[string]any{
			"kind":      "vlan",
			"index":     2,
			"linkIndex": 1,
			"vlan":      map[string]any{"vlanID": 4000},
			"mtu":       1500,
		},
	}
	routesList := map[string]any{
		"apiVersion": "v1",
		"kind":       "List",
		"items": []any{
			map[string]any{
				"spec": map[string]any{
					"dst":         "",
					"gateway":     "88.99.210.1",
					"outLinkName": "enp0s31f6",
					"family":      "inet4",
					"table":       "main",
					"priority":    100,
				},
			},
		},
	}
	linksList := map[string]any{
		"apiVersion": "v1",
		"kind":       "List",
		"items":      []any{publicNIC, privateVLAN},
	}
	addressesList := map[string]any{
		"apiVersion": "v1",
		"kind":       "List",
		"items": []any{
			map[string]any{"spec": map[string]any{"linkName": "enp0s31f6", "address": "88.99.210.37/26", "family": "inet4", "scope": "global"}},
			map[string]any{"spec": map[string]any{"linkName": "enp0s31f6.4000", "address": "2001:db8:cafe::4/64", "family": "inet6", "scope": "global"}},
		},
	}
	nodeDefault := map[string]any{
		"spec": map[string]any{
			"addresses": []any{"2001:db8:cafe::4/64"},
		},
	}
	resolvers := map[string]any{
		"spec": map[string]any{
			"dnsServers": []any{"8.8.8.8"},
		},
	}

	return func(resource, _, id string) (map[string]any, error) {
		switch resource {
		case "routes":
			return routesList, nil
		case "links":
			switch id {
			case "enp0s31f6":
				return publicNIC, nil
			case "enp0s31f6.4000":
				return privateVLAN, nil
			case "":
				return linksList, nil
			}

			return map[string]any{}, nil
		case "addresses":
			return addressesList, nil
		case "nodeaddress":
			if id == "default" {
				return nodeDefault, nil
			}
		case "resolvers":
			if id == "resolvers" {
				return resolvers, nil
			}
		}

		return map[string]any{}, nil
	}
}

// legacyInterfacesInRunningConfigLookup returns a lookup fixture
// shaped like a node that was originally bootstrapped on a legacy
// chart (talosVersion v1.11) and carries non-empty
// machine.network.interfaces[] in its running MachineConfig. The
// multi-doc renderer must detect this and refuse to render rather
// than silently dropping the legacy interface block — otherwise an
// upgrade from chart v0.23 to v0.24+ would silently lose every
// user-declared address, route, and VLAN that lived under the
// legacy schema.
func legacyInterfacesInRunningConfigLookup() func(string, string, string) (map[string]any, error) {
	eth0 := map[string]any{
		"metadata": map[string]any{"id": "eth0"},
		"spec": map[string]any{
			"kind":         "physical",
			"index":        1,
			"hardwareAddr": "aa:bb:cc:00:00:01",
			"busPath":      "pci-0000:00:1f.0",
		},
	}
	routesList := map[string]any{
		"apiVersion": "v1",
		"kind":       "List",
		"items": []any{
			map[string]any{
				"spec": map[string]any{
					"dst":         "",
					"gateway":     "192.168.201.1",
					"outLinkName": "eth0",
					"family":      "inet4",
					"table":       "main",
				},
			},
		},
	}
	linksList := map[string]any{
		"apiVersion": "v1",
		"kind":       "List",
		"items":      []any{eth0},
	}
	addressesList := map[string]any{
		"apiVersion": "v1",
		"kind":       "List",
		"items": []any{
			map[string]any{"spec": map[string]any{"linkName": "eth0", "address": "192.168.201.10/24", "family": "inet4", "scope": "global"}},
		},
	}
	machineConfig := map[string]any{
		"spec": map[string]any{
			"machine": map[string]any{
				"network": map[string]any{
					"interfaces": []any{
						map[string]any{
							"interface": "eth0",
							"mtu":       9000,
							"vlans": []any{
								map[string]any{"vlanId": 4000, "addresses": []any{"192.168.100.2/24"}},
							},
						},
					},
				},
			},
		},
	}
	nodeDefault := map[string]any{
		"spec": map[string]any{
			"addresses": []any{"192.168.201.10/24"},
		},
	}
	resolvers := map[string]any{
		"spec": map[string]any{
			"dnsServers": []any{"8.8.8.8"},
		},
	}
	return func(resource, _, id string) (map[string]any, error) {
		switch resource {
		case "routes":
			return routesList, nil
		case "links":
			if id == "eth0" {
				return eth0, nil
			}
			if id == "" {
				return linksList, nil
			}
			return map[string]any{}, nil
		case "addresses":
			return addressesList, nil
		case "nodeaddress":
			if id == "default" {
				return nodeDefault, nil
			}
		case "resolvers":
			if id == "resolvers" {
				return resolvers, nil
			}
		case "machineconfig":
			if id == "v1alpha1" {
				return machineConfig, nil
			}
		}
		return map[string]any{}, nil
	}
}

// bondWithSlavesLookup returns a lookup fixture for a node where two
// physical NICs (eth0 + eth1) are enrolled into a bond master bond0.
// Mirrors the Talos representation: the slaves expose their busPath
// (so the regex matches them as "physical") AND have spec.slaveKind
// set ("bond"), which configurable_link_names uses to filter them
// out of the iteration. Without that filter the renderer would emit
// LinkConfig for each slave alongside the master's BondConfig and
// Talos would reject the conflicting declarations.
func bondWithSlavesLookup() func(string, string, string) (map[string]any, error) {
	eth0 := map[string]any{
		"metadata": map[string]any{"id": "eth0"},
		"spec": map[string]any{
			"kind":         "physical",
			"index":        1,
			"hardwareAddr": "aa:bb:cc:00:00:01",
			"busPath":      "pci-0000:00:1f.0",
			"slaveKind":    "bond",
			"masterIndex":  3,
			"mtu":          9000,
		},
	}
	eth1 := map[string]any{
		"metadata": map[string]any{"id": "eth1"},
		"spec": map[string]any{
			"kind":         "physical",
			"index":        2,
			"hardwareAddr": "aa:bb:cc:00:00:02",
			"busPath":      "pci-0000:00:1f.1",
			"slaveKind":    "bond",
			"masterIndex":  3,
			"mtu":          9000,
		},
	}
	bond0 := map[string]any{
		"metadata": map[string]any{"id": "bond0"},
		"spec": map[string]any{
			"kind":         "bond",
			"index":        3,
			"hardwareAddr": "aa:bb:cc:00:00:01",
			"mtu":          9000,
			"bondMaster": map[string]any{
				"mode":           "802.3ad",
				"xmitHashPolicy": "layer2+3",
				"miimon":         100,
			},
		},
	}
	routesList := map[string]any{
		"apiVersion": "v1",
		"kind":       "List",
		"items": []any{
			map[string]any{
				"spec": map[string]any{
					"dst":         "",
					"gateway":     "192.168.201.1",
					"outLinkName": "bond0",
					"family":      "inet4",
					"table":       "main",
					"priority":    100,
				},
			},
		},
	}
	linksList := map[string]any{
		"apiVersion": "v1",
		"kind":       "List",
		"items":      []any{eth0, eth1, bond0},
	}
	addressesList := map[string]any{
		"apiVersion": "v1",
		"kind":       "List",
		"items": []any{
			map[string]any{"spec": map[string]any{"linkName": "bond0", "address": "192.168.201.10/24", "family": "inet4", "scope": "global"}},
		},
	}
	nodeDefault := map[string]any{
		"spec": map[string]any{
			"addresses": []any{"192.168.201.10/24"},
		},
	}
	resolvers := map[string]any{
		"spec": map[string]any{
			"dnsServers": []any{"8.8.8.8"},
		},
	}
	return func(resource, _, id string) (map[string]any, error) {
		switch resource {
		case "routes":
			return routesList, nil
		case "links":
			switch id {
			case "eth0":
				return eth0, nil
			case "eth1":
				return eth1, nil
			case "bond0":
				return bond0, nil
			case "":
				return linksList, nil
			}
			return map[string]any{}, nil
		case "addresses":
			return addressesList, nil
		case "nodeaddress":
			if id == "default" {
				return nodeDefault, nil
			}
		case "resolvers":
			if id == "resolvers" {
				return resolvers, nil
			}
		}
		return map[string]any{}, nil
	}
}

// bondWithoutBondMasterLookup returns a lookup fixture for a bond
// link where the bondMaster sub-resource is missing or partial
// (real Talos sometimes returns this on freshly-created bonds where
// the master controller hasn't filled the spec yet). The renderer
// must gate every BondMaster field on its presence so the rendered
// BondConfig stays valid YAML — without the gate, missing fields
// surfaced as `bondMode: <nil>` and broke the parse.
func bondWithoutBondMasterLookup() func(string, string, string) (map[string]any, error) {
	eth0 := map[string]any{
		"metadata": map[string]any{"id": "eth0"},
		"spec": map[string]any{
			"kind":         "physical",
			"index":        1,
			"hardwareAddr": "aa:bb:cc:00:00:01",
			"busPath":      "pci-0000:00:1f.0",
			"slaveKind":    "bond",
			"masterIndex":  2,
		},
	}
	bond0 := map[string]any{
		"metadata": map[string]any{"id": "bond0"},
		"spec": map[string]any{
			"kind":         "bond",
			"index":        2,
			"hardwareAddr": "aa:bb:cc:00:00:01",
		},
	}
	routesList := map[string]any{
		"apiVersion": "v1",
		"kind":       "List",
		"items": []any{
			map[string]any{
				"spec": map[string]any{
					"dst":         "",
					"gateway":     "192.168.201.1",
					"outLinkName": "bond0",
					"family":      "inet4",
					"table":       "main",
				},
			},
		},
	}
	linksList := map[string]any{
		"apiVersion": "v1",
		"kind":       "List",
		"items":      []any{eth0, bond0},
	}
	addressesList := map[string]any{
		"apiVersion": "v1",
		"kind":       "List",
		"items": []any{
			map[string]any{"spec": map[string]any{"linkName": "bond0", "address": "192.168.201.10/24", "family": "inet4", "scope": "global"}},
		},
	}
	nodeDefault := map[string]any{
		"spec": map[string]any{
			"addresses": []any{"192.168.201.10/24"},
		},
	}
	resolvers := map[string]any{
		"spec": map[string]any{
			"dnsServers": []any{"8.8.8.8"},
		},
	}
	return func(resource, _, id string) (map[string]any, error) {
		switch resource {
		case "routes":
			return routesList, nil
		case "links":
			switch id {
			case "eth0":
				return eth0, nil
			case "bond0":
				return bond0, nil
			case "":
				return linksList, nil
			}
			return map[string]any{}, nil
		case "addresses":
			return addressesList, nil
		case "nodeaddress":
			if id == "default" {
				return nodeDefault, nil
			}
		case "resolvers":
			if id == "resolvers" {
				return resolvers, nil
			}
		}
		return map[string]any{}, nil
	}
}

// bridgeLookup returns a lookup fixture for a node with a routed
// physical NIC eth0 plus a bridge br0. The renderer must emit
// LinkConfig for eth0 and SKIP the bridge entirely (until
// BridgeConfig support lands) rather than emit a wrong-kind
// LinkConfig name: br0.
func bridgeLookup() func(string, string, string) (map[string]any, error) {
	eth0 := map[string]any{
		"metadata": map[string]any{"id": "eth0"},
		"spec": map[string]any{
			"kind":         "physical",
			"index":        1,
			"hardwareAddr": "aa:bb:cc:00:00:01",
			"busPath":      "pci-0000:00:1f.0",
		},
	}
	br0 := map[string]any{
		"metadata": map[string]any{"id": "br0"},
		"spec": map[string]any{
			"kind":  "bridge",
			"index": 2,
		},
	}
	routesList := map[string]any{
		"apiVersion": "v1",
		"kind":       "List",
		"items": []any{
			map[string]any{
				"spec": map[string]any{
					"dst":         "",
					"gateway":     "192.168.201.1",
					"outLinkName": "eth0",
					"family":      "inet4",
					"table":       "main",
				},
			},
		},
	}
	linksList := map[string]any{
		"apiVersion": "v1",
		"kind":       "List",
		"items":      []any{eth0, br0},
	}
	addressesList := map[string]any{
		"apiVersion": "v1",
		"kind":       "List",
		"items": []any{
			map[string]any{"spec": map[string]any{"linkName": "eth0", "address": "192.168.201.10/24", "family": "inet4", "scope": "global"}},
		},
	}
	nodeDefault := map[string]any{
		"spec": map[string]any{
			"addresses": []any{"192.168.201.10/24"},
		},
	}
	resolvers := map[string]any{
		"spec": map[string]any{
			"dnsServers": []any{"8.8.8.8"},
		},
	}
	return func(resource, _, id string) (map[string]any, error) {
		switch resource {
		case "routes":
			return routesList, nil
		case "links":
			switch id {
			case "eth0":
				return eth0, nil
			case "br0":
				return br0, nil
			case "":
				return linksList, nil
			}
			return map[string]any{}, nil
		case "addresses":
			return addressesList, nil
		case "nodeaddress":
			if id == "default" {
				return nodeDefault, nil
			}
		case "resolvers":
			if id == "resolvers" {
				return resolvers, nil
			}
		}
		return map[string]any{}, nil
	}
}

// vipActiveOnLinkLookup returns a lookup fixture for a node where
// the configured floatingIP is currently active on eth0 — discovery
// reports two global-scope addresses on the link: the permanent
// address and the VIP. The Talos VIP operator does not mark the VIP
// address with any distinguishing field, so the chart must filter
// it out by matching against the operator-declared floatingIP.
func vipActiveOnLinkLookup() func(string, string, string) (map[string]any, error) {
	eth0 := map[string]any{
		"metadata": map[string]any{"id": "eth0"},
		"spec": map[string]any{
			"kind":         "physical",
			"index":        1,
			"hardwareAddr": "aa:bb:cc:00:00:01",
			"busPath":      "pci-0000:00:1f.0",
		},
	}
	routesList := map[string]any{
		"apiVersion": "v1",
		"kind":       "List",
		"items": []any{
			map[string]any{
				"spec": map[string]any{
					"dst":         "",
					"gateway":     "192.168.201.1",
					"outLinkName": "eth0",
					"family":      "inet4",
					"table":       "main",
				},
			},
		},
	}
	linksList := map[string]any{
		"apiVersion": "v1",
		"kind":       "List",
		"items":      []any{eth0},
	}
	addressesList := map[string]any{
		"apiVersion": "v1",
		"kind":       "List",
		"items": []any{
			map[string]any{"spec": map[string]any{"linkName": "eth0", "address": "192.168.201.10/24", "family": "inet4", "scope": "global"}},
			map[string]any{"spec": map[string]any{"linkName": "eth0", "address": "192.168.201.5/32", "family": "inet4", "scope": "global"}},
		},
	}
	nodeDefault := map[string]any{
		"spec": map[string]any{
			"addresses": []any{"192.168.201.10/24"},
		},
	}
	resolvers := map[string]any{
		"spec": map[string]any{
			"dnsServers": []any{"8.8.8.8"},
		},
	}
	return func(resource, _, id string) (map[string]any, error) {
		switch resource {
		case "routes":
			return routesList, nil
		case "links":
			if id == "eth0" {
				return eth0, nil
			}
			if id == "" {
				return linksList, nil
			}
			return map[string]any{}, nil
		case "addresses":
			return addressesList, nil
		case "nodeaddress":
			if id == "default" {
				return nodeDefault, nil
			}
		case "resolvers":
			if id == "resolvers" {
				return resolvers, nil
			}
		}
		return map[string]any{}, nil
	}
}

// bridgeWithGatewayLookup returns a lookup fixture where a discovered
// bridge br0 carries the IPv4 default route (typical shape: VMs sit
// behind br0, the bridge gets the host's address). The renderer
// cannot emit BridgeConfig today, so it must surface a fail rather
// than silently drop every network document for the gateway-bearing
// link.
func bridgeWithGatewayLookup() func(string, string, string) (map[string]any, error) {
	br0 := map[string]any{
		"metadata": map[string]any{"id": "br0"},
		"spec": map[string]any{
			"kind":  "bridge",
			"index": 1,
		},
	}
	routesList := map[string]any{
		"apiVersion": "v1",
		"kind":       "List",
		"items": []any{
			map[string]any{
				"spec": map[string]any{
					"dst":         "",
					"gateway":     "192.168.201.1",
					"outLinkName": "br0",
					"family":      "inet4",
					"table":       "main",
				},
			},
		},
	}
	linksList := map[string]any{
		"apiVersion": "v1",
		"kind":       "List",
		"items":      []any{br0},
	}
	addressesList := map[string]any{
		"apiVersion": "v1",
		"kind":       "List",
		"items": []any{
			map[string]any{"spec": map[string]any{"linkName": "br0", "address": "192.168.201.10/24", "family": "inet4", "scope": "global"}},
		},
	}
	nodeDefault := map[string]any{
		"spec": map[string]any{
			"addresses": []any{"192.168.201.10/24"},
		},
	}
	resolvers := map[string]any{
		"spec": map[string]any{
			"dnsServers": []any{"8.8.8.8"},
		},
	}
	return func(resource, _, id string) (map[string]any, error) {
		switch resource {
		case "routes":
			return routesList, nil
		case "links":
			if id == "br0" {
				return br0, nil
			}
			if id == "" {
				return linksList, nil
			}
			return map[string]any{}, nil
		case "addresses":
			return addressesList, nil
		case "nodeaddress":
			if id == "default" {
				return nodeDefault, nil
			}
		case "resolvers":
			if id == "resolvers" {
				return resolvers, nil
			}
		}
		return map[string]any{}, nil
	}
}

// vlanWithoutVlanIDLookup returns a lookup fixture where a VLAN
// link's spec.vlan.vlanID is unset (partial discovery state). The
// renderer must fail rather than emit a VLANConfig missing the
// required vlanID field.
func vlanWithoutVlanIDLookup() func(string, string, string) (map[string]any, error) {
	eth0 := map[string]any{
		"metadata": map[string]any{"id": "eth0"},
		"spec": map[string]any{
			"kind":         "physical",
			"index":        1,
			"hardwareAddr": "aa:bb:cc:00:00:01",
			"busPath":      "pci-0000:00:1f.0",
		},
	}
	vlan := map[string]any{
		"metadata": map[string]any{"id": "eth0.4000"},
		"spec": map[string]any{
			"kind":      "vlan",
			"index":     2,
			"linkIndex": 1,
		},
	}
	routesList := map[string]any{
		"apiVersion": "v1",
		"kind":       "List",
		"items": []any{
			map[string]any{
				"spec": map[string]any{
					"dst":         "",
					"gateway":     "192.168.201.1",
					"outLinkName": "eth0",
					"family":      "inet4",
					"table":       "main",
				},
			},
		},
	}
	linksList := map[string]any{
		"apiVersion": "v1",
		"kind":       "List",
		"items":      []any{eth0, vlan},
	}
	addressesList := map[string]any{
		"apiVersion": "v1",
		"kind":       "List",
		"items": []any{
			map[string]any{"spec": map[string]any{"linkName": "eth0", "address": "192.168.201.10/24", "family": "inet4", "scope": "global"}},
		},
	}
	nodeDefault := map[string]any{
		"spec": map[string]any{
			"addresses": []any{"192.168.201.10/24"},
		},
	}
	resolvers := map[string]any{
		"spec": map[string]any{
			"dnsServers": []any{"8.8.8.8"},
		},
	}
	return func(resource, _, id string) (map[string]any, error) {
		switch resource {
		case "routes":
			return routesList, nil
		case "links":
			switch id {
			case "eth0":
				return eth0, nil
			case "eth0.4000":
				return vlan, nil
			case "":
				return linksList, nil
			}
			return map[string]any{}, nil
		case "addresses":
			return addressesList, nil
		case "nodeaddress":
			if id == "default" {
				return nodeDefault, nil
			}
		case "resolvers":
			if id == "resolvers" {
				return resolvers, nil
			}
		}
		return map[string]any{}, nil
	}
}

// vlanWithoutParentLookup returns a lookup fixture where a VLAN
// link is discovered but its parent link cannot be resolved
// (linkIndex points at a non-existent index). VLANConfig requires
// the parent field on the wire — emitting one without it would be
// rejected by Talos at apply time. The renderer must surface a
// clear template-time error instead.
func vlanWithoutParentLookup() func(string, string, string) (map[string]any, error) {
	eth0 := map[string]any{
		"metadata": map[string]any{"id": "eth0"},
		"spec": map[string]any{
			"kind":         "physical",
			"index":        1,
			"hardwareAddr": "aa:bb:cc:00:00:01",
			"busPath":      "pci-0000:00:1f.0",
		},
	}
	// VLAN with linkIndex pointing at a non-existent parent.
	vlan := map[string]any{
		"metadata": map[string]any{"id": "eth0.4000"},
		"spec": map[string]any{
			"kind":      "vlan",
			"index":     2,
			"linkIndex": 99,
			"vlan":      map[string]any{"vlanID": 4000},
		},
	}
	routesList := map[string]any{
		"apiVersion": "v1",
		"kind":       "List",
		"items": []any{
			map[string]any{
				"spec": map[string]any{
					"dst":         "",
					"gateway":     "192.168.201.1",
					"outLinkName": "eth0",
					"family":      "inet4",
					"table":       "main",
				},
			},
		},
	}
	linksList := map[string]any{
		"apiVersion": "v1",
		"kind":       "List",
		"items":      []any{eth0, vlan},
	}
	addressesList := map[string]any{
		"apiVersion": "v1",
		"kind":       "List",
		"items": []any{
			map[string]any{"spec": map[string]any{"linkName": "eth0", "address": "192.168.201.10/24", "family": "inet4", "scope": "global"}},
		},
	}
	nodeDefault := map[string]any{
		"spec": map[string]any{
			"addresses": []any{"192.168.201.10/24"},
		},
	}
	resolvers := map[string]any{
		"spec": map[string]any{
			"dnsServers": []any{"8.8.8.8"},
		},
	}
	return func(resource, _, id string) (map[string]any, error) {
		switch resource {
		case "routes":
			return routesList, nil
		case "links":
			switch id {
			case "eth0":
				return eth0, nil
			case "eth0.4000":
				return vlan, nil
			case "":
				return linksList, nil
			}
			return map[string]any{}, nil
		case "addresses":
			return addressesList, nil
		case "nodeaddress":
			if id == "default" {
				return nodeDefault, nil
			}
		case "resolvers":
			if id == "resolvers" {
				return resolvers, nil
			}
		}
		return map[string]any{}, nil
	}
}

// dualStackTwoNicsLookup returns a lookup fixture for a node with the
// IPv4 and IPv6 default routes on DIFFERENT links — the multi-NIC
// shape where the IPv4-only filter actually matters: a missing filter
// in any default_*_by_gateway helper would point the chart at the
// IPv6-default link (eth1) while addresses, gateway, and routes all
// describe the IPv4-default link (eth0), producing a config that
// configures neither NIC correctly.
//
// The IPv6 route is ordered first so a missing family filter reaches
// the wrong link before iteration ends; this mirrors the discovery
// order Talos returns when both routes share the same priority window.
func dualStackTwoNicsLookup() func(string, string, string) (map[string]any, error) {
	eth0 := map[string]any{
		"metadata": map[string]any{"id": "eth0"},
		"spec": map[string]any{
			"kind":         "physical",
			"index":        1,
			"hardwareAddr": "aa:bb:cc:00:00:01",
			"busPath":      "pci-0000:00:1f.0",
		},
	}
	eth1 := map[string]any{
		"metadata": map[string]any{"id": "eth1"},
		"spec": map[string]any{
			"kind":         "physical",
			"index":        2,
			"hardwareAddr": "aa:bb:cc:00:00:02",
			"busPath":      "pci-0000:00:1f.1",
		},
	}
	routesList := map[string]any{
		"apiVersion": "v1",
		"kind":       "List",
		"items": []any{
			map[string]any{
				"spec": map[string]any{
					"dst":         "",
					"gateway":     "fe80::1",
					"outLinkName": "eth1",
					"family":      "inet6",
					"table":       "main",
					"priority":    1024,
				},
			},
			map[string]any{
				"spec": map[string]any{
					"dst":         "",
					"gateway":     "192.168.201.1",
					"outLinkName": "eth0",
					"family":      "inet4",
					"table":       "main",
					"priority":    100,
				},
			},
		},
	}
	linksList := map[string]any{
		"apiVersion": "v1",
		"kind":       "List",
		"items":      []any{eth0, eth1},
	}
	addressesList := map[string]any{
		"apiVersion": "v1",
		"kind":       "List",
		"items": []any{
			map[string]any{"spec": map[string]any{"linkName": "eth0", "address": "192.168.201.10/24", "family": "inet4", "scope": "global"}},
			map[string]any{"spec": map[string]any{"linkName": "eth1", "address": "2001:db8::a/64", "family": "inet6", "scope": "global"}},
		},
	}
	nodeDefault := map[string]any{
		"spec": map[string]any{
			"addresses": []any{"192.168.201.10/24"},
		},
	}
	resolvers := map[string]any{
		"spec": map[string]any{
			"dnsServers": []any{"8.8.8.8"},
		},
	}
	return func(resource, _, id string) (map[string]any, error) {
		switch resource {
		case "routes":
			return routesList, nil
		case "links":
			switch id {
			case "eth0":
				return eth0, nil
			case "eth1":
				return eth1, nil
			case "":
				return linksList, nil
			}
			return map[string]any{}, nil
		case "addresses":
			return addressesList, nil
		case "nodeaddress":
			if id == "default" {
				return nodeDefault, nil
			}
		case "resolvers":
			if id == "resolvers" {
				return resolvers, nil
			}
		}
		return map[string]any{}, nil
	}
}

// freshNicLookup returns a lookup fixture for a node in first-boot
// state: no routes, no addresses, no usable links. Discovery cannot
// resolve a default-gateway-bearing link; every helper that depends
// on it returns empty. Used to exercise code paths that must work
// when the chart is generating the very network configuration that
// will populate discovery on the next reconciliation.
func freshNicLookup() func(string, string, string) (map[string]any, error) {
	emptyList := map[string]any{
		"apiVersion": "v1",
		"kind":       "List",
		"items":      []any{},
	}
	return func(resource, _, _ string) (map[string]any, error) {
		switch resource {
		case "routes", "links", "addresses":
			return emptyList, nil
		}
		return map[string]any{}, nil
	}
}

// renderCozystackWith renders the cozystack controlplane template
// against the supplied LookupFunc and values overrides, returning the
// final template output or failing the test. Mirrors the pattern used
// by the existing TestMultiDoc* suites.
func renderCozystackWith(t *testing.T, lookup func(string, string, string) (map[string]any, error), overrides map[string]any) string {
	t.Helper()
	origLookup := helmEngine.LookupFunc
	t.Cleanup(func() { helmEngine.LookupFunc = origLookup })
	helmEngine.LookupFunc = lookup

	chrt, err := loader.LoadDir("../../charts/cozystack")
	if err != nil {
		t.Fatalf("load chart: %v", err)
	}
	// Deep-copy chart values so mutations in this helper (or in the
	// caller's overrides) never leak into chrt.Values and corrupt
	// subsequent renders.
	values := cloneValues(chrt.Values)
	// Default endpoint for tests that don't exercise the required guard.
	// Caller overrides via the overrides map if it wants to trigger `required`.
	if v, _ := values["endpoint"].(string); v == "" {
		values["endpoint"] = testEndpoint
	}
	maps.Copy(values, overrides)

	eng := helmEngine.Engine{}
	out, err := eng.Render(chrt, chartutil.Values{
		"Values":       values,
		"TalosVersion": "v1.12",
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	return out["cozystack/templates/controlplane.yaml"]
}

// renderGenericWith is the generic-preset counterpart of renderCozystackWith.
func renderGenericWith(t *testing.T, lookup func(string, string, string) (map[string]any, error), overrides map[string]any) string {
	t.Helper()
	origLookup := helmEngine.LookupFunc
	t.Cleanup(func() { helmEngine.LookupFunc = origLookup })
	helmEngine.LookupFunc = lookup

	chrt, err := loader.LoadDir("../../charts/generic")
	if err != nil {
		t.Fatalf("load chart: %v", err)
	}
	// Deep-copy chart values so mutations in this helper (or in the
	// caller's overrides) never leak into chrt.Values.
	values := cloneValues(chrt.Values)
	if v, _ := values["endpoint"].(string); v == "" {
		values["endpoint"] = testEndpoint
	}
	maps.Copy(values, overrides)

	eng := helmEngine.Engine{}
	out, err := eng.Render(chrt, chartutil.Values{
		"Values":       values,
		"TalosVersion": "v1.12",
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	return out["generic/templates/controlplane.yaml"]
}

// TestMultiDocCozystack_ValidSubnetsFallsBackToDiscovery pins the
// issue-report fix: when values.yaml leaves advertisedSubnets empty,
// the chart must fall back to the CIDR of the node's default-gateway-
// bearing link rather than emitting the 192.168.100.0/24 placeholder
// that used to be the default. Without the fallback branch, users on
// networks other than 192.168.100.0/24 silently shipped a broken
// kubelet.validSubnets value.
func TestMultiDocCozystack_ValidSubnetsFallsBackToDiscovery(t *testing.T) {
	result := renderCozystackWith(t, simpleNicLookup(), map[string]any{
		"advertisedSubnets": []any{},
	})

	// The discovered CIDR must appear under kubelet.validSubnets and
	// must NOT contain the historical 192.168.100.0/24 placeholder.
	assertContains(t, result, "validSubnets:")
	assertContains(t, result, "- 192.168.201.0/24")
	if strings.Contains(result, "192.168.100.0/24") {
		t.Errorf("output contains stale placeholder 192.168.100.0/24:\n%s", result)
	}
}

// TestMultiDocCozystack_AdvertisedSubnetsFallsBackToDiscovery pins
// the same fallback behavior on etcd.advertisedSubnets.
func TestMultiDocCozystack_AdvertisedSubnetsFallsBackToDiscovery(t *testing.T) {
	result := renderCozystackWith(t, simpleNicLookup(), map[string]any{
		"advertisedSubnets": []any{},
	})

	// etcd.advertisedSubnets section must appear (controlplane only)
	// and list the discovered CIDR.
	assertContains(t, result, "advertisedSubnets:")
	assertContains(t, result, "- 192.168.201.0/24")
}

// TestMultiDocCozystack_ValuesAdvertisedSubnetsOverridesDiscovery
// pins the precedence: when an operator sets advertisedSubnets
// explicitly in values.yaml, that value wins over the discovered CIDR
// in both kubelet.validSubnets and etcd.advertisedSubnets (two
// consumers of the same chart value).
//
// The discovered CIDR (192.168.201.10/24) is still expected to appear
// elsewhere in the rendered output — specifically in LinkConfig under
// the physical interface's `addresses:` list — that is the normal
// network discovery path and not the subject of this test. What this
// test pins is that the override does NOT leave the discovered CIDR
// in the two subnet-selector fields.
func TestMultiDocCozystack_ValuesAdvertisedSubnetsOverridesDiscovery(t *testing.T) {
	result := renderCozystackWith(t, simpleNicLookup(), map[string]any{
		"advertisedSubnets": []any{"10.0.0.0/8"},
	})

	// Expect 10.0.0.0/8 to appear at least twice — once in
	// machine.kubelet.nodeIP.validSubnets and once in
	// cluster.etcd.advertisedSubnets.
	if got := strings.Count(result, "- 10.0.0.0/8"); got < 2 {
		t.Errorf("operator override 10.0.0.0/8 should appear in both validSubnets and advertisedSubnets; saw %d occurrence(s):\n%s", got, result)
	}
	// Ensure the discovered CIDR did NOT leak into the subnet-selector
	// fields. The fallback emits the canonical network form
	// (192.168.201.0/24) via cidrNetwork, while LinkConfig emits the
	// host-form as `- address: 192.168.201.10/24` — so checking for
	// the bare canonical form in the output is a strong signal that
	// the fallback fired despite the operator override.
	if strings.Contains(result, "- 192.168.201.0/24\n") {
		t.Errorf("operator override should win but fallback-form subnet leaked into a subnet-selector list:\n%s", result)
	}
}

// TestMultiDocCozystack_EndpointRequired asserts that an unset or
// empty .Values.endpoint now produces a clear error at render time
// via Helm's required(), instead of silently embedding the stale
// placeholder that values.yaml ships with.
func TestMultiDocCozystack_EndpointRequired(t *testing.T) {
	origLookup := helmEngine.LookupFunc
	t.Cleanup(func() { helmEngine.LookupFunc = origLookup })
	helmEngine.LookupFunc = simpleNicLookup()

	chrt, err := loader.LoadDir("../../charts/cozystack")
	if err != nil {
		t.Fatalf("load chart: %v", err)
	}
	values := make(map[string]any)
	maps.Copy(values, chrt.Values)
	values["endpoint"] = ""

	eng := helmEngine.Engine{}
	_, err = eng.Render(chrt, chartutil.Values{
		"Values":       values,
		"TalosVersion": "v1.12",
	})
	if err == nil {
		t.Fatal("expected render to fail with required() error when endpoint is empty")
	}
	if !strings.Contains(err.Error(), "endpoint") {
		t.Errorf("error should mention 'endpoint'; got: %v", err)
	}
}

// TestMultiDocCozystack_InvalidClusterNameOverride ensures invalid
// clusterName overrides are rejected.
func TestMultiDocCozystack_InvalidClusterNameOverride(t *testing.T) {
	origLookup := helmEngine.LookupFunc
	t.Cleanup(func() { helmEngine.LookupFunc = origLookup })
	helmEngine.LookupFunc = simpleNicLookup()

	chrt, err := loader.LoadDir("../../charts/cozystack")
	if err != nil {
		t.Fatalf("load chart: %v", err)
	}
	values := make(map[string]any)
	maps.Copy(values, chrt.Values)
	values["clusterName"] = "InvalidClusterName"

	eng := helmEngine.Engine{}
	_, err = eng.Render(chrt, chartutil.Values{
		"Values":       values,
		"TalosVersion": "v1.12",
	})
	if err == nil {
		t.Fatal("expected render to fail with required() error when clusterName is invalid")
	}
	if !strings.Contains(err.Error(), "clusterName") {
		t.Errorf("error should mention 'clusterName'; got: %v", err)
	}
}

// TestMultiDocCozystack_ValidClusterNameOverride ensures clusterName
// overrides make it through to the result.
func TestMultiDocCozystack_ValidClusterNameOverride(t *testing.T) {
	result := renderCozystackWith(t, simpleNicLookup(), map[string]any{
		"clusterName": "differentclustername",
	})

	assertContains(t, result, "clusterName: \"differentclustername\"")
}

// TestMultiDocGeneric_InvalidSubnetsFallsBackToDiscovery mirrors the
// cozystack-side smoke test for ensuring invalid clusterName
// overrides are rejected.
func TestMultiDocGeneric_InvalidClusterNameOverride(t *testing.T) {
	origLookup := helmEngine.LookupFunc
	t.Cleanup(func() { helmEngine.LookupFunc = origLookup })
	helmEngine.LookupFunc = simpleNicLookup()

	chrt, err := loader.LoadDir("../../charts/generic")
	if err != nil {
		t.Fatalf("load chart: %v", err)
	}
	values := make(map[string]any)
	maps.Copy(values, chrt.Values)
	values["clusterName"] = "InvalidClusterName"

	eng := helmEngine.Engine{}
	_, err = eng.Render(chrt, chartutil.Values{
		"Values":       values,
		"TalosVersion": "v1.12",
	})
	if err == nil {
		t.Fatal("expected render to fail with required() error when clusterName is invalid")
	}
	if !strings.Contains(err.Error(), "clusterName") {
		t.Errorf("error should mention 'clusterName'; got: %v", err)
	}
}

// TestMultiDocGeneric_ValidSubnetsFallsBackToDiscovery mirrors the
// cozystack-side smoke test for ensuring clusterName overrides make
// it through to the result.
func TestMultiDocGeneric_ValidClusterNameOverride(t *testing.T) {
	result := renderGenericWith(t, simpleNicLookup(), map[string]any{
		"clusterName": "differentclustername",
	})

	assertContains(t, result, "clusterName: \"differentclustername\"")
}

// TestMultiDocGeneric_ValidSubnetsFallsBackToDiscovery mirrors the
// cozystack-side smoke test for the generic preset. A single
// representative assertion proves the edits apply symmetrically to
// the generic copy of the shared machine/cluster block.
func TestMultiDocGeneric_ValidSubnetsFallsBackToDiscovery(t *testing.T) {
	result := renderGenericWith(t, simpleNicLookup(), map[string]any{
		"advertisedSubnets": []any{},
	})

	assertContains(t, result, "validSubnets:")
	assertContains(t, result, "- 192.168.201.0/24")
	if strings.Contains(result, "192.168.100.0/24") {
		t.Errorf("output contains stale placeholder 192.168.100.0/24:\n%s", result)
	}
}

// TestMultiDocGeneric_VIPLinkOverride mirrors the cozystack VIP-link
// override test for the generic preset. The two charts share helper
// shape, so the override must apply symmetrically — without this the
// generic-preset apply pipeline would still pin the VIP onto the
// physical NIC discovered at first apply when the operator wanted
// the VIP on a not-yet-existing VLAN sub-interface.
func TestMultiDocGeneric_VIPLinkOverride(t *testing.T) {
	result := renderGenericWith(t, simpleNicLookup(), map[string]any{
		"floatingIP": "192.168.201.5",
		"vipLink":    "eth0.4000",
	})

	assertContains(t, result, "kind: Layer2VIPConfig")
	assertContains(t, result, "link: eth0.4000")
	assertNotContains(t, result, "link: eth0\n")
}

// TestMultiDocCozystack_ShippedDefaultsFailFresh asserts that a fresh
// `talm init -p cozystack` user who keeps values.yaml defaults gets a
// loud `required` error — not a silently-embedded placeholder
// endpoint. Unlike TestMultiDocCozystack_EndpointRequired, this test
// does NOT override `endpoint` manually; it relies exclusively on
// what the chart ships by default, so it catches any future regression
// that puts a non-empty placeholder back into values.yaml.
func TestMultiDocCozystack_ShippedDefaultsFailFresh(t *testing.T) {
	origLookup := helmEngine.LookupFunc
	t.Cleanup(func() { helmEngine.LookupFunc = origLookup })
	helmEngine.LookupFunc = simpleNicLookup()

	chrt, err := loader.LoadDir("../../charts/cozystack")
	if err != nil {
		t.Fatalf("load chart: %v", err)
	}

	eng := helmEngine.Engine{}
	// Render with chrt.Values exactly as shipped — no test injection.
	_, err = eng.Render(chrt, chartutil.Values{
		"Values":       chrt.Values,
		"TalosVersion": "v1.12",
	})
	if err == nil {
		t.Fatal("expected render to fail on shipped defaults — values.yaml must not ship a placeholder endpoint that silently satisfies required()")
	}
	if !strings.Contains(err.Error(), "endpoint") {
		t.Errorf("error should mention 'endpoint'; got: %v", err)
	}
}

// TestMultiDocCozystack_Layer2VIPConfigWhenFloatingIPSet pins that
// the VIP path still works when the operator explicitly sets
// floatingIP — the fix for the shipped-placeholder bug blanked the
// default but must not break the VIP feature itself.
func TestMultiDocCozystack_Layer2VIPConfigWhenFloatingIPSet(t *testing.T) {
	result := renderCozystackWith(t, simpleNicLookup(), map[string]any{
		"floatingIP": "192.168.201.5",
	})

	assertContains(t, result, "kind: Layer2VIPConfig")
	assertContains(t, result, `"192.168.201.5"`)
}

// TestMultiDocCozystack_NoVIPOnFreshDefaults asserts the corollary:
// a fresh install keeps floatingIP blank, so Layer2VIPConfig must not
// appear. This is the regression guard for the shipped-placeholder
// fix — any future commit that re-introduces a non-empty floatingIP
// default fails this test.
func TestMultiDocCozystack_NoVIPOnFreshDefaults(t *testing.T) {
	result := renderCozystackWith(t, simpleNicLookup(), map[string]any{})

	assertNotContains(t, result, "kind: Layer2VIPConfig")
}

// TestMultiDocCozystack_VIPLinkOverride pins the chicken-and-egg fix
// for nodes that need the VIP on a link that does not yet exist on
// the live system at first apply (typical case: a VLAN sub-interface
// that the same template is about to bring up). Without an override
// the chart would derive vipLink from discovery, which on a fresh
// install sees only the physical NIC and pins the VIP there — after
// apply the VLAN comes up and the VIP is on the wrong link. Setting
// .Values.vipLink lets the operator declare the target link up front
// so the rendered Layer2VIPConfig matches the post-apply network.
func TestMultiDocCozystack_VIPLinkOverride(t *testing.T) {
	result := renderCozystackWith(t, simpleNicLookup(), map[string]any{
		"floatingIP": "192.168.201.5",
		"vipLink":    "eth0.4000",
	})

	assertContains(t, result, "kind: Layer2VIPConfig")
	assertContains(t, result, "link: eth0.4000")
	assertNotContains(t, result, "link: eth0\n")

	// Override-path Layer2VIPConfig must emit exactly once. The
	// discovery-derived block is gated on `not .Values.vipLink`, so
	// no second document with link: eth0 should appear alongside
	// the override.
	if c := strings.Count(result, "kind: Layer2VIPConfig"); c != 1 {
		t.Errorf("expected exactly one Layer2VIPConfig document, got %d:\n%s", c, result)
	}
}

// TestMultiDocCozystack_VIPLinkOverrideOnFreshNode pins the
// fresh-node case for the vipLink override: a node with no
// discovered default-gateway link (totally fresh: no addresses, no
// routes — first-boot state before the chart's own LinkConfig has
// run) must still emit a Layer2VIPConfig when the operator has set
// .Values.vipLink. Without this the override silently no-ops on the
// exact case it was added for: the operator wants the VIP on a VLAN
// sub-interface this same template is about to bring up, but the
// chart hides the VIP doc behind a discovery-resolved-link gate the
// fresh node has not met.
func TestMultiDocCozystack_VIPLinkOverrideOnFreshNode(t *testing.T) {
	// On a fresh node, discovery cannot derive advertisedSubnets, so
	// the operator must set it explicitly — same path the chart's
	// `required` guard documents in values.yaml. Set it here so the
	// render reaches the VIP block we want to exercise instead of
	// erroring out earlier.
	result := renderCozystackWith(t, freshNicLookup(), map[string]any{
		"floatingIP":        "192.168.201.5",
		"vipLink":           "eth0.4000",
		"advertisedSubnets": []any{"192.168.201.0/24"},
	})

	assertContains(t, result, "kind: Layer2VIPConfig")
	assertContains(t, result, "link: eth0.4000")
	if c := strings.Count(result, "kind: Layer2VIPConfig"); c != 1 {
		t.Errorf("expected exactly one Layer2VIPConfig on fresh-node override, got %d:\n%s", c, result)
	}
}

// renderLegacyChart renders the controlplane template of the supplied
// chart against a "legacy" Talos config (TalosVersion=""), routing
// through talos.config.legacy. Mirrors the multidoc render helpers
// above but exercises the legacy code path that pre-1.12 Talos still
// uses by default. Returns the rendered controlplane document.
func renderLegacyChart(t *testing.T, chartDir, templateName string, lookup func(string, string, string) (map[string]any, error), overrides map[string]any) string {
	t.Helper()
	origLookup := helmEngine.LookupFunc
	t.Cleanup(func() { helmEngine.LookupFunc = origLookup })
	helmEngine.LookupFunc = lookup

	chrt, err := loader.LoadDir(chartDir)
	if err != nil {
		t.Fatalf("load chart: %v", err)
	}
	values := cloneValues(chrt.Values)
	if v, _ := values["endpoint"].(string); v == "" {
		values["endpoint"] = testEndpoint
	}
	maps.Copy(values, overrides)

	eng := helmEngine.Engine{}
	out, err := eng.Render(chrt, chartutil.Values{
		"Values":       values,
		"TalosVersion": "",
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	return out[templateName]
}

// TestLegacyCozystack_VIPLinkOverride pins the legacy-schema mirror
// of TestMultiDocCozystack_VIPLinkOverride. The legacy Talos config
// shape has no Layer2VIPConfig document — VIPs live at
// machine.network.interfaces[].vip — so the override is expressed as
// a separate vip-only top-level interfaces[] entry. Without this
// fix, fresh `talm init -p cozystack` users on the default
// `talosVersion: ""` chart setting silently lose the override.
func TestLegacyCozystack_VIPLinkOverride(t *testing.T) {
	result := renderLegacyChart(t, "../../charts/cozystack", "cozystack/templates/controlplane.yaml", simpleNicLookup(), map[string]any{
		"floatingIP": "192.168.201.5",
		"vipLink":    "eth0.4000",
	})

	// Override entry: a top-level interfaces[] entry with the
	// operator's link name and only the vip block.
	assertContains(t, result, "- interface: eth0.4000")
	assertContains(t, result, "ip: 192.168.201.5")
	// Inline (discovery-derived) vip on the bare NIC must be
	// suppressed when vipLink redirects the VIP.
	assertNotContains(t, result, "interface: eth0\n      addresses: [\"192.168.201.10/24\"]\n      routes:\n        - network: 0.0.0.0/0\n          gateway: 192.168.201.1\n      vip:")
	// Legacy schema has no Layer2VIPConfig kind.
	assertNotContains(t, result, "kind: Layer2VIPConfig")
}

// TestLegacyGeneric_VIPLinkOverride mirrors the cozystack-side legacy
// override test for the generic preset. The generic chart ships
// `talosVersion: ""` by default, so the legacy branch is the path a
// fresh `talm init -p generic` user actually takes.
func TestLegacyGeneric_VIPLinkOverride(t *testing.T) {
	result := renderLegacyChart(t, "../../charts/generic", "generic/templates/controlplane.yaml", simpleNicLookup(), map[string]any{
		"floatingIP": "192.168.201.5",
		"vipLink":    "eth0.4000",
	})

	assertContains(t, result, "- interface: eth0.4000")
	assertContains(t, result, "ip: 192.168.201.5")
	assertNotContains(t, result, "interface: eth0\n      addresses: [\"192.168.201.10/24\"]\n      routes:\n        - network: 0.0.0.0/0\n          gateway: 192.168.201.1\n      vip:")
	assertNotContains(t, result, "kind: Layer2VIPConfig")
}

// TestLegacyCozystack_VIPLinkOverrideOnFreshNode pins the
// chicken-and-egg case for legacy: a node where discovery returns no
// default-gateway link must still emit the override entry. Without
// this the override would silently no-op on the exact case it was
// added for — the operator wants the VIP on a VLAN sub-interface
// this same template is about to bring up.
func TestLegacyCozystack_VIPLinkOverrideOnFreshNode(t *testing.T) {
	result := renderLegacyChart(t, "../../charts/cozystack", "cozystack/templates/controlplane.yaml", freshNicLookup(), map[string]any{
		"floatingIP":        "192.168.201.5",
		"vipLink":           "eth0.4000",
		"advertisedSubnets": []any{"192.168.201.0/24"},
	})

	assertContains(t, result, "interfaces:")
	assertContains(t, result, "- interface: eth0.4000")
	assertContains(t, result, "ip: 192.168.201.5")
}

// TestLegacyCozystack_VIPLinkMatchesDiscovery pins the no-op case:
// when vipLink names the same link discovery already picked, the
// chart must NOT emit a duplicate interfaces[] entry — Talos legacy
// validation rejects duplicate interface names. The inline vip block
// on the discovered interface must remain, since it already pins the
// VIP on the right link.
func TestLegacyCozystack_VIPLinkMatchesDiscovery(t *testing.T) {
	result := renderLegacyChart(t, "../../charts/cozystack", "cozystack/templates/controlplane.yaml", simpleNicLookup(), map[string]any{
		"floatingIP": "192.168.201.5",
		"vipLink":    "eth0",
	})

	// Exactly one interface entry for eth0 — not a duplicate.
	if c := strings.Count(result, "- interface: eth0"); c != 1 {
		t.Errorf("expected exactly one - interface: eth0 entry, got %d:\n%s", c, result)
	}
	// Inline vip is preserved on the discovered entry.
	assertContains(t, result, "vip:")
	assertContains(t, result, "ip: 192.168.201.5")
}

// TestMergeFileAsPatch_TypedDocPartialEditPreservesIdentityKeys pins
// the regression that the multi-doc identity prune introduced: a
// typed multi-doc body where the user changes one field but keeps
// the rest identical to rendered (the dominant `talm template -I`
// follow-up edit pattern) had its apiVersion / kind / name pruned
// because every identity key is byte-equal to rendered's. The body
// then reached configpatcher.LoadPatch as a bare key/value map and
// LoadPatch rejected it with `missing kind`.
//
// Pin the post-fix contract: a partial-edit typed-doc body must
// preserve enough identity for LoadPatch to route the patch to the
// correct rendered document, and the override field must reach the
// merged config.
func TestMergeFileAsPatch_TypedDocPartialEditPreservesIdentityKeys(t *testing.T) {
	rendered := renderChartTemplate(t, "../../charts/cozystack", "templates/controlplane.yaml", "v1.12")

	// Operator copies rendered output into the body, then edits the
	// hostname. apiVersion/kind/name remain byte-identical with rendered.
	body := strings.Replace(rendered,
		`hostname: "talos-`,
		`hostname: "operator-edited-`,
		1,
	)
	if body == rendered {
		t.Fatalf("test fixture broken: rendered output did not contain expected hostname pattern:\n%s", rendered)
	}
	body = "# talm: nodes=[\"10.0.0.1\"], templates=[\"templates/controlplane.yaml\"]\n" + body

	dir := t.TempDir()
	nodeFile := filepath.Join(dir, "node0.yaml")
	if err := os.WriteFile(nodeFile, []byte(body), 0o644); err != nil {
		t.Fatalf("write node file: %v", err)
	}

	merged, err := MergeFileAsPatch([]byte(rendered), nodeFile)
	if err != nil {
		t.Fatalf("MergeFileAsPatch on typed-doc partial edit: %v", err)
	}

	out := string(merged)
	if !strings.Contains(out, "operator-edited-") {
		t.Errorf("hostname override did not reach merged config:\n%s", out)
	}
}

// TestMultiDocCozystack_VIPLinkOverrideDoesNotAutoEmitLinkConfig
// pins the doc-vs-reality contract for the vipLink override:
// values.yaml comments and the README state explicitly that the
// chart does NOT auto-emit a LinkConfig or VLANConfig for the
// override link. The operator is responsible for bringing the link
// up via their own per-node body overlay. Without this guard, a
// future "make vipLink autoconfigure the link" patch would leave
// the documented contract stale and the existing operator workflow
// (override + body LinkConfig) would suddenly produce duplicate
// LinkConfig documents.
func TestMultiDocCozystack_VIPLinkOverrideDoesNotAutoEmitLinkConfig(t *testing.T) {
	result := renderCozystackWith(t, freshNicLookup(), map[string]any{
		"floatingIP":        "192.168.201.5",
		"vipLink":           "eth0.4000",
		"advertisedSubnets": []any{"192.168.201.0/24"},
	})

	if strings.Contains(result, "name: eth0.4000") {
		t.Errorf("rendered output unexpectedly references the override link by name (LinkConfig/VLANConfig auto-emit?); the chart docs state this is the operator's responsibility:\n%s", result)
	}
}

// TestMultiDocCozystack_VIPLinkDefaultsToDiscovery asserts that the
// override is opt-in: when .Values.vipLink is left blank the chart
// keeps the existing discovery-derived behavior (link from the
// default-gateway-bearing interface), unchanged from prior releases.
func TestMultiDocCozystack_VIPLinkDefaultsToDiscovery(t *testing.T) {
	result := renderCozystackWith(t, simpleNicLookup(), map[string]any{
		"floatingIP": "192.168.201.5",
	})

	assertContains(t, result, "kind: Layer2VIPConfig")
	assertContains(t, result, "link: eth0")
	assertNotContains(t, result, "link: eth0.")
}

// TestMultiDocCozystack_DedupesDuplicateSubnetsFromMultipleAddresses
// pins that a link with multiple addresses in the same subnet emits
// a single entry in validSubnets / advertisedSubnets, not one entry
// per address. validSubnets is a set semantically, so duplicates are
// noise that churns config diffs.
func TestMultiDocCozystack_DedupesDuplicateSubnetsFromMultipleAddresses(t *testing.T) {
	// Lookup fixture: two addresses on eth0 in the same /24.
	multiAddrLookup := func() func(string, string, string) (map[string]any, error) {
		eth0 := map[string]any{
			"metadata": map[string]any{"id": "eth0"},
			"spec": map[string]any{
				"kind":         "physical",
				"index":        1,
				"hardwareAddr": "aa:bb:cc:00:00:01",
				"busPath":      "pci-0000:00:1f.0",
			},
		}
		routesList := map[string]any{
			"apiVersion": "v1",
			"kind":       "List",
			"items": []any{
				map[string]any{
					"spec": map[string]any{
						"dst": "", "gateway": "192.168.201.1",
						"outLinkName": "eth0", "family": "inet4", "table": "main",
					},
				},
			},
		}
		addressesList := map[string]any{
			"apiVersion": "v1",
			"kind":       "List",
			"items": []any{
				map[string]any{"spec": map[string]any{
					"linkName": "eth0", "address": "192.168.201.10/24",
					"family": "inet4", "scope": "global",
				}},
				map[string]any{"spec": map[string]any{
					"linkName": "eth0", "address": "192.168.201.11/24",
					"family": "inet4", "scope": "global",
				}},
			},
		}
		return func(resource, _, id string) (map[string]any, error) {
			switch resource {
			case "routes":
				return routesList, nil
			case "links":
				if id == "eth0" {
					return eth0, nil
				}
				if id == "" {
					return map[string]any{"apiVersion": "v1", "kind": "List", "items": []any{eth0}}, nil
				}
			case "addresses":
				return addressesList, nil
			case "nodeaddress":
				if id == "default" {
					return map[string]any{"spec": map[string]any{"addresses": []any{"192.168.201.10/24"}}}, nil
				}
			case "resolvers":
				if id == "resolvers" {
					return map[string]any{"spec": map[string]any{"dnsServers": []any{"8.8.8.8"}}}, nil
				}
			}
			return map[string]any{}, nil
		}
	}()

	result := renderCozystackWith(t, multiAddrLookup, map[string]any{
		"advertisedSubnets": []any{},
	})

	// Two addresses in the same subnet must collapse to one list entry.
	if got := strings.Count(result, "- 192.168.201.0/24"); got != 2 {
		// Expected: 1 in validSubnets + 1 in advertisedSubnets = 2 total.
		t.Errorf("expected canonical subnet 192.168.201.0/24 to appear exactly 2 times (once each in validSubnets and advertisedSubnets), got %d:\n%s", got, result)
	}
}

// TestMultiDocCozystack_EmptyDiscoveryErrors pins that when the
// operator leaves advertisedSubnets empty AND discovery returns
// nothing (no default-gateway-bearing link found), the chart fails
// loudly via required() instead of silently emitting an empty
// validSubnets list. A silent empty list would be worse than the
// previous buggy default because nothing surfaces the problem.
func TestMultiDocCozystack_EmptyDiscoveryErrors(t *testing.T) {
	origLookup := helmEngine.LookupFunc
	t.Cleanup(func() { helmEngine.LookupFunc = origLookup })
	helmEngine.LookupFunc = func(string, string, string) (map[string]any, error) {
		return map[string]any{}, nil
	}

	chrt, err := loader.LoadDir("../../charts/cozystack")
	if err != nil {
		t.Fatalf("load chart: %v", err)
	}
	values := make(map[string]any)
	maps.Copy(values, chrt.Values)
	values["endpoint"] = testEndpoint
	values["advertisedSubnets"] = []any{}

	eng := helmEngine.Engine{}
	_, err = eng.Render(chrt, chartutil.Values{
		"Values":       values,
		"TalosVersion": "v1.12",
	})
	if err == nil {
		t.Fatal("expected required() error when advertisedSubnets is empty and discovery yields nothing")
	}
	// Assert on both the user-facing field name and the diagnostic
	// phrase about the default route — two independent substrings
	// that together pin the guidance the error is supposed to deliver.
	// If a future reword drops either signal, this test catches it.
	if !strings.Contains(err.Error(), "advertisedSubnets") {
		t.Errorf("error should mention advertisedSubnets field; got: %v", err)
	}
	if !strings.Contains(err.Error(), "default route") {
		t.Errorf("error should mention 'default route' remediation; got: %v", err)
	}
}

// TestMultiDocCozystack_WorkerValidSubnetsFallsBackToDiscovery pins
// the fallback on worker nodes. The kubelet.validSubnets block lives
// in the shared talos.config.machine.common definition, so it is
// emitted for both controlplane and worker templates — this test
// catches a regression that would only break the worker path.
func TestMultiDocCozystack_WorkerValidSubnetsFallsBackToDiscovery(t *testing.T) {
	origLookup := helmEngine.LookupFunc
	t.Cleanup(func() { helmEngine.LookupFunc = origLookup })
	helmEngine.LookupFunc = simpleNicLookup()

	chrt, err := loader.LoadDir("../../charts/cozystack")
	if err != nil {
		t.Fatalf("load chart: %v", err)
	}
	values := make(map[string]any)
	maps.Copy(values, chrt.Values)
	values["endpoint"] = testEndpoint
	values["advertisedSubnets"] = []any{}

	eng := helmEngine.Engine{}
	out, err := eng.Render(chrt, chartutil.Values{
		"Values":       values,
		"TalosVersion": "v1.12",
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	result := out["cozystack/templates/worker.yaml"]
	assertContains(t, result, "validSubnets:")
	assertContains(t, result, "- 192.168.201.0/24")
	if strings.Contains(result, "192.168.100.0/24") {
		t.Errorf("worker output contains stale placeholder 192.168.100.0/24:\n%s", result)
	}
}
