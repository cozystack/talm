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

// Contract: rendered `cluster:` section semantics for the cozystack and
// generic charts. The tests here pin user-facing behaviour of every
// values.yaml knob that surfaces under `cluster.*` for each schema
// (legacy pre-v1.12 single-doc, v1.12+ multi-doc) and each machine type
// (controlplane, worker).
//
// IMPORTANT: cozystack and generic are NOT siblings. cozystack carries
// many opinionated defaults (clusterDomain, OIDC, allocateNodeCIDRs,
// allowSchedulingOnControlPlanes, proxy disabled, discovery disabled,
// unconditional 127.0.0.1 in certSANs, sysctls, kernel modules, files,
// registries). generic ships a minimal config and intentionally omits
// all of those. The tests below are explicit about which chart owns
// which contract — and the generic-only "absence" tests pin that
// generic stays minimal even if cozystack adds new defaults later.
//
// Each test is preceded by a Contract: comment describing what is
// being pinned and why. That comment is the user-facing documentation;
// the test body is the executable enforcement of it.

package engine

import (
	"testing"
)

// helmEngineEmptyLookup is the offline-mode LookupFunc used by every
// contract test that does not exercise discovery-driven branches. It
// returns an empty map for every (resource, namespace, name) tuple so
// helpers like talm.discovered.* yield empty strings/JSON arrays. Tests
// that need real discovery state plug in their own LookupFunc via
// renderChartTemplateWithLookup.
func helmEngineEmptyLookup(string, string, string) (map[string]any, error) {
	return map[string]any{}, nil
}

// chartCell is one (chart, schema, machineType) cell of the test
// matrix. The verbose name is intentional — it appears in `go test -v`
// output, and a reader should immediately see which chart, which Talos
// schema, and which machine type failed.
type chartCell struct {
	name         string
	chartPath    string
	templateFile string
	talosVersion string // empty for legacy schema, "v1.12.0" for multi-doc
}

const (
	cozystackChartPath = "../../charts/cozystack"
	genericChartPath   = "../../charts/generic"
	controlplaneTpl    = "templates/controlplane.yaml"
	workerTpl          = "templates/worker.yaml"
	multidocTalos      = "v1.12.0"
)

// allCells enumerates every (chart × schema × machineType) cell.
func allCells() []chartCell {
	return []chartCell{
		{"cozystack/legacy/controlplane", cozystackChartPath, controlplaneTpl, ""},
		{"cozystack/legacy/worker", cozystackChartPath, workerTpl, ""},
		{"cozystack/multidoc/controlplane", cozystackChartPath, controlplaneTpl, multidocTalos},
		{"cozystack/multidoc/worker", cozystackChartPath, workerTpl, multidocTalos},
		{"generic/legacy/controlplane", genericChartPath, controlplaneTpl, ""},
		{"generic/legacy/worker", genericChartPath, workerTpl, ""},
		{"generic/multidoc/controlplane", genericChartPath, controlplaneTpl, multidocTalos},
		{"generic/multidoc/worker", genericChartPath, workerTpl, multidocTalos},
	}
}

// cozystackCells returns the four cozystack matrix entries.
func cozystackCells() []chartCell {
	out := []chartCell{}
	for _, c := range allCells() {
		if c.chartPath == cozystackChartPath {
			out = append(out, c)
		}
	}
	return out
}

// cozystackControlplaneCells returns cozystack controlplane (legacy + multidoc).
func cozystackControlplaneCells() []chartCell {
	out := []chartCell{}
	for _, c := range cozystackCells() {
		if c.templateFile == controlplaneTpl {
			out = append(out, c)
		}
	}
	return out
}

// genericCells returns the four generic matrix entries.
func genericCells() []chartCell {
	out := []chartCell{}
	for _, c := range allCells() {
		if c.chartPath == genericChartPath {
			out = append(out, c)
		}
	}
	return out
}

