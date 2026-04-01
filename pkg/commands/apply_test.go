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

func TestBuildApplyRenderOptions(t *testing.T) {
	origTalosVersion := applyCmdFlags.talosVersion
	origKubeVersion := applyCmdFlags.kubernetesVersion
	origDebug := applyCmdFlags.debug
	origRootDir := Config.RootDir
	defer func() {
		applyCmdFlags.talosVersion = origTalosVersion
		applyCmdFlags.kubernetesVersion = origKubeVersion
		applyCmdFlags.debug = origDebug
		Config.RootDir = origRootDir
	}()

	applyCmdFlags.talosVersion = "v1.12"
	applyCmdFlags.kubernetesVersion = "1.31.0"
	applyCmdFlags.debug = false
	Config.RootDir = "/project"

	opts := buildApplyRenderOptions(
		[]string{"templates/controlplane.yaml"},
		"/project/secrets.yaml",
	)

	if !opts.Full {
		t.Error("expected Full=true for template rendering path")
	}
	if !opts.Offline {
		t.Error("expected Offline=true for template rendering path")
	}
	if opts.Root != "/project" {
		t.Errorf("expected Root=/project, got %s", opts.Root)
	}
	if opts.TalosVersion != "v1.12" {
		t.Errorf("expected TalosVersion=v1.12, got %s", opts.TalosVersion)
	}
	if opts.WithSecrets != "/project/secrets.yaml" {
		t.Errorf("expected WithSecrets=/project/secrets.yaml, got %s", opts.WithSecrets)
	}
	if len(opts.TemplateFiles) != 1 || opts.TemplateFiles[0] != "templates/controlplane.yaml" {
		t.Errorf("expected TemplateFiles=[templates/controlplane.yaml], got %v", opts.TemplateFiles)
	}
}

func TestBuildApplyPatchOptions(t *testing.T) {
	origTalosVersion := applyCmdFlags.talosVersion
	origKubeVersion := applyCmdFlags.kubernetesVersion
	origDebug := applyCmdFlags.debug
	defer func() {
		applyCmdFlags.talosVersion = origTalosVersion
		applyCmdFlags.kubernetesVersion = origKubeVersion
		applyCmdFlags.debug = origDebug
	}()

	applyCmdFlags.talosVersion = "v1.12"
	applyCmdFlags.kubernetesVersion = "1.31.0"
	applyCmdFlags.debug = false

	opts := buildApplyPatchOptions("/project/secrets.yaml")

	if opts.Full {
		t.Error("expected Full=false for direct patch path")
	}
	if opts.Offline {
		t.Error("expected Offline=false for direct patch path")
	}
	if opts.Root != "" {
		t.Errorf("expected empty Root for direct patch path, got %s", opts.Root)
	}
	if len(opts.TemplateFiles) != 0 {
		t.Errorf("expected no TemplateFiles for direct patch path, got %v", opts.TemplateFiles)
	}
}

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

	// Use a slice with extra capacity so append inside the closure could
	// theoretically leak back to GlobalArgs if the copy is shallow
	nodes := make([]string, 1, 10)
	nodes[0] = "10.0.0.1"
	GlobalArgs.Nodes = nodes

	inner := func(ctx context.Context, c *client.Client) error {
		return nil
	}

	wrapped := wrapWithNodeContext(inner)
	if err := wrapped(context.Background(), nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify GlobalArgs.Nodes is unchanged after wrapWithNodeContext call
	if !slices.Equal(GlobalArgs.Nodes, []string{"10.0.0.1"}) {
		t.Errorf("GlobalArgs.Nodes was mutated to %v, expected [10.0.0.1]", GlobalArgs.Nodes)
	}

	// Verify that the defensive copy is independent: mutating GlobalArgs
	// after wrapper creation doesn't affect a subsequent call
	GlobalArgs.Nodes = []string{"10.0.0.2"}

	var capturedCtx context.Context
	inner2 := func(ctx context.Context, c *client.Client) error {
		capturedCtx = ctx
		return nil
	}
	wrapped2 := wrapWithNodeContext(inner2)
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
