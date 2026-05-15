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

// Contract: rendered `machine:` section semantics for the cozystack
// and generic charts. Pins user-facing behaviour of every field that
// the chart emits under machine.* — type, nodeLabels, kubelet,
// sysctls, kernel modules, certSANs, files, install — across the
// chart × schema × machineType matrix.
//
// As with contract_cluster_test.go, the cozystack and generic charts
// diverge sharply in this area: cozystack ships a heavy opinionated
// default set (sysctls, kernel modules, hardcoded files, install.image
// pinned to a published Talos build, machine-level certSANs with
// 127.0.0.1), while generic emits the bare minimum (machine.type,
// kubelet.nodeIP, install.disk). Tests below pin both shapes.

package engine

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// === Shared contracts (both charts, both schemas, both machine types) ===

// Contract: machine.type always matches the rendered template name.
// templates/controlplane.yaml emits `type: controlplane`,
// templates/worker.yaml emits `type: worker`. A regression that swaps
// the two would render every node config with the wrong machine type
// — a Talos hard-fail on apply.
func TestContract_Machine_Type_MatchesTemplate(t *testing.T) {
	for _, cell := range allCells() {
		t.Run(cell.name, func(t *testing.T) {
			out := renderChartTemplate(t, cell.chartPath, cell.templateFile, cell.talosVersion)
			if cell.templateFile == controlplaneTpl {
				assertContains(t, out, "type: controlplane")
			} else {
				assertContains(t, out, "type: worker")
			}
		})
	}
}

// Contract: kubelet.nodeIP.validSubnets is always emitted (controls
// which IP kubelet advertises). The list comes either from
// values.advertisedSubnets (when set) or from the discovery fallback
// (subnet of the IPv4-default-gateway-bearing link). Empty discovery
// + empty advertisedSubnets is a chart-level fail with a guidance
// message — pinned in contract_errors_test.go.
//
// renderChartTemplate injects testAdvertisedSubnet so this test
// exercises the explicit-values branch.
func TestContract_Machine_Kubelet_NodeIPValidSubnets(t *testing.T) {
	for _, cell := range allCells() {
		t.Run(cell.name, func(t *testing.T) {
			out := renderChartTemplate(t, cell.chartPath, cell.templateFile, cell.talosVersion)
			assertContains(t, out, "kubelet:")
			assertContains(t, out, "nodeIP:")
			assertContains(t, out, "validSubnets:")
			assertContains(t, out, "- "+testAdvertisedSubnet)
		})
	}
}

// Contract: install.disk is always emitted, sourced from
// talm.discovered.system_disk_name. With offline lookup the helper
// returns an empty string, but the chart still emits the key with a
// quoted empty value. The contract is "this key is present"; the
// resolved value depends on discovery state and is exercised
// elsewhere.
func TestContract_Machine_Install_DiskAlwaysEmitted(t *testing.T) {
	for _, cell := range allCells() {
		t.Run(cell.name, func(t *testing.T) {
			out := renderChartTemplate(t, cell.chartPath, cell.templateFile, cell.talosVersion)
			assertContains(t, out, "install:")
			assertContains(t, out, "disk:")
		})
	}
}

// === Controlplane-only contracts ===

// Contract: cozystack controlplane templates emit `nodeLabels` with a
// `$patch: delete` directive removing the
// `node.kubernetes.io/exclude-from-external-load-balancers` label
// that Kubernetes adds by default. This is required for cozystack's
// VIP / external-LB topology — the label otherwise pins the LB target
// off the control-plane and breaks single-node / VIP setups.
//
// Worker templates never emit nodeLabels.
func TestContract_Machine_NodeLabels_PatchDelete_Cozystack(t *testing.T) {
	for _, cell := range cozystackControlplaneCells() {
		t.Run(cell.name, func(t *testing.T) {
			out := renderChartTemplate(t, cell.chartPath, cell.templateFile, cell.talosVersion)
			assertContains(t, out, "nodeLabels:")
			assertContains(t, out, "node.kubernetes.io/exclude-from-external-load-balancers:")
			assertContains(t, out, "$patch: delete")
		})
	}
}

