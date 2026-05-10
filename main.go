package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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

// completionInternal is cobra's reserved internal subcommand name
// driving Tab-key autocompletion. Constant because it appears both
// in skipConfigCommands and in cobra's exported API.
const (
	initSubcommandName   = "init"
	completionSubcommand = "completion"
	completionInternal   = "__complete"
)

// cmdNameTalm is the binary name used both as the cobra root
// command's Use field and via -X main.cmdNameTalm in build tooling.
const cmdNameTalm = "talm"

// Version is the talm release tag baked at build time via ldflags
// (`-X main.Version=v0.27.0`); the literal value here is the local
// development fallback.
//
//nolint:gochecknoglobals // ldflags-injected build version, idiomatic for go release tooling.
var Version = "dev"

// skipConfigCommands lists commands that should not load Chart.yaml config.
// - init: creates the config, so it doesn't exist yet
// - completion: generates shell completion scripts
// - __complete: cobra's internal command for shell autocompletion (Tab key).
//
//nolint:gochecknoglobals // immutable lookup table consulted by isCommandOrParent during PersistentPreRunE; init-time literal.
var skipConfigCommands = []string{initSubcommandName, completionSubcommand, completionInternal}

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

func Execute() error {
	rootCmd.PersistentFlags().StringVar(
		&commands.GlobalArgs.Talosconfig,
		"talosconfig",
		"",
		fmt.Sprintf("The path to the Talos configuration file. Defaults to '%s' env variable if set, otherwise '%s' and '%s' in order.",
			constants.TalosConfigEnvVar,
			filepath.Join("$HOME", constants.TalosDir, constants.TalosconfigFilename),
			filepath.Join(constants.ServiceAccountMountPath, constants.TalosconfigFilename),
		),
	)
	rootCmd.PersistentFlags().StringVar(&commands.Config.RootDir, "root", ".", "root directory of the project")
	rootCmd.PersistentFlags().StringVar(&commands.GlobalArgs.CmdContext, "context", "", "Context to be used in command")
	rootCmd.PersistentFlags().StringSliceVarP(&commands.GlobalArgs.Nodes, "nodes", "n", []string{}, "target the specified nodes")
	rootCmd.PersistentFlags().StringSliceVarP(&commands.GlobalArgs.Endpoints, "endpoints", "e", []string{}, "override default endpoints in Talos configuration")
	rootCmd.PersistentFlags().StringVar(&commands.GlobalArgs.Cluster, "cluster", "", "Cluster to connect to if a proxy endpoint is used.")
	rootCmd.PersistentFlags().BoolVar(&commands.GlobalArgs.SkipVerify, "skip-verify", false, "skip TLS certificate verification (keeps client authentication)")
	rootCmd.PersistentFlags().Bool("version", false, "Print the version number of the application")

	cmd, err := rootCmd.ExecuteContextC(context.Background())
	if err != nil && !common.SuppressErrors {
		fmt.Fprintln(os.Stderr, err.Error())

		for _, hint := range errors.GetAllHints(err) {
			fmt.Fprintf(os.Stderr, "hint: %s\n", hint)
		}

		errorString := err.Error()
		//nolint:godox // tracked as #153-followup; cobra validation returns plain fmt.Errorf without a typed error, requires substring matching until cobra ships sentinel errors.
		// FIXME(#153-followup): cobra arg/flag validation returns plain
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
		if strings.HasPrefix(Version, "v") {
			commands.Config.InitOptions.Version = strings.TrimPrefix(Version, `v`)
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

	if commands.Config.ApplyOptions.Timeout == "" {
		commands.Config.ApplyOptions.Timeout = constants.ConfigTryTimeout.String()
	} else {
		var err error

		commands.Config.ApplyOptions.TimeoutDuration, err = time.ParseDuration(commands.Config.ApplyOptions.Timeout)
		if err != nil {
			//nolint:wrapcheck // already wrapped via errors.Wrapf, WithHint adds operator-facing guidance
			return errors.WithHint(
				errors.Wrapf(err, "parsing applyOptions.timeout %q from %s", commands.Config.ApplyOptions.Timeout, filename),
				"applyOptions.timeout in Chart.yaml must be a Go duration literal (e.g. \"30s\", \"2m\", \"1h\")",
			)
		}
	}

	return nil
}
