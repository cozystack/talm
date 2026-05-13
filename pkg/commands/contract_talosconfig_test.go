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

package commands

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/siderolabs/talos/pkg/machinery/client/config"
)

// stageTalosconfigFixture creates a project root sufficient for
// regenerateTalosconfig: Chart.yaml carrying a cluster name and
// secrets.yaml carrying a real (shared) talos secrets bundle. The
// resulting tempdir is returned; Config.RootDir is pointed at it
// for the duration of the test via t.Cleanup.
//
// The shared secrets bundle is generated once per test process by
// loadSharedSecretsYAML (defined in contract_template_test.go) —
// running secrets.NewBundle inside every test would dominate the
// suite runtime (~half a second per call). Tests that mutate the
// bundle are expected to make their own copy.
func stageTalosconfigFixture(t *testing.T, clusterName string) string {
	t.Helper()

	dir := t.TempDir()

	if err := os.WriteFile(
		filepath.Join(dir, "Chart.yaml"),
		[]byte("apiVersion: v2\nname: "+clusterName+"\nversion: 0.1.0\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(
		filepath.Join(dir, "secrets.yaml"),
		loadSharedSecretsYAML(t),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	prevRoot := Config.RootDir
	prevExplicit := Config.RootDirExplicit
	Config.RootDir = dir
	Config.RootDirExplicit = true

	t.Cleanup(func() {
		Config.RootDir = prevRoot
		Config.RootDirExplicit = prevExplicit
	})

	return dir
}

// withGlobalEndpoints saves GlobalArgs.Endpoints, sets it to the
// supplied list for the duration of the test, and restores the
// original via t.Cleanup. The talosconfig regenerate flow reads
// GlobalArgs.Endpoints at call time, so cross-test pollution is
// real if not isolated.
func withGlobalEndpoints(t *testing.T, endpoints []string) {
	t.Helper()

	prev := append([]string(nil), GlobalArgs.Endpoints...)
	GlobalArgs.Endpoints = endpoints

	t.Cleanup(func() {
		GlobalArgs.Endpoints = prev
	})
}

// readTalosconfigContextEndpoints opens the project's talosconfig
// and returns the endpoints from the context that the helper
// chose (clusterName if present, otherwise the file's
// Context field).
func readTalosconfigContextEndpoints(t *testing.T, dir, clusterName string) []string {
	t.Helper()

	cfg, err := config.Open(filepath.Join(dir, talosconfigName))
	if err != nil {
		t.Fatalf("open talosconfig: %v", err)
	}

	ctxName := clusterName
	if _, ok := cfg.Contexts[ctxName]; !ok {
		ctxName = cfg.Context
	}

	ctx, ok := cfg.Contexts[ctxName]
	if !ok {
		t.Fatalf("talosconfig has no context %q (cluster=%q); contexts=%v", ctxName, clusterName, mapKeys(cfg.Contexts))
	}

	return append([]string(nil), ctx.Endpoints...)
}

func mapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	return keys
}

// TestContract_RegenerateTalosconfig_NoOldConfig_HonorsEndpointsFlag
// pins the call-site fix at pkg/commands/talosconfig.go: when no
// existing talosconfig is present, the regenerate flow seeds the
// new context's endpoints from --endpoints (via GlobalArgs.Endpoints).
// Previously the assignment site hardcoded defaultLocalEndpoint and
// silently discarded the operator's flag.
//
// This test pins the actual call site — the helper's behaviour is
// already pinned by TestResolveTalosconfigEndpoints_* in
// init_test.go, but those tests would still pass if someone reverted
// the call site to []string{defaultLocalEndpoint}. The contract is
// "the wire result honors --endpoints", and that lives at the file
// produced by regenerateTalosconfig.
func TestContract_RegenerateTalosconfig_NoOldConfig_HonorsEndpointsFlag(t *testing.T) {
	clusterName := "test-cluster"

	dir := stageTalosconfigFixture(t, clusterName)
	withGlobalEndpoints(t, []string{"10.0.80.201"})

	if err := regenerateTalosconfig(); err != nil {
		t.Fatalf("regenerateTalosconfig: %v", err)
	}

	got := readTalosconfigContextEndpoints(t, dir, clusterName)
	if len(got) != 1 || got[0] != "10.0.80.201" {
		t.Errorf("--endpoints must propagate to talosconfig context; got %v, want [10.0.80.201]", got)
	}
}

// TestContract_RegenerateTalosconfig_NoOldConfig_NoFlag_UsesLoopbackFallback
// pins the fallback shape: when --endpoints is omitted, the
// regenerated talosconfig still has a valid (non-empty) endpoints
// list — the loopback placeholder. Without this fallback the
// regenerated talosconfig would be unusable (downstream client
// construction fails on empty endpoints).
func TestContract_RegenerateTalosconfig_NoOldConfig_NoFlag_UsesLoopbackFallback(t *testing.T) {
	clusterName := "test-cluster"

	dir := stageTalosconfigFixture(t, clusterName)
	withGlobalEndpoints(t, nil)

	if err := regenerateTalosconfig(); err != nil {
		t.Fatalf("regenerateTalosconfig: %v", err)
	}

	got := readTalosconfigContextEndpoints(t, dir, clusterName)
	if len(got) != 1 || got[0] != defaultLocalEndpoint {
		t.Errorf("empty --endpoints must fall back to defaultLocalEndpoint; got %v, want [%s]", got, defaultLocalEndpoint)
	}
}

// TestContract_RegenerateTalosconfig_OldConfigWins_FlagIgnored pins
// the documented behaviour: when an existing talosconfig is present,
// regenerate preserves its endpoints and ignores --endpoints. The
// command's Long help spells this out ("Preserves endpoints and
// nodes from existing config"). Without this pin a future
// contributor "fixing" the --endpoints flag to always take
// precedence would silently change the documented contract.
func TestContract_RegenerateTalosconfig_OldConfigWins_FlagIgnored(t *testing.T) {
	clusterName := "test-cluster"

	dir := stageTalosconfigFixture(t, clusterName)

	// Seed an existing talosconfig with operator-curated endpoints
	// that differ from both the loopback and the --endpoints flag
	// below. The regenerate flow must preserve these.
	existingTalosconfig := []byte(
		"context: " + clusterName + "\n" +
			"contexts:\n" +
			"  " + clusterName + ":\n" +
			"    endpoints:\n" +
			"      - 10.0.99.99\n" +
			"      - 10.0.99.100\n",
	)
	if err := os.WriteFile(filepath.Join(dir, talosconfigName), existingTalosconfig, 0o600); err != nil {
		t.Fatal(err)
	}

	// --endpoints is set to something else — the test pins that the
	// old-config endpoints win, so this list must NOT appear in the
	// result.
	withGlobalEndpoints(t, []string{"1.2.3.4"})

	if err := regenerateTalosconfig(); err != nil {
		t.Fatalf("regenerateTalosconfig: %v", err)
	}

	got := readTalosconfigContextEndpoints(t, dir, clusterName)
	if len(got) != 2 || got[0] != "10.0.99.99" || got[1] != "10.0.99.100" {
		t.Errorf("existing talosconfig endpoints must win over --endpoints; got %v, want [10.0.99.99 10.0.99.100]", got)
	}

	for _, e := range got {
		if e == "1.2.3.4" {
			t.Errorf("--endpoints (1.2.3.4) must NOT leak into preserved context; got %v", got)
		}
	}
}