// genericControlplaneCells returns generic controlplane (legacy + multidoc).
func genericControlplaneCells() []chartCell {
	out := []chartCell{}
	for _, c := range genericCells() {
		if c.templateFile == controlplaneTpl {
			out = append(out, c)
		}
	}
	return out
}

// allWorkerCells returns every worker cell across both charts.
func allWorkerCells() []chartCell {
	out := []chartCell{}
	for _, c := range allCells() {
		if c.templateFile == workerTpl {
			out = append(out, c)
		}
	}
	return out
}

// chartNameFor extracts the chart's directory name (== Chart.Name for
// both shipped charts).
func chartNameFor(c chartCell) string {
	if c.chartPath == cozystackChartPath {
		return "cozystack"
	}
	return "generic"
}

// === Shared contracts: hold across both charts, both schemas, both machine types ===

// Contract: cluster.clusterName always equals the chart's Chart.Name
// when no override is supplied. Both charts hardcode this in their
// _helpers.tpl as `clusterName: "{{ .Chart.Name }}"`. The value is
// baked into Talos PKI cert SANs and ETCD bootstrap identity, so a
// drift here breaks every existing node's trust chain on next apply.
// The trailing quote in the expected substring is significant: the
// chart wraps the value in a double-quoted scalar, so a future move to
// a raw scalar (`clusterName: cozystack`) is a YAML-level shift this
// test catches.
func TestContract_Cluster_ClusterName_DefaultsToChartName(t *testing.T) {
	for _, cell := range allCells() {
		t.Run(cell.name, func(t *testing.T) {
			out := renderChartTemplate(t, cell.chartPath, cell.templateFile, cell.talosVersion)
			expected := `clusterName: "` + chartNameFor(cell) + `"`
			assertContains(t, out, expected)
		})
	}
}

// Contract: podSubnets default is 10.244.0.0/16 in BOTH charts. The
// list form is significant: even a single-element default is rendered
// as a YAML list, because Talos requires `podSubnets` to be a sequence
// — a future "optimization" that emits a scalar would break Talos
// parsing.
func TestContract_Cluster_PodSubnets_Default(t *testing.T) {
	for _, cell := range allCells() {
		t.Run(cell.name, func(t *testing.T) {
			out := renderChartTemplate(t, cell.chartPath, cell.templateFile, cell.talosVersion)
			assertContains(t, out, "podSubnets:")
			assertContains(t, out, "- 10.244.0.0/16")
		})
	}
}

// Contract: serviceSubnets default is 10.96.0.0/16 in BOTH charts.
// Same list-form contract as podSubnets.
func TestContract_Cluster_ServiceSubnets_Default(t *testing.T) {
	for _, cell := range allCells() {
		t.Run(cell.name, func(t *testing.T) {
			out := renderChartTemplate(t, cell.chartPath, cell.templateFile, cell.talosVersion)
			assertContains(t, out, "serviceSubnets:")
			assertContains(t, out, "- 10.96.0.0/16")
		})
	}
}

// Contract: cluster.controlPlane.endpoint is always rendered as a
// double-quoted string. Talos parses this as a string, but quoting
// makes the YAML unambiguous for any downstream tool that re-parses
// the rendered output.
func TestContract_Cluster_Endpoint_Quoted(t *testing.T) {
	for _, cell := range allCells() {
		t.Run(cell.name, func(t *testing.T) {
			out := renderChartTemplate(t, cell.chartPath, cell.templateFile, cell.talosVersion)
			assertContains(t, out, `endpoint: "`+testEndpoint+`"`)
		})
	}
}

// === Worker-only contracts (cluster.* fields that must NOT appear on workers) ===

