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

// Contract: chart-level validation error messages. Every `fail` and
// `required` directive in cozystack and generic _helpers.tpl emits a
// user-facing error string. These strings appear in `talm template`
// stderr and CI logs; users grep them to debug bad inputs. Pinning the
// substrings here means changing an error message becomes an
// intentional, reviewable act, not a silent break for everyone with a
// matching grep in their alerting.
//
// The fail catalogue (one test per fail across the two charts):
//   1. endpoint missing  → `required` guard
//   2. advertisedSubnets empty + no discovery default route → `fail`
//   3. multi-doc + existing legacy machine.network.interfaces[] → `fail`
//   4. multi-doc + VLAN with unresolvable parent → `fail`
//   5. multi-doc + VLAN with no vlanID → `fail`
//
// The bridge-as-gateway case used to be item 4 in this catalogue
// when it was a hard-fail; after BridgeConfig emission landed it
// no longer fails, and its negative pin lives in
// contract_network_multidoc_test.go alongside the related
// BridgeConfigEmitted contract.

package engine

import (
	"maps"
	"strings"
	"testing"

	helmEngine "github.com/cozystack/talm/pkg/engine/helm"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/chartutil"
)

// renderExpectingError calls the Helm engine directly with the given
// chart, lookup, values, and talosVersion. Returns the raw error from
// helm Render so the test can assert on the message body. Unlike
// renderChartTemplate, this helper does not call t.Fatal on failure —
// failure IS the contract under test. A successful render is the
// failure case: an unexpected absence of the error.
func renderExpectingError(t *testing.T, chartPath, talosVersion string, lookup func(string, string, string) (map[string]any, error), values map[string]any) error {
	t.Helper()

	origLookup := helmEngine.LookupFunc
	t.Cleanup(func() { helmEngine.LookupFunc = origLookup })
	if lookup != nil {
		helmEngine.LookupFunc = lookup
	} else {
		helmEngine.LookupFunc = helmEngineEmptyLookup
	}

	chrt, err := loader.LoadDir(chartPath)
	if err != nil {
		t.Fatalf("load chart %s: %v", chartPath, err)
	}

	merged := cloneValues(chrt.Values)
	maps.Copy(merged, values)

	eng := helmEngine.Engine{}
	_, err = eng.Render(chrt, chartutil.Values{
		"Values":       merged,
		"TalosVersion": talosVersion,
	})
	//nolint:wrapcheck // helm.Engine.Render returns a typed error; tests assert via require.Error/NoError.
	return err
}

// === required: endpoint ===

// Contract: when values.endpoint is empty (or absent), both charts
// fail the render with a long required-guard message that explains
// (a) the field is cluster-wide, (b) why it cannot be auto-derived,
// (c) which topologies (VIP, external LB, single-node) need what.
// The message is identical between cozystack and generic — pin a
// substring stable enough to survive minor wording tweaks.
func TestContract_Errors_EndpointRequired(t *testing.T) {
	for _, chartPath := range []string{cozystackChartPath, genericChartPath} {
		t.Run(chartPath, func(t *testing.T) {
			err := renderExpectingError(t, chartPath, "", helmEngineEmptyLookup, map[string]any{
				"endpoint":          "",
				"advertisedSubnets": []any{testAdvertisedSubnet},
			})
			if err == nil {
				t.Fatalf("expected required-endpoint error, got nil")
			}
			msg := err.Error()
			if !strings.Contains(msg, "endpoint") {
				t.Errorf("error must mention 'endpoint', got: %s", msg)
			}
			if !strings.Contains(msg, "https://") {
				t.Errorf("error must show URL example, got: %s", msg)
			}
			if !strings.Contains(msg, "talm template") {
				t.Errorf("error must mention 'talm template' (explains why no auto-derivation), got: %s", msg)
			}
		})
	}
}

// === fail: advertisedSubnets empty + empty discovery ===

// Contract: when values.advertisedSubnets is empty AND discovery
// returns no default-gateway-bearing link, both charts fail with a
// guidance message naming both the values key and the operator's
// recourse (set explicitly, or ensure a default route exists).
func TestContract_Errors_AdvertisedSubnetsEmptyAndNoDiscovery(t *testing.T) {
	for _, chartPath := range []string{cozystackChartPath, genericChartPath} {
		t.Run(chartPath, func(t *testing.T) {
			err := renderExpectingError(t, chartPath, "", helmEngineEmptyLookup, map[string]any{
				"endpoint":          testEndpoint,
				"advertisedSubnets": []any{}, // explicit empty
			})
			if err == nil {
				t.Fatalf("expected advertisedSubnets-empty error, got nil")
			}
			msg := err.Error()
			if !strings.Contains(msg, "advertisedSubnets") {
				t.Errorf("error must mention 'advertisedSubnets', got: %s", msg)
			}
			if !strings.Contains(msg, "default-gateway-bearing link") {
				t.Errorf("error must mention 'default-gateway-bearing link', got: %s", msg)
			}
			if !strings.Contains(msg, "talm template") {
				t.Errorf("error must mention 'talm template', got: %s", msg)
			}
			// Operator recourse is named both ways: edit values, or fix routing.
			if !strings.Contains(msg, "values.yaml") {
				t.Errorf("error must mention values.yaml as recourse, got: %s", msg)
			}
		})
	}
}

