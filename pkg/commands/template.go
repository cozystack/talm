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
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cockroachdb/errors"
	"github.com/cozystack/talm/pkg/engine"
	"github.com/cozystack/talm/pkg/modeline"
	"github.com/cozystack/talm/pkg/secureperm"
	"github.com/spf13/cobra"

	"github.com/siderolabs/talos/pkg/machinery/client"
	"github.com/siderolabs/talos/pkg/machinery/constants"
)

//nolint:gochecknoglobals // cobra command flag struct, idiomatic for cobra-based CLIs
var templateCmdFlags struct {
	insecure          bool
	configFiles       []string // -f/--files
	valueFiles        []string // --values
	templateFiles     []string // -t/--template
	stringValues      []string // --set-string
	values            []string // --set
	fileValues        []string // --set-file
	jsonValues        []string // --set-json
	literalValues     []string // --set-literal
	talosVersion      string
	withSecrets       string
	full              bool
	debug             bool
	offline           bool
	kubernetesVersion string
	inplace           bool
	nodesFromArgs     bool
	endpointsFromArgs bool
	templatesFromArgs bool
}

//nolint:gochecknoglobals // cobra command, idiomatic for cobra-based CLIs
var templateCmd = &cobra.Command{
	Use:   "template",
	Short: "Render templates locally and display the output",
	Long:  ``,
	Args:  cobra.NoArgs,
	PreRunE: func(cmd *cobra.Command, _ []string) error {
		templateCmdFlags.valueFiles = append(Config.TemplateOptions.ValueFiles, templateCmdFlags.valueFiles...)
		templateCmdFlags.values = append(Config.TemplateOptions.Values, templateCmdFlags.values...)
		templateCmdFlags.stringValues = append(Config.TemplateOptions.StringValues, templateCmdFlags.stringValues...)
		templateCmdFlags.fileValues = append(Config.TemplateOptions.FileValues, templateCmdFlags.fileValues...)
		templateCmdFlags.jsonValues = append(Config.TemplateOptions.JsonValues, templateCmdFlags.jsonValues...)

		templateCmdFlags.literalValues = append(Config.TemplateOptions.LiteralValues, templateCmdFlags.literalValues...)
		if !cmd.Flags().Changed("talos-version") {
			templateCmdFlags.talosVersion = Config.TemplateOptions.TalosVersion
		}

		if !cmd.Flags().Changed("with-secrets") {
			templateCmdFlags.withSecrets = Config.TemplateOptions.WithSecrets
		}

		if !cmd.Flags().Changed("kubernetes-version") {
			templateCmdFlags.kubernetesVersion = Config.TemplateOptions.KubernetesVersion
		}

		if !cmd.Flags().Changed("full") {
			templateCmdFlags.full = Config.TemplateOptions.Full
		}

		if !cmd.Flags().Changed("debug") {
			templateCmdFlags.debug = Config.TemplateOptions.Debug
		}

		if !cmd.Flags().Changed("offline") {
			templateCmdFlags.offline = Config.TemplateOptions.Offline
		}

		templateCmdFlags.templatesFromArgs = len(templateCmdFlags.templateFiles) > 0
		templateCmdFlags.nodesFromArgs = len(GlobalArgs.Nodes) > 0
		templateCmdFlags.endpointsFromArgs = len(GlobalArgs.Endpoints) > 0
		// Set dummy endpoint to avoid errors on building clinet
		if len(GlobalArgs.Endpoints) == 0 {
			GlobalArgs.Endpoints = append(GlobalArgs.Endpoints, defaultLocalEndpoint)
		}

		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		templateFunc := template
		if len(templateCmdFlags.configFiles) > 0 {
			templateFunc = templateWithFiles
		}

		if templateCmdFlags.offline {
			return templateFunc(args)(cmd.Context(), nil)
		}

		if templateCmdFlags.insecure {
			return WithClientMaintenance(nil, templateFunc(args))
		}

		if GlobalArgs.SkipVerify {
			return WithClientSkipVerify(templateFunc(args))
		}

		return WithClient(templateFunc(args))
	},
}