// Contract: worker templates never emit controlplane-only cluster
// blocks (apiServer, controllerManager, scheduler, etcd, proxy,
// discovery, allowSchedulingOnControlPlanes). Talos rejects these on
// worker configs — leaking any of them is a hard validation error on
// `talm apply`. Both charts, both schemas.
func TestContract_Cluster_NoControlplaneBlocksOnWorker(t *testing.T) {
	for _, cell := range allWorkerCells() {
		t.Run(cell.name, func(t *testing.T) {
			out := renderChartTemplate(t, cell.chartPath, cell.templateFile, cell.talosVersion)
			assertNotContains(t, out, "apiServer:")
			assertNotContains(t, out, "controllerManager:")
			assertNotContains(t, out, "scheduler:")
			assertNotContains(t, out, "etcd:")
			assertNotContains(t, out, "allowSchedulingOnControlPlanes")
			assertNotContains(t, out, "proxy:")
			assertNotContains(t, out, "discovery:")
		})
	}
}

// === cozystack-only contracts ===

// Contract: cozystack ships clusterDomain: cozy.local, surfaced as
// `cluster.network.dnsDomain: cozy.local`. This is a cozystack
// convention — the generic chart does not expose clusterDomain at all
// (see TestContract_Cluster_NoClusterDomainOnGeneric).
func TestContract_Cluster_ClusterDomain_Cozystack(t *testing.T) {
	for _, cell := range cozystackCells() {
		t.Run(cell.name, func(t *testing.T) {
			out := renderChartTemplate(t, cell.chartPath, cell.templateFile, cell.talosVersion)
			assertContains(t, out, `dnsDomain: "cozy.local"`)
		})
	}
}

// Contract: cozystack unconditionally prepends 127.0.0.1 to
// apiServer.certSANs on the controlplane. Required for talosctl /
// kubectl running on the control-plane node itself: without 127.0.0.1
// in the cert SANs, local-loopback API access fails TLS validation.
// Generic does NOT emit this — see
// TestContract_Cluster_NoUnconditionalLoopbackOnGeneric.
func TestContract_Cluster_CertSANs_LoopbackUnconditional_Cozystack(t *testing.T) {
	for _, cell := range cozystackControlplaneCells() {
		t.Run(cell.name, func(t *testing.T) {
			out := renderChartTemplate(t, cell.chartPath, cell.templateFile, cell.talosVersion)
			assertContains(t, out, "certSANs:")
			assertContains(t, out, "- 127.0.0.1")
		})
	}
}

// Contract: cozystack appends operator-supplied certSANs after the
// hardcoded 127.0.0.1. Pins the additive behaviour so a regression
// that replaces the loopback entry with the user list (instead of
// appending) would surface here.
func TestContract_Cluster_CertSANs_AppendsUserValues_Cozystack(t *testing.T) {
	out := renderCozystackWith(t, helmEngineEmptyLookup, map[string]any{
		"certSANs":          []any{"api.example.com", "10.0.0.1"},
		"advertisedSubnets": []any{testAdvertisedSubnet},
	})
	assertContains(t, out, "- 127.0.0.1")
	assertContains(t, out, "- api.example.com")
	assertContains(t, out, "- 10.0.0.1")
}

// Contract: cozystack does NOT emit oidc-* extraArgs unless
// oidcIssuerUrl is set. An always-emitted empty oidc-issuer-url is a
// kube-apiserver startup error.
func TestContract_Cluster_OIDC_AbsentByDefault_Cozystack(t *testing.T) {
	for _, cell := range cozystackControlplaneCells() {
		t.Run(cell.name, func(t *testing.T) {
			out := renderChartTemplate(t, cell.chartPath, cell.templateFile, cell.talosVersion)
			assertNotContains(t, out, "oidc-issuer-url")
			assertNotContains(t, out, "oidc-client-id")
		})
	}
}

