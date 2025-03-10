//go:generate go run tools/generate_presets.go

package main

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/cozystack/talm/pkg/commands"
	_ "github.com/siderolabs/talos/cmd/talosctl/acompat"
	"github.com/siderolabs/talos/cmd/talosctl/cmd/common"
	"github.com/siderolabs/talos/pkg/machinery/constants"
	"github.com/spf13/cobra"
)

var Version = "dev"

// rootCmd represents the base command when called without any subcommands.
var rootCmd = &cobra.Command{
	Use:               "talm",
	Short:             "Manage Talos the GitOps Way!",
	Long:              ``,
	Version:           Version,
	SilenceErrors:     true,
	SilenceUsage:      true,
	DisableAutoGenTag: true,
}

func main() {
	if err := Execute(); err != nil {
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
	rootCmd.PersistentFlags().Bool("version", false, "Print the version number of the application")

	cmd, err := rootCmd.ExecuteContextC(context.Background())
	if err != nil && !common.SuppressErrors {
		fmt.Fprintln(os.Stderr, err.Error())

		errorString := err.Error()
		// TODO: this is a nightmare, but arg-flag related validation returns simple `fmt.Errorf`, no way to distinguish
		//       these errors
		if strings.Contains(errorString, "arg(s)") || strings.Contains(errorString, "flag") || strings.Contains(errorString, "command") {
			fmt.Fprintln(os.Stderr)
			fmt.Fprintln(os.Stderr, cmd.UsageString())
		}
	}

	return err
}

func init() {
	cobra.OnInitialize(initConfig)

	for _, cmd := range commands.Commands {
		rootCmd.AddCommand(cmd)
	}
}

func initConfig() {
	cmdName := os.Args[1]
	cmd, _, err := rootCmd.Find([]string{cmdName})
	if err != nil || cmd == nil {
		return
	}
	if cmd.HasParent() && cmd.Parent() != rootCmd {
		cmd = cmd.Parent()
	}
	if strings.HasPrefix(cmd.Use, "init") {
		if strings.HasPrefix(Version, "v") {
			commands.Config.InitOptions.Version = strings.TrimPrefix(Version, `v`)
		} else {
			commands.Config.InitOptions.Version = "0.1.0"
		}
	} else {
		if !strings.HasPrefix(cmd.Use, "completion") {
			configFile := filepath.Join(commands.Config.RootDir, "Chart.yaml")
			if err := loadConfig(configFile); err != nil {
				fmt.Fprintf(os.Stderr, "Error loading configuration: %v\n", err)
				os.Exit(1)
			}
		}
	}
}

func loadConfig(filename string) error {
	data, err := ioutil.ReadFile(filename)
	if err != nil {
		return fmt.Errorf("error reading configuration file: %w", err)
	}

	if err := yaml.Unmarshal(data, &commands.Config); err != nil {
		return fmt.Errorf("error unmarshalling configuration: %w", err)
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
			panic(err)
		}
	}
	return nil
}