// Contract: generic chart never emits nodeLabels (it does not have
// the cozystack-specific exclude-from-LB removal). Pinning the
// absence prevents accidental copy-paste from cozystack into generic.
func TestContract_Machine_NodeLabels_AbsentOnGeneric(t *testing.T) {
	for _, cell := range genericCells() {
		t.Run(cell.name, func(t *testing.T) {
			out := renderChartTemplate(t, cell.chartPath, cell.templateFile, cell.talosVersion)
			assertNotContains(t, out, "nodeLabels:")
			assertNotContains(t, out, "exclude-from-external-load-balancers")
		})
	}
}

// Contract: nodeLabels never appears on worker templates (cozystack
// or generic). Worker nodes do not need the LB-exclusion patch.
func TestContract_Machine_NodeLabels_AbsentOnWorker(t *testing.T) {
	for _, cell := range allWorkerCells() {
		t.Run(cell.name, func(t *testing.T) {
			out := renderChartTemplate(t, cell.chartPath, cell.templateFile, cell.talosVersion)
			assertNotContains(t, out, "nodeLabels:")
		})
	}
}

// === cozystack-only contracts ===

// Contract: cozystack pins kubelet extraConfig with cpuManagerPolicy:
// static and maxPods: 512. These are cozystack-specific defaults
// chosen for production density / DPDK-style workloads. A regression
// here silently changes pod-density limits cluster-wide.
func TestContract_Machine_Kubelet_ExtraConfig_Cozystack(t *testing.T) {
	for _, cell := range cozystackCells() {
		t.Run(cell.name, func(t *testing.T) {
			out := renderChartTemplate(t, cell.chartPath, cell.templateFile, cell.talosVersion)
			assertContains(t, out, "extraConfig:")
			assertContains(t, out, "cpuManagerPolicy: static")
			assertContains(t, out, "maxPods: 512")
		})
	}
}

// Contract: cozystack ships three IPv4 ARP-cache sysctls
// (gc_thresh1=4096, gc_thresh2=8192, gc_thresh3=16384). Required for
// dense pod / service deployments; default Talos values run out at
// ~1024 entries. Values are quoted strings (Talos sysctls API
// requires string-typed values).
func TestContract_Machine_Sysctls_GCThresh_Cozystack(t *testing.T) {
	for _, cell := range cozystackCells() {
		t.Run(cell.name, func(t *testing.T) {
			out := renderChartTemplate(t, cell.chartPath, cell.templateFile, cell.talosVersion)
			assertContains(t, out, "sysctls:")
			assertContains(t, out, `net.ipv4.neigh.default.gc_thresh1: "4096"`)
			assertContains(t, out, `net.ipv4.neigh.default.gc_thresh2: "8192"`)
			assertContains(t, out, `net.ipv4.neigh.default.gc_thresh3: "16384"`)
		})
	}
}

// Contract: cozystack does NOT emit vm.nr_hugepages by default
// (values.nr_hugepages: 0). The sysctl is opt-in: a future regression
// that always emitted vm.nr_hugepages: "0" would clobber any
// host-level hugepages tuning.
func TestContract_Machine_Sysctls_NrHugepages_AbsentByDefault_Cozystack(t *testing.T) {
	for _, cell := range cozystackCells() {
		t.Run(cell.name, func(t *testing.T) {
			out := renderChartTemplate(t, cell.chartPath, cell.templateFile, cell.talosVersion)
			assertNotContains(t, out, "vm.nr_hugepages")
		})
	}
}

// Contract: when an operator sets nr_hugepages, cozystack emits the
// sysctl as a quoted string (Talos requires sysctl values typed as
// strings). The override path renders only when the value is truthy
// (non-zero), so 0 stays absent.
func TestContract_Machine_Sysctls_NrHugepages_PresentWhenSet_Cozystack(t *testing.T) {
	out := renderCozystackWith(t, helmEngineEmptyLookup, map[string]any{
		"nr_hugepages":      1024,
		"advertisedSubnets": []any{testAdvertisedSubnet},
	})
	assertContains(t, out, `vm.nr_hugepages: "1024"`)
}

