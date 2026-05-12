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

package applycheck

import (
	"bytes"
	"fmt"
	"io"

	"github.com/cockroachdb/errors"
	yaml "gopkg.in/yaml.v3"
)

// RefKind classifies a host-resource reference extracted from a rendered
// MachineConfig.
type RefKind int

const (
	// RefKindLink is a host network link by name (eth0, bond0, vlan tag, ...).
	RefKindLink RefKind = iota
	// RefKindDiskLiteral is an install/extra disk identified by a literal
	// device path (machine.install.disk: /dev/sda).
	RefKindDiskLiteral
	// RefKindDiskSelector is an install/user-volume disk identified by a
	// selector (size, model, serial, wwid, modalias, type, busPath).
	RefKindDiskSelector
)

// DiskSelector mirrors the Talos v1alpha1 InstallDiskSelector schema (also
// used by UserVolumeConfig provisioning). Fields are left as raw strings so
// the walker stays YAML-only; the evaluator interprets each.
type DiskSelector struct {
	Size     string `yaml:"size,omitempty"`
	Name     string `yaml:"name,omitempty"`
	Model    string `yaml:"model,omitempty"`
	Serial   string `yaml:"serial,omitempty"`
	Modalias string `yaml:"modalias,omitempty"`
	UUID     string `yaml:"uuid,omitempty"`
	WWID     string `yaml:"wwid,omitempty"`
	Type     string `yaml:"type,omitempty"`
	BusPath  string `yaml:"busPath,omitempty"`
}

// IsZero reports whether the selector has no fields set; the walker uses
// this to avoid emitting empty selector refs.
func (s *DiskSelector) IsZero() bool {
	return *s == DiskSelector{}
}

// Ref is a host-side reference the walker found in the rendered MachineConfig.
type Ref struct {
	Kind     RefKind
	Name     string       // populated for RefKindLink and RefKindDiskLiteral
	Selector DiskSelector // populated for RefKindDiskSelector (zero value otherwise)
	Source   string       // human-readable JSONPath pointing at the offending field
}

// WalkRefs parses the rendered MachineConfig bytes and returns every
// host-resource reference it contains. Both the v1.11 nested
// machine.network.interfaces[] form and the v1.12 multi-doc form
// (LinkConfig / BondConfig / VLANConfig / BridgeConfig / Layer2VIPConfig
// plus UserVolumeConfig.provisioning.diskSelector) are supported.
// Unknown documents are ignored.
func WalkRefs(rendered []byte) ([]Ref, error) {
	if len(bytes.TrimSpace(rendered)) == 0 {
		return nil, nil
	}

	dec := yaml.NewDecoder(bytes.NewReader(rendered))

	var refs []Ref

	for docIndex := 0; ; docIndex++ {
		var doc map[string]any

		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return nil, errors.Wrapf(err, "applycheck: decoding YAML document %d", docIndex)
		}

		if doc == nil {
			continue
		}

		refs = walkDocument(refs, doc, docIndex)
	}

	return refs, nil
}

// walkDocument dispatches between the v1.11 root config shape (top-level
// `machine:`) and the v1.12 multi-doc shape (top-level `kind:`). Documents
// matching neither shape are ignored — Talos accepts a small handful of
// vendor-extension docs the walker does not need to know about.
func walkDocument(refs []Ref, doc map[string]any, docIndex int) []Ref {
	if machine, ok := doc["machine"].(map[string]any); ok {
		return walkV1Alpha1Root(refs, machine, fmt.Sprintf("doc[%d].machine", docIndex))
	}

	if kind, ok := doc["kind"].(string); ok {
		return walkMultidocKind(refs, doc, kind, fmt.Sprintf("doc[%d]", docIndex))
	}

	return refs
}

