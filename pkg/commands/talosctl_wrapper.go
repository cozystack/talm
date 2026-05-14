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
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/cockroachdb/errors"
	taloscommands "github.com/siderolabs/talos/cmd/talosctl/cmd/talos"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"k8s.io/client-go/tools/clientcmd"
)

// crashdumpCmdName labels the upstream crashdump subcommand both
// inside the wrapper dispatch and in test fixtures that synthesise
// a wrapped crashdump. Hoisted so a future rename touches one site.
const crashdumpCmdName = "crashdump"

// rotateCACmdName labels the upstream rotate-ca subcommand in the
// dispatch + tests. Mirrors crashdumpCmdName's hoist rationale.
const rotateCACmdName = "rotate-ca"

// rootShadowedPersistentFlags is the set of flag names whose
// upstream PersistentFlag registrations must be dropped by the
// propagation pass. Two motivating categories:
//
//  1. **talm-root duplicates.** talm's own `registerRootFlags`
//     already registers --talosconfig, --root, --context, --nodes,
//     --endpoints, --cluster, --skip-verify, --version bound to
//     `commands.GlobalArgs.<X>`. Upstream's `addCommand` pattern
//     (see siderolabs/talos cmd/talosctl/cmd/talos/root.go)
//     registers the same names as PersistentFlags on every
//     top-level command it imports, but bound to
//     `taloscommands.GlobalArgs.<X>`. Propagating those would
//     shadow talm's bindings — pflag would write to upstream's
//     variables, then the wrapper's PreRunE sync
//     (`taloscommands.GlobalArgs = commands.GlobalArgs`) would
//     OVERWRITE the just-parsed value with talm's (empty) one.
//     Operators see "nodes are not set for the command" after
//     passing --nodes.
//  2. **Intentional drops.** Some upstream persistent flags bind
//     to `taloscommands.GlobalArgs` fields that talm does not
//     model (e.g. SideroV1KeysDir). Letting them propagate would
//     accept the flag, parse it into the upstream side, then have
//     the sync wipe it — silent acceptance with no effect.
//     Dropping forces a clean "unknown flag" error so the operator
//     knows the option isn't supported in talm.
//
// Either way, the propagation skip is correct. Other upstream
// persistent flags (--namespace on imageCmd, --insecure on
// metaCmd, --overlays on image-talos-bundle, etc.) continue to
// propagate normally — they have no talm-root counterpart, bind
// to upstream's own variables which are read at upstream RunE
// time, and the sync direction doesn't disturb them.
//
//nolint:gochecknoglobals // package-level map keyed by name; cheap O(1) lookup in the propagation hot path.
var rootShadowedPersistentFlags = map[string]struct{}{
	talosconfigName:   {},
	"root":            {},
	"context":         {},
	flagNameNodes:     {},
	flagNameEndpoints: {},
	"cluster":         {},
	"skip-verify":     {},
	"version":         {},
	// siderov1-keys-dir is registered by upstream addCommand into
	// taloscommands.GlobalArgs.SideroV1KeysDir, but talm does not
	// model that auth path and never populates the field. If we
	// let the upstream registration propagate, the parsed value
	// would be wiped by the wrapper PreRunE's struct sync
	// (`taloscommands.GlobalArgs = commands.GlobalArgs`) the same
	// way --nodes was. Shadowing here means `talm <subcmd>
	// --siderov1-keys-dir` errors with "unknown flag" — clean
	// refusal instead of silent acceptance + drop.
	"siderov1-keys-dir": {},
}

// renameFlagShorthand clones an upstream flag with its shorthand
// replaced by `newShorthand`. Every pflag.Flag metadata field —
// including ShorthandDeprecated, which controls upstream's deprecation
// warning behaviour — is mirrored so downstream consumers that
// introspect the wrapped flag observe the same shape as the original.
// Value is intentionally NOT deep-copied: the parser must write to
// upstream's variable so the wrapped RunE (assigned from upstream)
// can still read its own state.
func renameFlagShorthand(flag *pflag.Flag, newShorthand string) *pflag.Flag {
	return &pflag.Flag{
		Name:                flag.Name,
		Usage:               flag.Usage,
		Value:               flag.Value,
		DefValue:            flag.DefValue,
		Changed:             flag.Changed,
		NoOptDefVal:         flag.NoOptDefVal,
		Deprecated:          flag.Deprecated,
		Hidden:              flag.Hidden,
		Shorthand:           newShorthand,
		ShorthandDeprecated: flag.ShorthandDeprecated,
		Annotations:         flag.Annotations,
	}
}

