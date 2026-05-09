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

// Contract: schema selection by `templateOptions.talosVersion` (or
// --talos-version CLI flag). Both shipped charts use the same
// `talos.config` entry point in _helpers.tpl:
//
//   {{- if and .TalosVersion (not (semverCompare "<1.12.0-0" .TalosVersion)) }}
//   {{- include "talos.config.multidoc" . }}
//   {{- else }}
//   {{- include "talos.config.legacy" . }}
//   {{- end }}
//
// The contract is:
//   - empty TalosVersion       → legacy
//   - <1.12.0-0 (semver)       → legacy
//   - >=1.12.0-0 (semver)      → multi-doc
//   - "v1.12.0" / "v1.12"      → multi-doc (sprig accepts both)
//
// The pre-release suffix matters: "v1.12.0-rc.1" still satisfies
// >=1.12.0-0 (the -0 anchor sorts before any pre-release). Talm uses
// this so a node booted off a v1.12.0-rc.1 maintenance image still
// gets multi-doc.
//
// Tests below pin both the routing decision (which include() fires)
// and the structural difference between the two outputs (legacy emits
// machine.network section; multi-doc emits separate HostnameConfig /
// ResolverConfig / LinkConfig documents joined by `---`).

package engine

import (
	"strings"
	"testing"
)

// Contract: empty talosVersion (the default when neither
// templateOptions.talosVersion nor --talos-version is set) renders the
// legacy schema. Distinguishing marker: `machine.network` block
// exists, with hostname/nameservers/interfaces under it.
func TestContract_Schema_EmptyVersionRendersLegacy(t *testing.T) {
	for _, chartPath := range []string{cozystackChartPath, genericChartPath} {
		t.Run(chartPath, func(t *testing.T) {
			out := renderChartTemplate(t, chartPath, controlplaneTpl)
			assertContains(t, out, "network:")
			assertContains(t, out, "hostname:")
			assertContains(t, out, "nameservers:")
			// Multi-doc marker MUST be absent.
			assertNotContains(t, out, "kind: HostnameConfig")
			assertNotContains(t, out, "kind: ResolverConfig")
			assertNotContains(t, out, "kind: LinkConfig")
		})
	}
}

// Contract: any version <1.12.0 renders legacy. Includes 1.10, 1.11,
// pre-releases of 1.12 with explicit -alpha/-beta below the -0 anchor
// (semverCompare uses the -0 lower-bound trick to anchor at the
// earliest possible 1.12.0-anything).
func TestContract_Schema_VersionsBefore112RenderLegacy(t *testing.T) {
	versions := []string{
		"v1.10.0",
		"v1.11.0",
		"v1.11.5",
		"1.11", // sprig semver accepts a partial version
	}
	for _, v := range versions {
		t.Run(v, func(t *testing.T) {
			out := renderChartTemplate(t, cozystackChartPath, controlplaneTpl, v)
			assertNotContains(t, out, "kind: HostnameConfig")
			assertContains(t, out, "network:")
			assertContains(t, out, "hostname:")
		})
	}
}

// Contract: v1.12.0 and later versions render multi-doc. Distinguishing
// marker: HostnameConfig / ResolverConfig documents joined by `---`.
func TestContract_Schema_Versions112AndLaterRenderMultidoc(t *testing.T) {
	versions := []string{
		"v1.12.0",
		"v1.12.5",
		"v1.13.0",
		"v2.0.0",
		"1.12", // partial version, accepted by sprig semver
	}
	for _, v := range versions {
		t.Run(v, func(t *testing.T) {
			out := renderChartTemplate(t, cozystackChartPath, controlplaneTpl, v)
			assertContains(t, out, "kind: HostnameConfig")
			assertContains(t, out, "kind: ResolverConfig")
			// Document separator MUST appear at least once between
			// machine.* and the first --- HostnameConfig. The
			// surrounding bytes can be \n or \r\n depending on the
			// platform (helm engine emits the host's line ending on
			// Windows), so match the literal `---` token rather than
			// pinning a specific newline pair.
			if !strings.Contains(out, testYAMLDocSeparator) {
				t.Errorf("multi-doc render missing `---` separator:\n%s", out)
			}
		})
	}
}