// Contract: cozystack pins six kernel modules (openvswitch, drbd, zfs,
// spl, vfio_pci, vfio_iommu_type1). Each is required by a specific
// cozystack feature: openvswitch for Cilium netkit-style routing, drbd
// for DRBD storage, zfs+spl for ZFS, vfio_* for PCI passthrough.
// drbd carries `parameters: [usermode_helper=disabled]` — required so
// drbd does not invoke /sbin/drbdadm at every state change (the
// cozystack image does not ship drbdadm). Removing any of these
// modules silently breaks the matching feature.
func TestContract_Machine_KernelModules_Cozystack(t *testing.T) {
	for _, cell := range cozystackCells() {
		t.Run(cell.name, func(t *testing.T) {
			out := renderChartTemplate(t, cell.chartPath, cell.templateFile, cell.talosVersion)
			assertContains(t, out, "kernel:")
			assertContains(t, out, "modules:")
			assertContains(t, out, "- name: openvswitch")
			assertContains(t, out, "- name: drbd")
			assertContains(t, out, "- usermode_helper=disabled")
			assertContains(t, out, "- name: zfs")
			assertContains(t, out, "- name: spl")
			assertContains(t, out, "- name: vfio_pci")
			assertContains(t, out, "- name: vfio_iommu_type1")
		})
	}
}

// === Cozystack extension points (extraKernelModules / extraSysctls / extraKubeletExtraArgs / extraMachineFiles) ===
//
// The cozystack preset ships hard-coded defaults for kernel modules,
// sysctls, kubelet extraConfig, and machine.files. Operators wanting
// to extend any of these without forking the preset set the matching
// `extra*` values key; the chart appends operator entries to the
// built-in set.
//
// The built-in set is load-bearing for cozystack's storage /
// networking / runtime stack and is NEVER overridable by an
// extension entry:
//
//   - List-shaped `extra*` values (extraKernelModules,
//     extraMachineFiles) append unconditionally; duplicates by
//     identifying field are tolerated at apply time by Talos
//     (modules dedupe on load; collision-by-path on machine.files
//     is the operator's responsibility).
//   - Map-shaped `extra*` values (extraKubeletExtraArgs, extraSysctls)
//     are guarded at template time by chart-level `fail`: any operator
//     key that names a built-in (e.g. extraSysctls.gc_thresh1) blocks
//     the render with a hint pointing at the offending key. yaml.v3
//     (used by Talos config decode and by the upgrade-time body
//     writeback in this same branch) rejects duplicate map keys on
//     decode, so a silent emit-both merge would produce a config that
//     cannot round-trip. The escape hatch is to fork the preset.
//
// The contract tests below pin both shapes: `_AppendValues` /
// `_MergeInto*` round-trip-decode the rendered output through yaml.v3
// (substring asserts on the bytes would silently pass for invalid
// duplicate-key output), and `_RejectsCollisionWithBuiltin` pins the
// failure mode for every built-in key.

// Contract: values.extraKernelModules APPENDS to the cozystack preset's
// built-in module list — it never overrides. The built-in six
// (openvswitch, drbd, zfs, spl, vfio_pci, vfio_iommu_type1) are
// load-bearing for cozystack's storage/networking stack, so an
// operator who supplies extra modules must NOT silently drop any of
// the built-ins.
func TestContract_Machine_ExtraKernelModules_Cozystack_AppendValues(t *testing.T) {
	out := renderCozystackWith(t, helmEngineEmptyLookup, map[string]any{
		"advertisedSubnets": []any{testAdvertisedSubnet},
		"extraKernelModules": []any{
			map[string]any{"name": "nf_conntrack"},
			map[string]any{"name": "br_netfilter"},
		},
	})
	// Built-in six still present.
	assertContains(t, out, "- name: openvswitch")
	assertContains(t, out, "- name: drbd")
	assertContains(t, out, "- usermode_helper=disabled")
	assertContains(t, out, "- name: zfs")
	assertContains(t, out, "- name: spl")
	assertContains(t, out, "- name: vfio_pci")
	assertContains(t, out, "- name: vfio_iommu_type1")
	// Operator-supplied modules appended.
	assertContains(t, out, "- name: nf_conntrack")
	assertContains(t, out, "- name: br_netfilter")
}