// Contract: when oidcIssuerUrl is set, cozystack emits the four
// oidc-* extraArgs (issuer-url is operator-supplied; client-id /
// username-claim / groups-claim are hardcoded). This contract
// guarantees that an operator only needs to set oidcIssuerUrl to get a
// fully-working OIDC integration.
func TestContract_Cluster_OIDC_PresentWhenSet_Cozystack(t *testing.T) {
	const issuer = "https://oidc.example.com"
	out := renderCozystackWith(t, helmEngineEmptyLookup, map[string]any{
		"oidcIssuerUrl":     issuer,
		"advertisedSubnets": []any{testAdvertisedSubnet},
	})
	assertContains(t, out, `oidc-issuer-url: "`+issuer+`"`)
	assertContains(t, out, `oidc-client-id: "kubernetes"`)
	assertContains(t, out, `oidc-username-claim: "preferred_username"`)
	assertContains(t, out, `oidc-groups-claim: "groups"`)
}

// Contract: cozystack ships allocateNodeCIDRs: true. The
// controllerManager extraArgs reflects this with `allocate-node-cidrs:
// true` AND `cluster-cidr: <podSubnets joined>`. A regression that
// flipped the default would silently disable kube-controller-manager's
// IPAM and break pod networking on every new node.
func TestContract_Cluster_AllocateNodeCIDRs_Default_Cozystack(t *testing.T) {
	for _, cell := range cozystackControlplaneCells() {
		t.Run(cell.name, func(t *testing.T) {
			out := renderChartTemplate(t, cell.chartPath, cell.templateFile, cell.talosVersion)
			assertContains(t, out, "allocate-node-cidrs: true")
			assertContains(t, out, `cluster-cidr: "10.244.0.0/16"`)
		})
	}
}

// Contract: when an operator sets allocateNodeCIDRs: false, cozystack
// emits ONLY `allocate-node-cidrs: false` and does NOT emit the
// cluster-cidr extraArg. Leaving cluster-cidr present while
// allocate-node-cidrs is false triggers a kube-controller-manager
// warning. The conditional emission is the contract.
func TestContract_Cluster_AllocateNodeCIDRs_Disabled_Cozystack(t *testing.T) {
	out := renderCozystackWith(t, helmEngineEmptyLookup, map[string]any{
		"allocateNodeCIDRs": false,
		"advertisedSubnets": []any{testAdvertisedSubnet},
	})
	assertContains(t, out, "allocate-node-cidrs: false")
	assertNotContains(t, out, "cluster-cidr:")
}

// Contract: cozystack control-plane nodes are schedulable
// (`allowSchedulingOnControlPlanes: true`). Required for cozystack
// edge / single-node deployments where workloads must run on
// control-plane capacity. Flipping this default silently breaks small
// clusters.
func TestContract_Cluster_AllowSchedulingOnControlPlanes_Cozystack(t *testing.T) {
	for _, cell := range cozystackControlplaneCells() {
		t.Run(cell.name, func(t *testing.T) {
			out := renderChartTemplate(t, cell.chartPath, cell.templateFile, cell.talosVersion)
			assertContains(t, out, "allowSchedulingOnControlPlanes: true")
		})
	}
}

// Contract: cozystack disables kube-proxy chart-wide (Cilium-or-similar
// CNI owns service routing). A regression that re-enabled kube-proxy
// would double-program iptables and break service routing in subtle
// ways.
func TestContract_Cluster_ProxyDisabled_Cozystack(t *testing.T) {
	for _, cell := range cozystackControlplaneCells() {
		t.Run(cell.name, func(t *testing.T) {
			out := renderChartTemplate(t, cell.chartPath, cell.templateFile, cell.talosVersion)
			assertContains(t, out, "proxy:")
			assertContains(t, out, "disabled: true")
		})
	}
}

// Contract: cozystack disables Talos's built-in cluster discovery
// because cozystack handles cluster bootstrap differently.
func TestContract_Cluster_DiscoveryDisabled_Cozystack(t *testing.T) {
	for _, cell := range cozystackControlplaneCells() {
		t.Run(cell.name, func(t *testing.T) {
			out := renderChartTemplate(t, cell.chartPath, cell.templateFile, cell.talosVersion)
			assertContains(t, out, "discovery:")
			assertContains(t, out, "enabled: false")
		})
	}
}

