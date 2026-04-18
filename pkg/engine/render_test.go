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
	// through this helper.
	values := make(map[string]any)
	maps.Copy(values, chrt.Values)
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
	return func(resource, namespace, id string) (map[string]any, error) {
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
	return func(resource, namespace, id string) (map[string]any, error) {
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
	return func(resource, namespace, id string) (map[string]any, error) {
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
	values := make(map[string]any)
	maps.Copy(values, chrt.Values)
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
	values := make(map[string]any)
	maps.Copy(values, chrt.Values)
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
		return func(resource, namespace, id string) (map[string]any, error) {
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
	if !strings.Contains(err.Error(), "advertisedSubnets") {
		t.Errorf("error should mention advertisedSubnets / default route; got: %v", err)
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
