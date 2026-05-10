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

package engine

// Test fixture literals hoisted from engine_test.go (mirrored from
// upstream Helm engine_test) so goconst's per-package threshold is
// satisfied with a single source of truth and a future Helm-bump
// rename ripples through one file.
const (
	tplFooDirect           = "/mychart/templates/foo.tpl"
	tplBarDirect           = "/mychart/templates/bar.tpl"
	tplFooNested           = "/mychart/templates/charts/foo/charts/bar/templates/foo.tpl"
	tplFooSubChartFoo      = "/mychart/templates/charts/foo/templates/foo.tpl"
	tplBarSubChartFoo      = "/mychart/templates/charts/foo/templates/bar.tpl"
	tplFooSubChartBar      = "/mychart/templates/charts/bar/templates/foo.tpl"
	tplFooHelpersDirect    = "/mychart/templates/_foo.tpl"
	helmFuncFromJSON       = "fromJson"
	helmTestVersion        = "1.2.3"
	helmTestDefaultValue   = "DEFAULT"
	helmKeyValues          = "Values"
	helmKeyChartChild      = "child"
	helmKeyName            = "Name"
	helmKeyChart           = "Chart"
	helmKeyRelease         = "Release"
	helmKeyBasePath        = "BasePath"
	helmFixtureIssue9981   = "issue9981"
	helmFixtureBaseAreUs   = "All your base are belong to us"
	helmFixtureDeepest     = "deepest"
	helmFixtureHerrick     = "herrick"
	helmFixture90sMeme     = "That 90s meme"
	helmFixtureTplFunction = "TplFunction"
	helmFixtureTestRelease = "TestRelease"
	helmFixtureExtraField  = "extra-field"
)