// === generic-only contracts: pin that generic stays minimal ===

// Contract: generic chart does NOT expose clusterDomain in values.yaml
// and does NOT emit `dnsDomain:` in rendered output. If a future
// "convergence" change adds clusterDomain to generic, this test
// flags it explicitly so the change is intentional.
func TestContract_Cluster_NoClusterDomainOnGeneric(t *testing.T) {
	for _, cell := range genericCells() {
		t.Run(cell.name, func(t *testing.T) {
			out := renderChartTemplate(t, cell.chartPath, cell.templateFile, cell.talosVersion)
			assertNotContains(t, out, "dnsDomain:")
			assertNotContains(t, out, "cozy.local")
		})
	}
}

// Contract: generic chart does NOT prepend 127.0.0.1 to certSANs. The
// generic apiServer block emits certSANs only when values.certSANs is
// non-empty (no unconditional loopback). This pins the minimal
// philosophy: generic ships exactly what the operator asks for, no
// hidden defaults.
func TestContract_Cluster_NoUnconditionalLoopbackOnGeneric(t *testing.T) {
	for _, cell := range genericControlplaneCells() {
		t.Run(cell.name, func(t *testing.T) {
			out := renderChartTemplate(t, cell.chartPath, cell.templateFile, cell.talosVersion)
			// Loopback must not appear at all when values.certSANs is empty
			// (the default). renderChartTemplate does not inject certSANs.
			assertNotContains(t, out, "- 127.0.0.1")
		})
	}
}

// Contract: generic chart does NOT carry cozystack-specific cluster
// defaults (OIDC, allocateNodeCIDRs, allowSchedulingOnControlPlanes,
// proxy disabled, discovery disabled). Pinning the absence prevents
// accidental "I copied the cozystack default into generic" drift.
func TestContract_Cluster_NoCozystackDefaultsOnGeneric(t *testing.T) {
	for _, cell := range genericCells() {
		t.Run(cell.name, func(t *testing.T) {
			out := renderChartTemplate(t, cell.chartPath, cell.templateFile, cell.talosVersion)
			assertNotContains(t, out, "oidc-issuer-url")
			assertNotContains(t, out, "allocate-node-cidrs")
			assertNotContains(t, out, "cluster-cidr")
			assertNotContains(t, out, "allowSchedulingOnControlPlanes")
			assertNotContains(t, out, "proxy:")
			// `discovery:` must not appear under cluster.* on generic. The
			// substring is unique enough since the chart never emits a
			// cluster.discovery block elsewhere; tightening to a YAML-path
			// match is left for a later iteration.
			assertNotContains(t, out, "discovery:")
		})
	}
}

// Contract: generic chart's cluster.apiServer block is emitted on
// controlplane but is empty when values.certSANs is unset. This
// matches the chart's deliberate minimalism — apiServer exists as a
// container for operator additions, not for hidden defaults.
func TestContract_Cluster_GenericApiServerBlockExistsButEmpty(t *testing.T) {
	for _, cell := range genericControlplaneCells() {
		t.Run(cell.name, func(t *testing.T) {
			out := renderChartTemplate(t, cell.chartPath, cell.templateFile, cell.talosVersion)
			assertContains(t, out, "apiServer:")
			// No certSANs sub-key when values.certSANs is unset.
			assertNotContains(t, out, "certSANs:\n  - ")
		})
	}
}

// Contract: generic chart appends operator-supplied certSANs verbatim,
// without any 127.0.0.1 prepended.
func TestContract_Cluster_GenericCertSANsAppendsVerbatim(t *testing.T) {
	out := renderGenericWith(t, helmEngineEmptyLookup, map[string]any{
		"certSANs":          []any{"api.example.com"},
		"advertisedSubnets": []any{testAdvertisedSubnet},
	})
	assertContains(t, out, "- api.example.com")
	assertNotContains(t, out, "- 127.0.0.1")
}
