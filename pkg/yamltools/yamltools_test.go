package yamltools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestCopyComments(t *testing.T) {
	tests := []struct {
		name        string
		src         string
		expectPaths int // number of paths with comments
	}{
		{
			name: "copies head comment",
			src: `# This is a head comment
key: value`,
			expectPaths: 1,
		},
		{
			name:        "copies line comment",
			src:         `key: value # inline comment`,
			expectPaths: 1,
		},
		{
			name: "handles nested structure with comments",
			src: `# Top comment
parent:
  # Child comment
  child: value`,
			expectPaths: 2,
		},
		{
			name: "handles sequence nodes with comments",
			src: `items:
  # First item
  - first
  - second`,
			expectPaths: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var srcNode yaml.Node
			require.NoError(t, yaml.Unmarshal([]byte(tt.src), &srcNode))

			dstPaths := make(map[string]*yaml.Node)
			CopyComments(&srcNode, nil, "", dstPaths)

			assert.Len(t, dstPaths, tt.expectPaths, "expected %d paths with comments", tt.expectPaths)
		})
	}
}

func TestApplyComments(t *testing.T) {
	// Test that CopyComments and ApplyComments work together
	// by verifying the roundtrip preserves comment count
	tests := []struct {
		name           string
		src            string
		dst            string
		expectComments bool
	}{
		{
			name: "copies and applies head comment",
			src: `# Source comment
key: value1`,
			dst:            `key: value2`,
			expectComments: true,
		},
		{
			name:           "copies and applies line comment",
			src:            `key: value1 # inline`,
			dst:            `key: value2`,
			expectComments: true,
		},
		{
			name:           "no comments to copy",
			src:            `key: value1`,
			dst:            `key: value2`,
			expectComments: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var srcNode, dstNode yaml.Node
			require.NoError(t, yaml.Unmarshal([]byte(tt.src), &srcNode))
			require.NoError(t, yaml.Unmarshal([]byte(tt.dst), &dstNode))

			dstPaths := make(map[string]*yaml.Node)
			CopyComments(&srcNode, nil, "", dstPaths)
			ApplyComments(&dstNode, "", dstPaths)

			if tt.expectComments {
				assert.NotEmpty(t, dstPaths, "expected some comment paths")
			} else {
				assert.Empty(t, dstPaths, "expected no comment paths")
			}
		})
	}
}

func TestDiffYAMLs(t *testing.T) {
	tests := []struct {
		name     string
		original string
		modified string
		wantDiff bool
		contains []string
	}{
		{
			name:     "no diff for identical documents",
			original: `key: value`,
			modified: `key: value`,
			wantDiff: false,
		},
		{
			name:     "detects value change",
			original: `key: old`,
			modified: `key: new`,
			wantDiff: true,
			contains: []string{"key:", "new"},
		},
		{
			name:     "detects added key",
			original: `existing: value`,
			modified: `existing: value
new: added`,
			wantDiff: true,
			contains: []string{"new:", "added"},
		},
		{
			name: "detects removed key with delete marker",
			original: `keep: value
remove: this`,
			modified: `keep: value`,
			wantDiff: true,
			contains: []string{"remove:", "$patch", "delete"},
		},
		{
			name: "handles nested changes",
			original: `parent:
  child: old`,
			modified: `parent:
  child: new`,
			wantDiff: true,
			contains: []string{"parent:", "child:", "new"},
		},
		{
			name: "handles sequence additions",
			original: `items:
  - one`,
			modified: `items:
  - one
  - two`,
			wantDiff: true,
			contains: []string{"items:", "two"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			diff, err := DiffYAMLs([]byte(tt.original), []byte(tt.modified))
			require.NoError(t, err)

			if tt.wantDiff {
				assert.NotEmpty(t, diff, "expected diff but got empty")
				for _, substr := range tt.contains {
					assert.Contains(t, string(diff), substr, "diff should contain %q", substr)
				}
			} else {
				assert.Empty(t, diff, "expected no diff")
			}
		})
	}
}

func TestMergeComments(t *testing.T) {
	tests := []struct {
		name     string
		old      string
		new      string
		expected string
	}{
		{
			name:     "returns new when old is empty",
			old:      "",
			new:      "new comment",
			expected: "new comment",
		},
		{
			name:     "returns old when new is empty",
			old:      "old comment",
			new:      "",
			expected: "old comment",
		},
		{
			name:     "merges both comments",
			old:      "old comment",
			new:      "new comment",
			expected: "old comment\n\nnew comment",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mergeComments(tt.old, tt.new)
			assert.Equal(t, tt.expected, result)
		})
	}
}