// Contract: an empty values.extraKernelModules (or its default `[]`)
// leaves the cozystack module list IDENTICAL to the built-in six — no
// `[]` suffix, no empty-list artifact, no trailing module lines. The
// `{{- with .Values.extraKernelModules }}` guard relies on Helm's
// emptiness check; a regression that swaps `with` for a bare
// `toYaml .Values.extraKernelModules` would emit `[]` after
// vfio_iommu_type1 and either fail YAML parse or pin the wrong list
// shape. Boundary case between the unset path
// (TestContract_Machine_KernelModules_Cozystack) and the set path
// (_AppendValues).
func TestContract_Machine_ExtraKernelModules_Cozystack_EmptyOmitsAppend(t *testing.T) {
	out := renderCozystackWith(t, helmEngineEmptyLookup, map[string]any{
		"advertisedSubnets":  []any{testAdvertisedSubnet},
		"extraKernelModules": []any{},
	})
	// Built-in six present.
	assertContains(t, out, "- name: vfio_iommu_type1")
	// No empty-list artifact after the built-in tail.
	assertNotContains(t, out, "vfio_iommu_type1\n    []")
	assertNotContains(t, out, "vfio_iommu_type1\n[]")
	// Exactly six `- name:` lines inside the modules block: parse the
	// block bounds and count. Anchors `kernel:` / `certSANs:` are the
	// adjacent siblings under machine.* in the cozystack chart.
	kernelIdx := strings.Index(out, "  kernel:")
	if kernelIdx < 0 {
		t.Fatalf("kernel: block missing from rendered output")
	}
	tail := out[kernelIdx:]
	endIdx := strings.Index(tail, "  certSANs:")
	if endIdx < 0 {
		t.Fatalf("certSANs: sibling missing — block bounds undetectable")
	}
	block := tail[:endIdx]
	gotCount := strings.Count(block, "- name:")
	if gotCount != 6 {
		t.Errorf("expected 6 modules in kernel.modules block with empty extraKernelModules, got %d\nblock:\n%s", gotCount, block)
	}
}

// Contract: values.extraKernelModules entries pass through verbatim,
// including the `parameters:` list. A module that needs a module-param
// (the way the built-in drbd carries `usermode_helper=disabled`) must
// emit it identically when supplied through values, otherwise the
// values-driven path silently differs from the hard-coded one.
func TestContract_Machine_ExtraKernelModules_Cozystack_PreservesParameters(t *testing.T) {
	out := renderCozystackWith(t, helmEngineEmptyLookup, map[string]any{
		"advertisedSubnets": []any{testAdvertisedSubnet},
		"extraKernelModules": []any{
			map[string]any{
				"name":       "nf_conntrack",
				"parameters": []any{"hashsize=131072"},
			},
		},
	})
	assertContains(t, out, "- name: nf_conntrack")
	assertContains(t, out, "- hashsize=131072")
}

// Contract: values.extraKubeletExtraArgs entries merge into the
// kubelet.extraConfig map alongside the cozystack preset's
// `cpuManagerPolicy: static` and `maxPods: 512`. The merge is
// validated by round-trip through yaml.v3 — Talos's config decoder
// uses yaml.v3 which REJECTS duplicate map keys, so the rendered
// output must contain each key exactly once. Operator keys must be
// disjoint from the built-in set (the collision case is rejected at
// render time — see _RejectsCollisionWithBuiltin).
func TestContract_Machine_ExtraKubeletExtraArgs_Cozystack_MergeIntoExtraConfig(t *testing.T) {
	out := renderCozystackWith(t, helmEngineEmptyLookup, map[string]any{
		"advertisedSubnets": []any{testAdvertisedSubnet},
		"extraKubeletExtraArgs": map[string]any{
			"feature-gates": "NodeSwap=true",
		},
	})
	extraConfig := decodeKubeletExtraConfig(t, out)
	if got, want := extraConfig["cpuManagerPolicy"], "static"; got != want {
		t.Errorf("cpuManagerPolicy: got %v, want %v", got, want)
	}
	// yaml.v3 decodes the bare numeric 512 as int; the test pins the
	// numeric type as well as the value so a regression that quoted
	// the built-in would also surface.
	if got, want := extraConfig["maxPods"], 512; got != want {
		t.Errorf("maxPods: got %v (%T), want %v (int)", got, got, want)
	}
	if got, want := extraConfig["feature-gates"], "NodeSwap=true"; got != want {
		t.Errorf("feature-gates: got %v, want %v", got, want)
	}
}