func template(args []string) func(ctx context.Context, c *client.Client) error {
	return func(ctx context.Context, c *client.Client) error {
		output, err := generateOutput(ctx, c, args)
		if err != nil {
			return err
		}

		//nolint:forbidigo // CLI command output is the user-facing rendered config
		fmt.Println(output)

		return nil
	}
}

func templateWithFiles(args []string) func(ctx context.Context, c *client.Client) error {
	return func(ctx context.Context, _ *client.Client) error {
		// Expand directories to YAML files
		expandedFiles, err := ExpandFilePaths(templateCmdFlags.configFiles)
		if err != nil {
			return err
		}

		// Detect root from files if specified, otherwise fallback to cwd
		err = DetectAndSetRootFromFiles(expandedFiles)
		if err != nil {
			return err
		}

		firstFileProcessed := false

		for _, configFile := range expandedFiles {
			err = templateOneFile(ctx, args, configFile, &firstFileProcessed)
			if err != nil {
				return err
			}

			// Reset args
			firstFileProcessed = true

			if !templateCmdFlags.templatesFromArgs {
				templateCmdFlags.templateFiles = []string{}
			}

			if !templateCmdFlags.nodesFromArgs {
				GlobalArgs.Nodes = []string{}
			}

			if !templateCmdFlags.endpointsFromArgs {
				GlobalArgs.Endpoints = []string{}
			}
		}

		return nil
	}
}

// templateOneFile renders one config file: parses its modeline,
// updates the package-level state for nodes/endpoints/templates,
// then dispatches the per-file render through the appropriate client
// mode. Splitting the per-file work out of templateWithFiles keeps
// the outer function's cognitive complexity within the linter's gate.
func templateOneFile(ctx context.Context, args []string, configFile string, firstFileProcessed *bool) error {
	modelineConfig, err := modeline.ReadAndParseModeline(configFile)
	if err != nil {
		return errors.Wrap(err, "modeline parsing failed")
	}

	if !templateCmdFlags.templatesFromArgs {
		if len(modelineConfig.Templates) == 0 {
			//nolint:wrapcheck // sentinel constructed in-place; WithHint attaches operator guidance
			return errors.WithHint(
				errors.New("modeline does not contain templates information"),
				"add a `# talm: templates=[...]` modeline at the top of the node file or pass --template explicitly",
			)
		}

		templateCmdFlags.templateFiles = modelineConfig.Templates
	}

	if !templateCmdFlags.nodesFromArgs {
		GlobalArgs.Nodes = modelineConfig.Nodes
	}

	if !templateCmdFlags.endpointsFromArgs {
		GlobalArgs.Endpoints = modelineConfig.Endpoints
	}

	//nolint:forbidigo // CLI progress line surfaces the file-to-target mapping for the operator
	fmt.Printf("- talm: file=%s, nodes=%s, endpoints=%s, templates=%s\n", configFile, GlobalArgs.Nodes, GlobalArgs.Endpoints, templateCmdFlags.templateFiles)

	if len(GlobalArgs.Nodes) < 1 {
		//nolint:wrapcheck // sentinel constructed in-place; WithHint attaches operator guidance
		return errors.WithHint(
			errors.New("nodes are not set for the command"),
			"set the targets via --nodes, a `# talm: nodes=[...]` modeline at the top of the node file, or the talosconfig context",
		)
	}

	if len(templateCmdFlags.configFiles) != 0 && len(templateCmdFlags.templateFiles) < 1 {
		//nolint:wrapcheck // sentinel constructed in-place; WithHint attaches operator guidance
		return errors.WithHint(
			errors.New("templates are not set for the command"),
			"set the templates via --template or a `# talm: templates=[...]` modeline at the top of the node file",
		)
	}

	tmpl := buildTemplateRunner(args, configFile, firstFileProcessed)

	return runTemplate(ctx, tmpl)
}

