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
				"clusterName":              name,
				testFieldAdvertisedSubnets: []any{testAdvertisedSubnet},
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
		{"uppercase", "MyCluster", testDNS1123Subdomain},
		{"underscore", "my_cluster", testDNS1123Subdomain},
		{"leading dash", "-bad", testDNS1123Subdomain},
		{"trailing dash", "bad-", testDNS1123Subdomain},
		{"space", "my cluster", testDNS1123Subdomain},
		{"empty label between dots", "foo..bar", testDNS1123Subdomain},
		{"subdomain too long", strings.Repeat("a", 254), "253"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := renderExpectingError(t, cozystackChartPath, multidocTalos, helmEngineEmptyLookup, map[string]any{
				"clusterName":              tc.clusterName,
				"endpoint":                 testEndpoint,
				testFieldAdvertisedSubnets: []any{testAdvertisedSubnet},
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

// Contract: an unquoted numeric YAML scalar (e.g. `clusterName: 123`
// in values.yaml) is parsed by Helm as an int, not a string. The
// helper coerces .value through `printf "%v"` before any length /
// regex check, so the validator emits a normal DNS-1123 fail with
// the field name and the stringified value instead of crashing the
// template at `len of type int`. Pin so a regression that drops the
// coercion surfaces here.
func TestContract_DNS1123_ClusterName_NumericYAMLScalarCoerced(t *testing.T) {
	err := renderExpectingError(t, cozystackChartPath, multidocTalos, helmEngineEmptyLookup, map[string]any{
		"clusterName":              123,
		"endpoint":                 testEndpoint,
		testFieldAdvertisedSubnets: []any{testAdvertisedSubnet},
	})
	if err == nil {
		// Note: "123" is actually a valid DNS-1123 subdomain (digits
		// are allowed). If the helper coerces correctly AND the value
		// passes validation, the render succeeds — that is also
		// acceptable. The bug we are pinning against is the
		// `len of type int` crash; either successful render or a
		// DNS-1123 fail is fine, the template panic is not.
		return
	}
	if strings.Contains(err.Error(), "len of type int") {
		t.Errorf("template crashed on numeric value instead of coercing to string; got: %v", err)
	}
}

// Contract: an invalid numeric value (e.g. negative numbers render
// with a leading dash, which DNS-1123 rejects) coerces and then
// fails with the regular DNS-1123 message naming the field and
// quoting the stringified value. Pin both the coercion AND the
// downstream validator wiring.
func TestContract_DNS1123_ClusterName_InvalidNumericFailsCleanly(t *testing.T) {
	err := renderExpectingError(t, cozystackChartPath, multidocTalos, helmEngineEmptyLookup, map[string]any{
		"clusterName":              -1,
		"endpoint":                 testEndpoint,
		testFieldAdvertisedSubnets: []any{testAdvertisedSubnet},
	})
	if err == nil {
		t.Fatal("expected fail for clusterName=-1")
	}
	if strings.Contains(err.Error(), "len of type") {
		t.Errorf("template crashed on numeric value; got: %v", err)
	}
	if !strings.Contains(err.Error(), testDNS1123Subdomain) {
		t.Errorf("expected DNS-1123 subdomain message after coercion, got: %v", err)
	}
	if !strings.Contains(err.Error(), "clusterName") {
		t.Errorf("error must name 'clusterName' field, got: %v", err)
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
		{"uppercase", "Cozy.Local", testDNS1123Subdomain},
		{"underscore", "cozy_local", testDNS1123Subdomain},
		{"empty", "", "non-empty DNS-1123 subdomain"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := renderExpectingError(t, cozystackChartPath, multidocTalos, helmEngineEmptyLookup, map[string]any{
				"clusterDomain":            tc.clusterDomain,
				"endpoint":                 testEndpoint,
				testFieldAdvertisedSubnets: []any{testAdvertisedSubnet},
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
// unchanged and is rendered as a quoted string. The quote pin
// guards against a value that happens to look numeric (e.g. a
// subdomain consisting only of digits) being parsed as a YAML
// number by downstream consumers; cluster names follow the same
// quoting convention.
func TestContract_DNS1123_ClusterDomain_DefaultPasses(t *testing.T) {
	out := renderCozystackWith(t, helmEngineEmptyLookup, map[string]any{
		testFieldAdvertisedSubnets: []any{testAdvertisedSubnet},
	})
	assertContains(t, out, `dnsDomain: "cozy.local"`)
}

// === generic chart: same helper, same contract ===

// Contract: generic chart pipes clusterName through the same
// helper. Pin one negative case here so a regression that touches
// only the cozystack template surfaces on generic too.
func TestContract_DNS1123_GenericClusterName_RejectsInvalid(t *testing.T) {
	err := renderExpectingError(t, genericChartPath, multidocTalos, helmEngineEmptyLookup, map[string]any{
		"clusterName":              "Invalid_Name",
		"endpoint":                 testEndpoint,
		testFieldAdvertisedSubnets: []any{testAdvertisedSubnet},
	})
	if err == nil {
		t.Fatal("expected render to fail")
	}
	if !strings.Contains(err.Error(), "clusterName") {
		t.Errorf("error must name 'clusterName' field, got: %v", err)
	}
	if !strings.Contains(err.Error(), testDNS1123Subdomain) {
		t.Errorf("error must mention DNS-1123 subdomain, got: %v", err)
	}
}