// === fail: multi-doc + legacy machine.network.interfaces[] in running config ===

// legacyInterfacesLookup builds a LookupFunc whose `machineconfig`
// resource returns a v1alpha1 spec carrying a non-empty
// machine.network.interfaces[] list. Triggers the multi-doc renderer's
// hard-fail guard against silently dropping legacy network state.
func legacyInterfacesLookup() func(string, string, string) (map[string]any, error) {
	machineconfig := map[string]any{
		"spec": map[string]any{
			"machine": map[string]any{
				"network": map[string]any{
					"interfaces": []any{
						map[string]any{
							"interface": "eth0",
							"addresses": []any{"192.168.1.10/24"},
						},
					},
				},
			},
		},
	}
	return func(resource, _, id string) (map[string]any, error) {
		if resource == "machineconfig" && id == "v1alpha1" {
			return machineconfig, nil
		}
		return map[string]any{}, nil
	}
}

// Contract: multi-doc renderer aborts if the running MachineConfig
// already carries machine.network.interfaces[]. The fail message
// explains both the why (renderer cannot translate legacy block to
// LinkConfig/VLANConfig/BondConfig) and the operator recourse (move
// to body overlay, or pin Talos version <1.12). The detected legacy
// block is included for debugging.
func TestContract_Errors_MultidocLegacyInterfacesBlock(t *testing.T) {
	for _, chartPath := range []string{cozystackChartPath, genericChartPath} {
		t.Run(chartPath, func(t *testing.T) {
			err := renderExpectingError(t, chartPath, multidocTalos, legacyInterfacesLookup(), map[string]any{
				"endpoint":          testEndpoint,
				"advertisedSubnets": []any{testAdvertisedSubnet},
			})
			if err == nil {
				t.Fatalf("expected legacy-interfaces fail, got nil")
			}
			msg := err.Error()
			if !strings.Contains(msg, "talm:") {
				t.Errorf("error must use 'talm:' prefix for the multi-doc fail family, got: %s", msg)
			}
			if !strings.Contains(msg, "multi-doc renderer") {
				t.Errorf("error must mention 'multi-doc renderer', got: %s", msg)
			}
			if !strings.Contains(msg, "machine.network.interfaces[]") {
				t.Errorf("error must reference legacy field path, got: %s", msg)
			}
			if !strings.Contains(msg, "LinkConfig") || !strings.Contains(msg, "VLANConfig") || !strings.Contains(msg, "BondConfig") {
				t.Errorf("error must list the typed v1.12 doc kinds as recourse, got: %s", msg)
			}
			if !strings.Contains(msg, "v1.11") {
				t.Errorf("error must mention v1.11 as the version-pin recourse, got: %s", msg)
			}
		})
	}
}

// === fail: multi-doc + VLAN with no parent ===

// vlanMissingParentLookup builds a LookupFunc whose VLAN link has a
// linkIndex pointing at a non-existent parent. Triggers the
// missing-parent guard in the VLAN branch of the multi-doc renderer.
func vlanMissingParentLookup() func(string, string, string) (map[string]any, error) {
	vlan := map[string]any{
		"metadata": map[string]any{"id": "vlan100"},
		"spec": map[string]any{
			"kind":      "vlan",
			"index":     20,
			"linkIndex": 999, // points at nothing
			"vlan": map[string]any{
				"vlanID": 100,
			},
		},
	}
	links := map[string]any{
		"apiVersion": "v1",
		"kind":       "List",
		"items":      []any{vlan},
	}
	routes := map[string]any{
		"apiVersion": "v1",
		"kind":       "List",
		"items": []any{
			map[string]any{
				"spec": map[string]any{
					"dst":         "",
					"gateway":     "192.168.1.1",
					"outLinkName": "vlan100",
					"family":      "inet4",
					"table":       "main",
				},
			},
		},
	}
	return func(resource, _, id string) (map[string]any, error) {
		switch resource {
		case "links":
			if id == "vlan100" {
				return vlan, nil
			}
			if id == "" {
				return links, nil
			}
		case "routes":
			return routes, nil
		}
		return map[string]any{}, nil
	}
}

