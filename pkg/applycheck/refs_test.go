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

package applycheck_test

import (
	"strings"
	"testing"

	"github.com/cozystack/talm/pkg/applycheck"
)

const v1_11NestedLinkAndDisk = `version: v1alpha1
debug: false
machine:
  type: controlplane
  install:
    disk: /dev/sda
  network:
    hostname: cp-01
    interfaces:
      - interface: eth0
        addresses:
          - 192.0.2.5/24
        routes:
          - network: 0.0.0.0/0
            gateway: 192.0.2.1
`

const v1_12MultidocLinkConfig = `version: v1alpha1
debug: false
machine:
  type: controlplane
  install:
    diskSelector:
      model: "Samsung*"
      size: ">= 100GB"
---
apiVersion: v1alpha1
kind: LinkConfig
name: eth0
up: true
addresses:
  - address: 192.0.2.5/24
---
apiVersion: v1alpha1
kind: VLANConfig
name: eth0.4000
parent: eth0
vlanID: 4000
---
apiVersion: v1alpha1
kind: BondConfig
name: bond0
links:
  - eth1
  - eth2
---
apiVersion: v1alpha1
kind: BridgeConfig
name: br0
links:
  - eth3
  - bond0
---
apiVersion: v1alpha1
kind: Layer2VIPConfig
name: 192.0.2.10
link: bond1
`

func findRef(refs []applycheck.Ref, kind applycheck.RefKind, name string) (applycheck.Ref, bool) {
	for _, r := range refs {
		if r.Kind == kind && r.Name == name {
			return r, true
		}
	}

	return applycheck.Ref{}, false
}

func TestWalkRefs_v1_11_ExtractsLinkAndLiteralDisk(t *testing.T) {
	t.Parallel()

	refs, err := applycheck.WalkRefs([]byte(v1_11NestedLinkAndDisk))
	if err != nil {
		t.Fatalf("WalkRefs error: %v", err)
	}

	eth0, ok := findRef(refs, applycheck.RefKindLink, "eth0")
	if !ok {
		t.Fatalf("expected link ref for eth0, got refs=%+v", refs)
	}

	if eth0.Source == "" {
		t.Errorf("expected non-empty Source on link ref, got empty")
	}

	disk, ok := findRef(refs, applycheck.RefKindDiskLiteral, "/dev/sda")
	if !ok {
		t.Fatalf("expected disk literal ref for /dev/sda, got refs=%+v", refs)
	}

	if disk.Source == "" {
		t.Errorf("expected non-empty Source on disk literal ref, got empty")
	}
}

func TestWalkRefs_v1_12_ExtractsLinksFromMultidocAndDiskSelector(t *testing.T) {
	t.Parallel()

	refs, err := applycheck.WalkRefs([]byte(v1_12MultidocLinkConfig))
	if err != nil {
		t.Fatalf("WalkRefs error: %v", err)
	}

	// Walker emits only references to *existing* links, not names of
	// virtual links being created by the apply. So:
	//   - LinkConfig.name -> emitted (override of an existing physical NIC)
	//   - VLANConfig.parent -> emitted (the parent link must exist)
	//   - VLANConfig.name -> NOT emitted (the VLAN child is being created)
	//   - BondConfig.links[] -> emitted (slave NICs must exist)
	//   - BondConfig.name -> NOT emitted (the bond is being created)
	//   - BridgeConfig.links[] -> emitted (port NICs must exist)
	//   - BridgeConfig.name -> NOT emitted (the bridge is being created)
	wantLinks := []string{"eth0" /* LinkConfig.name + VLANConfig.parent */, "eth1", "eth2" /* BondConfig.links */, "eth3" /* BridgeConfig.links */}
	for _, name := range wantLinks {
		if _, ok := findRef(refs, applycheck.RefKindLink, name); !ok {
			t.Errorf("expected link ref for %q, got refs=%+v", name, refs)
		}
	}

	// Names of virtual links being created must NOT surface (bond0 is
	// a BondConfig.name + a BridgeConfig.links[] entry; the .name path
	// should not appear, only the .links[] one. eth0.4000 is a
	// VLANConfig.name with no other references — should not appear at
	// all).
	if _, ok := findRef(refs, applycheck.RefKindLink, "eth0.4000"); ok {
		t.Errorf("VLANConfig.name (eth0.4000) leaked as a ref; only the .parent should validate")
	}

	// Layer2VIPConfig.link is a distinct ref site. Use a name that no other
	// document mentions (bond1) so the assertion proves handleLayer2VIP
	// actually emitted the ref, rather than incidentally picking it up
	// from a sibling LinkConfig.
	vip, ok := findRef(refs, applycheck.RefKindLink, "bond1")
	if !ok {
		t.Errorf("expected Layer2VIPConfig.link=bond1 to surface as a link ref")
	} else if !strings.HasSuffix(vip.Source, ".link") {
		t.Errorf("Layer2VIPConfig.link ref Source = %q, want suffix .link", vip.Source)
	}

	// Disk selector ref must carry the model+size combination.
	var diskRef *applycheck.Ref
	for i := range refs {
		if refs[i].Kind == applycheck.RefKindDiskSelector {
			diskRef = &refs[i]

			break
		}
	}

	if diskRef == nil {
		t.Fatalf("expected one disk selector ref, got refs=%+v", refs)
	}

	if diskRef.Selector.Model != "Samsung*" {
		t.Errorf("selector.Model = %q, want Samsung*", diskRef.Selector.Model)
	}

	if diskRef.Selector.Size != ">= 100GB" {
		t.Errorf("selector.Size = %q, want >= 100GB", diskRef.Selector.Size)
	}
}

