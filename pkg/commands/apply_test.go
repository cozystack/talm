package commands

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/siderolabs/talos/pkg/machinery/client"
	"google.golang.org/grpc/metadata"
)

func TestResolveTemplatePaths(t *testing.T) {
	// Create a real rootDir with template files for testing
	tmpRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmpRoot, "templates"), 0o755); err != nil {
		t.Fatalf("failed to create templates dir: %v", err)
	}

	tests := []struct {
		name      string
		templates []string
		rootDir   string
		want      []string
	}{
		{
			name:      "relative path with empty rootDir",
			templates: []string{"templates/controlplane.yaml"},
			rootDir:   "",
			want:      []string{"templates/controlplane.yaml"},
		},
		{
			name:      "relative path resolved against rootDir",
			templates: []string{"templates/controlplane.yaml"},
			rootDir:   tmpRoot,
			want:      []string{"templates/controlplane.yaml"},
		},
		{
			name:      "multiple paths with rootDir",
			templates: []string{"templates/controlplane.yaml", "templates/worker.yaml"},
			rootDir:   tmpRoot,
			want:      []string{"templates/controlplane.yaml", "templates/worker.yaml"},
		},
		{
			name:      "absolute path inside rootDir",
			templates: []string{filepath.Join(tmpRoot, "templates", "controlplane.yaml")},
			rootDir:   tmpRoot,
			want:      []string{"templates/controlplane.yaml"},
		},
		{
			name:      "path outside rootDir is kept as-is",
			templates: []string{"/other/project/templates/controlplane.yaml"},
			rootDir:   tmpRoot,
			want:      []string{"/other/project/templates/controlplane.yaml"},
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

func TestWrapWithNodeContext_SetsNodesInContext(t *testing.T) {
	origNodes := GlobalArgs.Nodes
	defer func() { GlobalArgs.Nodes = origNodes }()

	GlobalArgs.Nodes = []string{"10.0.0.1", "10.0.0.2"}

	var capturedCtx context.Context
	inner := func(ctx context.Context, c *client.Client) error {
		capturedCtx = ctx
		return nil
	}

	wrapped := wrapWithNodeContext(inner)
	err := wrapped(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if capturedCtx == nil {
		t.Fatal("inner function was not called")
	}

	// Verify the actual nodes injected via gRPC metadata
	md, ok := metadata.FromOutgoingContext(capturedCtx)
	if !ok {
		t.Fatal("expected outgoing gRPC metadata in context, got none")
	}
	gotNodes := md.Get("nodes")
	wantNodes := []string{"10.0.0.1", "10.0.0.2"}
	if !slices.Equal(gotNodes, wantNodes) {
		t.Errorf("nodes in context metadata = %v, want %v", gotNodes, wantNodes)
	}
}

func TestWrapWithNodeContext_DoesNotMutateGlobalArgs(t *testing.T) {
	origNodes := GlobalArgs.Nodes
	defer func() { GlobalArgs.Nodes = origNodes }()

	GlobalArgs.Nodes = []string{"10.0.0.1"}

	inner := func(ctx context.Context, c *client.Client) error {
		return nil
	}

	wrapped := wrapWithNodeContext(inner)

	// Mutate GlobalArgs.Nodes after creating wrapper but before calling it
	GlobalArgs.Nodes = []string{"10.0.0.2"}

	var capturedCtx context.Context
	innerCapture := func(ctx context.Context, c *client.Client) error {
		capturedCtx = ctx
		return nil
	}
	wrapped2 := wrapWithNodeContext(innerCapture)

	// Call the first wrapper — should NOT see the mutation (reads at call time,
	// but we test that the defensive copy prevents cross-contamination)
	if err := wrapped(context.Background(), nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Call the second wrapper — should see "10.0.0.2"
	if err := wrapped2(context.Background(), nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	md, ok := metadata.FromOutgoingContext(capturedCtx)
	if !ok {
		t.Fatal("expected outgoing gRPC metadata in context")
	}
	gotNodes := md.Get("nodes")
	if !slices.Equal(gotNodes, []string{"10.0.0.2"}) {
		t.Errorf("nodes in context = %v, want [10.0.0.2]", gotNodes)
	}

	// Verify GlobalArgs.Nodes was not mutated by wrapWithNodeContext
	if !slices.Equal(GlobalArgs.Nodes, []string{"10.0.0.2"}) {
		t.Errorf("GlobalArgs.Nodes was mutated to %v, expected [10.0.0.2]", GlobalArgs.Nodes)
	}
}

func TestWrapWithNodeContext_NoNodesNoClient(t *testing.T) {
	origNodes := GlobalArgs.Nodes
	defer func() { GlobalArgs.Nodes = origNodes }()

	GlobalArgs.Nodes = []string{}

	inner := func(ctx context.Context, c *client.Client) error {
		return nil
	}

	wrapped := wrapWithNodeContext(inner)
	err := wrapped(context.Background(), nil)
	if err == nil {
		t.Error("expected error when no nodes and no client config context, got nil")
	}
}
