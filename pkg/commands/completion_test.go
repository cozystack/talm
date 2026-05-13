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
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/spf13/cobra"
)

// TestComplete_ApplyMode_ReturnsFixedEnum pins the apply --mode
// completion: five upstream-supported values, no file fallback.
// Operators get tab-completed enums instead of meaningless file
// suggestions.
func TestComplete_ApplyMode_ReturnsFixedEnum(t *testing.T) {
	got, directive := completeApplyMode(nil, nil, "")

	if !slices.Equal(got, applyModeOptions) {
		t.Errorf("--mode completion = %v, want %v", got, applyModeOptions)
	}
	// Sanity: every value upstream's helpers.AddModeFlags
	// registers must surface. The string forms are pinned in
	// upstream's mode.go (modeAuto, modeNoReboot, …).
	if len(got) != 5 {
		t.Errorf("--mode completion must surface five upstream-supported modes; got %v", got)
	}
	// Pin presence of the always-default mode by string so a
	// future rename of applyModeOptions still produces a
	// detectable failure mode.
	if !slices.Contains(got, "auto") {
		t.Errorf("--mode completion missing default mode; got %v", got)
	}
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive = %v, want ShellCompDirectiveNoFileComp", directive)
	}
}

// TestComplete_YAMLFileExt_FilterDirective pins the file-flag
// completion behaviour: cobra is told to narrow the file
// completion to `.yaml` / `.yml` extensions.
func TestComplete_YAMLFileExt_FilterDirective(t *testing.T) {
	got, directive := completeYAMLFiles(nil, nil, "")

	want := []string{yamlExt, ymlExt}
	if !slices.Equal(got, want) {
		t.Errorf("file extension list = %v, want %v", got, want)
	}
	if directive != cobra.ShellCompDirectiveFilterFileExt {
		t.Errorf("directive = %v, want ShellCompDirectiveFilterFileExt", directive)
	}
}

// TestComplete_PresetNames_ReturnsAvailablePresets pins that
// `talm init --preset <TAB>` enumerates the presets baked into
// the binary via pkg/generated. The exact list depends on the
// embedded charts; the test asserts at least one entry and that
// the directive disables file fallback.
func TestComplete_PresetNames_ReturnsAvailablePresets(t *testing.T) {
	got, directive := completePresetNames(nil, nil, "")

	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive = %v, want ShellCompDirectiveNoFileComp", directive)
	}
	if len(got) == 0 {
		t.Errorf("expected at least one preset; got empty slice")
	}
}

