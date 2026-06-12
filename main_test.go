package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cockroachdb/errors"
	"github.com/cozystack/talm/pkg/commands"
	"github.com/cozystack/talm/pkg/generated"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// buildCommandHierarchy creates a cobra command hierarchy from a path like
// ["talm", "completion", "bash"] and returns the leaf command.
func buildCommandHierarchy(path []string) *cobra.Command {
	if len(path) == 0 {
		return nil
	}

	root := &cobra.Command{Use: path[0]}
	parent := root

	for _, name := range path[1:] {
		child := &cobra.Command{Use: name}
		parent.AddCommand(child)
		parent = child
	}

	return parent
}

func TestIsCommandOrParent(t *testing.T) {
	tests := []struct {
		name     string
		cmdPath  []string
		names    []string
		expected bool
	}{
		{
			name:     "direct completion command",
			cmdPath:  []string{"talm", "completion"},
			names:    []string{"init", "completion"},
			expected: true,
		},
		{
			name:     "completion bash subcommand",
			cmdPath:  []string{"talm", "completion", "bash"},
			names:    []string{"init", "completion"},
			expected: true,
		},
		{
			name:     "init command",
			cmdPath:  []string{"talm", "init"},
			names:    []string{"init", "completion"},
			expected: true,
		},
		{
			name:     "apply command should not match",
			cmdPath:  []string{"talm", "apply"},
			names:    []string{"init", "completion"},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			leaf := buildCommandHierarchy(tt.cmdPath)
			result := isCommandOrParent(leaf, tt.names...)
			if result != tt.expected {
				t.Errorf("isCommandOrParent() = %v, want %v", result, tt.expected)
			}
		})
	}
}

// snapshotConfigState captures and restores the package-level
// commands.Config and commands.GlobalArgs that loadConfig mutates.
// loadConfig writes to Config (yaml.Unmarshal) AND to GlobalArgs
// (the Talosconfig-fallback assignment), so both must be saved to
// avoid cross-test leakage.
func snapshotConfigState(t *testing.T) {
	t.Helper()

	savedConfig := commands.Config
	savedArgs := commands.GlobalArgs

	t.Cleanup(func() {
		commands.Config = savedConfig
		commands.GlobalArgs = savedArgs
	})
}

// TestLoadConfig_InvalidApplyTimeoutReturnsError pins that a
// malformed `applyOptions.timeout` in the project Chart.yaml
// surfaces as a regular error (with operator-facing hint), not a
// runtime panic. main used to call `panic(err)` here, which
// crashed the talm process before cobra could format the error;
// the error path lets the surrounding command runner print a
// cleanly-wrapped failure instead.
func TestLoadConfig_InvalidApplyTimeoutReturnsError(t *testing.T) {
	dir := t.TempDir()
	chartPath := filepath.Join(dir, "Chart.yaml")
	body := "apiVersion: v2\nname: test\nversion: 0.1.0\napplyOptions:\n  timeout: \"this-is-not-a-duration\"\n"
	if err := os.WriteFile(chartPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write Chart.yaml: %v", err)
	}

	snapshotConfigState(t)

	err := loadConfig(chartPath)
	if err == nil {
		t.Fatal("expected error for malformed applyOptions.timeout, got nil")
	}
	if !strings.Contains(err.Error(), "applyOptions.timeout") {
		t.Errorf("error message must mention applyOptions.timeout (the bad field); got: %v", err)
	}
	if !strings.Contains(err.Error(), "this-is-not-a-duration") {
		t.Errorf("error message must echo the bad value so the operator can correlate; got: %v", err)
	}

	// The hint chain must keep an operator-actionable explanation
	// of the expected format. Without it, the operator sees only
	// "time: invalid duration" and has to guess the canonical form.
	hints := errors.GetAllHints(err)
	if len(hints) == 0 {
		t.Errorf("expected at least one hint guiding the operator on the duration format; got bare error: %v", err)
	}
	combined := strings.Join(hints, "\n")
	if !strings.Contains(combined, "duration") {
		t.Errorf("hint chain must mention the duration format; got %q", combined)
	}
}