// Contract: an operator key in extraKubeletExtraArgs that collides
// with a built-in extraConfig key (cpuManagerPolicy, maxPods) MUST
// fail at render time with a precise hint. yaml.v3 rejects duplicate
// map keys on decode, so a silent merge would produce a Talos config
// that cannot round-trip. Fork-the-preset is the documented escape
// hatch; this test pins the rejection so the fork path is the only
// way an operator can change a built-in default.
func TestContract_Machine_ExtraKubeletExtraArgs_Cozystack_RejectsCollisionWithBuiltin(t *testing.T) {
	cases := []string{"cpuManagerPolicy", "maxPods"}
	for _, key := range cases {
		t.Run(key, func(t *testing.T) {
			err := renderCozystackExpectError(t, helmEngineEmptyLookup, map[string]any{
				"advertisedSubnets": []any{testAdvertisedSubnet},
				"extraKubeletExtraArgs": map[string]any{
					key: "operator-value",
				},
			})
			if err == nil {
				t.Fatalf("expected render error for collision on key %q, got nil", key)
			}
			msg := err.Error()
			if !strings.Contains(msg, key) {
				t.Errorf("error message must name the offending key %q; got: %v", key, err)
			}
			if !strings.Contains(msg, "extraKubeletExtraArgs") {
				t.Errorf("error message must name the offending values key extraKubeletExtraArgs; got: %v", err)
			}
		})
	}
}

// Contract: values.extraSysctls entries merge into machine.sysctls
// alongside the cozystack preset's gc_thresh* + vm.nr_hugepages
// defaults. Round-trip-decoded through yaml.v3 so a regression that
// emits a duplicate key would fail decode here.
func TestContract_Machine_ExtraSysctls_Cozystack_MergeIntoSysctls(t *testing.T) {
	out := renderCozystackWith(t, helmEngineEmptyLookup, map[string]any{
		"advertisedSubnets": []any{testAdvertisedSubnet},
		"extraSysctls": map[string]any{
			"net.core.somaxconn": "65535",
		},
	})
	sysctls := decodeMachineSysctls(t, out)
	// Talos requires sysctl values be strings; the chart's hardcoded
	// gc_thresh* entries are explicitly quoted to match. Pin both the
	// value and the string type so a regression that emits unquoted
	// integers would surface.
	for _, k := range []string{
		"net.ipv4.neigh.default.gc_thresh1",
		"net.ipv4.neigh.default.gc_thresh2",
		"net.ipv4.neigh.default.gc_thresh3",
	} {
		v, ok := sysctls[k].(string)
		if !ok {
			t.Errorf("sysctl %q: expected string, got %T (value %v)", k, sysctls[k], sysctls[k])
		}
		if v == "" {
			t.Errorf("sysctl %q: expected non-empty string value, got empty", k)
		}
	}
	if got, want := sysctls["net.core.somaxconn"], "65535"; got != want {
		t.Errorf("operator sysctl: got %v, want %v", got, want)
	}
}

// Contract: an operator key in extraSysctls that collides with the
// preset's built-in machine.sysctls keys MUST fail render. Same
// rationale as extraKubeletExtraArgs.
func TestContract_Machine_ExtraSysctls_Cozystack_RejectsCollisionWithBuiltin(t *testing.T) {
	cases := []string{
		"vm.nr_hugepages",
		"net.ipv4.neigh.default.gc_thresh1",
		"net.ipv4.neigh.default.gc_thresh2",
		"net.ipv4.neigh.default.gc_thresh3",
	}
	for _, key := range cases {
		t.Run(key, func(t *testing.T) {
			err := renderCozystackExpectError(t, helmEngineEmptyLookup, map[string]any{
				"advertisedSubnets": []any{testAdvertisedSubnet},
				"extraSysctls": map[string]any{
					key: "operator-value",
				},
			})
			if err == nil {
				t.Fatalf("expected render error for collision on key %q, got nil", key)
			}
			msg := err.Error()
			if !strings.Contains(msg, key) {
				t.Errorf("error message must name the offending key %q; got: %v", key, err)
			}
			if !strings.Contains(msg, "extraSysctls") {
				t.Errorf("error message must name the offending values key extraSysctls; got: %v", err)
			}
		})
	}
}

// decodeKubeletExtraConfig parses every YAML document in the rendered
// output and returns the machine.kubelet.extraConfig map from
// whichever document carries it. Uses yaml.v3 directly (the decoder
// Talos uses internally) so a duplicate map key in the rendered
// output fails decoding here — the test surfaces the defect instead
// of asserting on substring presence over invalid YAML.
func decodeKubeletExtraConfig(t *testing.T, out string) map[string]any {
	t.Helper()
	return decodeMachineSubMap(t, out, "kubelet", "extraConfig")
}

func decodeMachineSysctls(t *testing.T, out string) map[string]any {
	t.Helper()
	return decodeMachineSubMap(t, out, "sysctls")
}

