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

// Contract: `talm init --name <X>` validates X as a DNS-1123
// subdomain in PreRunE before any file is written. The check uses
// k8s.io/apimachinery/pkg/util/validation.IsDNS1123Subdomain so the
// error wording, character class, and length limits match the
// upstream Kubernetes contract that the rendered Talos config
// downstream relies on.

package commands

import (
	"strings"
	"testing"
)

// withInitFlagsSnapshot captures the package-level initCmdFlags so a
// test can mutate them without leaking into subsequent tests.
func withInitFlagsSnapshot(t *testing.T) {
	t.Helper()
	saved := initCmdFlags
	t.Cleanup(func() { initCmdFlags = saved })
}

// Contract: a valid DNS-1123 subdomain passes PreRunE without
// touching the validation guard. Includes single-label, multi-label
// (`my.cluster.example`), leading-digit, dashes-in-the-middle. The
// shipped chart names (`cozystack`, `generic`, `talm`) ALL must
// pass — they are valid by construction, so a regression in the
// validator that rejected them would brick every default install.
func TestContract_InitPreRun_AcceptsValidDNS1123Subdomain(t *testing.T) {
	withInitFlagsSnapshot(t)

	cases := []string{
		testPresetCozystack,
		testPresetGeneric,
		presetTalmLibrary,
		testClusterName,
		"my.cluster.example",
		"1leading-digit",
		"a",      // single character
		"prod-2", // trailing digit
	}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			initCmdFlags.preset = testPresetCozystack
			initCmdFlags.name = name
			initCmdFlags.encrypt = false
			initCmdFlags.decrypt = false
			initCmdFlags.update = false
			initCmdFlags.image = ""
			if err := initCmd.PreRunE(initCmd, nil); err != nil {
				t.Errorf("expected %q to pass PreRunE, got: %v", name, err)
			}
		})
	}
}

// Contract: an invalid DNS-1123 subdomain is rejected in PreRunE
// with an error that names the offending value AND includes the
// upstream k8s validator message (so the operator sees the precise
// constraint that was violated). Each row covers a distinct
// failure class so a regression that loosens the validator only on
// some axis surfaces here.
func TestContract_InitPreRun_RejectsInvalidDNS1123Subdomain(t *testing.T) {
	withInitFlagsSnapshot(t)

	cases := []struct {
		name        string
		clusterName string
		expectInMsg string // substring of the k8s validator message
	}{
		{"uppercase", "MyCluster", "lower case"},
		{"underscore", "my_cluster", testValidatorAlphanumeric},
		{"leading dash", "-bad", testValidatorAlphanumeric},
		{"trailing dash", "bad-", testValidatorAlphanumeric},
		{"space", "my cluster", testValidatorAlphanumeric},
		{"empty label between dots", "foo..bar", testValidatorAlphanumeric},
		{"subdomain too long", strings.Repeat("a", 254), "253"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			initCmdFlags.preset = testPresetCozystack
			initCmdFlags.name = tc.clusterName
			initCmdFlags.encrypt = false
			initCmdFlags.decrypt = false
			initCmdFlags.update = false
			initCmdFlags.image = ""

			err := initCmd.PreRunE(initCmd, nil)
			if err == nil {
				t.Fatalf("expected %q to fail PreRunE", tc.clusterName)
			}
			if !strings.Contains(err.Error(), `"`+tc.clusterName+`"`) {
				t.Errorf("error must quote the offending value, got: %v", err)
			}
			if !strings.Contains(err.Error(), "DNS-1123 subdomain") {
				t.Errorf("error must mention 'DNS-1123 subdomain' for grep-ability, got: %v", err)
			}
			if !strings.Contains(err.Error(), tc.expectInMsg) {
				t.Errorf("error must include upstream substring %q, got: %v", tc.expectInMsg, err)
			}
		})
	}
}

// Contract: validation runs ONLY when --name applies — under
// --encrypt / --decrypt / --update the name flag is not required, so
// the validator must not fire on an empty initCmdFlags.name. Pin
// so a regression that always validates would break the
// regenerate-talosconfig flow operators rely on (they pass --decrypt
// without --name).
//
// These modes also require an existing project root, so the test
// stages a fixture with Chart.yaml + secrets.yaml inside a tempdir
// and points Config.RootDir at it. Without the project-root setup
// PreRunE fails earlier than the validator can be reached.
func TestContract_InitPreRun_SkipsValidationOnExclusiveModes(t *testing.T) {
	withInitFlagsSnapshot(t)

	cases := []struct {
		name string
		set  func()
	}{
		{testEncryptFlag, func() { initCmdFlags.encrypt = true }},
		{testDecryptFlag, func() { initCmdFlags.decrypt = true }},
		{"update", func() { initCmdFlags.update = true }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			makeProjectRoot(t, dir)
			setRoot(t, dir)

			initCmdFlags.preset = ""
			initCmdFlags.name = ""
			initCmdFlags.encrypt = false
			initCmdFlags.decrypt = false
			initCmdFlags.update = false
			initCmdFlags.image = ""
			tc.set()

			if err := initCmd.PreRunE(initCmd, nil); err != nil {
				t.Errorf("expected --%s with empty name to pass PreRunE, got: %v", tc.name, err)
			}
		})
	}
}
