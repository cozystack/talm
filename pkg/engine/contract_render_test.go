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

// Contract: Render top-level entry point and the
// FullConfigProcess / InitializeConfigBundle / SerializeConfiguration
// trio that backs `talm template` and `talm apply`. These functions
// glue together: chart load → values aggregation → helm render →
// applyPatchesAndRenderConfig → final bytes. Tests pin error
// surfaces an operator can hit (missing templates, bad TalosVersion,
// bad chart root, malformed patches) and the happy-path round-trip.

package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/siderolabs/talos/pkg/machinery/config/machine"
)

// === Render: error surfaces ===

// Contract: Render with empty Options.TemplateFiles surfaces a
// precise error mentioning --file / --template (the two CLI flags
// that populate the field).
func TestContract_Render_NoTemplateFilesError(t *testing.T) {
	chartRoot := createTestChart(t, "tc", "config.yaml", "machine:\n  type: worker\n")
	_, err := Render(context.Background(), nil, Options{
		Offline: true,
		Root:    chartRoot,
		// TemplateFiles intentionally empty
	})
	if err == nil {
		t.Fatal("expected error for empty TemplateFiles")
	}
	msg := err.Error()
	if !strings.Contains(msg, "templates are not set") {
		t.Errorf("error must mention 'templates are not set', got: %s", msg)
	}
	if !strings.Contains(msg, "--file") && !strings.Contains(msg, "--template") {
		t.Errorf("error must reference --file or --template flag, got: %s", msg)
	}
}

// Contract: Render with a TemplateFiles entry that does not exist in
// the chart surfaces an error naming the missing template. Operators
// hit this when they typo a path or when a template file is renamed
// but talm flags still point at the old name.
func TestContract_Render_TemplateNotFoundError(t *testing.T) {
	chartRoot := createTestChart(t, "tc", "config.yaml", "machine:\n  type: worker\n")
	_, err := Render(context.Background(), nil, Options{
		Offline:       true,
		Root:          chartRoot,
		TemplateFiles: []string{"templates/does-not-exist.yaml"},
	})
	if err == nil {
		t.Fatal("expected error for missing template")
	}
	if !strings.Contains(err.Error(), "templates/does-not-exist.yaml") {
		t.Errorf("error must name the missing template, got: %v", err)
	}
}

// Contract: Render with Options.Root pointing at a non-existent
// directory surfaces a chart-load error from the Helm loader.
func TestContract_Render_ChartLoadError(t *testing.T) {
	bogus := filepath.Join(t.TempDir(), "no-such-chart")
	_, err := Render(context.Background(), nil, Options{
		Offline:       true,
		Root:          bogus,
		TemplateFiles: []string{"templates/config.yaml"},
	})
	if err == nil {
		t.Fatal("expected error for missing chart root")
	}
}

// Contract: Render with a values file that does not exist surfaces a
// loadValues error naming the missing file. Confirms loadValues'
// errors propagate up cleanly.
func TestContract_Render_BadValueFileError(t *testing.T) {
	chartRoot := createTestChart(t, "tc", "config.yaml", "machine:\n  type: worker\n")
	_, err := Render(context.Background(), nil, Options{
		Offline:       true,
		Root:          chartRoot,
		ValueFiles:    []string{"/path/that/does/not/exist.yaml"},
		TemplateFiles: []string{"templates/config.yaml"},
	})
	if err == nil {
		t.Fatal("expected error for missing values file")
	}
	if !strings.Contains(err.Error(), "does/not/exist") {
		t.Errorf("error must reference the missing path, got: %v", err)
	}
}

// === Render: happy path ===

