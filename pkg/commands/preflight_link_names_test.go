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
	"slices"
	"testing"

	"github.com/cozystack/talm/pkg/applycheck"
	"github.com/siderolabs/talos/pkg/machinery/resources/network"
)

func TestSnapshotLinkNames_IncludesAliasAndAltNames(t *testing.T) {
	t.Parallel()

	plain := network.NewLinkStatus(network.NamespaceName, "eth1")

	aliased := network.NewLinkStatus(network.NamespaceName, "eth0")
	aliased.TypedSpec().Alias = "uplink0"
	aliased.TypedSpec().AltNames = []string{"enp0s1", "slot-3"}

	got := snapshotLinkNames(slices.Values([]*network.LinkStatus{plain, aliased}))

	want := []string{"eth1", "eth0", "uplink0", "enp0s1", "slot-3"}
	if !slices.Equal(got, want) {
		t.Fatalf("snapshotLinkNames() = %v, want %v", got, want)
	}
}

// TestSnapshotLinkNames_ResolvesAliasedLinkRefs chains the snapshot the apply
// builds to the walker that consumes it: machinery lets BGPInstanceConfig and
// VRFConfig name a link by its alias, and the apply must not block that.
func TestSnapshotLinkNames_ResolvesAliasedLinkRefs(t *testing.T) {
	t.Parallel()

	const rendered = `apiVersion: v1alpha1
kind: BGPInstanceConfig
name: bgp0
advertise:
  - uplink0
neighbors:
  - link: uplink0
vrf: uplink0
---
apiVersion: v1alpha1
kind: VRFConfig
name: vrf0
links:
  - uplink0
`

	refs, err := applycheck.WalkRefs([]byte(rendered))
	if err != nil {
		t.Fatalf("WalkRefs() error = %v", err)
	}

	if len(refs) == 0 {
		t.Fatal("WalkRefs() returned no refs; the aliased names are no longer validated")
	}

	link := network.NewLinkStatus(network.NamespaceName, "eth0")
	link.TypedSpec().Alias = "uplink0"

	snapshot := applycheck.HostSnapshot{Links: snapshotLinkNames(slices.Values([]*network.LinkStatus{link}))}

	for _, finding := range applycheck.ValidateRefs(refs, snapshot) {
		if finding.IsBlocker() {
			t.Errorf("blocked an aliased link: ref=%+v reason=%s", finding.Ref, finding.Reason)
		}
	}
}
