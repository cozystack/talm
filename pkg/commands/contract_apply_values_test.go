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

import (
	"slices"
	"testing"
)

// applyValueFlagNames is the set of value-source flags `talm apply` must
// expose so it renders identically to `talm template`. apply re-renders from
// the modeline at apply time, so a value supplied only at template time would
// otherwise be silently dropped (issue #221).
//
//nolint:gochecknoglobals // test fixture: shared flag-name list for the table tests below.
var applyValueFlagNames = []string{"values", "set", "set-string", "set-file", "set-json", "set-literal"}

// TestApplyFlags_ValueSourcesRegistered pins that apply registers every value
// source template has. Without these flags, the template↔apply value
// inconsistency (#221) cannot be closed.
func TestApplyFlags_ValueSourcesRegistered(t *testing.T) {
	for _, name := range applyValueFlagNames {
		if applyCmd.Flags().Lookup(name) == nil {
			t.Errorf("apply must register the --%s flag to stay consistent with `talm template`; got nil", name)
		}
	}
}

// TestApplyFlags_ValueFlagTypesMatchTemplate pins that the apply value flags
// use the same cobra value type as template. --values is a StringSlice;
// --set* are StringArray (a StringSlice would split a --set value on commas
// inside it, corrupting JSON / literal payloads). A type mismatch between the
// two commands would parse the same CLI input differently.
func TestApplyFlags_ValueFlagTypesMatchTemplate(t *testing.T) {
	for _, name := range applyValueFlagNames {
		applyFlag := applyCmd.Flags().Lookup(name)
		templateFlag := templateCmd.Flags().Lookup(name)

		if applyFlag == nil || templateFlag == nil {
			t.Fatalf("both apply and template must register --%s", name)
		}

		if applyFlag.Value.Type() != templateFlag.Value.Type() {
			t.Errorf("--%s type mismatch: apply=%s template=%s — the same CLI input would parse differently",
				name, applyFlag.Value.Type(), templateFlag.Value.Type())
		}
	}
}

// TestSetApplyValueOptions_MergesConfigThenCLI pins the merge contract:
// Chart.yaml templateOptions.* form the base layer and apply's CLI flags are
// appended on top. The engine's loadValues applies sources left-to-right, so
// the CLI flags (appended last) must win over the Chart.yaml defaults — the
// same ordering `talm template` uses.
func TestSetApplyValueOptions_MergesConfigThenCLI(t *testing.T) {
	restore := snapshotApplyValueState()
	defer restore()

	Config.RootDir = testProjectRoot
	Config.TemplateOptions.Values = []string{"a=fromconfig"}
	Config.TemplateOptions.StringValues = []string{"s=fromconfig"}
	Config.TemplateOptions.FileValues = []string{"f=config.txt"}
	Config.TemplateOptions.JsonValues = []string{`j={"k":1}`}
	Config.TemplateOptions.LiteralValues = []string{"l=fromconfig"}

	applyCmdFlags.values = []string{"a=fromcli"}
	applyCmdFlags.stringValues = []string{"s=fromcli"}
	applyCmdFlags.fileValues = []string{"f=cli.txt"}
	applyCmdFlags.jsonValues = []string{`j={"k":2}`}
	applyCmdFlags.literalValues = []string{"l=fromcli"}

	opts := buildApplyRenderOptions([]string{testTemplateControlplaneRel}, testProjectRoot+"/secrets.yaml")

	assertConfigThenCLI(t, "Values", opts.Values, "a=fromconfig", "a=fromcli")
	assertConfigThenCLI(t, "StringValues", opts.StringValues, "s=fromconfig", "s=fromcli")
	assertConfigThenCLI(t, "FileValues", opts.FileValues, "f=config.txt", "f=cli.txt")
	assertConfigThenCLI(t, "JsonValues", opts.JsonValues, `j={"k":1}`, `j={"k":2}`)
	assertConfigThenCLI(t, "LiteralValues", opts.LiteralValues, "l=fromconfig", "l=fromcli")
}

// TestSetApplyValueOptions_ResolvesConfigValueFilesAgainstRoot pins that a
// Chart.yaml-declared (relative) value file is anchored on the project root,
// while a CLI --values path is left CWD-relative and appended after.
func TestSetApplyValueOptions_ResolvesConfigValueFilesAgainstRoot(t *testing.T) {
	restore := snapshotApplyValueState()
	defer restore()

	Config.RootDir = testProjectRoot
	Config.TemplateOptions.ValueFiles = []string{"values-secret.encrypted.yaml", "/abs/extra.yaml"}
	applyCmdFlags.valueFiles = []string{"cli-relative.yaml"}

	opts := buildApplyRenderOptions([]string{testTemplateControlplaneRel}, testProjectRoot+"/secrets.yaml")

	want := []string{
		testProjectRoot + "/values-secret.encrypted.yaml", // config-origin relative → joined with root
		"/abs/extra.yaml",   // config-origin absolute → passthrough
		"cli-relative.yaml", // CLI-origin → CWD-relative, appended last
	}
	if !slices.Equal(opts.ValueFiles, want) {
		t.Errorf("ValueFiles resolution mismatch:\n got=%v\nwant=%v", opts.ValueFiles, want)
	}
}

// TestBuildApplyPatchOptions_OmitsValueSources pins that the direct-patch path
// (a non-modelined `-f` file) does NOT carry value sources. That path generates
// the base config from secrets and applies the file as a patch via
// FullConfigProcess — it never renders chart templates, so values have nothing
// to render into. Threading them here would be dead config; values apply only
// on the template-rendering path (buildApplyRenderOptions).
func TestBuildApplyPatchOptions_OmitsValueSources(t *testing.T) {
	restore := snapshotApplyValueState()
	defer restore()

	Config.RootDir = testProjectRoot
	Config.TemplateOptions.ValueFiles = []string{"values-secret.encrypted.yaml"}
	applyCmdFlags.values = []string{"x=1"}

	opts := buildApplyPatchOptions(testProjectRoot + "/secrets.yaml")

	if len(opts.ValueFiles) != 0 {
		t.Errorf("direct-patch path must not carry value files (no chart render); got %v", opts.ValueFiles)
	}
	if len(opts.Values) != 0 {
		t.Errorf("direct-patch path must not carry --set values (no chart render); got %v", opts.Values)
	}
	// The bundle-generation options it DOES need are still present.
	if opts.WithSecrets != testProjectRoot+"/secrets.yaml" {
		t.Errorf("direct-patch path must carry WithSecrets; got %q", opts.WithSecrets)
	}
}

// snapshotApplyValueState saves and restores the package-level Config and
// applyCmdFlags fields these tests mutate, so they do not leak into sibling
// tests that share the same globals.
func snapshotApplyValueState() func() {
	origRoot := Config.RootDir
	origTmpl := Config.TemplateOptions
	origFlags := applyCmdFlags

	return func() {
		Config.RootDir = origRoot
		Config.TemplateOptions = origTmpl
		applyCmdFlags = origFlags
	}
}

func assertConfigThenCLI(t *testing.T, field string, got []string, configVal, cliVal string) {
	t.Helper()

	want := []string{configVal, cliVal}
	if !slices.Equal(got, want) {
		t.Errorf("%s must merge config-first then CLI (so CLI wins in loadValues):\n got=%v\nwant=%v", field, got, want)
	}
}