// walkV1Alpha1Root handles the legacy nested form: machine.network.interfaces[]
// for link refs and machine.install.{disk,diskSelector} for disk refs.
func walkV1Alpha1Root(refs []Ref, machine map[string]any, basePath string) []Ref {
	if install, ok := machine["install"].(map[string]any); ok {
		refs = appendDiskRefs(refs, install, basePath+".install")
	}

	network, ok := machine["network"].(map[string]any)
	if !ok {
		return refs
	}

	interfaces, ok := network["interfaces"].([]any)
	if !ok {
		return refs
	}

	for i, iface := range interfaces {
		ifaceMap, ok := iface.(map[string]any)
		if !ok {
			continue
		}

		name, ok := ifaceMap["interface"].(string)
		if !ok || name == "" {
			continue
		}

		refs = append(refs, Ref{
			Kind:   RefKindLink,
			Name:   name,
			Source: fmt.Sprintf("%s.network.interfaces[%d].interface", basePath, i),
		})
	}

	return refs
}

// multidocHandler emits the refs for one v1.12 multi-doc kind. Handlers are
// registered in multidocHandlers and dispatched by walkMultidocKind; this
// keeps walkMultidocKind a flat lookup instead of a giant switch.
type multidocHandler func(refs []Ref, doc map[string]any, basePath string) []Ref

//nolint:gochecknoglobals // dispatch table for multidoc kinds; static after init.
var multidocHandlers = map[string]multidocHandler{
	// LinkConfig.name is emitted: the typical case is an override of an
	// existing physical NIC (ens5 settings, MTU on eth0), where the
	// operator wants validation to catch a typoed interface name. The
	// rarer "create-a-fresh-virtual-link" case is covered by BondConfig/
	// BridgeConfig/VLANConfig below, which intentionally do NOT validate
	// their own .name field — those names describe a virtual link being
	// created by the apply, not an existing one to reference.
	//
	// YAML field names mirror Talos's v1alpha1 schema verbatim:
	//
	//   - bond.go BondLinks ->  `links`
	//   - bridge.go BridgeLinks -> `links` (NOT `ports`)
	//   - vlan.go ParentLinkConfig -> `parent` (NOT `link`)
	//   - layer2_vip.go LinkName -> `link`
	"LinkConfig":      handleNameOnly,
	"BondConfig":      handleListOnly("links"),
	"VLANConfig":      handleParentOnly,
	"BridgeConfig":    handleListOnly("links"),
	"Layer2VIPConfig": handleLayer2VIP,
	"HCloudVIPConfig": handleLayer2VIP,
	// DHCPv4Config / DHCPv6Config / EthernetConfig use .name to
	// reference an *existing* link — typo there is a Phase 1 catch.
	// Note: dhcp4.go/dhcp6.go/ethernet.go share the CommonLinkConfig
	// inline, so a typoed name still surfaces as a missing-link
	// finding the same way LinkConfig does.
	"DHCPv4Config":   handleNameOnly,
	"DHCPv6Config":   handleNameOnly,
	"EthernetConfig": handleNameOnly,
	// WireguardConfig / DummyLinkConfig / LinkAliasConfig describe
	// virtual links being created. Their .name is the new resource,
	// not an existing-link reference — intentionally not in the
	// dispatch table so they don't get a name-based Phase 1 finding.
	"UserVolumeConfig": handleUserVolume,
}

// walkMultidocKind handles v1.12 multi-doc shapes by kind discriminator.
// Unknown kinds are intentionally ignored — Talos extensions and future
// kinds should not cause the walker to error.
func walkMultidocKind(refs []Ref, doc map[string]any, kind, basePath string) []Ref {
	handler, ok := multidocHandlers[kind]
	if !ok {
		return refs
	}

	return handler(refs, doc, basePath)
}

func handleNameOnly(refs []Ref, doc map[string]any, basePath string) []Ref {
	return appendNameRef(refs, doc, basePath)
}

// handleListOnly emits only the list-valued slaves/ports of the doc,
// not the doc's own .name. Used for BondConfig (its .name describes a
// virtual bond being created by the apply; the .links[] members are
// pre-existing physical NICs that must be present).
func handleListOnly(listKey string) multidocHandler {
	return func(refs []Ref, doc map[string]any, basePath string) []Ref {
		return appendListRefs(refs, doc, listKey, basePath+"."+listKey)
	}
}

