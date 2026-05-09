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

// Shared test-only string constants. Centralized so the goconst linter
// has a single canonical reference; renaming any of these touches one
// location instead of dozens of test fixtures.
const (
	// presetCozystack and presetGeneric are the two preset names that
	// dominate the test suite. They have to match real preset names
	// shipped under pkg/generated/charts/, so a typo here would make
	// the affected tests pass against a phantom preset.
	presetCozystack = "cozystack"
	presetGeneric   = "generic"

	// testNodeAddrA / testNodeAddrB / testNodeAddrC are the three
	// canonical reserved-range IPs the apply / template suite uses
	// to address fake Talos nodes. Documentation-range RFC 5737
	// 192.0.2.0/24 would be even more strictly correct, but these
	// values are how every existing test fixture spells "node".
	testNodeAddrA = "10.0.0.1"
	testNodeAddrC = "10.0.0.3"

	// testTalosVersion / testKubernetesVersion are the version
	// pair the apply-options builders are tested against; pinning
	// them here keeps fixture and assertion in sync after a future
	// version bump.
	testTalosVersion      = "v1.12"
	testKubernetesVersion = "1.31.0"

	// testProjectRoot is the synthetic absolute Config.RootDir the
	// apply-options builder tests use to verify path resolution
	// without touching the real filesystem layout.
	testProjectRoot = "/project"

	// testTalmApply / testTalosconfigName are user-facing literals
	// the apply-options assertions reference verbatim — the engine
	// reads CommandName for FailIfMultiNodes wording, and
	// "talosconfig" is the talosconfig basename pinned by every
	// node-resolution test.
	testTalmApply       = "talm apply"
	testTalosconfigName = "talosconfig"

	// testTemplateControlplane / testTemplateOutsideRoot are the
	// canonical template paths exercised by both
	// resolveTemplatePaths and resolveEngineTemplatePaths suites.
	// The "..templates/" prefix is the historical regression seed
	// that taught isOutsideRoot the difference between ".."
	// (parent) and "..templates" (sibling).
	testTemplateControlplane = "..templates/controlplane.yaml"

	// testNodeFixtureA / testNodeFixtureFingerprint are
	// fixture-only strings that appear in restore-on-error / fake
	// maintenance call paths.
	testNodeFixtureA           = "original-A"
	testNodeFixtureFingerprint = "fp-1"

	// testFooLiteral is the placeholder relPath token used by
	// isOutsideRoot's table-driven cases; six occurrences exceed
	// goconst's threshold so a single hoisted const documents the
	// intent.
	testFooLiteral = "foo"

	// testNodeAddrB is the second canonical reserved-range IP used
	// across multi-node fixtures; pairs with testNodeAddrA / testNodeAddrC.
	testNodeAddrB = "10.0.0.2"

	// testNodeFixtureB is the second restore-on-error fixture node;
	// pairs with testNodeFixtureA.
	testNodeFixtureB = "original-B"

	// testTemplateControlplaneRel / testTemplateWorker /
	// testTemplateMissing / testTemplateConfig are the canonical
	// inside-root template paths used by the apply / template /
	// contract suites. Hoisted to a single const so goconst sees one
	// reference per literal.
	testTemplateControlplaneRel = "templates/controlplane.yaml"
	testTemplateWorker          = "templates/worker.yaml"
	testTemplateMissing         = "templates/missing.yaml"
	testTemplateConfig          = "templates/config.yaml"
)
