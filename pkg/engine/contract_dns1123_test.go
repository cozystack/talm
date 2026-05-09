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

// Contract: the talm.validate.dns1123subdomain helper in
// charts/talm/templates/_helpers.tpl. Both shipped charts pipe
// values.yaml clusterName and (cozystack only) clusterDomain through
// it so chart-render-time validation stays symmetric with the Go-side
// validation.IsDNS1123Subdomain check on `talm init --name`.
//
// Tests render the cozystack chart with crafted overrides and assert
// on the returned error message — this exercises the same code path
// users hit at runtime.

package engine

import (
	"strings"
	"testing"
)

// === clusterName: helper happy paths ===

// Contract: the helper accepts every shape that should be valid:
// single-label, multi-label, leading digit, dashes-in-the-middle,
// single-character. The shipped chart names ("cozystack",
// "generic", "talm") MUST pass — these are the default fallback
// when values.yaml.clusterName is empty.
func TestContract_DNS1123_ClusterName_HappyPaths(t *testing.T) {
	cases := []string{
		"cozystack",
		"generic",
		"my-cluster",
		"my.cluster.example",
		"1leading-digit",
		"a", // single character
		"prod-2",
	}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			out := renderCozystackWith(t, helmEngineEmptyLookup, map[string]any{
				"clusterName":       name,
				"advertisedSubnets": []any{testAdvertisedSubnet},
			})
			assertContains(t, out, `clusterName: "`+name+`"`)
		})
	}
}

// === clusterName: helper rejection paths ===

// Contract: every invalid shape fails the render with a message
// that names the field, quotes the offending value, and identifies
// the violation class so an operator can act without consulting
// the chart source.
func TestContract_DNS1123_ClusterName_RejectsInvalid(t *testing.T) {
	cases := []struct {
		name        string
		clusterName string
		wantInError string // substring that must appear in the failure message
	}{
		{"uppercase", "MyCluster", "DNS-1123 subdomain"},
		{"underscore", "my_cluster", "DNS-1123 subdomain"},
		{"leading dash", "-bad", "DNS-1123 subdomain"},
		{"trailing dash", "bad-", "DNS-1123 subdomain"},
		{"space", "my cluster", "DNS-1123 subdomain"},
		{"empty label between dots", "foo..bar", "DNS-1123 subdomain"},
		{"subdomain too long", strings.Repeat("a", 254), "253"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := renderExpectingError(t, cozystackChartPath, multidocTalos, helmEngineEmptyLookup, map[string]any{
				"clusterName":       tc.clusterName,
				"endpoint":          testEndpoint,
				"advertisedSubnets": []any{testAdvertisedSubnet},
			})
			if err == nil {
				t.Fatalf("expected render to fail for clusterName=%q", tc.clusterName)
			}
			if !strings.Contains(err.Error(), "clusterName") {
				t.Errorf("error must name 'clusterName' field, got: %v", err)
			}
			if !strings.Contains(err.Error(), tc.wantInError) {
				t.Errorf("error must contain %q, got: %v", tc.wantInError, err)
			}
		})
	}
}

// === clusterDomain: helper applied (cozystack only) ===

// Contract: cozystack's clusterDomain (network.dnsDomain) flows
// through the same validator. Pinning here closes the gap that
// previously existed: clusterDomain was unvalidated so an invalid
// value reached Talos and surfaced as an opaque downstream error.
func TestContract_DNS1123_ClusterDomain_RejectsInvalid(t *testing.T) {
	cases := []struct {
		name          string
		clusterDomain string
		wantInError   string
	}{
		{"uppercase", "Cozy.Local", "DNS-1123 subdomain"},
		{"underscore", "cozy_local", "DNS-1123 subdomain"},
		{"empty", "", "non-empty DNS-1123 subdomain"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := renderExpectingError(t, cozystackChartPath, multidocTalos, helmEngineEmptyLookup, map[string]any{
				"clusterDomain":     tc.clusterDomain,
				"endpoint":          testEndpoint,
				"advertisedSubnets": []any{testAdvertisedSubnet},
			})
			if err == nil {
				t.Fatalf("expected render to fail for clusterDomain=%q", tc.clusterDomain)
			}
			if !strings.Contains(err.Error(), "clusterDomain") {
				t.Errorf("error must name 'clusterDomain' field, got: %v", err)
			}
			if !strings.Contains(err.Error(), tc.wantInError) {
				t.Errorf("error must contain %q, got: %v", tc.wantInError, err)
			}
		})
	}
}

// Contract: the default cozy.local value passes validation
// unchanged. A regression that tightened the regex would break
// every fresh cozystack install.
func TestContract_DNS1123_ClusterDomain_DefaultPasses(t *testing.T) {
	out := renderCozystackWith(t, helmEngineEmptyLookup, map[string]any{
		"advertisedSubnets": []any{testAdvertisedSubnet},
	})
	assertContains(t, out, "dnsDomain: cozy.local")
}

// === generic chart: same helper, same contract ===

// Contract: generic chart pipes clusterName through the same
// helper. Pin one negative case here so a regression that touches
// only the cozystack template surfaces on generic too.
func TestContract_DNS1123_GenericClusterName_RejectsInvalid(t *testing.T) {
	err := renderExpectingError(t, genericChartPath, multidocTalos, helmEngineEmptyLookup, map[string]any{
		"clusterName":       "Invalid_Name",
		"endpoint":          testEndpoint,
		"advertisedSubnets": []any{testAdvertisedSubnet},
	})
	if err == nil {
		t.Fatal("expected render to fail")
	}
	if !strings.Contains(err.Error(), "clusterName") {
		t.Errorf("error must name 'clusterName' field, got: %v", err)
	}
	if !strings.Contains(err.Error(), "DNS-1123 subdomain") {
		t.Errorf("error must mention DNS-1123 subdomain, got: %v", err)
	}
}