func TestWalkRefs_EmptyInput_NoRefsNoError(t *testing.T) {
	t.Parallel()

	refs, err := applycheck.WalkRefs(nil)
	if err != nil {
		t.Fatalf("WalkRefs(nil) error: %v", err)
	}

	if len(refs) != 0 {
		t.Errorf("WalkRefs(nil) returned %d refs, want 0", len(refs))
	}
}

// TestWalkRefs_v1_12_DHCPEthernetEmitNameRef pins that the
// "ethernet-shaped" v1alpha1 documents — DHCPv4Config, DHCPv6Config,
// EthernetConfig — surface their `.name` as a Phase 1 link
// reference. Each describes a configuration applied to an existing
// host link; a typoed name there is a Phase 1 catch.
func TestWalkRefs_v1_12_DHCPEthernetEmitNameRef(t *testing.T) {
	t.Parallel()

	body := `apiVersion: v1alpha1
kind: DHCPv4Config
name: ens5
---
apiVersion: v1alpha1
kind: DHCPv6Config
name: ens6
---
apiVersion: v1alpha1
kind: EthernetConfig
name: ens7
`
	refs, err := applycheck.WalkRefs([]byte(body))
	if err != nil {
		t.Fatalf("WalkRefs: %v", err)
	}

	for _, name := range []string{"ens5", "ens6", "ens7"} {
		if _, ok := findRef(refs, applycheck.RefKindLink, name); !ok {
			t.Errorf("expected link ref for %q, got refs=%+v", name, refs)
		}
	}
}

// TestWalkRefs_v1_12_VirtualCreatorsSkipNameRef pins that the
// virtual-link-creator documents — WireguardConfig, DummyLinkConfig,
// LinkAliasConfig — do NOT surface their `.name` as a link ref.
// Their .name is the new resource being created, not a reference
// to an existing link; emitting it as a ref would block every
// legitimate create-wireguard / create-dummy / create-alias apply
// (the same class of bug as the earlier Bond/VLAN/Bridge .name
// false-positive).
func TestWalkRefs_v1_12_VirtualCreatorsSkipNameRef(t *testing.T) {
	t.Parallel()

	body := `apiVersion: v1alpha1
kind: WireguardConfig
name: wg0
---
apiVersion: v1alpha1
kind: DummyLinkConfig
name: dummy0
---
apiVersion: v1alpha1
kind: LinkAliasConfig
name: my-alias
selector:
  match: physical
`
	refs, err := applycheck.WalkRefs([]byte(body))
	if err != nil {
		t.Fatalf("WalkRefs: %v", err)
	}

	for _, fake := range []string{"wg0", "dummy0", "my-alias"} {
		if _, ok := findRef(refs, applycheck.RefKindLink, fake); ok {
			t.Errorf("virtual-creator name %q leaked as a ref; only existing-link refs should be emitted", fake)
		}
	}
}

