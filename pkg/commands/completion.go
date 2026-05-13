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
	"strings"

	"github.com/cozystack/talm/pkg/generated"
	"github.com/cozystack/talm/pkg/modeline"
	"github.com/siderolabs/talos/pkg/machinery/client/config"
	"github.com/spf13/cobra"
)

// Field names reused across completion helpers and the shadow-flags
// map in talosctl_wrapper.go. Hoisted to constants to satisfy
// goconst across the package.
const (
	flagNameNodes     = "nodes"
	flagNameEndpoints = "endpoints"
	yamlExt           = "yaml"
	ymlExt            = "yml"
	nodesDirName      = "nodes"
)

// applyModeOptions enumerates the apply / patch / edit `--mode`
// values upstream talosctl exposes via helpers.AddModeFlags. Pinned
// here rather than imported because upstream's keys live inside a
// per-call map, not a package-level constant — we cannot reflect
// them at completion time without instantiating the Mode flag.
//
//nolint:gochecknoglobals // immutable lookup table used by completeApplyMode at completion time.
var applyModeOptions = []string{"auto", "no-reboot", "reboot", "staged", "try"}

// completePresetNames implements shell completion for the `--preset`
// flag of `talm init`: the available presets are baked into the
// binary at build time, so the completion is deterministic and the
// directive disables file-completion fallback.
func completePresetNames(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	presets, err := generated.AvailablePresets()
	if err != nil {
		// Surfaces no completions plus the error so the shell
		// does not silently fall back to file completion.
		return nil, cobra.ShellCompDirectiveError
	}

	return presets, cobra.ShellCompDirectiveNoFileComp
}

// completeApplyMode implements shell completion for the `--mode`
// flag of `talm apply`. Fixed enum, no file fallback.
func completeApplyMode(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	return applyModeOptions, cobra.ShellCompDirectiveNoFileComp
}

// completeYAMLFiles implements shell completion for flags that
// accept YAML file paths (`-f / --file`, `--values`, `-t / --template`,
// `--with-secrets`). The directive narrows the file-completion
// fallback to `*.yaml` and `*.yml`.
func completeYAMLFiles(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	return []string{yamlExt, ymlExt}, cobra.ShellCompDirectiveFilterFileExt
}

// completeNodeFiles is the ValidArgsFunction for `apply` / `template`
// / `upgrade`'s positional file argument. Walks `nodes/` under the
// detected project root, filters to YAML files whose first line is
// a talm modeline. Operator types `talm upgrade <TAB>` and gets the
// list of modelined node files, not every yaml in the project.
func completeNodeFiles(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	root := Config.RootDir
	if root == "" {
		// Pre-PreRunE invocation (cobra's __complete path may
		// fire before our root detection runs). Best-effort
		// fallback: walk from CWD.
		cwd, err := os.Getwd()
		if err != nil {
			return nil, cobra.ShellCompDirectiveError
		}

		root = cwd
	}

	nodesDir := filepath.Join(root, nodesDirName)

	entries, err := os.ReadDir(nodesDir)
	if err != nil {
		// `nodes/` may not exist on a brand-new project; fall
		// back to default file completion so the operator can
		// still pick files manually.
		return []string{yamlExt, ymlExt}, cobra.ShellCompDirectiveFilterFileExt
	}

	var matches []string

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		if !strings.HasSuffix(name, "."+yamlExt) && !strings.HasSuffix(name, "."+ymlExt) {
			continue
		}

		path := filepath.Join(nodesDirName, name)
		if !strings.HasPrefix(path, toComplete) && toComplete != "" {
			continue
		}

		// FindAndParseModeline accepts leading operator comments per
		// #178; using a strict first-line-only parser would hide
		// files that `talm template -I` just produced from
		// `talm apply <TAB>` / `talm upgrade <TAB>`. Both
		// ErrModelineNotFound and malformed-modeline parse errors
		// drop the candidate from the completion list — completion
		// must not block on individual-file parse failures, and a
		// malformed modeline is still not a useful tab target.
		if _, _, err := modeline.FindAndParseModeline(filepath.Join(root, path)); err != nil {
			continue
		}

		matches = append(matches, path)
	}

	return matches, cobra.ShellCompDirectiveNoFileComp
}

// CompleteTalosconfigNodes is the exported entry point for
// completion of the root `--nodes` persistent flag (#170). main.go
// wires it via cobra.RegisterFlagCompletionFunc.
//
//nolint:gochecknoglobals // package-level var holding the closure produced by completeTalosconfigField; the alternative (per-call construction) re-allocates on every shell completion invocation.
var CompleteTalosconfigNodes = completeTalosconfigField(flagNameNodes)

// CompleteTalosconfigEndpoints is the exported entry point for
// completion of the root `--endpoints` persistent flag (#170).
//
//nolint:gochecknoglobals // see CompleteTalosconfigNodes.
var CompleteTalosconfigEndpoints = completeTalosconfigField(flagNameEndpoints)

// completeTalosconfigField builds the completion function for the
// `--nodes` / `--endpoints` root persistent flags. Loads the
// in-scope talosconfig and returns the union of the requested field
// across every context. Closes over `field` so the call sites stay
// one-liners.
//
// `field` must be "nodes" or "endpoints"; any other value yields an
// empty completion list (caller error).
func completeTalosconfigField(field string) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		path := resolveTalosconfigPathForCompletion()
		if path == "" {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		cfg, err := config.Open(path)
		if err != nil {
			return nil, cobra.ShellCompDirectiveError
		}

		seen := map[string]struct{}{}
		out := []string{}

		for _, ctx := range cfg.Contexts {
			var values []string

			switch field {
			case flagNameNodes:
				values = ctx.Nodes
			case flagNameEndpoints:
				values = ctx.Endpoints
			default:
				return nil, cobra.ShellCompDirectiveError
			}

			for _, item := range values {
				if _, dup := seen[item]; dup {
					continue
				}

				seen[item] = struct{}{}
				out = append(out, item)
			}
		}

		return out, cobra.ShellCompDirectiveNoFileComp
	}
}

// resolveTalosconfigPathForCompletion returns the talosconfig path
// the completion should read. Mirrors the in-process precedence:
//  1. --talosconfig flag (already in GlobalArgs.Talosconfig)
//  2. $TALOSCONFIG env var (cobra parses the flag default into
//     GlobalArgs.Talosconfig only if the operator passed --talosconfig
//     at invocation; for completion we have to consult the env
//     explicitly)
//  3. project-local talosconfig (Config.RootDir/talosconfig)
//
// Returns the empty string if no candidate is readable. Completion
// callers translate empty into an empty completion list rather than
// an error — completion must not block on missing files.
func resolveTalosconfigPathForCompletion() string {
	if GlobalArgs.Talosconfig != "" {
		if _, err := os.Stat(GlobalArgs.Talosconfig); err == nil {
			return GlobalArgs.Talosconfig
		}
	}

	if env := os.Getenv("TALOSCONFIG"); env != "" {
		// $TALOSCONFIG is operator-controlled, not network-tainted;
		// the lint is a false positive for env-derived paths.
		if _, err := os.Stat(env); err == nil { //nolint:gosec // G304: operator-supplied env, not network-tainted
			return env
		}
	}

	if Config.RootDir != "" {
		candidate := filepath.Join(Config.RootDir, "talosconfig")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}

	return ""
}
