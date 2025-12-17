// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package commands

import (
	taloscommands "github.com/siderolabs/talos/cmd/talosctl/cmd/talos"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// wrapTalosCommand wraps a talosctl command to add --file flag support
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
			// Create a copy with new shorthand
			newFlag := &pflag.Flag{
				Name:        flag.Name,
				Usage:       flag.Usage,
				Value:       flag.Value,
				DefValue:    flag.DefValue,
				Changed:     flag.Changed,
				Deprecated:  flag.Deprecated,
				Hidden:      flag.Hidden,
				Shorthand:   "F", // Change shorthand from 'f' to 'F'
				Annotations: flag.Annotations,
			}
			wrappedCmd.Flags().AddFlag(newFlag)
		} else {
			wrappedCmd.Flags().AddFlag(flag)
		}
	})

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

		for _, configFile := range filesToProcess {
			if err := processModelineAndUpdateGlobals(configFile, nodesFromArgs, endpointsFromArgs, false); err != nil {
				return err
			}
		}
		// Sync GlobalArgs to talosctl commands
		taloscommands.GlobalArgs = GlobalArgs
		if originalPreRunE != nil {
			return originalPreRunE(cmd, args)
		}
		return nil
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
		"apply-config": true, // talm has its own apply command
		"upgrade":      true, // talm has its own upgrade command
		"config":       true, // talm manages config differently
		"patch":        true, // not needed in talm
		"upgrade-k8s":  true, // not needed in talm
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
		wrappedCmd := wrapTalosCommand(cmd, cmd.Use)
		// Keep the original command name from talosctl
		addCommand(wrappedCmd)
	}
}