// TestLoadConfig_ValidApplyTimeoutParses pins the success path:
// a well-formed duration parses into TimeoutDuration. Guards
// against a refactor that flips the polarity of the error branch
// or stops storing the parsed duration.
func TestLoadConfig_ValidApplyTimeoutParses(t *testing.T) {
	dir := t.TempDir()
	chartPath := filepath.Join(dir, "Chart.yaml")
	body := "apiVersion: v2\nname: test\nversion: 0.1.0\napplyOptions:\n  timeout: \"45s\"\n"
	if err := os.WriteFile(chartPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write Chart.yaml: %v", err)
	}

	snapshotConfigState(t)

	if err := loadConfig(chartPath); err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if got := commands.Config.ApplyOptions.TimeoutDuration; got.String() != "45s" {
		t.Errorf("TimeoutDuration = %v, want 45s", got)
	}
}

// TestLoadConfig_EmptyApplyTimeoutResolvesDefault pins that the
// default-string path also populates TimeoutDuration. Pre-existing
// on main: the parse used to live only in the else branch, so an
// empty applyOptions.timeout left TimeoutDuration at its zero
// value despite the Timeout string being filled with the default.
// The current shape parses unconditionally; this test guards a
// future refactor that splits the branches again.
func TestLoadConfig_EmptyApplyTimeoutResolvesDefault(t *testing.T) {
	dir := t.TempDir()
	chartPath := filepath.Join(dir, "Chart.yaml")
	body := "apiVersion: v2\nname: test\nversion: 0.1.0\n"
	if err := os.WriteFile(chartPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write Chart.yaml: %v", err)
	}

	snapshotConfigState(t)

	if err := loadConfig(chartPath); err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if got := commands.Config.ApplyOptions.TimeoutDuration; got == 0 {
		t.Errorf("TimeoutDuration is zero after loadConfig with empty applyOptions.timeout; the default-string path must parse the resolved default into the duration")
	}
	if got := commands.Config.ApplyOptions.Timeout; got == "" {
		t.Errorf("Timeout string is empty after loadConfig; the default-string path must fill it from constants.ConfigTryTimeout")
	}
}

// TestLoadConfig_StrictChartsParses pins the Chart.yaml → Config channel
// of strict enforcement: `strictCharts: true` must land in
// commands.Config.StrictCharts. This is the team/CI-wide form the README
// recommends; a yaml-tag typo would silently disable it while the
// per-invocation --strict-charts flag kept working, and no other test
// would notice.
func TestLoadConfig_StrictChartsParses(t *testing.T) {
	dir := t.TempDir()
	chartPath := filepath.Join(dir, "Chart.yaml")
	body := "apiVersion: v2\nname: test\nversion: 0.1.0\nstrictCharts: true\n"
	if err := os.WriteFile(chartPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write Chart.yaml: %v", err)
	}

	snapshotConfigState(t)

	if err := loadConfig(chartPath); err != nil {
		t.Fatalf("loadConfig: %v", err)
	}

	if !commands.Config.StrictCharts {
		t.Error("strictCharts: true in Chart.yaml did not set Config.StrictCharts; the committed enforcement channel is broken")
	}
}

