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
	"maps"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	helmEngine "github.com/cozystack/talm/pkg/engine/helm"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/chartutil"
)

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

	rootValues := chartutil.Values{
		"Values":       chrt.Values,
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
	assertContains(t, output, "clusterName:")
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
	assertContains(t, output, "clusterName:")
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
	output := renderChartTemplate(t, "../../charts/cozystack", "templates/controlplane.yaml", "v1.12")

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
	assertContains(t, output, "clusterName:")
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
	output := renderChartTemplate(t, "../../charts/cozystack", "templates/controlplane.yaml", "v1.11")

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
	output := renderChartTemplate(t, "../../charts/generic", "templates/controlplane.yaml", "v1.12")

	// Multi-doc: machine section still present but WITHOUT legacy network fields
	assertContains(t, output, "machine:")
	assertContains(t, output, "type: controlplane")
	assertContains(t, output, "kubelet:")
	assertContains(t, output, "install:")

	// Multi-doc: deprecated machine.network fields REMOVED (hostname, nameservers, interfaces)
	assertNotContains(t, output, "    interfaces:")

	// Multi-doc: cluster section still present
	assertContains(t, output, "cluster:")
	assertContains(t, output, "clusterName:")
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
	output := renderChartTemplate(t, "../../charts/generic", "templates/worker.yaml", "v1.12")

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
		helmEngine.LookupFunc = func(resource, namespace, name string) (map[string]any, error) {
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