// buildTemplateRunner returns the per-file render-and-emit closure.
// Extracted so both templateOneFile and the dispatcher in runTemplate
// can stay flat.
func buildTemplateRunner(args []string, configFile string, firstFileProcessed *bool) func(ctx context.Context, c *client.Client) error {
	return func(ctx context.Context, c *client.Client) error {
		output, err := generateOutput(ctx, c, args)
		if err != nil {
			return err
		}

		if templateCmdFlags.inplace {
			return writeInplaceRendered(configFile, output)
		}

		if *firstFileProcessed {
			//nolint:forbidigo // multi-document YAML separator is part of the user-facing output stream
			fmt.Println("---")
		}

		//nolint:forbidigo // CLI command output is the user-facing rendered config
		fmt.Printf("%s", output)

		return nil
	}
}

// runTemplate dispatches the per-file template runner across the four
// possible client modes (offline, insecure maintenance, skip-verify,
// authenticated). The dispatch is a switch over package-level flag
// state; the per-file logic sits in tmpl.
func runTemplate(ctx context.Context, tmpl func(ctx context.Context, c *client.Client) error) error {
	switch {
	case templateCmdFlags.offline:
		return tmpl(ctx, nil)
	case templateCmdFlags.insecure:
		return WithClientMaintenance(nil, tmpl)
	case GlobalArgs.SkipVerify:
		return WithClientSkipVerify(tmpl)
	default:
		return WithClient(tmpl)
	}
}

func generateOutput(ctx context.Context, c *client.Client, _ []string) (string, error) {
	// Resolve secrets.yaml path relative to project root if not absolute
	withSecretsPath := ResolveSecretsPath(templateCmdFlags.withSecrets)

	// Resolve template file paths relative to project root
	resolvedTemplateFiles := resolveEngineTemplatePaths(templateCmdFlags.templateFiles, Config.RootDir)

	opts := engine.Options{
		ValueFiles:        templateCmdFlags.valueFiles,
		StringValues:      templateCmdFlags.stringValues,
		Values:            templateCmdFlags.values,
		FileValues:        templateCmdFlags.fileValues,
		JsonValues:        templateCmdFlags.jsonValues,
		LiteralValues:     templateCmdFlags.literalValues,
		TalosVersion:      templateCmdFlags.talosVersion,
		WithSecrets:       withSecretsPath,
		Full:              templateCmdFlags.full,
		Debug:             templateCmdFlags.debug,
		Root:              Config.RootDir,
		Offline:           templateCmdFlags.offline,
		KubernetesVersion: templateCmdFlags.kubernetesVersion,
		TemplateFiles:     resolvedTemplateFiles,
		CommandName:       "talm template",
	}

	result, err := engine.Render(ctx, c, opts)
	if err != nil {
		return "", errors.Wrap(err, "failed to render templates")
	}

	templatePathsForModeline := buildModelineTemplatePaths(templateCmdFlags.templateFiles, Config.RootDir)

	mline, err := modeline.GenerateModeline(GlobalArgs.Nodes, GlobalArgs.Endpoints, templatePathsForModeline)
	if err != nil {
		return "", errors.Wrap(err, "failed to generate modeline")
	}

	warn := "# THIS FILE IS AUTOGENERATED. PREFER TEMPLATE EDITS OVER MANUAL ONES."

	output := fmt.Sprintf("%s\n%s\n%s\n", mline, warn, string(result))

	return output, nil
}

// buildModelineTemplatePaths converts each template path into the
// forward-slash, root-relative form to be embedded in the generated
// modeline. Splits the per-path branching out of generateOutput so
// the outer function's cognitive complexity stays within the
// linter's gate.
func buildModelineTemplatePaths(templateFiles []string, rootDir string) []string {
	out := make([]string, len(templateFiles))

	absRootDir, err := filepath.Abs(rootDir)
	if err != nil {
		// If we can't get absolute root, normalize original paths for modeline
		for i, p := range templateFiles {
			out[i] = engine.NormalizeTemplatePath(p)
		}

		return out
	}

	for i, templatePath := range templateFiles {
		out[i] = engine.NormalizeTemplatePath(modelinePathFor(templatePath, absRootDir))
	}

	return out
}