// TestSurfaceChartDrift_StrictSources pins the OR between the two strict
// opt-ins consumed by surfaceChartDrift: the committed Chart.yaml field
// (Config.StrictCharts) and the per-invocation --strict-charts flag. Either
// alone must block on a drifted project; with both off the same project
// must only warn.
func TestSurfaceChartDrift_StrictSources(t *testing.T) {
	snapshot := func(t *testing.T, root string) {
		t.Helper()
		snapshotConfigState(t)

		savedVersion := Version
		savedFlag := strictChartsFlag
		t.Cleanup(func() {
			Version = savedVersion
			strictChartsFlag = savedFlag
		})

		Version = "0.30.0"
		commands.Config.RootDir = root
	}

	t.Run("Chart.yaml strictCharts alone blocks", func(t *testing.T) {
		snapshot(t, writeDriftedTalmProject(t))
		commands.Config.StrictCharts = true
		strictChartsFlag = false

		if err := surfaceChartDrift(); err == nil {
			t.Error("strictCharts: true from Chart.yaml must block on drift without the flag")
		}
	})

	t.Run("--strict-charts flag alone blocks", func(t *testing.T) {
		snapshot(t, writeDriftedTalmProject(t))
		commands.Config.StrictCharts = false
		strictChartsFlag = true

		if err := surfaceChartDrift(); err == nil {
			t.Error("--strict-charts must block on drift without the Chart.yaml field")
		}
	})

	t.Run("neither source set only warns", func(t *testing.T) {
		snapshot(t, writeDriftedTalmProject(t))
		commands.Config.StrictCharts = false
		strictChartsFlag = false

		if err := surfaceChartDrift(); err != nil {
			t.Errorf("drift without strict opt-in must warn, not block: %v", err)
		}
	})
}

// TestRegisterRootFlags_NodesHasNoShorthand pins that talm's
// root `--nodes` does NOT claim the `-n` shorthand. With `-n`
// registered as the alias for `--nodes`, the global captures any
// `-n <value>` an operator types — `talm get hostnames -n network
// --nodes $NODE --endpoints $NODE` quietly parses `network` as a
// second node entry, then fails inside the gRPC name resolver with
// "produced zero addresses". Operators who type `-n namespace`
// (kubectl muscle memory) now get a clean cobra "flag -n not
// defined" error — loud refusal instead of silent
// misinterpretation. The long form `--nodes` keeps working.
// Upstream talosctl does not register `-n` for `--namespace` on
// any wrapped subcommand (image's PersistentFlags --namespace and
// get's local --namespace are both shorthand-free), so the change
// closes a shadow trap without introducing an inherited-alias gap.
func TestRegisterRootFlags_NodesHasNoShorthand(t *testing.T) {
	// registerRootFlags writes default empty strings back through
	// the cobra/pflag StringVar bindings into commands.GlobalArgs
	// and commands.Config, which are package-level mutables.
	// snapshotConfigState (defined above) saves+restores them so
	// other tests aren't poisoned.
	snapshotConfigState(t)

	cmd := &cobra.Command{Use: "talm-test"}
	registerRootFlags(cmd)

	flag := cmd.PersistentFlags().Lookup("nodes")
	if flag == nil {
		t.Fatal("expected --nodes to be registered, got nil")
	}

	if flag.Shorthand != "" {
		t.Errorf("--nodes shorthand: got %q, want empty (otherwise it shadows upstream `-n / --namespace`)", flag.Shorthand)
	}
}

// TestRegisterRootFlags_StrictChartsRendersWithoutValuePlaceholder pins that
// --strict-charts renders in --help as a plain bool flag, not as one taking
// an argument. pflag's UnquoteUsage treats the first backtick-quoted word in
// a flag's usage string as the flag's value-placeholder name; a backtick in
// the --strict-charts usage made --help show `--strict-charts talm init
// --update`, as if the bool flag took a string argument.
func TestRegisterRootFlags_StrictChartsRendersWithoutValuePlaceholder(t *testing.T) {
	snapshotConfigState(t)

	cmd := &cobra.Command{Use: "talm-test"}
	registerRootFlags(cmd)

	flag := cmd.PersistentFlags().Lookup("strict-charts")
	if flag == nil {
		t.Fatal("expected --strict-charts to be registered, got nil")
	}

	name, _ := pflag.UnquoteUsage(flag)
	if name != "" {
		t.Errorf("--strict-charts (a bool flag) renders with value placeholder %q; remove backticks from its usage string", name)
	}
}

