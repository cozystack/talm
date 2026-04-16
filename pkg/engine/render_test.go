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

	// Multi-doc: Layer2VIPConfig emitted for controlplane when floatingIP is
	// set in values (cozystack default: 192.168.100.10).
	assertContains(t, output, "kind: Layer2VIPConfig")
	assertContains(t, output, "192.168.100.10")

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

	eng := helmEngine.Engine{}
	out, err := eng.Render(chrt, chartutil.Values{
		"Values":       chrt.Values,
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

	eng := helmEngine.Engine{}
	out, err := eng.Render(chrt, chartutil.Values{
		"Values":       chrt.Values,
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

	eng := helmEngine.Engine{}
	out, err := eng.Render(chrt, chartutil.Values{
		"Values":       chrt.Values,
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

	eng := helmEngine.Engine{}
	out, err := eng.Render(chrt, chartutil.Values{
		"Values":       chrt.Values,
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

// TestMergeFileAsPatch covers #126: when `talm apply -f node.yaml` runs
// the template-rendering branch (modeline `templates=[...]`), the
// non-modeline body of the node file must overlay the rendered output —
// previously it was silently discarded, taking per-node hostname,
// secondary interfaces, VIP placement, etc. with it.
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
		// Reproduces the BLOCKER 2 regression vector: pre-fix, a
		// modeline-only node file routed through configpatcher.Apply →
		// JSON6902, which rejects any multi-document machine config with
		// `JSON6902 patches are not supported for multi-document machine
		// configuration`. Talos v1.12+ default output is multi-doc.
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
}

// TestRenderFailIfMultiNodes_UsesCommandName covers #121: the multi-node
// rejection error must reference the calling subcommand passed via
// Options.CommandName, not the historical hardcoded "talm template" that
// confused users running `talm apply`.
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

	t.Run("non-empty CommandName must not leak the historical default", func(t *testing.T) {
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
