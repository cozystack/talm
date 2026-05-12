package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cockroachdb/errors"
	"github.com/cozystack/talm/pkg/commands"
	"github.com/spf13/cobra"
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