// modelinePathFor returns the path that should be embedded in the
// modeline for a single template entry. Resolves the path relative
// to rootDir, handles outside-root inputs by falling back to a
// canonical templates/<basename>, and prefers a canonical
// templates/<basename> when an inside-root absolute path resolves to
// the same file. Returns the original path on any unrecoverable
// error.
func modelinePathFor(templatePath, absRootDir string) string {
	absTemplatePath, ok := absTemplatePathFor(templatePath)
	if !ok {
		return templatePath
	}

	relPath, err := filepath.Rel(absRootDir, absTemplatePath)
	if err != nil {
		return templatePath
	}

	relPath = filepath.Clean(relPath)
	if isOutsideRoot(relPath) {
		fallback, found := findOutsideRootFallback(templatePath, absRootDir)
		if !found {
			return templatePath
		}

		return fallback
	}

	return canonicalizeInsideRootPath(templatePath, absTemplatePath, absRootDir, relPath)
}

// absTemplatePathFor resolves templatePath to an absolute filesystem
// path. Returns ok=false if the input is relative and cannot be
// promoted to absolute.
func absTemplatePathFor(templatePath string) (string, bool) {
	if filepath.IsAbs(templatePath) {
		return templatePath, true
	}

	abs, err := filepath.Abs(templatePath)
	if err != nil {
		return "", false
	}

	return abs, true
}

// findOutsideRootFallback handles the case where templatePath
// resolves outside absRootDir. It looks for a file with the same
// basename under <root>/templates/ or <root>/, returning the
// matching relative path if found.
func findOutsideRootFallback(templatePath, absRootDir string) (string, bool) {
	templateName := filepath.Base(templatePath)
	for _, possiblePath := range []string{filepath.Join("templates", templateName), templateName} {
		_, err := os.Stat(filepath.Join(absRootDir, possiblePath))
		if err == nil {
			return possiblePath, true
		}
	}

	return "", false
}

// canonicalizeInsideRootPath, given an inside-root templatePath,
// picks the cleanest path to embed in the modeline. If templatePath
// does not exist on disk but a sibling under
// <root>/templates/<basename> does, the latter is used. If both
// exist and point to the same file (e.g.
// nodes/templates/controlplane.yaml symlinked to
// templates/controlplane.yaml), the shorter canonical form wins.
func canonicalizeInsideRootPath(templatePath, absTemplatePath, absRootDir, relPath string) string {
	templateName := filepath.Base(templatePath)
	canonicalPath := filepath.Join("templates", templateName)
	canonicalFullPath := filepath.Join(absRootDir, canonicalPath)

	_, err := os.Stat(absTemplatePath)
	if err != nil {
		_, statErr := os.Stat(canonicalFullPath)
		if statErr == nil {
			return canonicalPath
		}

		return relPath
	}

	canonicalInfo, err := os.Stat(canonicalFullPath)
	if err != nil {
		return relPath
	}

	originalInfo, err := os.Stat(absTemplatePath)
	if err != nil {
		return relPath
	}

	if os.SameFile(originalInfo, canonicalInfo) {
		return canonicalPath
	}

	return relPath
}

