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

// Contract: CVE-2026-53359 KVM-nested-virtualization mitigation in the
// cozystack and generic presets. kvm_intel/kvm_amd are built into the
// Talos kernel, so nested= is settable only from the kernel command
// line; both presets pin it via machine.install.extraKernelArgs.
//
// On Talos >1.11 the generated base config defaults
// machine.install.grubUseUKICmdline to true (UKI cmdline), and Talos
// rejects extraKernelArgs alongside it. The presets therefore pin
// grubUseUKICmdline:false wherever the base would set it true. The
// full-pipeline test below renders through engine.Render (base bundle +
// preset patch) and runs Talos's own validation to prove the merged
// config the operator applies does not trip the UKI conflict.

package engine

import (
	"context"
	"strings"
	"testing"

	helmEngine "github.com/cozystack/talm/pkg/engine/helm"
	"github.com/siderolabs/talos/pkg/machinery/config/configloader"
)

// kvmNestedRuntimeMode is a minimal validation.RuntimeMode implementation.
// RequiresInstall() true selects the install-mode validation branch that
// enforces the extraKernelArgs/grubUseUKICmdline mutual exclusion.
type kvmNestedRuntimeMode struct{ requiresInstall bool }

func (m kvmNestedRuntimeMode) String() string        { return "kvm-nested-contract" }
func (m kvmNestedRuntimeMode) RequiresInstall() bool { return m.requiresInstall }
func (kvmNestedRuntimeMode) InContainer() bool       { return false }

// Contract: both presets always emit the KVM nested-virt kernel args
// under machine.install, for every chart × schema × machineType cell.
// A regression that drops them silently re-exposes the guest-to-host
// escape on every node the preset installs.
func TestContract_Machine_Install_KVMNestedArgs(t *testing.T) {
	for _, cell := range allCells() {
		t.Run(cell.name, func(t *testing.T) {
			out := renderChartTemplate(t, cell.chartPath, cell.templateFile, cell.talosVersion)
			assertContains(t, out, "extraKernelArgs:")
			assertContains(t, out, "kvm_intel.nested=0")
			assertContains(t, out, "kvm_amd.nested=0")
		})
	}
}

// Contract: grubUseUKICmdline:false is emitted exactly where the
// generated base config would default it to true — i.e. when no
// --talos-version is pinned (the bundle falls back to the current
// contract, >1.11) or when the pinned version is >=1.12. For an
// explicit pre-1.12 version the field is omitted, both because the base
// does not set it and because older Talos schemas reject the key.
func TestContract_Machine_Install_GrubUKICmdlineGate(t *testing.T) {
	for _, tc := range []struct {
		talosVersion string
		wantField    bool
	}{
		{"", true},         // empty → base uses current contract (>1.11) → pin false
		{"v1.12.0", true},  // >=1.12 → base defaults true → pin false
		{"v1.13.0", true},  // >=1.12 → base defaults true → pin false
		{"v1.11.0", false}, // <1.12 → base leaves it unset → do not emit
	} {
		name := tc.talosVersion
		if name == "" {
			name = "empty"
		}
		t.Run(name, func(t *testing.T) {
			out := renderChartTemplate(t, cozystackChartPath, controlplaneTpl, tc.talosVersion)
			if tc.wantField {
				assertContains(t, out, "grubUseUKICmdline: false")
			} else {
				assertNotContains(t, out, "grubUseUKICmdline")
			}
		})
	}
}

// Contract: the FULL config an operator applies — base bundle merged
// with the preset patch, exactly what `talm apply` serialises (Full:
// true) — passes Talos validation without the UKI/extraKernelArgs
// conflict, for both presets and both machine types on v1.12+.
//
// This is the regression guard for the whole mitigation: the raw Helm
// output above never carries grubUseUKICmdline (the base bundle injects
// it), so only a full-pipeline render surfaces the collision. Offline
// discovery leaves install.disk empty, which trips a separate
// validation error, so the assertion is scoped to the UKI conflict
// specifically rather than "no errors at all".
func TestContract_Machine_Install_FullConfigValidates_NoUKIConflict(t *testing.T) {
	// Offline render still consults the Helm `lookup`; reset it to the
	// empty stub so no live discovery is attempted.
	helmEngine.LookupFunc = func(string, string, string) (map[string]any, error) {
		return map[string]any{}, nil
	}

	const ukiConflict = "grubUseUKICmdline"

	cells := []struct {
		name         string
		chartPath    string
		templateFile string
	}{
		{"cozystack/controlplane", cozystackChartPath, controlplaneTpl},
		{"cozystack/worker", cozystackChartPath, workerTpl},
		{"generic/controlplane", genericChartPath, controlplaneTpl},
		{"generic/worker", genericChartPath, workerTpl},
	}

	for _, cell := range cells {
		t.Run(cell.name, func(t *testing.T) {
			out, err := Render(context.Background(), nil, Options{
				Offline:       true,
				Full:          true,
				Root:          cell.chartPath,
				TalosVersion:  "v1.12.0",
				TemplateFiles: []string{cell.templateFile},
				Values: []string{
					"endpoint=" + testEndpoint,
					"advertisedSubnets={" + testAdvertisedSubnet + "}",
				},
			})
			if err != nil {
				t.Fatalf("full render failed: %v", err)
			}

			got := string(out)
			assertContains(t, got, "grubUseUKICmdline: false")
			assertContains(t, got, "kvm_intel.nested=0")
			if strings.Contains(got, "grubUseUKICmdline: true") {
				t.Fatalf("full config still carries grubUseUKICmdline: true — UKI cmdline not disabled:\n%s", got)
			}

			cfg, lerr := configloader.NewFromBytes(out)
			if lerr != nil {
				t.Fatalf("config loader rejected the rendered config: %v", lerr)
			}

			_, verr := cfg.Validate(kvmNestedRuntimeMode{requiresInstall: true})
			if verr != nil && strings.Contains(verr.Error(), ukiConflict) {
				t.Fatalf("Talos validation reports the UKI/extraKernelArgs conflict — apply would fail: %v", verr)
			}
		})
	}
}