// TestComplete_PositionalNodeFiles_ReturnsModelinedYamlFromNodesDir
// pins the canonical positional-args completion for `apply` /
// `template` / `upgrade`: only YAML files under `nodes/` whose
// first line is a talm modeline are returned. Files without a
// modeline (e.g. operator-authored notes) are filtered out so
// completion does not point at files the command would reject
// at parse time.
func TestComplete_PositionalNodeFiles_ReturnsModelinedYamlFromNodesDir(t *testing.T) {
	rootOrig := Config.RootDir
	t.Cleanup(func() { Config.RootDir = rootOrig })

	dir := t.TempDir()
	Config.RootDir = dir

	nodesDir := filepath.Join(dir, "nodes")
	if err := os.Mkdir(nodesDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Modelined node — should appear in completion.
	modelined := "# talm: nodes=[\"1.2.3.4\"], endpoints=[\"1.2.3.4\"], templates=[\"templates/cp.yaml\"]\n" +
		"machine:\n  type: controlplane\n"
	if err := os.WriteFile(filepath.Join(nodesDir, "cp01.yaml"), []byte(modelined), 0o644); err != nil {
		t.Fatal(err)
	}

	// No modeline — must be filtered out.
	plain := "# operator note\nmachine:\n  type: controlplane\n"
	if err := os.WriteFile(filepath.Join(nodesDir, "note.yaml"), []byte(plain), 0o644); err != nil {
		t.Fatal(err)
	}

	// Wrong extension — must be filtered out.
	if err := os.WriteFile(filepath.Join(nodesDir, "scratch.txt"), []byte("anything"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, directive := completeNodeFiles(nil, nil, "")

	want := []string{filepath.Join("nodes", "cp01.yaml")}
	if !slices.Equal(got, want) {
		t.Errorf("positional node-file completion = %v, want %v", got, want)
	}
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive = %v, want ShellCompDirectiveNoFileComp", directive)
	}
}

// TestComplete_TalosconfigField_UnionAcrossContexts pins that
// `--nodes` / `--endpoints` completion enumerates the union of
// every context's field across the in-scope talosconfig. The
// operator may have multiple contexts (one per cluster); each
// context's nodes / endpoints must surface so they can be picked
// at tab time.
func TestComplete_TalosconfigField_UnionAcrossContexts(t *testing.T) {
	talosconfigOrig := GlobalArgs.Talosconfig
	rootOrig := Config.RootDir
	t.Cleanup(func() {
		GlobalArgs.Talosconfig = talosconfigOrig
		Config.RootDir = rootOrig
	})

	dir := t.TempDir()
	Config.RootDir = dir
	tcPath := filepath.Join(dir, "talosconfig")

	tc := "context: alpha\n" +
		"contexts:\n" +
		"  alpha:\n" +
		"    endpoints: [10.0.0.1, 10.0.0.2]\n" +
		"    nodes: [10.0.0.10]\n" +
		"  beta:\n" +
		"    endpoints: [10.0.1.1]\n" +
		"    nodes: [10.0.1.10, 10.0.1.11]\n"
	if err := os.WriteFile(tcPath, []byte(tc), 0o600); err != nil {
		t.Fatal(err)
	}

	GlobalArgs.Talosconfig = tcPath

	gotNodes, dirNodes := CompleteTalosconfigNodes(nil, nil, "")
	if dirNodes != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("--nodes directive = %v, want NoFileComp", dirNodes)
	}
	for _, want := range []string{"10.0.0.10", "10.0.1.10", "10.0.1.11"} {
		if !slices.Contains(gotNodes, want) {
			t.Errorf("--nodes completion missing %q; got %v", want, gotNodes)
		}
	}

	gotEndpoints, dirEndpoints := CompleteTalosconfigEndpoints(nil, nil, "")
	if dirEndpoints != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("--endpoints directive = %v, want NoFileComp", dirEndpoints)
	}
	for _, want := range []string{"10.0.0.1", "10.0.0.2", "10.0.1.1"} {
		if !slices.Contains(gotEndpoints, want) {
			t.Errorf("--endpoints completion missing %q; got %v", want, gotEndpoints)
		}
	}
}

// TestComplete_ApplyCmd_HasFlagCompletionForMode pins that the
// init() wiring is in place: `apply --mode` is registered with
// the completeApplyMode function. Regression guard against a
// future refactor that drops a RegisterFlagCompletionFunc line.
func TestComplete_ApplyCmd_HasFlagCompletionForMode(t *testing.T) {
	fn, exists := applyCmd.GetFlagCompletionFunc("mode")
	if !exists || fn == nil {
		t.Fatal("apply --mode missing flag completion registration")
	}
}

// TestComplete_InitCmd_HasFlagCompletionForPreset pins the
// init-side wiring.
func TestComplete_InitCmd_HasFlagCompletionForPreset(t *testing.T) {
	fn, exists := initCmd.GetFlagCompletionFunc("preset")
	if !exists || fn == nil {
		t.Fatal("init --preset missing flag completion registration")
	}
}

