package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/cockroachdb/errors"
	"gopkg.in/yaml.v3"

	"github.com/cozystack/talm/pkg/commands"
	_ "github.com/siderolabs/talos/cmd/talosctl/acompat"
	"github.com/siderolabs/talos/cmd/talosctl/cmd/common"
	"github.com/siderolabs/talos/pkg/machinery/constants"
	"github.com/spf13/cobra"
)

const (
	// initSubcommandName is the cobra subcommand that creates the
	// project Chart.yaml and the init-time prefix check in main()
	// branches on.
	initSubcommandName = "init"
	// completionSubcommand is cobra's user-facing shell-completion
	// subcommand (talm completion bash | zsh | fish).
	completionSubcommand = "completion"
	// completionInternal is cobra's reserved internal subcommand
	// name driving Tab-key autocompletion. Constant because it
	// appears both in skipConfigCommands and in cobra's exported
	// API.
	completionInternal = "__complete"
	// dmesgSubcommandName labels the hidden migration stub for the
	// retired `talm dmesg` command. The stub errors with a hint
	// pointing at `talm logs kernel --tail=N`; it must skip
	// Chart.yaml loading so the migration hint surfaces even when
	// the operator runs it outside a talm project.
	dmesgSubcommandName = "dmesg"
)

// cmdNameTalm is the binary name used both as the cobra root
// command's Use field and via -X main.cmdNameTalm in build tooling.
const cmdNameTalm = "talm"

// Version is the talm build version baked in at link time via ldflags
// (`-X main.Version=...`). The two release build paths inject different
// forms: goreleaser strips the leading "v" (e.g. `0.27.0`, from
// `{{.Version}}`) while the Makefile's `git describe --tags` keeps it
// (e.g. `v0.27.0`). releaseVersion normalizes both. The literal "dev"
// here is the local source-build fallback.
//
//nolint:gochecknoglobals // ldflags-injected build version, idiomatic for go release tooling.
var Version = devVersion

// devVersion is the build-version sentinel for a local source build (no
// ldflags injection). releaseVersion treats it as "not a release", so
// release-only behavior such as the chart-drift check stays off.
const devVersion = "dev"

// strictChartsFlag is bound to the --strict-charts persistent flag. When set
// (or when Chart.yaml carries strictCharts: true), a content difference
// between the project's vendored charts/talm/ and the binary's built-in copy
// becomes a hard error instead of a warning.
//
//nolint:gochecknoglobals // cobra persistent flag binds to package-level state, consistent with the rest of this file.
var strictChartsFlag bool

// skipConfigCommands lists commands that should not load Chart.yaml config.
// - init: creates the config, so it doesn't exist yet
// - completion: generates shell completion scripts
// - __complete: cobra's internal command for shell autocompletion (Tab key).
// - dmesg: retired migration stub; must error with the hint regardless of cwd.
//
//nolint:gochecknoglobals // immutable lookup table consulted by isCommandOrParent during PersistentPreRunE; init-time literal.
var skipConfigCommands = []string{initSubcommandName, completionSubcommand, completionInternal, dmesgSubcommandName}

// rootCmd represents the base command when called without any subcommands.
//
//nolint:gochecknoglobals // cobra root command; cobra's library design requires a stable package-level *Command.
var rootCmd = &cobra.Command{
	Use:               cmdNameTalm,
	Short:             "Manage Talos the GitOps Way!",
	Long:              ``,
	Version:           Version,
	SilenceErrors:     true,
	SilenceUsage:      true,
	DisableAutoGenTag: true,
}

func main() {
	err := Execute()
	if err != nil {
		os.Exit(1)
	}
}

