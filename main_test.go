package main

import (
	"testing"

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
