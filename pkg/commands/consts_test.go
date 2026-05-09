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

// Shared string constants used across test files. Hoisted out of the
// individual _test.go files so goconst is satisfied with one source of
// truth — and so a future preset rename or override-image bump is one
// edit instead of a sweep across every contract test.
const (
	// Preset names from the embedded chart bundle. Tests use them as
	// flag values and as expected results from preset-detection
	// helpers. Test-only fixtures (this file is _test.go, so they
	// are not visible to non-test code).
	presetCozystack = "cozystack"
	presetGeneric   = "generic"

	// myClusterName is the canonical "valid DNS-1123 subdomain"
	// fixture; every contract test that needs a non-trivial cluster
	// name uses it so a future rename is one edit.
	myClusterName = "my-cluster"

	// installerImageABC is the override image fixture for
	// applyImageOverride / validateImageOverride contract tests.
	// It looks like a real factory.talos.dev reference but is bound
	// to v1.13.0 specifically so test output stays deterministic.
	installerImageABC = "factory.talos.dev/installer/abc:v1.13.0"

	// encryptFlag / decryptFlag are the literal sub-test names AND
	// the flag identifiers passed to the validator under test;
	// keeping them constants lets a single rename ripple through the
	// table-driven cases without drift.
	encryptFlag = "encrypt"
	decryptFlag = "decrypt"

	// alphanumericLabel is a substring of the upstream
	// k8s.io/apimachinery validator's error message. Every
	// table-driven case that asserts the validator surfaced its
	// message uses this string so a kubelet-bump that touches the
	// upstream wording fails in one place.
	alphanumericLabel = "alphanumeric"

	// linuxGOOS mirrors runtime.GOOS for the linux-only contract
	// reproducers (Getwd-after-rmdir, Abs-with-removed-CWD).
	linuxGOOS = "linux"
)