// propagatePersistentFlags mirrors the upstream command's persistent
// flags onto the wrapped command's PersistentFlags so cobra's
// mergePersistentFlags walks them through to wrapped children at
// parse time. Skips talm-root-shadowed names so the wrapper PreRunE's
// `taloscommands.GlobalArgs = commands.GlobalArgs` sync direction
// stays correct (see rootShadowedPersistentFlags godoc for the
// failure mode). The -f → -F shorthand rename mirrors the local-flag
// loop's treatment of the same collision class.
func propagatePersistentFlags(cmd, wrappedCmd *cobra.Command) {
	cmd.PersistentFlags().VisitAll(func(flag *pflag.Flag) {
		if _, shadowed := rootShadowedPersistentFlags[flag.Name]; shadowed {
			return
		}

		if flag.Shorthand == "f" {
			wrappedCmd.PersistentFlags().AddFlag(renameFlagShorthand(flag, "F"))

			return
		}

		wrappedCmd.PersistentFlags().AddFlag(flag)
	})
}

// wrapTalosCommand wraps a talosctl command to add --file flag support.
//
//nolint:gocognit,gocyclo,cyclop,funlen // cobra wrapper for talosctl forward; branching over (--insecure, --talosconfig override, file/template flags, modeline) is each one short branch.
func wrapTalosCommand(cmd *cobra.Command, cmdName string) *cobra.Command {
	// Create a copy of the command to avoid modifying the original
	wrappedCmd := &cobra.Command{
		Use:                cmd.Use,
		Short:              cmd.Short,
		Long:               cmd.Long,
		Example:            cmd.Example,
		Aliases:            cmd.Aliases,
		SuggestFor:         cmd.SuggestFor,
		Args:               cmd.Args,
		ValidArgsFunction:  cmd.ValidArgsFunction,
		RunE:               cmd.RunE,
		Run:                cmd.Run,
		DisableFlagParsing: cmd.DisableFlagParsing,
		TraverseChildren:   cmd.TraverseChildren,
	}

	// Copy all flags from original command and handle -f flag conflict
	fileFlagExists := false

	cmd.Flags().VisitAll(func(flag *pflag.Flag) {
		// Check if this is the --file flag
		if flag.Name == "file" {
			fileFlagExists = true
		}

		// If this flag has shorthand 'f', we need to change it to 'F'
		if flag.Shorthand == "f" {
			wrappedCmd.Flags().AddFlag(renameFlagShorthand(flag, "F"))
		} else {
			wrappedCmd.Flags().AddFlag(flag)
		}
	})

	// Also copy persistent flags so children of this wrapped command
	// inherit them through cobra's standard mergePersistentFlags() at
	// parse time. cmd.Flags() above enumerates only the LOCAL flag set
	// — persistent flags from the upstream parent's PersistentFlags()
	// are stored separately and merged at runtime via VisitParents.
	// Without this pass the wrapped tree loses every parent-registered
	// persistent flag (--namespace on imageCmd, --insecure on metaCmd,
	// and the rest of the ~25 flags affected by the dropped chain).
	//
	// The same -f → -F shorthand rename applies: defensive against a
	// future upstream that registers a persistent flag with shorthand
	// "f". Today no such upstream flag exists, but the cost is one
	// branch and the surface stays uniform with the local-flag loop.
	propagatePersistentFlags(cmd, wrappedCmd)

	// Add --file flag only if it doesn't already exist in the original command
	var configFiles []string

	if !fileFlagExists {
		// Double-check that the flag doesn't exist after copying
		if wrappedCmd.Flags().Lookup("file") == nil {
			wrappedCmd.Flags().StringSliceVarP(&configFiles, "file", "f", nil, "specify config files or patches in a YAML file (can specify multiple)")
		}
	}
	// Note: If --file flag already exists, we'll get its value in PreRunE via cmd.Flags().GetStringSlice("file")

	// Wrap PreRunE to process modeline files and sync GlobalArgs
	originalPreRunE := cmd.PreRunE
	wrappedCmd.PreRunE = func(cmd *cobra.Command, args []string) error {
		// Special handling for kubeconfig command: set default values for --merge and --force
		baseCmdName := cmdName
		if idx := strings.Index(cmdName, " "); idx > 0 {
			baseCmdName = cmdName[:idx]
		}

		if baseCmdName == defaultKubeconfigName {
			if !cmd.Flags().Changed("force") && cmd.Flags().Lookup("force") != nil {
				//nolint:wrapcheck // pflag.Set typed error surfaced verbatim.
				if err := cmd.Flags().Set("force", "true"); err != nil {
					return err
				}
			}
		}

		nodesFromArgs := len(GlobalArgs.Nodes) > 0
		endpointsFromArgs := len(GlobalArgs.Endpoints) > 0

		// Get config files from flag (either our new flag or existing talosctl flag)
		var filesToProcess []string
		if len(configFiles) > 0 {
			// Use our variable if flag was added by us
			filesToProcess = configFiles
		} else if fileFlag := cmd.Flags().Lookup("file"); fileFlag != nil {
			// Get value from existing flag
			if fileFlagValue, err := cmd.Flags().GetStringSlice("file"); err == nil {
				filesToProcess = fileFlagValue
			}
		}

		// Expand directories to YAML files
		expandedFiles, err := ExpandFilePaths(filesToProcess)
		if err != nil {
			return err
		}

		// Detect root from files if specified, otherwise fallback to cwd
		if err := DetectAndSetRootFromFiles(expandedFiles); err != nil {
			return err
		}

		for _, configFile := range expandedFiles {
			if _, err := processModelineAndUpdateGlobals(configFile, nodesFromArgs, endpointsFromArgs, false); err != nil {
				return err
			}
		}

		// Ensure talosconfig path is set to project root if not explicitly set via flag
		EnsureTalosconfigPath(cmd)

		// Sync GlobalArgs to talosctl commands
		taloscommands.GlobalArgs = GlobalArgs

		// Note: --skip-verify is now supported for all commands via GlobalArgs.SkipVerify

		if originalPreRunE != nil {
			return originalPreRunE(cmd, args)
		}

		return nil
	}

	// Extract base command name for comparison
	baseCmdName := cmdName
	if idx := strings.Index(cmdName, " "); idx > 0 {
		baseCmdName = cmdName[:idx]
	}

	originalRunE := wrappedCmd.RunE

	// Special handling for kubeconfig command
	if baseCmdName == defaultKubeconfigName {
		wrapKubeconfigCommand(wrappedCmd, originalRunE)
	}

	// Special handling for upgrade command
	if baseCmdName == upgradeCmdName {
		wrapUpgradeCommand(wrappedCmd, originalRunE)
	}

	// Special handling for rotate-ca command
	if baseCmdName == rotateCACmdName {
		wrapRotateCACommand(wrappedCmd, originalRunE)
	}

	// Special handling for crashdump: upstream pre-validates
	// GlobalArgs.Nodes before its own RunE runs, but crashdump's
	// documented shape is `--init-node` / `--control-plane-nodes` /
	// `--worker-nodes` rather than `--nodes`. Populate GlobalArgs.Nodes
	// from those per-class flags so the upstream guard is satisfied
	// and the operator-facing deprecation message can surface.
	if baseCmdName == crashdumpCmdName {
		wrapCrashdumpCommand(wrappedCmd)
	}

	// Special handling for the interactive-only commands
	// (dashboard, edit): refuse non-tty stdin up front so the
	// operator gets a clear hint instead of a no-output failure.
	// dashboard would panic in tcell teardown; edit would hang in
	// the kubectl external-editor helper. See wrapTUICommand
	// godoc for the per-command rationale.
	if baseCmdName == dashboardCmdName || baseCmdName == editCmdName {
		wrapTUICommand(wrappedCmd, baseCmdName)
	}

	// Special handling for reset: flip talm's default to the
	// META-preserving selective wipe when the operator passed no
	// wipe-related flags. Upstream's --wipe-mode=all destroys META
	// and prevents self-recovery; the safer default matches the
	// recipe operators reach for anyway. See wrapResetCommand
	// godoc for the precedence rules.
	if baseCmdName == resetCmdName {
		wrapResetCommand(wrappedCmd)
	}

	// Copy all subcommands
	for _, subCmd := range cmd.Commands() {
		wrappedCmd.AddCommand(wrapTalosCommand(subCmd, subCmd.Name()))
	}

	return wrappedCmd
}