// handleParentOnly emits only the parent reference of a VLAN doc, not
// its own .name. The .name is the VLAN tag's child link name (a new
// virtual link being created); the .parent is the parent that must
// exist. The YAML key in v1alpha1 is `parent`, not `link`
// (vlan.go ParentLinkConfig `yaml:"parent"`).
func handleParentOnly(refs []Ref, doc map[string]any, basePath string) []Ref {
	parent, ok := doc["parent"].(string)
	if !ok || parent == "" {
		return refs
	}

	return append(refs, Ref{Kind: RefKindLink, Name: parent, Source: basePath + ".parent"})
}

func handleLayer2VIP(refs []Ref, doc map[string]any, basePath string) []Ref {
	link, ok := doc["link"].(string)
	if !ok || link == "" {
		return refs
	}

	return append(refs, Ref{Kind: RefKindLink, Name: link, Source: basePath + ".link"})
}

func handleUserVolume(refs []Ref, doc map[string]any, basePath string) []Ref {
	prov, ok := doc["provisioning"].(map[string]any)
	if !ok {
		return refs
	}

	sel, ok := prov["diskSelector"].(map[string]any)
	if !ok {
		return refs
	}

	selector := selectorFromMap(sel)
	if selector.IsZero() {
		return refs
	}

	return append(refs, Ref{
		Kind:     RefKindDiskSelector,
		Selector: selector,
		Source:   basePath + ".provisioning.diskSelector",
	})
}

// appendNameRef emits a single RefKindLink keyed by doc["name"], or a no-op
// when name is missing or empty.
func appendNameRef(refs []Ref, doc map[string]any, basePath string) []Ref {
	name, ok := doc["name"].(string)
	if !ok || name == "" {
		return refs
	}

	return append(refs, Ref{Kind: RefKindLink, Name: name, Source: basePath + ".name"})
}

// appendDiskRefs emits the install-disk-shaped refs from the v1.11
// machine.install block (literal disk path OR selector). Both fields may
// be present simultaneously in older configs; the walker emits whichever
// it finds without judging precedence — that's the validator's call.
func appendDiskRefs(refs []Ref, install map[string]any, basePath string) []Ref {
	if disk, ok := install["disk"].(string); ok && disk != "" {
		refs = append(refs, Ref{
			Kind:   RefKindDiskLiteral,
			Name:   disk,
			Source: basePath + ".disk",
		})
	}

	if sel, ok := install["diskSelector"].(map[string]any); ok {
		if selector := selectorFromMap(sel); !selector.IsZero() {
			refs = append(refs, Ref{
				Kind:     RefKindDiskSelector,
				Selector: selector,
				Source:   basePath + ".diskSelector",
			})
		}
	}

	return refs
}

// appendListRefs emits one RefKindLink per string entry in doc[key].
func appendListRefs(refs []Ref, doc map[string]any, key, basePath string) []Ref {
	items, ok := doc[key].([]any)
	if !ok {
		return refs
	}

	for i, item := range items {
		name, ok := item.(string)
		if !ok || name == "" {
			continue
		}

		refs = append(refs, Ref{
			Kind:   RefKindLink,
			Name:   name,
			Source: fmt.Sprintf("%s[%d]", basePath, i),
		})
	}

	return refs
}

// selectorFromMap converts a generic YAML map (as decoded into map[string]any)
// into a DiskSelector. Numeric size matchers parse to ints in YAML; coerce
// every value through fmt.Sprintf so the selector stays string-typed.
func selectorFromMap(m map[string]any) DiskSelector {
	get := func(key string) string {
		v, ok := m[key]
		if !ok || v == nil {
			return ""
		}

		return fmt.Sprintf("%v", v)
	}

	return DiskSelector{
		Size:     get("size"),
		Name:     get("name"),
		Model:    get("model"),
		Serial:   get("serial"),
		Modalias: get("modalias"),
		UUID:     get("uuid"),
		WWID:     get("wwid"),
		Type:     get("type"),
		BusPath:  get("busPath"),
	}
}