func decodeMachineSubMap(t *testing.T, out string, path ...string) map[string]any {
	t.Helper()

	dec := yaml.NewDecoder(strings.NewReader(out))
	for {
		var doc map[string]any
		err := dec.Decode(&doc)
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			t.Fatalf("decoding rendered YAML: %v", err)
		}
		machine, ok := doc["machine"].(map[string]any)
		if !ok {
			continue
		}
		cur := any(machine)
		for _, p := range path {
			m, ok := cur.(map[string]any)
			if !ok {
				cur = nil
				break
			}
			cur = m[p]
		}
		if m, ok := cur.(map[string]any); ok {
			return m
		}
	}
	t.Fatalf("no document carried machine.%s in rendered output:\n%s", strings.Join(path, "."), out)
	return nil
}

// Contract: values.extraMachineFiles entries append to machine.files
// alongside the cozystack preset's CRI-customization and lvm.conf
// entries. The built-in two must stay intact.
func TestContract_Machine_ExtraMachineFiles_Cozystack_AppendsToFiles(t *testing.T) {
	out := renderCozystackWith(t, helmEngineEmptyLookup, map[string]any{
		"advertisedSubnets": []any{testAdvertisedSubnet},
		"extraMachineFiles": []any{
			map[string]any{
				"path":    "/etc/example/operator.conf",
				"op":      "create",
				"content": "hello=world\n",
			},
		},
	})
	// Built-in files preserved.
	assertContains(t, out, "path: /etc/cri/conf.d/20-customization.part")
	assertContains(t, out, "path: /etc/lvm/lvm.conf")
	// Operator-supplied entry appended.
	assertContains(t, out, "path: /etc/example/operator.conf")
	assertContains(t, out, "hello=world")
}

// === Generic extension points: emit-only-when-set ===
//
// The generic preset ships no defaults for any of these blocks. Each
// rendered block appears ONLY when the matching values key is
// non-empty. Pinning the on-state here so a regression that always
// emits an empty `modules: []` (etc.) would fail; the off-state stays
// pinned by TestContract_Machine_NoCozystackOpinionsOnGeneric.

// Contract: generic preset emits machine.kernel.modules ONLY when
// values.extraKernelModules is non-empty.
func TestContract_Machine_ExtraKernelModules_Generic_NonEmptyEmitsBlock(t *testing.T) {
	out := renderGenericWith(t, helmEngineEmptyLookup, map[string]any{
		"advertisedSubnets": []any{testAdvertisedSubnet},
		"extraKernelModules": []any{
			map[string]any{"name": "nf_conntrack"},
		},
	})
	assertContains(t, out, "kernel:")
	assertContains(t, out, "modules:")
	assertContains(t, out, "- name: nf_conntrack")
}

// Contract: generic preset emits kubelet.extraConfig ONLY when
// values.extraKubeletExtraArgs is non-empty (default is `{}`, so the
// preset renders kubelet with only nodeIP.validSubnets).
func TestContract_Machine_ExtraKubeletExtraArgs_Generic_NonEmptyEmitsBlock(t *testing.T) {
	out := renderGenericWith(t, helmEngineEmptyLookup, map[string]any{
		"advertisedSubnets": []any{testAdvertisedSubnet},
		"extraKubeletExtraArgs": map[string]any{
			"feature-gates": "NodeSwap=true",
		},
	})
	assertContains(t, out, "extraConfig:")
	assertContains(t, out, "feature-gates: NodeSwap=true")
}

// Contract: generic preset emits machine.sysctls ONLY when
// values.extraSysctls is non-empty.
func TestContract_Machine_ExtraSysctls_Generic_NonEmptyEmitsBlock(t *testing.T) {
	out := renderGenericWith(t, helmEngineEmptyLookup, map[string]any{
		"advertisedSubnets": []any{testAdvertisedSubnet},
		"extraSysctls": map[string]any{
			"net.core.somaxconn": "65535",
		},
	})
	assertContains(t, out, "sysctls:")
	assertContains(t, out, "net.core.somaxconn:")
	assertContains(t, out, `"65535"`)
}

// Contract: generic preset emits machine.files ONLY when
// values.extraMachineFiles is non-empty.
func TestContract_Machine_ExtraMachineFiles_Generic_NonEmptyEmitsBlock(t *testing.T) {
	out := renderGenericWith(t, helmEngineEmptyLookup, map[string]any{
		"advertisedSubnets": []any{testAdvertisedSubnet},
		"extraMachineFiles": []any{
			map[string]any{
				"path":    "/etc/example/operator.conf",
				"op":      "create",
				"content": "hello=world\n",
			},
		},
	})
	assertContains(t, out, "files:")
	assertContains(t, out, "path: /etc/example/operator.conf")
}