// writeDriftedTalmProject creates a project whose vendored charts/talm/
// cannot match the binary's embedded library (the helpers template carries
// a local edit), so CheckChartDrift reports drift.
func writeDriftedTalmProject(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	dir := filepath.Join(root, "charts", "talm", "templates")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "charts", "talm", "Chart.yaml"),
		[]byte("apiVersion: v2\nname: talm\nversion: 0.30.0\ntype: library\n"), 0o644); err != nil {
		t.Fatalf("write Chart.yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "_helpers.tpl"),
		[]byte("{{- /* drifted local edit, not the embedded copy */ -}}\n"), 0o644); err != nil {
		t.Fatalf("write helpers: %v", err)
	}

	return root
}

// writeMatchingTalmProject materializes a vendored charts/talm/ that is
// byte-identical to the embedded library (stamping version into Chart.yaml),
// so CheckChartDrift reports no drift.
func writeMatchingTalmProject(t *testing.T, version string) string {
	t.Helper()

	files, err := generated.TalmLibraryFiles()
	if err != nil {
		t.Fatalf("TalmLibraryFiles: %v", err)
	}

	root := t.TempDir()
	for rel, content := range files {
		if filepath.Base(rel) == "Chart.yaml" {
			content = strings.ReplaceAll(content, "name: %s", "name: talm")
			content = strings.ReplaceAll(content, "version: %s", "version: "+version)
		}

		dest := filepath.Join(root, "charts", "talm", filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(dest, []byte(content), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	return root
}

func hintsContain(err error, substr string) bool {
	for _, hint := range errors.GetAllHints(err) {
		if strings.Contains(hint, substr) {
			return true
		}
	}

	return false
}

// writeDriftedPresetProject materializes a project whose .talm-preset.lock
// pins a baseline hash that cannot match the embedded cozystack preset, so
// CheckPresetDrift reports drift (the older-binary-init scenario).
func writeDriftedPresetProject(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	lock := "preset: cozystack\npresetHash: " + strings.Repeat("0", 64) + "\n"
	if err := os.WriteFile(filepath.Join(root, ".talm-preset.lock"), []byte(lock), 0o644); err != nil {
		t.Fatalf("write preset lock: %v", err)
	}

	return root
}

// TestEvaluateChartDrift pins the warn-vs-fail decision that drives the
// user-facing behavior: strict mode aborts with a remediation hint,
// non-strict drift warns without blocking, a dev build is silent even with
// drift present, and a content-identical library never trips on a
// version-only difference.
func TestEvaluateChartDrift(t *testing.T) {
	t.Run("strict drift returns a hard error hinting at init --update", func(t *testing.T) {
		root := writeDriftedTalmProject(t)

		warning, err := evaluateChartDrift("0.30.0", root, true)
		if err == nil {
			t.Fatal("expected a strict-mode error for a drifted project")
		}
		if warning != "" {
			t.Errorf("strict error path must not also emit a warning, got %q", warning)
		}
		if !hintsContain(err, "talm init --update --preset") {
			t.Errorf("strict-mode error must hint at `talm init --update --preset`, hints: %v", errors.GetAllHints(err))
		}
		if strings.Contains(err.Error(), "or ignore if this is intentional") {
			t.Errorf("strict-mode error must not carry the warning's ignore suggestion — the command just refused to run: %v", err)
		}
	})

	t.Run("non-strict drift warns without blocking", func(t *testing.T) {
		root := writeDriftedTalmProject(t)

		warning, err := evaluateChartDrift("0.30.0", root, false)
		if err != nil {
			t.Fatalf("non-strict drift must not block the command: %v", err)
		}
		if !strings.Contains(warning, "charts/talm") {
			t.Errorf("expected a drift warning mentioning charts/talm, got %q", warning)
		}
	})

	t.Run("unreadable vendored tree under strict blocks the command", func(t *testing.T) {
		// charts/talm as a file: the drift check cannot read the tree.
		// Strict mode exists for enforcement; an unverifiable baseline
		// passing silently would defeat it exactly when the project is
		// broken.
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, "charts"), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(root, "charts", "talm"), []byte("not a directory"), 0o644); err != nil {
			t.Fatalf("write file: %v", err)
		}

		warning, err := evaluateChartDrift("0.30.0", root, true)
		if err == nil {
			t.Fatal("a drift check failure must block under strict mode, not silently pass")
		}
		if warning != "" {
			t.Errorf("strict error path must not also emit a warning, got %q", warning)
		}
	})

	t.Run("unreadable vendored tree without strict downgrades to a warning", func(t *testing.T) {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, "charts"), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(root, "charts", "talm"), []byte("not a directory"), 0o644); err != nil {
			t.Fatalf("write file: %v", err)
		}

		warning, err := evaluateChartDrift("0.30.0", root, false)
		if err != nil {
			t.Fatalf("a non-strict drift check failure must not block the command: %v", err)
		}
		if !strings.Contains(warning, "could not check drift") {
			t.Errorf("expected a could-not-check warning, got %q", warning)
		}
	})

	t.Run("missing vendored library under strict blocks the command", func(t *testing.T) {
		// Deleting charts/talm/ is the cheapest bypass of the check —
		// under opt-in enforcement a missing baseline must block, not
		// pass more quietly than a corrupted one.
		root := t.TempDir()

		warning, err := evaluateChartDrift("0.30.0", root, true)
		if err == nil {
			t.Fatal("a missing vendored library must block under strict mode, not silently pass")
		}
		if warning != "" {
			t.Errorf("strict error path must not also emit a warning, got %q", warning)
		}
		if !hintsContain(err, "talm init --update --preset") {
			t.Errorf("strict-mode error must hint at `talm init --update --preset`, hints: %v", errors.GetAllHints(err))
		}
	})

	t.Run("missing vendored library without strict is silent", func(t *testing.T) {
		root := t.TempDir()

		warning, err := evaluateChartDrift("0.30.0", root, false)
		if err != nil || warning != "" {
			t.Errorf("a project with nothing vendored must stay silent without strict, got warning=%q err=%v", warning, err)
		}
	})

	t.Run("dev build is silent even with drift present and strict set", func(t *testing.T) {
		root := writeDriftedTalmProject(t)

		warning, err := evaluateChartDrift("dev", root, true)
		if err != nil || warning != "" {
			t.Errorf("dev build must be a no-op, got warning=%q err=%v", warning, err)
		}
	})

	t.Run("matching library is silent under strict mode despite a newer binary version", func(t *testing.T) {
		root := writeMatchingTalmProject(t, "0.30.0")

		warning, err := evaluateChartDrift("0.31.0", root, true)
		if err != nil || warning != "" {
			t.Errorf("a content-identical library must not drift on a version-only difference, got warning=%q err=%v", warning, err)
		}
	})
}