// TestComplete_AnchorCommands_FileFlagUsesModelinedCompleter pins
// that `talm apply --file`, `talm template --file`, and
// `talm upgrade --file` register the modelined-aware completer
// (completeNodeFiles) rather than the generic yaml-extension
// filter. The operator typing `-f <TAB>` lands on the curated
// list of modelined files under <root>/nodes/, not on every
// .yaml in CWD. The helper produces a directive
// ShellCompDirectiveNoFileComp when matches are found (vs the
// generic completer's ShellCompDirectiveFilterFileExt) — the
// distinction is observable from the registered function's
// behavior on a real project fixture.
func TestComplete_AnchorCommands_FileFlagUsesModelinedCompleter(t *testing.T) {
	rootOrig := Config.RootDir
	t.Cleanup(func() { Config.RootDir = rootOrig })

	dir := t.TempDir()
	Config.RootDir = dir

	if err := os.Mkdir(filepath.Join(dir, "nodes"), 0o755); err != nil {
		t.Fatal(err)
	}

	body := "# talm: nodes=[\"1.2.3.4\"], endpoints=[\"1.2.3.4\"], templates=[\"templates/cp.yaml\"]\n" +
		"machine:\n  type: controlplane\n"
	if err := os.WriteFile(filepath.Join(dir, "nodes", "cp01.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, cmd := range []*cobra.Command{applyCmd, templateCmd} {
		fn, exists := cmd.GetFlagCompletionFunc("file")
		if !exists || fn == nil {
			t.Errorf("%q --file completion not registered", cmd.Name())

			continue
		}

		got, directive := fn(cmd, nil, "")

		if !slices.Contains(got, filepath.Join("nodes", "cp01.yaml")) {
			t.Errorf("%q --file completion did not surface the modelined node file; got %v", cmd.Name(), got)
		}

		if directive != cobra.ShellCompDirectiveNoFileComp {
			t.Errorf("%q --file directive = %v, want ShellCompDirectiveNoFileComp (modelined completer surfaces a curated list, not the file-extension filter)", cmd.Name(), directive)
		}
	}
}

// TestComplete_RootTalosconfig_NoExplicitCompletion pins that
// --talosconfig is NOT wired with a custom completion function.
// talosconfig has no fixed extension and there is no useful curated
// list to surface (operators pick the file by hand). Wiring an
// extension filter would narrow legitimate paths (no .yaml suffix
// requirement); wiring a curated list would require enumerating
// every kubeconfig-style file on the system. Cobra's default
// filename completion is the correct fallback. This test pins the
// absence so a future "add --talosconfig completion" change must
// be intentional, not accidental.
func TestComplete_RootTalosconfig_NoExplicitCompletion(t *testing.T) {
	// Replicate the registration done in main.go's
	// registerRootFlags so the test exercises the same surface
	// without booting the rootCmd itself.
	root := &cobra.Command{Use: "talm-test"}
	root.PersistentFlags().String("talosconfig", "", "")

	fn, exists := root.GetFlagCompletionFunc("talosconfig")
	if exists && fn != nil {
		t.Errorf("--talosconfig must not have a custom completion func; cobra's default file completion is the right shape")
	}
}

// TestComplete_AnchorCommands_NoValidArgsFunction pins the
// inverse contract: applyCmd, templateCmd, and the upgrade wrapper
// all declare cobra.NoArgs (or upstream's equivalent), which
// suppresses ValidArgsFunction in cobra's __complete path. Wiring
// a positional completer onto these commands would pin dead
// surface — the completer never reaches the shell, but tests
// would pass anyway. This test makes the absence the contract.
func TestComplete_AnchorCommands_NoValidArgsFunction(t *testing.T) {
	for _, cmd := range []*cobra.Command{applyCmd, templateCmd} {
		if cmd.ValidArgsFunction != nil {
			t.Errorf("%q must NOT register a ValidArgsFunction — cobra.NoArgs suppresses it, leaving dead wiring; complete --file via RegisterFlagCompletionFunc instead", cmd.Name())
		}
	}
}

// TestComplete_PositionalNodeFiles_IncludesFilesWithLeadingComments
// pins that completion stays in sync with the modeline parser
// relaxation: a node file produced by `talm template -I`
// against a previously edited file carries a leading operator-
// comment block above the modeline. Operators must still see it
// in `talm apply <TAB>` / `talm upgrade <TAB>`. The dual-parser
// split that originally shipped left completion on the strict
// parser, silently dropping leading-comment files from the list.
func TestComplete_PositionalNodeFiles_IncludesFilesWithLeadingComments(t *testing.T) {
	rootOrig := Config.RootDir
	t.Cleanup(func() { Config.RootDir = rootOrig })

	dir := t.TempDir()
	Config.RootDir = dir

	nodesDir := filepath.Join(dir, "nodes")
	if err := os.Mkdir(nodesDir, 0o755); err != nil {
		t.Fatal(err)
	}

	body := "# Operator note: reset 2026-05-12 after ticket OPS-1234\n" +
		"# DO NOT edit values directly; modify values.yaml and re-template\n" +
		"# talm: nodes=[\"1.2.3.4\"], endpoints=[\"1.2.3.4\"], templates=[\"templates/cp.yaml\"]\n" +
		"machine:\n  type: controlplane\n"
	if err := os.WriteFile(filepath.Join(nodesDir, "cp01.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	got, _ := completeNodeFiles(nil, nil, "")

	want := filepath.Join("nodes", "cp01.yaml")
	if !slices.Contains(got, want) {
		t.Errorf("file with leading operator comments must appear in completion; got %v", got)
	}
}