// Contract: cozystack always prepends 127.0.0.1 to machine.certSANs
// (separate from the controlplane-only cluster.apiServer.certSANs
// pinned in contract_cluster_test.go). machine-level certSANs control
// the talosd API certificate; without 127.0.0.1, local talosctl
// against the node fails TLS validation on loopback. Both controlplane
// and worker templates emit it.
func TestContract_Machine_CertSANs_LoopbackUnconditional_Cozystack(t *testing.T) {
	for _, cell := range cozystackCells() {
		t.Run(cell.name, func(t *testing.T) {
			out := renderChartTemplate(t, cell.chartPath, cell.templateFile, cell.talosVersion)
			// machine.certSANs is at indent 2 ("  certSANs:"). The cluster.apiServer
			// variant is at indent 4 — the same substring matches both,
			// but cozystack workers have no cluster.apiServer at all, so
			// passing on workers proves machine.certSANs is present.
			assertContains(t, out, "certSANs:")
			assertContains(t, out, "- 127.0.0.1")
		})
	}
}

// Contract: cozystack emits two `machine.files[]` entries:
//  1. /etc/cri/conf.d/20-customization.part — sets
//     device_ownership_from_security_context = true on both legacy
//     (io.containerd.grpc.v1.cri) and v2 (io.containerd.cri.v1.runtime)
//     plugin paths. Required for SR-IOV / GPU device plugins to
//     surface inside privileged containers.
//  2. /etc/lvm/lvm.conf — disables LVM backup/archive and sets a
//     global_filter that excludes drbd, dm-, zd- devices. Required so
//     LVM does not race the storage stack at boot.
//
// Both files use op: create / op: overwrite respectively. A regression
// removing either silently breaks GPU/SRIOV or LVM ordering.
func TestContract_Machine_Files_Cozystack(t *testing.T) {
	for _, cell := range cozystackCells() {
		t.Run(cell.name, func(t *testing.T) {
			out := renderChartTemplate(t, cell.chartPath, cell.templateFile, cell.talosVersion)
			assertContains(t, out, "files:")
			assertContains(t, out, "path: /etc/cri/conf.d/20-customization.part")
			assertContains(t, out, "op: create")
			assertContains(t, out, "device_ownership_from_security_context = true")
			assertContains(t, out, `[plugins."io.containerd.grpc.v1.cri"]`)
			assertContains(t, out, `[plugins."io.containerd.cri.v1.runtime"]`)
			assertContains(t, out, "path: /etc/lvm/lvm.conf")
			assertContains(t, out, "op: overwrite")
			assertContains(t, out, "permissions: 0o644")
			assertContains(t, out, `r|^/dev/drbd.*|`)
			assertContains(t, out, `r|^/dev/dm-.*|`)
			assertContains(t, out, `r|^/dev/zd.*|`)
		})
	}
}

// Contract: cozystack ships a default install.image pointing at the
// cozystack-built Talos image. Operators can override via values.image
// (or the `talm init --image` flag, see init.go). The default tag is
// versioned (`v1.12.6` at the time of writing) — the test pins only
// the registry/repo prefix so a routine version bump does not require
// updating this test.
func TestContract_Machine_Install_Image_DefaultsToCozystackBuild(t *testing.T) {
	for _, cell := range cozystackCells() {
		t.Run(cell.name, func(t *testing.T) {
			out := renderChartTemplate(t, cell.chartPath, cell.templateFile, cell.talosVersion)
			assertContains(t, out, "image: ghcr.io/cozystack/cozystack/talos:")
		})
	}
}

// Contract: when an operator sets values.image, cozystack emits the
// override verbatim (no leading whitespace munging, no quoting).
// Pins the substitution path so a future regression that adds quoting
// or trimming would surface here.
func TestContract_Machine_Install_Image_Override_Cozystack(t *testing.T) {
	const customImage = "registry.example.com/talos:custom-build"
	out := renderCozystackWith(t, helmEngineEmptyLookup, map[string]any{
		"image":             customImage,
		"advertisedSubnets": []any{testAdvertisedSubnet},
	})
	assertContains(t, out, "image: "+customImage)
}

// === generic-only contracts: pin minimalism ===