// Contract: multi-doc renderer aborts when a VLAN's parent cannot be
// resolved. VLANConfig requires the `parent` field; emitting one
// without it produces a document Talos rejects on apply. The fail
// names the offending VLAN and the recourse (fix discovery, or
// declare the VLAN explicitly).
func TestContract_Errors_MultidocVLANMissingParent(t *testing.T) {
	for _, chartPath := range []string{cozystackChartPath, genericChartPath} {
		t.Run(chartPath, func(t *testing.T) {
			err := renderExpectingError(t, chartPath, multidocTalos, vlanMissingParentLookup(), map[string]any{
				"endpoint":          testEndpoint,
				"advertisedSubnets": []any{testAdvertisedSubnet},
			})
			if err == nil {
				t.Fatalf("expected vlan-missing-parent fail, got nil")
			}
			msg := err.Error()
			if !strings.Contains(msg, "talm:") {
				t.Errorf("error must use 'talm:' prefix, got: %s", msg)
			}
			if !strings.Contains(msg, `VLAN "vlan100"`) {
				t.Errorf("error must name the VLAN 'vlan100', got: %s", msg)
			}
			if !strings.Contains(msg, "parent link") {
				t.Errorf("error must reference the missing 'parent link', got: %s", msg)
			}
			if !strings.Contains(msg, "spec.linkIndex") {
				t.Errorf("error must reference spec.linkIndex (the discovery field), got: %s", msg)
			}
		})
	}
}

// === fail: multi-doc + VLAN with no vlanID ===

// vlanMissingVlanIDLookup builds a LookupFunc whose VLAN link has a
// resolvable parent but no spec.vlan.vlanID. Triggers the missing-
// vlanID guard, the symmetric counterpart of the missing-parent fail.
func vlanMissingVlanIDLookup() func(string, string, string) (map[string]any, error) {
	parent := map[string]any{
		"metadata": map[string]any{"id": "eth0"},
		"spec": map[string]any{
			"kind":    "physical",
			"index":   1,
			"busPath": "pci-0000:00:1f.6",
		},
	}
	vlan := map[string]any{
		"metadata": map[string]any{"id": "vlan100"},
		"spec": map[string]any{
			"kind":      "vlan",
			"index":     20,
			"linkIndex": 1, // points at parent eth0
			"vlan":      map[string]any{
				// vlanID intentionally absent
			},
		},
	}
	links := map[string]any{
		"apiVersion": "v1",
		"kind":       "List",
		"items":      []any{parent, vlan},
	}
	routes := map[string]any{
		"apiVersion": "v1",
		"kind":       "List",
		"items": []any{
			map[string]any{
				"spec": map[string]any{
					"dst":         "",
					"gateway":     "192.168.1.1",
					"outLinkName": "vlan100",
					"family":      "inet4",
					"table":       "main",
				},
			},
		},
	}
	return func(resource, _, id string) (map[string]any, error) {
		switch resource {
		case "links":
			if id == "eth0" {
				return parent, nil
			}
			if id == "vlan100" {
				return vlan, nil
			}
			if id == "" {
				return links, nil
			}
		case "routes":
			return routes, nil
		}
		return map[string]any{}, nil
	}
}

// Contract: multi-doc renderer aborts when a VLAN has no resolvable
// vlanID. Symmetric counterpart of the missing-parent fail.
func TestContract_Errors_MultidocVLANMissingVlanID(t *testing.T) {
	for _, chartPath := range []string{cozystackChartPath, genericChartPath} {
		t.Run(chartPath, func(t *testing.T) {
			err := renderExpectingError(t, chartPath, multidocTalos, vlanMissingVlanIDLookup(), map[string]any{
				"endpoint":          testEndpoint,
				"advertisedSubnets": []any{testAdvertisedSubnet},
			})
			if err == nil {
				t.Fatalf("expected vlan-missing-vlanID fail, got nil")
			}
			msg := err.Error()
			if !strings.Contains(msg, "talm:") {
				t.Errorf("error must use 'talm:' prefix, got: %s", msg)
			}
			if !strings.Contains(msg, `VLAN "vlan100"`) {
				t.Errorf("error must name the VLAN 'vlan100', got: %s", msg)
			}
			if !strings.Contains(msg, "vlanID") {
				t.Errorf("error must mention 'vlanID', got: %s", msg)
			}
			if !strings.Contains(msg, "spec.vlan.vlanID") {
				t.Errorf("error must reference spec.vlan.vlanID (the discovery field), got: %s", msg)
			}
		})
	}
}