// registerRootFlags installs the persistent flag set on rootCmd.
// Extracted from Execute so tests can exercise the registration
// without running cobra's executor. Single-call contract: cobra
// panics on duplicate flag registration, so production calls this
// exactly once from Execute; tests must build a fresh
// *cobra.Command for each invocation.
func registerRootFlags(cmd *cobra.Command) {
	cmd.PersistentFlags().StringVar(
		&commands.GlobalArgs.Talosconfig,
		"talosconfig",
		"",
		fmt.Sprintf("The path to the Talos configuration file. Defaults to '%s' env variable if set, otherwise '%s' and '%s' in order.",
			constants.TalosConfigEnvVar,
			filepath.Join("$HOME", constants.TalosDir, constants.TalosconfigFilename),
			filepath.Join(constants.ServiceAccountMountPath, constants.TalosconfigFilename),
		),
	)
	cmd.PersistentFlags().StringVar(&commands.Config.RootDir, "root", ".", "root directory of the project")
	cmd.PersistentFlags().StringVar(&commands.GlobalArgs.CmdContext, "context", "", "Context to be used in command")
	// --nodes is registered WITHOUT the `-n` shorthand. The
	// previous registration carried `-n`, which silently captured
	// any `-n <value>` an operator typed — for example
	// `talm get hostnames -n network --nodes $NODE --endpoints
	// $NODE` parsed `network` as a second node entry and then
	// failed inside the gRPC name resolver with "produced zero
	// addresses". Operators who type `-n namespace` for a
	// subcommand argument (the muscle memory pattern from
	// `kubectl`-style CLIs) now get a clean "flag -n not defined"
	// from cobra — loud refusal instead of silent
	// misinterpretation. The long form `--nodes` and modeline
	// auto-population continue to work identically. Upstream
	// talosctl does NOT register `-n` for `--namespace` on any
	// subcommand (verified against image.go's PersistentFlags
	// StringVar and get.go's local --namespace StringVar — both
	// shorthand-free), so dropping `-n` from talm root closes a
	// shadow trap without introducing any inherited-alias gap.
	cmd.PersistentFlags().StringSliceVar(&commands.GlobalArgs.Nodes, "nodes", []string{}, "target the specified nodes")
	cmd.PersistentFlags().StringSliceVarP(&commands.GlobalArgs.Endpoints, "endpoints", "e", []string{}, "override default endpoints in Talos configuration")
	cmd.PersistentFlags().StringVar(&commands.GlobalArgs.Cluster, "cluster", "", "Cluster to connect to if a proxy endpoint is used.")
	cmd.PersistentFlags().BoolVar(&commands.GlobalArgs.SkipVerify, "skip-verify", false, "skip TLS certificate verification (keeps client authentication)")
	cmd.PersistentFlags().Bool("version", false, "Print the version number of the application")
	// No backticks in this usage string: pflag's UnquoteUsage treats the
	// first backtick-quoted word as the flag's value-placeholder name, which
	// on a bool flag misrenders --help as `--strict-charts talm init --update`
	// (as if it took an argument).
	cmd.PersistentFlags().BoolVar(&strictChartsFlag, "strict-charts", false, "fail if the project's vendored charts/talm/ or pinned preset baseline differs from the talm binary (run talm init --update --preset <preset> to re-sync)")

	// Shell completion for root persistent flags. --nodes /
	// --endpoints draw from the in-scope talosconfig contexts.
	// --talosconfig is not wired here — talosconfig has no fixed
	// extension and cobra's default file completion is already
	// the right shape for picking the file by hand.
	_ = cmd.RegisterFlagCompletionFunc("nodes", commands.CompleteTalosconfigNodes)
	_ = cmd.RegisterFlagCompletionFunc("endpoints", commands.CompleteTalosconfigEndpoints)
}

func Execute() error {
	registerRootFlags(rootCmd)

	cmd, err := rootCmd.ExecuteContextC(context.Background())
	if err != nil && !common.SuppressErrors {
		fmt.Fprintln(os.Stderr, err.Error())

		for _, hint := range errors.GetAllHints(err) {
			fmt.Fprintf(os.Stderr, "hint: %s\n", hint)
		}

		errorString := err.Error()
		//nolint:godox // cobra validation returns plain fmt.Errorf without a typed error; substring matching is the only way to distinguish those from talm's own errors until cobra ships sentinel errors.
		// FIXME: cobra arg/flag validation returns plain
		// fmt.Errorf without a typed error; substring-matching the
		// rendered message is the only way to distinguish those from
		// our own errors today. Track a refactor to wrap cobra
		// validation errors in a sentinel so this can become an
		// errors.Is check.
		if strings.Contains(errorString, "arg(s)") || strings.Contains(errorString, "flag") || strings.Contains(errorString, "command") {
			fmt.Fprintln(os.Stderr)
			fmt.Fprintln(os.Stderr, cmd.UsageString())
		}
	}

	//nolint:wrapcheck // cobra returns its own error chain; wrapping would change user-facing rendering and lose hints attached via cockroachdb/errors.WithHint inside command bodies.
	return err
}

