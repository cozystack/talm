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

	// Snapshot global commands.Config and restore on exit so we do
	// not leak the parsed (and un-parsed) state into other tests.
	saved := commands.Config
	t.Cleanup(func() { commands.Config = saved })

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

	saved := commands.Config
	t.Cleanup(func() { commands.Config = saved })

	if err := loadConfig(chartPath); err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if got := commands.Config.ApplyOptions.TimeoutDuration; got.String() != "45s" {
		t.Errorf("TimeoutDuration = %v, want 45s", got)
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
