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

// Contract: extractResourceData converts a COSI resource (as
// returned by Talos's gRPC ResourceService) into the plain-data
// shape that helm template's lookup() function consumes —
// `{metadata: {...}, spec: <yaml-decoded>}`. The yaml field is
// extracted via reflection on the unexported `yaml string` field of
// protobuf.Resource's protoSpec. Tests pin both the metadata
// projection and the yaml-parsed spec round-trip.

package engine

import (
	"testing"

	cosiv1alpha1 "github.com/cosi-project/runtime/api/v1alpha1"
	cosiproto "github.com/cosi-project/runtime/pkg/resource/protobuf"
)

// makeProtoResource is a small builder for protobuf.Resource — the
// COSI resource type Talos returns over gRPC. Wraps cosiproto.Unmarshal
// so test bodies stay tight.
func makeProtoResource(t *testing.T, namespace, kind, id, yamlSpec string) (*cosiproto.Resource, error) {
	t.Helper()
	//nolint:wrapcheck // protobuf.Unmarshal returns a typed error verified by the test's require.Error/NoError contract.
	return cosiproto.Unmarshal(&cosiv1alpha1.Resource{
		Metadata: &cosiv1alpha1.Metadata{
			Namespace: namespace,
			Type:      kind,
			Id:        id,
			Version:   "1",
			Owner:     "test",
			Phase:     "running",
		},
		Spec: &cosiv1alpha1.Spec{
			YamlSpec: yamlSpec,
		},
	})
}

// Contract: metadata is projected as a flat string-valued map with
// keys namespace, type, id, version, phase, owner. The chart's
// lookup() relies on these keys verbatim; renaming any one breaks
// every helper that does `lookup("...").metadata.X`.
func TestContract_ExtractResourceData_MetadataShape(t *testing.T) {
	res, err := makeProtoResource(t, "network", "Links.net.talos.dev", "eth0", "kind: physical\n")
	if err != nil {
		t.Fatal(err)
	}
	got, err := extractResourceData(res)
	if err != nil {
		t.Fatalf("extractResourceData: %v", err)
	}
	md, ok := got["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("expected metadata to be map, got %T", got["metadata"])
	}
	want := map[string]any{
		"namespace": "network",
		"type":      "Links.net.talos.dev",
		"id":        "eth0",
		"version":   "1",
		"phase":     "running",
		"owner":     "test",
	}
	for k, v := range want {
		if md[k] != v {
			t.Errorf("metadata[%q] = %v, want %v", k, md[k], v)
		}
	}
}

// Contract: spec is the YAML-decoded representation of the wire
// yaml field. A scalar string yaml decodes to map[string]any with
// the appropriate Go-typed values (strings, ints, nested maps).
// The chart helpers consume `.spec.X` paths, so the decoded shape
// matters.
func TestContract_ExtractResourceData_SpecYAMLDecoded(t *testing.T) {
	yamlSpec := `kind: physical
busPath: pci-0000:00:1f.0
hardwareAddr: aa:bb:cc:00:00:01
mtu: 1500
nested:
  key: value
`
	res, err := makeProtoResource(t, "network", "Links.net.talos.dev", "eth0", yamlSpec)
	if err != nil {
		t.Fatal(err)
	}
	got, err := extractResourceData(res)
	if err != nil {
		t.Fatalf("extractResourceData: %v", err)
	}
	spec, ok := got["spec"].(map[string]any)
	if !ok {
		t.Fatalf("expected spec to be map, got %T (%v)", got["spec"], got["spec"])
	}
	if spec["kind"] != "physical" {
		t.Errorf("spec.kind = %v, want 'physical'", spec["kind"])
	}
	if spec["busPath"] != "pci-0000:00:1f.0" {
		t.Errorf("spec.busPath = %v", spec["busPath"])
	}
	if spec["mtu"] != 1500 {
		t.Errorf("spec.mtu = %v (%T), want int 1500", spec["mtu"], spec["mtu"])
	}
	nested, ok := spec["nested"].(map[string]any)
	if !ok {
		t.Fatalf("spec.nested = %T", spec["nested"])
	}
	if nested["key"] != "value" {
		t.Errorf("spec.nested.key = %v", nested["key"])
	}
}

// Contract: empty YAML spec produces nil at spec — extractResourceData
// does not error. yaml.Unmarshal on an empty string yields nil. The
// chart helpers handle missing-spec via the standard `with` /
// `if .spec` patterns.
func TestContract_ExtractResourceData_EmptyYAMLSpec(t *testing.T) {
	res, err := makeProtoResource(t, "network", "Links.net.talos.dev", "eth0", "")
	if err != nil {
		t.Fatal(err)
	}
	got, err := extractResourceData(res)
	if err != nil {
		t.Fatalf("extractResourceData: %v", err)
	}
	// Metadata still emitted.
	if got["metadata"] == nil {
		t.Error("metadata missing on empty yaml")
	}
	// spec is the YAML-unmarshalled content of the empty string —
	// yaml.Unmarshal("") returns nil, so the spec entry is nil too.
	if got["spec"] != nil {
		t.Errorf("expected nil spec for empty yaml, got %v (%T)", got["spec"], got["spec"])
	}
}

// Contract: malformed YAML in the spec surfaces a clean error
// mentioning yaml unmarshal. Fail-fast so a corrupted resource on
// the wire surfaces in the caller's chart-rendering log instead of
// silently materialising as a half-decoded map.
func TestContract_ExtractResourceData_MalformedYAMLSpec(t *testing.T) {
	res, err := makeProtoResource(t, "network", "Links.net.talos.dev", "eth0", ":bad: yaml :")
	if err != nil {
		t.Fatal(err)
	}
	_, err = extractResourceData(res)
	if err == nil {
		t.Fatal("expected error for malformed yaml")
	}
}
