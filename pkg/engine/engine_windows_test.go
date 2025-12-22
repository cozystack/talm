//go:build windows

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

package engine

import "testing"

func TestNormalizeTemplatePathWindows(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"backslash", "templates\\file.yaml", "templates/file.yaml"},
		{"nested backslash", "templates\\nested\\file.yaml", "templates/nested/file.yaml"},
		{"mixed slashes", "templates\\nested/file.yaml", "templates/nested/file.yaml"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeTemplatePath(tt.input); got != tt.want {
				t.Errorf("normalizeTemplatePath(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