func init() {
	templateCmd.Flags().BoolVarP(&templateCmdFlags.insecure, "insecure", "i", false, "template using the insecure (encrypted with no auth) maintenance service")
	templateCmd.Flags().StringSliceVarP(&templateCmdFlags.configFiles, "file", "f", nil, "specify config files for in-place update (can specify multiple)")
	templateCmd.Flags().BoolVarP(&templateCmdFlags.inplace, "in-place", "I", false, "re-template and update generated files in place (overwrite them)")
	templateCmd.Flags().StringSliceVarP(&templateCmdFlags.valueFiles, "values", "", []string{}, "specify values in a YAML file (can specify multiple)")
	templateCmd.Flags().StringSliceVarP(&templateCmdFlags.templateFiles, "template", "t", []string{}, "specify templates to render manifest from (can specify multiple)")
	templateCmd.Flags().StringArrayVar(&templateCmdFlags.values, "set", []string{}, "set values on the command line (can specify multiple or separate values with commas: key1=val1,key2=val2)")
	templateCmd.Flags().StringArrayVar(&templateCmdFlags.stringValues, "set-string", []string{}, "set STRING values on the command line (can specify multiple or separate values with commas: key1=val1,key2=val2)")
	templateCmd.Flags().StringArrayVar(&templateCmdFlags.fileValues, "set-file", []string{}, "set values from respective files specified via the command line (can specify multiple or separate values with commas: key1=path1,key2=path2)")
	templateCmd.Flags().StringArrayVar(&templateCmdFlags.jsonValues, "set-json", []string{}, "set JSON values on the command line (can specify multiple or separate values with commas: key1=jsonval1,key2=jsonval2)")
	templateCmd.Flags().StringArrayVar(&templateCmdFlags.literalValues, "set-literal", []string{}, "set a literal STRING value on the command line")
	templateCmd.Flags().StringVar(&templateCmdFlags.talosVersion, "talos-version", "", "the desired Talos version to generate config for (backwards compatibility, e.g. v0.8)")
	templateCmd.Flags().StringVar(&templateCmdFlags.withSecrets, "with-secrets", "", "use a secrets file generated using 'gen secrets'")
	templateCmd.Flags().BoolVarP(&templateCmdFlags.full, "full", "", false, "show full resulting config, not only patch")
	templateCmd.Flags().BoolVarP(&templateCmdFlags.debug, "debug", "", false, "show only rendered patches")
	templateCmd.Flags().BoolVarP(&templateCmdFlags.offline, "offline", "", false, "disable gathering information and lookup functions")
	templateCmd.Flags().StringVar(&templateCmdFlags.kubernetesVersion, "kubernetes-version", constants.DefaultKubernetesVersion, "desired kubernetes version to run")

	addCommand(templateCmd)
}

// writeInplaceRendered writes the rendered template output over the
// node config file. Routes through secureperm because the rendered
// machine config embeds certs, PKI keys, and cluster join tokens —
// exactly the material that must not end up readable by other users
// on Windows (inherited DACL) or Unix (0o644).
func writeInplaceRendered(configFile, output string) error {
	if err := secureperm.WriteFile(configFile, []byte(output)); err != nil {
		return errors.Wrapf(err, "failed to write file %s", configFile)
	}

	_, _ = fmt.Fprintf(os.Stderr, "Updated.\n")

	return nil
}

// resolveEngineTemplatePaths resolves each template path to a
// forward-slash relative path under rootDir, ready to index into the
// helm engine's map keys. Relative inputs are resolved against CWD
// (matching the current shell context); if the resolved path lies
// outside rootDir, the function falls back to `templates/<basename>`
// under rootDir when such a file exists. Anything that cannot be
// resolved is returned normalized through engine.NormalizeTemplatePath
// so downstream map lookups never see backslashes.
//
// Extracted as a named function so Windows path handling can be
// exercised directly in unit tests — PowerShell callers pass
// backslash separators on `-t`, and a regression that reintroduces
// backslashes into the engine-bound string would otherwise only be
// observable at integration time.
func resolveEngineTemplatePaths(templateFiles []string, rootDir string) []string {
	resolved := make([]string, len(templateFiles))

	absRootDir, rootErr := filepath.Abs(rootDir)
	if rootErr != nil {
		for i, p := range templateFiles {
			resolved[i] = engine.NormalizeTemplatePath(p)
		}

		return resolved
	}

	for i, templatePath := range templateFiles {
		var absTemplatePath string
		if filepath.IsAbs(templatePath) {
			absTemplatePath = templatePath
		} else {
			var absErr error

			absTemplatePath, absErr = filepath.Abs(templatePath)
			if absErr != nil {
				resolved[i] = engine.NormalizeTemplatePath(templatePath)

				continue
			}
		}

		relPath, relErr := filepath.Rel(absRootDir, absTemplatePath)
		if relErr != nil {
			resolved[i] = engine.NormalizeTemplatePath(templatePath)

			continue
		}

		relPath = filepath.Clean(relPath)
		if isOutsideRoot(relPath) {
			templateName := filepath.Base(templatePath)
			possiblePath := filepath.Join("templates", templateName)

			fullPath := filepath.Join(absRootDir, possiblePath)
			if _, statErr := os.Stat(fullPath); statErr == nil {
				relPath = possiblePath
			} else {
				resolved[i] = engine.NormalizeTemplatePath(templatePath)

				continue
			}
		}

		resolved[i] = engine.NormalizeTemplatePath(relPath)
	}

	return resolved
}
