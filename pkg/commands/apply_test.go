package commands

import (
	"testing"
)

func TestShouldUseTemplateRendering(t *testing.T) {
	tests := []struct {
		name      string
		templates []string
		want      bool
	}{
		{"nil templates", nil, false},
		{"empty templates", []string{}, false},
		{"one template", []string{"templates/controlplane.yaml"}, true},
		{"multiple templates", []string{"templates/controlplane.yaml", "templates/worker.yaml"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldUseTemplateRendering(tt.templates)
			if got != tt.want {
				t.Errorf("shouldUseTemplateRendering(%v) = %v, want %v", tt.templates, got, tt.want)
			}
		})
	}
}

func TestResolveTemplatePaths(t *testing.T) {
	tests := []struct {
		name      string
		templates []string
		rootDir   string
		want      []string
	}{
		{
			name:      "simple relative path",
			templates: []string{"templates/controlplane.yaml"},
			rootDir:   "",
			want:      []string{"templates/controlplane.yaml"},
		},
		{
			name:      "multiple paths",
			templates: []string{"templates/controlplane.yaml", "templates/worker.yaml"},
			rootDir:   "",
			want:      []string{"templates/controlplane.yaml", "templates/worker.yaml"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveTemplatePaths(tt.templates, tt.rootDir)
			if len(got) != len(tt.want) {
				t.Fatalf("resolveTemplatePaths() returned %d items, want %d", len(got), len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("resolveTemplatePaths()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}