// TestEvaluatePresetDrift mirrors TestEvaluateChartDrift for the preset
// baseline (.talm-preset.lock): strict mode aborts with a remediation hint,
// non-strict drift warns without blocking, a dev build is silent, a
// freshly-pinned preset never drifts, and a project with no lock is silent.
func TestEvaluatePresetDrift(t *testing.T) {
	t.Run("strict drift returns a hard error hinting at init --update", func(t *testing.T) {
		root := writeDriftedPresetProject(t)

		warning, err := evaluatePresetDrift("0.30.0", root, true)
		if err == nil {
			t.Fatal("expected a strict-mode error for a drifted preset")
		}
		if warning != "" {
			t.Errorf("strict error path must not also emit a warning, got %q", warning)
		}
		if !hintsContain(err, "talm init --update --preset") {
			t.Errorf("strict-mode error must hint at `talm init --update --preset`, hints: %v", errors.GetAllHints(err))
		}
	})

	t.Run("non-strict drift warns without blocking", func(t *testing.T) {
		root := writeDriftedPresetProject(t)

		warning, err := evaluatePresetDrift("0.30.0", root, false)
		if err != nil {
			t.Fatalf("non-strict drift must not block the command: %v", err)
		}
		if !strings.Contains(warning, "preset") || !strings.Contains(warning, "cozystack") {
			t.Errorf("expected a preset drift warning naming the preset, got %q", warning)
		}
	})

	t.Run("dev build is silent even with drift present and strict set", func(t *testing.T) {
		root := writeDriftedPresetProject(t)

		warning, err := evaluatePresetDrift("dev", root, true)
		if err != nil || warning != "" {
			t.Errorf("dev build must be a no-op, got warning=%q err=%v", warning, err)
		}
	})

	t.Run("freshly-pinned preset is silent under strict mode despite a newer binary version", func(t *testing.T) {
		root := t.TempDir()
		if err := commands.WritePresetLock(root, "cozystack"); err != nil {
			t.Fatalf("WritePresetLock: %v", err)
		}

		warning, err := evaluatePresetDrift("0.31.0", root, true)
		if err != nil || warning != "" {
			t.Errorf("a freshly-pinned preset must not drift, got warning=%q err=%v", warning, err)
		}
	})

	t.Run("project without a preset lock is silent without strict", func(t *testing.T) {
		root := t.TempDir()

		warning, err := evaluatePresetDrift("0.30.0", root, false)
		if err != nil || warning != "" {
			t.Errorf("a project with no preset lock must be silent without strict, got warning=%q err=%v", warning, err)
		}
	})

	t.Run("missing preset lock under strict blocks the command", func(t *testing.T) {
		// A merge conflict resolved as "delete .talm-preset.lock" must
		// not pass MORE quietly than a corrupted lock — under opt-in
		// enforcement a missing baseline is indistinguishable from a
		// deleted one.
		root := t.TempDir()

		warning, err := evaluatePresetDrift("0.30.0", root, true)
		if err == nil {
			t.Fatal("a missing preset baseline must block under strict mode, not silently pass")
		}
		if warning != "" {
			t.Errorf("strict error path must not also emit a warning, got %q", warning)
		}
		if !hintsContain(err, "talm init --update --preset") {
			t.Errorf("strict-mode error must hint at `talm init --update --preset`, hints: %v", errors.GetAllHints(err))
		}
	})

	t.Run("malformed lock under strict blocks the command", func(t *testing.T) {
		// A lock corrupted by a bad merge is exactly the moment the
		// team's strictCharts enforcement must fire, not silently pass.
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, ".talm-preset.lock"), []byte("preset: [unclosed\n"), 0o644); err != nil {
			t.Fatalf("write lock: %v", err)
		}

		warning, err := evaluatePresetDrift("0.30.0", root, true)
		if err == nil {
			t.Fatal("an unreadable preset baseline must block under strict mode, not silently pass")
		}
		if warning != "" {
			t.Errorf("strict error path must not also emit a warning, got %q", warning)
		}
	})

	t.Run("malformed lock without strict downgrades to a warning", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, ".talm-preset.lock"), []byte("preset: [unclosed\n"), 0o644); err != nil {
			t.Fatalf("write lock: %v", err)
		}

		warning, err := evaluatePresetDrift("0.30.0", root, false)
		if err != nil {
			t.Fatalf("a non-strict drift check failure must not block the command: %v", err)
		}
		if !strings.Contains(warning, "could not check drift") {
			t.Errorf("expected a could-not-check warning, got %q", warning)
		}
	})
}

