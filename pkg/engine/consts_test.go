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

// Package-level test fixture constants shared across pkg/engine/*_test.go.
// Hoisted from inline literals to satisfy goconst at the strict-lint
// threshold; the values themselves are deliberate, stable test inputs.
//
// This file is _test.go suffixed, so the constants are visible only to
// the test build of package engine and never linked into the production
// binary.

package engine

const (
	// Helm/Talos top-level template keys exposed via chartutil.Values.
	testKeyValues       = "Values"
	testKeyTalosVersion = "TalosVersion"

	// COSI resource list kind used in lookup fixtures.
	testCOSIKindList = "List"

	// Common values.yaml field names referenced from string-keyed maps.
	testFieldAddress           = "address"
	testFieldAddresses         = "addresses"
	testFieldAdvertisedSubnets = "advertisedSubnets"

	// Reserved-range fixture IPs (RFC 5737/RFC 6890 documentation use).
	testIP10001       = "10.0.0.1"
	testIP1111        = "1.1.1.1"
	testIP1921681_1   = "192.168.1.1"
	testIP19216820199 = "192.168.201.99"
	testIP1921682015  = "192.168.201.5"
	testIP1921682011  = "192.168.201.1"

	// Reserved-range fixture CIDRs.
	testCIDR1000099_24    = "10.0.0.99/24"
	testCIDR0000_0        = "0.0.0.0/0"
	testCIDR10005_24      = "10.0.0.5/24"
	testCIDR10000_24      = "10.0.0.0/24"
	testCIDR100050_24     = "10.0.0.50/24"
	testCIDR1921681100_24 = "192.168.1.100/24"
	testCIDR19216811024   = "192.168.1.10/24"
	testCIDR192168201024  = "192.168.201.10/24"
	testCIDR1921682010_24 = "192.168.201.0/24"
	testCIDR1921681002_24 = "192.168.100.2/24"
	testCIDR889924947_26  = "88.99.249.47/26"

	// MAC address fixtures (RFC 7042 documentation range / synthetic).
	testMACFF     = "aa:bb:cc:dd:ee:ff"
	testMAC00     = "aa:bb:cc:dd:ee:00"
	testMAC01     = "aa:bb:cc:dd:ee:01"
	testMAC000001 = "aa:bb:cc:00:00:01"
	testMAC000002 = "aa:bb:cc:00:00:02"

	// Misc string literals reused across tests.
	testBondMode8023ad     = "802.3ad"
	testInvalidClusterName = "InvalidClusterName"
	testDNS1123Subdomain   = "DNS-1123 subdomain"
	testMissingValuesPath  = "/path/that/does/not/exist.yaml"
	testMarkdownExt        = ".md"
	testYAMLDocSeparator   = "---"
)