func init() {
	cobra.OnInitialize(initConfig)

	for _, cmd := range commands.Commands {
		rootCmd.AddCommand(cmd)
	}

	// Add PersistentPreRunE to handle root detection and config loading
	originalPersistentPreRunE := rootCmd.PersistentPreRunE
	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		// Detect and set project root using fallback strategy.
		//
		err := commands.DetectAndSetRoot(cmd, args)
		if err != nil {
			return err //nolint:wrapcheck // DetectAndSetRoot already wraps with cockroachdb/errors.WithHint internally.
		}

		// Load config after root detection (skip for init and completion commands)
		if !isCommandOrParent(cmd, skipConfigCommands...) {
			configFile := filepath.Join(commands.Config.RootDir, "Chart.yaml")

			err := loadConfig(configFile)
			if err != nil {
				return errors.Wrap(err, "error loading configuration")
			}

			if err := surfaceChartDrift(); err != nil {
				return err
			}
		}

		// Ensure talosconfig path is set to project root if not explicitly set via flag
		// This is needed for all commands that use talosctl client (template, apply, etc.)
		//
		//nolint:nestif // resolution-order dispatch (--talosconfig set ? bypass : { GlobalArgs.Talosconfig set ? use it : Chart.yaml fallback ? "talosconfig" } -> abs/rel resolution); flattening would scatter the documented order across helpers.
		if !cmd.PersistentFlags().Changed("talosconfig") {
			var talosconfigPath string
			if commands.GlobalArgs.Talosconfig != "" {
				// Use existing path from Chart.yaml or default
				talosconfigPath = commands.GlobalArgs.Talosconfig
			} else {
				// Use talosconfig from project root
				talosconfigPath = commands.Config.GlobalOptions.Talosconfig
				if talosconfigPath == "" {
					talosconfigPath = "talosconfig"
				}
			}
			// Make it absolute path relative to project root if it's relative
			if !filepath.IsAbs(talosconfigPath) {
				commands.GlobalArgs.Talosconfig = filepath.Join(commands.Config.RootDir, talosconfigPath)
			} else {
				commands.GlobalArgs.Talosconfig = talosconfigPath
			}
		}

		if originalPersistentPreRunE != nil {
			return originalPersistentPreRunE(cmd, args)
		}

		return nil
	}
}

// describeSuffixRegex matches the suffixes `git describe --tags --dirty`
// appends for a non-release tree: "-<commits>-g<hash>" on a non-tag commit
// and/or "-dirty" for local edits — including bare "-dirty" on an exact tag,
// where the edits may be to the embedded charts themselves. A version
// carrying either is a developer's WIP tree, not a release.
var describeSuffixRegex = regexp.MustCompile(`(-\d+-g[0-9a-f]+)?-dirty$|-\d+-g[0-9a-f]+$`)

// releaseVersion interprets the ldflags-injected build version. It returns
// the version with any leading "v" stripped and true for a tagged release
// build, or ("", false) for a dev/source build. Both release build paths
// must be accepted: goreleaser injects "0.30.0" (no "v") and the Makefile
// injects "v0.30.0" on an exact tag. Gating on the "v" prefix alone would
// silently disable release-only behavior on the goreleaser artifacts users
// actually download.
//
// A `git describe` suffix ("v0.29.0-5-gabc1234") marks a build from a
// non-tag commit: its embedded charts are a moving target the developer
// controls, so release-only behavior (the drift checks) must stay off —
// otherwise every contributor build raises false drift in any real project
// and hard-fails strict ones. The Makefile emits "dev" off-tag, but the
// parser rejects the describe shape regardless of how it was injected.
func releaseVersion(raw string) (string, bool) {
	if raw == "" || raw == devVersion {
		return "", false
	}

	if describeSuffixRegex.MatchString(raw) {
		return "", false
	}

	return strings.TrimPrefix(raw, "v"), true
}