func TestSkipConfigCommands(t *testing.T) {
	tests := []struct {
		name     string
		cmdPath  []string
		expected bool // true = should skip config loading
	}{
		{
			name:     "completion command",
			cmdPath:  []string{"talm", "completion"},
			expected: true,
		},
		{
			name:     "completion bash",
			cmdPath:  []string{"talm", "completion", "bash"},
			expected: true,
		},
		{
			name:     "completion zsh",
			cmdPath:  []string{"talm", "completion", "zsh"},
			expected: true,
		},
		{
			name:     "__complete (cobra internal for shell autocompletion)",
			cmdPath:  []string{"talm", "__complete"},
			expected: true,
		},
		{
			name:     "init command",
			cmdPath:  []string{"talm", "init"},
			expected: true,
		},
		{
			// dmesg is the retired migration stub — must skip
			// Chart.yaml loading so the "talm dmesg has been
			// removed" hint surfaces even outside a project
			// root. Without this membership the operator would
			// see an "error reading configuration file" instead
			// of the migration hint.
			name:     "dmesg (retired migration stub)",
			cmdPath:  []string{"talm", "dmesg"},
			expected: true,
		},
		{
			name:     "apply command should load config",
			cmdPath:  []string{"talm", "apply"},
			expected: false,
		},
		{
			name:     "template command should load config",
			cmdPath:  []string{"talm", "template"},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			leaf := buildCommandHierarchy(tt.cmdPath)
			// This uses the actual skipConfigCommands from main.go
			result := isCommandOrParent(leaf, skipConfigCommands...)
			if result != tt.expected {
				t.Errorf("skipConfigCommands check = %v, want %v (skipConfigCommands = %v)",
					result, tt.expected, skipConfigCommands)
			}
		})
	}
}