// Contract: generic chart does NOT emit cozystack-specific machine
// fields. extraConfig (cpuManagerPolicy, maxPods), sysctls, kernel
// modules, machine.certSANs (no unconditional 127.0.0.1), files,
// install.image — none of these appear when generic is rendered with
// its default values.yaml. Pinning the absence prevents accidental
// "I copied from cozystack" drift.
func TestContract_Machine_NoCozystackOpinionsOnGeneric(t *testing.T) {
	for _, cell := range genericCells() {
		t.Run(cell.name, func(t *testing.T) {
			out := renderChartTemplate(t, cell.chartPath, cell.templateFile, cell.talosVersion)
			// kubelet block is present but lacks extraConfig.
			assertNotContains(t, out, "extraConfig:")
			assertNotContains(t, out, "cpuManagerPolicy")
			assertNotContains(t, out, "maxPods")
			// No sysctls block.
			assertNotContains(t, out, "sysctls:")
			assertNotContains(t, out, "gc_thresh")
			// No kernel modules block.
			assertNotContains(t, out, "kernel:")
			assertNotContains(t, out, "openvswitch")
			assertNotContains(t, out, "drbd")
			// No machine-level files block.
			assertNotContains(t, out, "containerd")
			assertNotContains(t, out, "lvm.conf")
			// No install.image (generic ships no default image).
			assertNotContains(t, out, "image:")
		})
	}
}

// Contract: generic chart's machine.certSANs section appears only
// when an operator supplies values.certSANs. With the default empty
// list, the section is fully omitted (no `certSANs:` key on
// machine-level at all). This is a deliberate deviation from cozystack
// — the test pins the omission.
func TestContract_Machine_GenericCertSANs_AbsentByDefault(t *testing.T) {
	for _, cell := range genericCells() {
		t.Run(cell.name, func(t *testing.T) {
			out := renderChartTemplate(t, cell.chartPath, cell.templateFile, cell.talosVersion)
			// On worker generic emits no apiServer either, so the entire
			// "certSANs:" substring should be absent.
			if cell.templateFile == workerTpl {
				assertNotContains(t, out, "certSANs:")
			}
			// On controlplane the cluster.apiServer block uses `with` so
			// certSANs is also absent. The default-render output should
			// not contain the unconditional loopback either.
			assertNotContains(t, out, "- 127.0.0.1")
		})
	}
}

// Contract: when generic operator supplies machine-level certSANs via
// values.certSANs, the chart emits them on BOTH machine.certSANs and
// cluster.apiServer.certSANs (controlplane only) with no extra entries.
func TestContract_Machine_GenericCertSANs_AppendsBothLevels(t *testing.T) {
	out := renderGenericWith(t, helmEngineEmptyLookup, map[string]any{
		"certSANs":          []any{"san.example.com"},
		"advertisedSubnets": []any{testAdvertisedSubnet},
	})
	assertContains(t, out, "- san.example.com")
	assertNotContains(t, out, "- 127.0.0.1")
}

// Contract: cozystack chart includes a registries.mirrors block for
// docker.io pointing at https://mirror.gcr.io. This is emitted only
// in the legacy schema (multi-doc Talos uses RegistryMirrorConfig as
// a separate document, pinned in contract_network_test.go).
func TestContract_Machine_Registries_DockerMirror_LegacyCozystack(t *testing.T) {
	cases := []chartCell{
		{"cozystack/legacy/controlplane", cozystackChartPath, controlplaneTpl, ""},
		{"cozystack/legacy/worker", cozystackChartPath, workerTpl, ""},
	}
	for _, cell := range cases {
		t.Run(cell.name, func(t *testing.T) {
			out := renderChartTemplate(t, cell.chartPath, cell.templateFile, cell.talosVersion)
			assertContains(t, out, "registries:")
			assertContains(t, out, "mirrors:")
			assertContains(t, out, "docker.io:")
			assertContains(t, out, "- https://mirror.gcr.io")
		})
	}
}

// Contract: generic chart emits no registries block at all (no Docker
// mirror hardcoded). Operators must declare their own registry
// configuration via per-node body overlays.
func TestContract_Machine_NoRegistriesOnGeneric(t *testing.T) {
	for _, cell := range genericCells() {
		t.Run(cell.name, func(t *testing.T) {
			out := renderChartTemplate(t, cell.chartPath, cell.templateFile, cell.talosVersion)
			assertNotContains(t, out, "registries:")
			assertNotContains(t, out, "mirror.gcr.io")
		})
	}
}