// evaluateChartDrift decides the drift outcome for a build. It returns
// (warning, error): a non-empty warning to print to stderr, or a non-nil
// error to abort the command (strict mode), or both empty for the silent
// cases. Taking the version, project root, and strict flag as arguments
// keeps the warn-vs-fail decision pure (modulo the filesystem read inside
// CheckChartDrift) so it is unit-testable without the package globals.
//
// Cases: dev/source build → silent (embedded charts are a moving target the
// developer controls); drift-check I/O error → non-fatal warning (best
// effort, never blocks the command); drift + strict → hard error with a
// remediation hint; drift + non-strict → warning; no drift → silent.
func evaluateChartDrift(rawVersion, rootDir string, strict bool) (string, error) {
	version, ok := releaseVersion(rawVersion)
	if !ok {
		return "", nil
	}

	drift, msg, err := commands.CheckChartDrift(rootDir, version)

	return decideDrift(drift, msg, err, strict)
}

// evaluatePresetDrift is the preset-template counterpart of
// evaluateChartDrift. Same release-only gating and warn-vs-fail decision, but
// it consults CheckPresetDrift: the binary's preset hash vs the baseline
// pinned in .talm-preset.lock at init. Kept separate (rather than folded into
// evaluateChartDrift) so the library and preset drift signals stay
// independently testable and independently silenceable.
func evaluatePresetDrift(rawVersion, rootDir string, strict bool) (string, error) {
	version, ok := releaseVersion(rawVersion)
	if !ok {
		return "", nil
	}

	drift, msg, err := commands.CheckPresetDrift(rootDir, version)

	return decideDrift(drift, msg, err, strict)
}

// decideDrift folds a (drift, msg, err) result into the (warning, error)
// outcome shared by both drift checks: a check failure downgrades to a
// non-fatal warning (best effort, never blocks a command) — except under
// strict, where an unverifiable baseline is a hard error: the operator opted
// into enforcement, and a corrupted lock or unreadable vendored tree passing
// silently would defeat it exactly when the baseline broke; a MISSING
// baseline (commands.ErrNoBaseline) is silence without strict — projects
// from before baseline pinning should not nag — but a blocker under strict,
// where deleting the baseline must not pass more quietly than corrupting
// it; drift under strict is a hard error with a remediation hint; drift
// otherwise is a warning; no drift is silent.
func decideDrift(drift bool, msg string, err error, strict bool) (string, error) {
	switch {
	case errors.Is(err, commands.ErrNoBaseline) && !strict:
		return "", nil
	case errors.Is(err, commands.ErrNoBaseline):
		//nolint:wrapcheck // cockroachdb/errors.WithHint at boundary; project idiom.
		return "", errors.WithHint(
			errors.Wrap(err, "drift baseline missing under strict mode"),
			"run `talm init --update --preset <preset>` to vendor the library and pin the preset baseline, or unset strictCharts / drop --strict-charts",
		)
	case err != nil && strict:
		//nolint:wrapcheck // cockroachdb/errors.WithHint at boundary; project idiom.
		return "", errors.WithHint(
			errors.Wrap(err, "drift check failed under strict mode"),
			"repair the baseline (re-run `talm init --update --preset <preset>`), or unset strictCharts / drop --strict-charts to downgrade this to a warning",
		)
	case err != nil:
		return fmt.Sprintf("could not check drift: %v", err), nil
	case drift && strict:
		// The shared drift message ends with "(or ignore if this is
		// intentional)" — sound advice on a warning, contradictory on an
		// error the command just refused to run past. Strip it here
		// rather than threading a second message through both checkers.
		msg = strings.Replace(msg, " (or ignore if this is intentional)", "", 1)

		//nolint:wrapcheck // originating error built with errors.New; WithHint adds operator-facing guidance and is the project idiom.
		return "", errors.WithHint(
			errors.New(msg),
			"run `talm init --update --preset <preset>`, or unset strictCharts / drop --strict-charts to downgrade this to a warning",
		)
	case drift:
		return msg, nil
	default:
		return "", nil
	}
}