// TestWalkRefs_v1_12_RealTalosYAMLKeys pins the exact YAML field
// names Talos's v1alpha1 schema uses (verified against
// pkg/machinery/config/types/network/* in the cozystack/talos fork
// v0.0.0-20260126122716):
//
//   - bond.go BondLinks `yaml:"links"`
//   - bridge.go BridgeLinks `yaml:"links"` (NOT `ports`)
//   - vlan.go ParentLinkConfig `yaml:"parent"` (NOT `link`)
//   - layer2_vip.go LinkName `yaml:"link"`
//   - hcloud_vip.go LinkName `yaml:"link"`
//
// Without this contract a renamed YAML key in machinery would
// silently turn the walker into a no-op for the affected document
// class and Phase 1 would stop catching typos in those refs. This
// case was caught only by exercising a real cluster.
func TestWalkRefs_v1_12_RealTalosYAMLKeys(t *testing.T) {
	t.Parallel()

	body := `apiVersion: v1alpha1
kind: VLANConfig
name: ens5.42
parent: ens5
vlanID: 42
---
apiVersion: v1alpha1
kind: BridgeConfig
name: br0
links:
  - ens6
  - ens7
---
apiVersion: v1alpha1
kind: BondConfig
name: bond0
links:
  - ens8
  - ens9
---
apiVersion: v1alpha1
kind: Layer2VIPConfig
name: 10.0.0.1
link: bond0
---
apiVersion: v1alpha1
kind: HCloudVIPConfig
name: 10.0.0.2
link: bond0
`
	refs, err := applycheck.WalkRefs([]byte(body))
	if err != nil {
		t.Fatalf("WalkRefs: %v", err)
	}

	want := []struct {
		name   string
		source string // suffix
	}{
		{"ens5", ".parent"},   // VLANConfig.parent (NOT .link)
		{"ens6", ".links[0]"}, // BridgeConfig.links (NOT .ports)
		{"ens7", ".links[1]"},
		{"ens8", ".links[0]"}, // BondConfig.links
		{"ens9", ".links[1]"},
		{"bond0", ".link"}, // Layer2VIPConfig.link + HCloudVIPConfig.link
	}

	for _, w := range want {
		var found bool

		for _, r := range refs {
			if r.Kind == applycheck.RefKindLink && r.Name == w.name && strings.HasSuffix(r.Source, w.source) {
				found = true

				break
			}
		}

		if !found {
			t.Errorf("expected link ref %q with source suffix %q, got refs=%+v", w.name, w.source, refs)
		}
	}

	// Virtual-link names (the doc's own .name on VLAN/Bond/Bridge)
	// must NOT surface as refs.
	for _, fake := range []string{"ens5.42", "br0", "bond0"} {
		if _, ok := findRef(refs, applycheck.RefKindLink, fake); ok && fake != "bond0" {
			t.Errorf("name %q leaked as a ref; only existing-link refs should be emitted", fake)
		}
	}
}

// TestWalkRefs_AccidentalEncodings_Tolerated pins three real-world
// shapes that come out of editors and unzipping operations: a UTF-8
// BOM at the head of the file (common Windows artifact), CRLF line
// endings (same), and no trailing newline at EOF (no-final-newline
// editors). The walker uses gopkg.in/yaml.v3 which accepts all
// three, but a regression in any wrapper would silently produce
// zero refs and let a typoed config through.
func TestWalkRefs_AccidentalEncodings_Tolerated(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		body     string
		wantRefs int
	}{
		{
			name:     "UTF-8 BOM + CRLF",
			body:     "\xef\xbb\xbfmachine:\r\n  install:\r\n    disk: /dev/sda\r\n",
			wantRefs: 1,
		},
		{
			name:     "no trailing newline at EOF",
			body:     "apiVersion: v1alpha1\nkind: LinkConfig\nname: eth0",
			wantRefs: 1,
		},
		{
			name:     "only separators, no content",
			body:     "---\n---\n---\n",
			wantRefs: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			refs, err := applycheck.WalkRefs([]byte(tc.body))
			if err != nil {
				t.Errorf("error: %v", err)
			}

			if len(refs) != tc.wantRefs {
				t.Errorf("got %d refs, want %d (%+v)", len(refs), tc.wantRefs, refs)
			}
		})
	}
}

// TestWalkRefs_MalformedStructures_NoPanic pins the walker's
// tolerance for malformed shapes: corrupt YAML in fields the walker
// inspects must not panic the whole render. The walker uses
// best-effort type assertions and drops malformed entries silently
// (Talos's own parser will reject them downstream with a clearer
// error than the walker could give).
func TestWalkRefs_MalformedStructures_NoPanic(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
	}{
		{
			name: "machine.install is a string",
			body: `machine:
  install: "not a map"
`,
		},
		{
			name: "machine.network.interfaces is a string",
			body: `machine:
  network:
    interfaces: "not a list"
`,
		},
		{
			name: "interface entry is an int",
			body: `machine:
  network:
    interfaces:
      - 42
`,
		},
		{
			name: "LinkConfig with int name",
			body: `apiVersion: v1alpha1
kind: LinkConfig
name: 42
`,
		},
		{
			name: "BondConfig links contains non-strings",
			body: `apiVersion: v1alpha1
kind: BondConfig
name: bond0
links:
  - eth0
  - 42
  - null
`,
		},
		{
			name: "UserVolumeConfig provisioning missing",
			body: `apiVersion: v1alpha1
kind: UserVolumeConfig
name: data
`,
		},
		{
			name: "yaml document with only null",
			body: `---
null
---
machine:
  install:
    disk: /dev/sda
`,
		},
		{
			name: "doc with kind but no name",
			body: `apiVersion: v1alpha1
kind: LinkConfig
`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// The bar is "no panic, no error". Whether the walker
			// emits 0 or N refs is implementation detail; the
			// contract is that malformed shapes are *survivable*.
			_, err := applycheck.WalkRefs([]byte(tc.body))
			if err != nil {
				t.Errorf("malformed YAML body should not error, got %v", err)
			}
		})
	}
}