// TestReleaseVersion pins that both release build paths are recognized.
// goreleaser injects the version WITHOUT the "v" prefix (`-X
// main.Version={{.Version}}` → "0.30.0") while the Makefile's `git describe
// --tags` keeps it ("v0.30.0"). A previous gate accepted only the
// "v"-prefixed form, which silently disabled the chart-drift check and the
// init version stamp on every downloaded release. Both forms must parse to
// the same version and report isRelease=true; dev/empty builds must not.
func TestReleaseVersion(t *testing.T) {
	tests := []struct {
		name        string
		raw         string
		wantVersion string
		wantRelease bool
	}{
		{"goreleaser form (no v)", "0.30.0", "0.30.0", true},
		{"makefile form (with v)", "v0.30.0", "0.30.0", true},
		{"dev source build", "dev", "", false},
		{"empty version", "", "", false},
		{"prerelease no v", "0.30.0-rc.1", "0.30.0-rc.1", true},
		// git describe on a non-tag commit appends "-<commits>-g<hash>".
		// Such a build is a developer's WIP tree, not a release: its
		// embedded charts are a moving target, so treating it as a
		// release would raise false drift on every command (and hard-
		// fail strict projects). The Makefile now emits "dev" off-tag,
		// but the parser must reject the describe shape regardless of
		// how the string was injected.
		{"describe suffix (with v)", "v0.29.0-5-gabc1234", "", false},
		{"describe suffix (no v)", "0.29.0-5-gabc1234", "", false},
		{"describe suffix dirty", "v0.29.0-5-gabc1234-dirty", "", false},
		// git describe --dirty on an EXACT tag with local edits yields
		// "v0.29.0-dirty" (no commit suffix). Local edits can include the
		// embedded charts themselves, so this build must not pose as the
		// release it was forked from.
		{"dirty at exact tag", "v0.29.0-dirty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			version, isRelease := releaseVersion(tt.raw)
			if isRelease != tt.wantRelease {
				t.Errorf("releaseVersion(%q) isRelease = %v, want %v", tt.raw, isRelease, tt.wantRelease)
			}
			if version != tt.wantVersion {
				t.Errorf("releaseVersion(%q) version = %q, want %q", tt.raw, version, tt.wantVersion)
			}
		})
	}
}