// Contract: v1.12.0-rc.1 and similar pre-release tags satisfy the
// >=1.12.0-0 anchor and render multi-doc. This is the cluster-bootstrap
// case: a node booted off a v1.12.0-rc.* maintenance image must get
// multi-doc machine config; otherwise the legacy machine.network block
// would be parsed by a renderer that doesn't accept it.
func TestContract_Schema_Version112PreReleasesRenderMultidoc(t *testing.T) {
	versions := []string{
		"v1.12.0-rc.1",
		"v1.12.0-alpha.0",
		"v1.12.0-beta.5",
	}
	for _, v := range versions {
		t.Run(v, func(t *testing.T) {
			out := renderChartTemplate(t, cozystackChartPath, controlplaneTpl, v)
			assertContains(t, out, "kind: HostnameConfig")
			assertContains(t, out, "kind: ResolverConfig")
		})
	}
}

// Contract: legacy schema produces a single YAML document — no `---`
// separators in the body. Talos's legacy parser expects exactly one
// document. (Helm always prepends one leading newline; we check that
// no internal separator appears.)
func TestContract_Schema_LegacyIsSingleDocument(t *testing.T) {
	for _, chartPath := range []string{cozystackChartPath, genericChartPath} {
		t.Run(chartPath, func(t *testing.T) {
			out := renderChartTemplate(t, chartPath, controlplaneTpl)
			// Match a `---` token surrounded by ANY newline form
			// (\n or \r\n) — Windows-rendered output uses CRLF and
			// pinning `\n---\n` would falsely pass on Windows.
			// Scan line-by-line: a single line that is exactly `---`
			// means an internal document separator.
			for line := range strings.SplitSeq(out, "\n") {
				if strings.TrimRight(line, "\r") == testYAMLDocSeparator {
					t.Errorf("legacy render must not contain `---` separator:\n%s", out)
					break
				}
			}
		})
	}
}

// Contract: multi-doc schema emits at least three documents on
// controlplane: the main machine/cluster block, HostnameConfig, and
// ResolverConfig. The exact count varies (more documents appear when
// floatingIP is set, when discovery yields configurable links, etc.),
// but the floor is three. This pins the "we always emit a typed
// config for hostname and resolvers regardless of discovery state"
// contract.
func TestContract_Schema_MultidocAlwaysEmitsHostnameAndResolver(t *testing.T) {
	for _, chartPath := range []string{cozystackChartPath, genericChartPath} {
		t.Run(chartPath, func(t *testing.T) {
			out := renderChartTemplate(t, chartPath, controlplaneTpl, multidocTalos)
			assertContains(t, out, "kind: HostnameConfig")
			assertContains(t, out, "kind: ResolverConfig")
		})
	}
}

// Contract: multi-doc schema emits RegistryMirrorConfig only on the
// cozystack chart (which still ships the docker.io->mirror.gcr.io
// default). Generic chart does not emit any RegistryMirrorConfig
// document.
func TestContract_Schema_MultidocRegistryMirrorOnlyOnCozystack(t *testing.T) {
	cozystackOut := renderChartTemplate(t, cozystackChartPath, controlplaneTpl, multidocTalos)
	assertContains(t, cozystackOut, "kind: RegistryMirrorConfig")
	assertContains(t, cozystackOut, "name: docker.io")
	assertContains(t, cozystackOut, "url: https://mirror.gcr.io")

	genericOut := renderChartTemplate(t, genericChartPath, controlplaneTpl, multidocTalos)
	assertNotContains(t, genericOut, "kind: RegistryMirrorConfig")
	assertNotContains(t, genericOut, "mirror.gcr.io")
}

// Contract: schema selection is a per-render decision, not cached.
// Calling renderChartTemplate against the same chart with different
// talosVersion values in the same test process must yield different
// outputs (legacy vs multi-doc). Pins the absence of any global
// caching that would tie a chart's first-rendered schema to its
// subsequent renders.
func TestContract_Schema_SwitchableWithinSameProcess(t *testing.T) {
	legacyOut := renderChartTemplate(t, cozystackChartPath, controlplaneTpl, "v1.11.0")
	multidocOut := renderChartTemplate(t, cozystackChartPath, controlplaneTpl, "v1.12.0")
	if strings.Contains(legacyOut, "kind: HostnameConfig") {
		t.Errorf("legacy render leaked multi-doc marker; possible cache bug")
	}
	if !strings.Contains(multidocOut, "kind: HostnameConfig") {
		t.Errorf("multi-doc render lost its marker after a legacy render")
	}
}