func init() {
	// Import all commands from talosctl package, except those in the exclusion list
	// Commands to exclude (these are talm-specific or should not be exposed)
	excludedCommands := map[string]bool{
		"apply-config":  true, // talm has its own apply command
		"config":        true, // talm manages config differently
		"patch":         true, // not needed in talm
		"upgrade-k8s":   true, // not needed in talm
		dmesgCmdName:    true, // retired upstream (siderolabs/talos#13333); talm registers a hidden migration stub pointing at `talm logs kernel --tail=N`
		talosconfigName: true, // talm has its own talosconfig command
	}

	// Import and wrap each command from talosctl
	for _, cmd := range taloscommands.Commands {
		// Extract base command name (without arguments)
		baseName := cmd.Use
		if idx := len(baseName); idx > 0 {
			// Remove arguments from command name (everything after first space)
			for i, r := range baseName {
				if r == ' ' || r == '<' || r == '[' {
					baseName = baseName[:i]

					break
				}
			}
		}

		// Skip excluded commands
		if excludedCommands[baseName] {
			continue
		}

		// Wrap the command and add it to our commands list
		// Note: wrapTalosCommand recursively processes all subcommands, so they already have
		// the -f flag handling, --file flag (if needed), and PreRunE set automatically
		wrappedCmd := wrapTalosCommand(cmd, baseName)
		// Keep the original command name from talosctl
		addCommand(wrappedCmd)
	}
}