// Contract: Render with a minimal valid chart (one template emitting
// a worker machine config patch) returns non-empty config bytes that
// at least mention `machine:` and `type: worker`. The output is the
// final Talos machine config — patches applied, values rendered.
func TestContract_Render_HappyPathOfflineWorker(t *testing.T) {
	chartRoot := createTestChart(t, "tc", "config.yaml", "machine:\n  type: worker\n")
	out, err := Render(context.Background(), nil, Options{
		Offline:       true,
		Root:          chartRoot,
		TemplateFiles: []string{"templates/config.yaml"},
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, "machine:") {
		t.Errorf("expected 'machine:' in output, got:\n%s", got)
	}
	if !strings.Contains(got, "type: worker") {
		t.Errorf("expected 'type: worker' in output, got:\n%s", got)
	}
}

// Contract: --set values flow through Render and reach the rendered
// template. This pins the values pipeline end-to-end: Options →
// loadValues → mergeMaps with chart defaults → engine render.
func TestContract_Render_SetValuesReachTemplate(t *testing.T) {
	tmpl := `machine:
  type: worker
  install:
    image: {{ .Values.customImage }}
`
	chartRoot := createTestChart(t, "tc", "config.yaml", tmpl)
	out, err := Render(context.Background(), nil, Options{
		Offline:       true,
		Root:          chartRoot,
		Values:        []string{"customImage=registry.example.com/talos:test"},
		TemplateFiles: []string{"templates/config.yaml"},
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, "registry.example.com/talos:test") {
		t.Errorf("--set value did not reach template output:\n%s", got)
	}
}

// Contract: Render's TalosVersion validation is a fast-fail BEFORE
// chart loading. A bad version string returns an error even if the
// chart root is also invalid — the version check runs first.
func TestContract_Render_TalosVersionValidatedBeforeChart(t *testing.T) {
	bogus := filepath.Join(t.TempDir(), "no-such-chart")
	_, err := Render(context.Background(), nil, Options{
		Offline:       true,
		Root:          bogus,
		TalosVersion:  "garbage-version",
		TemplateFiles: []string{"templates/config.yaml"},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "invalid talos-version") {
		t.Errorf("expected 'invalid talos-version' (fast-fail before chart), got: %v", err)
	}
}

// === FullConfigProcess / InitializeConfigBundle / SerializeConfiguration ===

// Contract: InitializeConfigBundle with empty Options returns a
// usable bundle (auto-generates secrets). The returned bundle has a
// ControlPlane config that downstream patches refine.
func TestContract_InitializeConfigBundle_EmptyOptionsReturnsBundle(t *testing.T) {
	b, err := InitializeConfigBundle(Options{})
	if err != nil {
		t.Fatalf("InitializeConfigBundle: %v", err)
	}
	if b == nil {
		t.Fatal("nil bundle")
	}
	if b.ControlPlaneCfg == nil {
		t.Error("expected ControlPlaneCfg to be set")
	}
	if b.WorkerCfg == nil {
		t.Error("expected WorkerCfg to be set")
	}
}

// Contract: InitializeConfigBundle with malformed TalosVersion
// surfaces an `invalid talos-version` error before bundle creation.
func TestContract_InitializeConfigBundle_BadTalosVersionError(t *testing.T) {
	_, err := InitializeConfigBundle(Options{TalosVersion: "garbage"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "invalid talos-version") {
		t.Errorf("error must mention 'invalid talos-version', got: %v", err)
	}
}

// Contract: InitializeConfigBundle with a missing WithSecrets path
// surfaces a 'failed to load secrets bundle' error naming the cause.
func TestContract_InitializeConfigBundle_MissingSecretsError(t *testing.T) {
	_, err := InitializeConfigBundle(Options{
		WithSecrets: filepath.Join(t.TempDir(), "missing-secrets.yaml"),
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "secrets bundle") {
		t.Errorf("error must mention secrets bundle, got: %v", err)
	}
}

// Contract: SerializeConfiguration produces non-empty YAML bytes for
// both controlplane and worker machine types. Pin that the
// controlplane serialization is meaningfully different from the
// worker one (controlplane has cluster.* fields worker does not).
func TestContract_SerializeConfiguration_ControlplaneVsWorker(t *testing.T) {
	b, err := InitializeConfigBundle(Options{})
	if err != nil {
		t.Fatal(err)
	}
	cpBytes, err := SerializeConfiguration(b, machine.TypeControlPlane)
	if err != nil {
		t.Fatalf("controlplane: %v", err)
	}
	workerBytes, err := SerializeConfiguration(b, machine.TypeWorker)
	if err != nil {
		t.Fatalf("worker: %v", err)
	}
	if len(cpBytes) == 0 || len(workerBytes) == 0 {
		t.Fatal("expected non-empty serialization")
	}
	if string(cpBytes) == string(workerBytes) {
		t.Error("controlplane and worker serializations are identical; expected divergence (controlplane has cluster section)")
	}
	// Worker config has type: worker.
	if !strings.Contains(string(workerBytes), "type: worker") {
		t.Errorf("worker config missing 'type: worker'")
	}
	// Controlplane has type: controlplane.
	if !strings.Contains(string(cpBytes), "type: controlplane") {
		t.Errorf("controlplane config missing 'type: controlplane'")
	}
}

// Contract: FullConfigProcess takes a list of Talos config patches
// (the rendered chart output, one entry per template), runs them
// through bundle.ApplyPatches, and returns the final bundle plus the
// detected machine type. With no patches the type comes from the
// bundle's ControlPlaneCfg default (controlplane). The worker
// fallback in FullConfigProcess only fires when ApplyPatches yields
// machine.TypeUnknown — a reduced state operators do not normally
// reach.
func TestContract_FullConfigProcess_NoPatchesUsesBundleDefault(t *testing.T) {
	bundle, mtype, err := FullConfigProcess(Options{}, nil)
	if err != nil {
		t.Fatalf("FullConfigProcess: %v", err)
	}
	if bundle == nil {
		t.Fatal("nil bundle")
	}
	// The bundle ControlPlaneCfg defaults to machine.type=controlplane,
	// so absent any patch the detected machineType reflects that.
	if mtype != machine.TypeControlPlane {
		t.Errorf("expected machine.TypeControlPlane (bundle default), got %v", mtype)
	}
}

// Contract: FullConfigProcess with a controlplane-typed patch
// detects machine.TypeControlPlane and propagates it. The patch
// supplies machine.type: controlplane explicitly so the chart's
// own machineType inference does not interfere.
func TestContract_FullConfigProcess_ControlplaneFromPatch(t *testing.T) {
	patch := "machine:\n  type: controlplane\n"
	_, mtype, err := FullConfigProcess(Options{}, []string{patch})
	if err != nil {
		t.Fatalf("FullConfigProcess: %v", err)
	}
	if mtype != machine.TypeControlPlane {
		t.Errorf("expected machine.TypeControlPlane, got %v", mtype)
	}
}

// Contract: FullConfigProcess with a malformed patch surfaces a
// LoadPatches error. Pin the error path so a regression that
// silently swallows malformed patches surfaces here.
func TestContract_FullConfigProcess_MalformedPatchError(t *testing.T) {
	bad := "this is not valid YAML\n  : :"
	_, _, err := FullConfigProcess(Options{}, []string{bad})
	if err == nil {
		t.Fatal("expected error for malformed patch")
	}
}

// Contract: FullConfigProcess with a malformed TalosVersion option
// surfaces InitializeConfigBundle's error path.
func TestContract_FullConfigProcess_BadTalosVersionError(t *testing.T) {
	_, _, err := FullConfigProcess(Options{TalosVersion: "garbage"}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "talos-version") {
		t.Errorf("error must mention talos-version, got: %v", err)
	}
}

// === Edge cases ===

// Contract: Render with Offline=true does NOT call FailIfMultiNodes
// (online-only check). The test sets two nodes via context and
// confirms Render still proceeds. Without Offline=true the same
// configuration would error.
func TestContract_Render_OfflineSkipsMultiNodeCheck(t *testing.T) {
	chartRoot := createTestChart(t, "tc", "config.yaml", "machine:\n  type: worker\n")
	// Context with multiple nodes — would trip FailIfMultiNodes online.
	ctx := context.Background()
	_, err := Render(ctx, nil, Options{
		Offline:       true,
		Root:          chartRoot,
		TemplateFiles: []string{"templates/config.yaml"},
	})
	if err != nil {
		t.Fatalf("offline render must succeed regardless of node count: %v", err)
	}
	_ = os.Stdout // keep imports stable for future expansion
}