// surfaceChartDrift wires the drift evaluators to the package globals and
// emits any warning to stderr. The strict input is the OR of the committed
// Chart.yaml field and the per-run flag. Both the vendored library
// (charts/talm/) and the preset baseline (.talm-preset.lock) are checked; a
// strict failure on either aborts before the command body runs.
func surfaceChartDrift() error {
	strict := commands.Config.StrictCharts || strictChartsFlag

	for _, eval := range []func(string, string, bool) (string, error){
		evaluateChartDrift,
		evaluatePresetDrift,
	} {
		warning, err := eval(Version, commands.Config.RootDir, strict)
		if err != nil {
			return err
		}

		if warning != "" {
			fmt.Fprintf(os.Stderr, "WARN: %s\n", warning)
		}
	}

	return nil
}

func initConfig() {
	if len(os.Args) < 2 {
		return
	}

	cmdName := os.Args[1]

	cmd, _, err := rootCmd.Find([]string{cmdName})
	if err != nil || cmd == nil {
		return
	}

	if cmd.HasParent() && cmd.Parent() != rootCmd {
		cmd = cmd.Parent()
	}

	if strings.HasPrefix(cmd.Use, initSubcommandName) {
		// Stamp the real release version into the vendored charts; fall back
		// to the dev sentinel for source builds. Gating on the "v" prefix
		// here would stamp "0.1.0" on every goreleaser release (which injects
		// the version without "v").
		if version, ok := releaseVersion(Version); ok {
			commands.Config.InitOptions.Version = version
		} else {
			commands.Config.InitOptions.Version = "0.1.0"
		}
	}
}

// isCommandOrParent checks if the command or any of its parents matches one of the given names.
func isCommandOrParent(cmd *cobra.Command, names ...string) bool {
	for c := cmd; c != nil; c = c.Parent() {
		if slices.Contains(names, c.Name()) {
			return true
		}
	}

	return false
}

func loadConfig(filename string) error {
	data, err := os.ReadFile(filename)
	if err != nil {
		return errors.Wrap(err, "error reading configuration file")
	}

	//nolint:musttag // commands.Config relies on default field-name matching for Chart.yaml; adding yaml tags everywhere would be a cross-package rename and an API change for chart authors.
	err = yaml.Unmarshal(data, &commands.Config)
	if err != nil {
		return errors.Wrap(err, "error unmarshalling configuration")
	}

	if commands.GlobalArgs.Talosconfig == "" {
		commands.GlobalArgs.Talosconfig = commands.Config.GlobalOptions.Talosconfig
	}

	if commands.Config.TemplateOptions.KubernetesVersion == "" {
		commands.Config.TemplateOptions.KubernetesVersion = constants.DefaultKubernetesVersion
	}

	// Fill in the default-string path BEFORE parsing so both the
	// "operator left timeout empty" and "operator supplied a value"
	// branches end up with TimeoutDuration populated. The previous
	// shape parsed only in the else branch, leaving TimeoutDuration
	// at its zero value when the default kicked in — pre-existing
	// on main since the original "fix loading defaults" landed in
	// 2024.
	if commands.Config.ApplyOptions.Timeout == "" {
		commands.Config.ApplyOptions.Timeout = constants.ConfigTryTimeout.String()
	}

	parsed, err := time.ParseDuration(commands.Config.ApplyOptions.Timeout)
	if err != nil {
		//nolint:wrapcheck // already wrapped via errors.Wrapf, WithHint adds operator-facing guidance
		return errors.WithHint(
			errors.Wrapf(err, "parsing applyOptions.timeout %q from %s", commands.Config.ApplyOptions.Timeout, filename),
			"applyOptions.timeout in Chart.yaml must be a Go duration literal (e.g. \"30s\", \"2m\", \"1h\")",
		)
	}

	commands.Config.ApplyOptions.TimeoutDuration = parsed

	return nil
}