// normalizeEndpoint normalizes an endpoint by removing any existing
// port and protocol, then adding https:// and :6443. IPv6 literals
// are bracketed per RFC 3986 §3.2.2 (the assembly uses
// net.JoinHostPort which auto-bracket-wraps any host that contains
// a colon).
//
// Examples:
//   - "1.2.3.4"                       -> "https://1.2.3.4:6443"
//   - "1.2.3.4:50000"                 -> "https://1.2.3.4:6443"
//   - "https://1.2.3.4:50000"         -> "https://1.2.3.4:6443"
//   - "http://1.2.3.4"                -> "https://1.2.3.4:6443"
//   - "node.example.com"              -> "https://node.example.com:6443"
//   - "[2001:db8::1]:6443"            -> "https://[2001:db8::1]:6443"
//   - "[2001:db8::1]"                 -> "https://[2001:db8::1]:6443"
func normalizeEndpoint(endpoint string) string {
	// Remove protocol if present
	endpoint = strings.TrimPrefix(endpoint, "https://")
	endpoint = strings.TrimPrefix(endpoint, "http://")

	// Split host and port. net.SplitHostPort strips IPv6 brackets,
	// so the host returned may be a bare IPv6 literal that needs
	// re-bracketing for the URL form.
	host, _, err := net.SplitHostPort(endpoint)
	if err != nil {
		// No port in endpoint. Strip an outer pair of brackets if
		// present so the JoinHostPort below adds them back exactly
		// once for IPv6 literals.
		host = strings.TrimPrefix(strings.TrimSuffix(endpoint, "]"), "[")
	}

	// Use net.JoinHostPort to assemble — it bracketed IPv6 hosts
	// automatically (per RFC 3986 §3.2.2). Without this an IPv6
	// endpoint produced an unparseable URL like
	// "https://2001:db8::1:6443" instead of "https://[2001:db8::1]:6443".
	return "https://" + net.JoinHostPort(host, "6443")
}

// updateKubeconfigServer updates the server field in all clusters of the kubeconfig file.
func updateKubeconfigServer(kubeconfigPath, endpoint string) error {
	// Load kubeconfig
	config, err := clientcmd.LoadFromFile(kubeconfigPath)
	if err != nil {
		return errors.Wrap(err, "failed to load kubeconfig")
	}

	// Normalize endpoint
	normalizedEndpoint := normalizeEndpoint(endpoint)

	// Update server for all clusters
	updated := false

	for clusterName, cluster := range config.Clusters {
		if cluster.Server != normalizedEndpoint {
			cluster.Server = normalizedEndpoint
			updated = true

			fmt.Fprintf(os.Stderr, "Updated cluster %s server to %s\n", clusterName, normalizedEndpoint)
		}
	}

	// Save kubeconfig if updated
	if updated {
		if err := clientcmd.WriteToFile(*config, kubeconfigPath); err != nil {
			return errors.Wrap(err, "failed to write kubeconfig")
		}
	}

	return nil
}

// addToGitignore adds an entry to .gitignore if it doesn't already exist.
func addToGitignore(entry string) error {
	gitignoreFile := filepath.Join(Config.RootDir, ".gitignore")

	// Read existing .gitignore if it exists
	var content string

	if _, err := os.Stat(gitignoreFile); err == nil {
		existingContent, err := os.ReadFile(gitignoreFile)
		if err != nil {
			return errors.Wrap(err, "failed to read .gitignore")
		}

		content = string(existingContent)

		// Check if entry already exists
		lines := strings.SplitSeq(content, "\n")
		for line := range lines {
			line = strings.TrimSpace(line)
			if line == entry || strings.HasPrefix(line, entry+"/") {
				return nil // Already exists
			}
		}
	}

	// Append entry
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}

	content += entry + "\n"

	// Write back
	return os.WriteFile(gitignoreFile, []byte(content), 0o644) //nolint:gosec,mnd,wrapcheck // talosconfig is intentionally readable by sibling tooling.
}
