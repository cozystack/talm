package commands

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/cozystack/talm/pkg/engine"
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
	if opts.Offline {
		t.Error("expected Offline=false for online template rendering path")
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

// nodesFromOutgoingCtx pulls the gRPC outgoing-metadata "nodes" key — the same
// place client.WithNodes writes to. Used by the per-node loop tests to assert
// each iteration sees a single-node context.
func nodesFromOutgoingCtx(t *testing.T, ctx context.Context) []string {
	t.Helper()
	md, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		return nil
	}
	return md.Get("nodes")
}

// TestApplyTemplatesPerNode_LoopsOncePerNodeWithSingleNodeContext covers #120:
// the multi-node fan-out previously batched every node into a single gRPC
// context, which engine.Render's FailIfMultiNodes guard then rejected. The
// per-node loop must run render + merge + apply once per node, each with a
// context carrying exactly that one node, so the guard passes and each node
// resolves its own discovery.
func TestApplyTemplatesPerNode_LoopsOncePerNodeWithSingleNodeContext(t *testing.T) {
	dir := t.TempDir()
	configFile := filepath.Join(dir, "node.yaml")
	// Modeline-only file so MergeFileAsPatch is a no-op and the test stays
	// focused on the loop semantics rather than patch behaviour.
	if err := os.WriteFile(configFile, []byte("# talm: nodes=[\"a\",\"b\",\"c\"], templates=[\"templates/controlplane.yaml\"]\n"), 0o644); err != nil {
		t.Fatalf("write configFile: %v", err)
	}

	want := []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"}
	var renderCalls, applyCalls []string

	render := func(ctx context.Context, _ *client.Client, _ engine.Options) ([]byte, error) {
		got := nodesFromOutgoingCtx(t, ctx)
		if len(got) != 1 {
			t.Errorf("render: expected single-node ctx, got %v", got)
		}
		renderCalls = append(renderCalls, got...)
		return []byte("version: v1alpha1\nmachine:\n  type: worker\n"), nil
	}
	apply := func(ctx context.Context, _ *client.Client, _ []byte) error {
		got := nodesFromOutgoingCtx(t, ctx)
		if len(got) != 1 {
			t.Errorf("apply: expected single-node ctx, got %v", got)
		}
		applyCalls = append(applyCalls, got...)
		return nil
	}

	if err := applyTemplatesPerNode(context.Background(), nil, engine.Options{}, configFile, want, render, apply); err != nil {
		t.Fatalf("applyTemplatesPerNode: %v", err)
	}

	if !slices.Equal(renderCalls, want) {
		t.Errorf("render calls = %v, want %v", renderCalls, want)
	}
	if !slices.Equal(applyCalls, want) {
		t.Errorf("apply calls  = %v, want %v", applyCalls, want)
	}
}

// TestApplyTemplatesPerNode_BatchedContextIsRejected covers the regression
// vector that motivated the per-node loop: feeding engine.Render a context
// with multiple nodes produces a FailIfMultiNodes error. The per-node loop is
// the cure; this test pins the disease.
func TestApplyTemplatesPerNode_BatchedContextIsRejected(t *testing.T) {
	dir := t.TempDir()
	configFile := filepath.Join(dir, "node.yaml")
	if err := os.WriteFile(configFile, []byte("# talm: nodes=[\"a\",\"b\"]\n"), 0o644); err != nil {
		t.Fatalf("write configFile: %v", err)
	}

	// Sanity: when the loop hands engine.Render a single-node ctx, the
	// guard is satisfied. We exercise this above. Here we assert that if
	// somebody tried to feed a multi-node ctx to render directly, the
	// real engine.Render would reject it — the bug we are working around.
	multiCtx := client.WithNodes(context.Background(), "10.0.0.1", "10.0.0.2")
	_, err := engine.Render(multiCtx, nil, engine.Options{Offline: false, CommandName: "talm apply"})
	if err == nil {
		t.Fatal("engine.Render expected to reject multi-node ctx, got nil")
	}
}

// TestApplyTemplatesPerNode_NoNodesIsAnError guards against silently
// short-circuiting when GlobalArgs.Nodes is empty. The previous structure
// would happily run zero iterations — this pin makes it loud.
func TestApplyTemplatesPerNode_NoNodesIsAnError(t *testing.T) {
	dir := t.TempDir()
	configFile := filepath.Join(dir, "node.yaml")
	if err := os.WriteFile(configFile, []byte("# talm: nodes=[]\n"), 0o644); err != nil {
		t.Fatalf("write configFile: %v", err)
	}

	render := func(_ context.Context, _ *client.Client, _ engine.Options) ([]byte, error) {
		t.Fatal("render must not be called when there are zero nodes")
		return nil, nil
	}
	apply := func(_ context.Context, _ *client.Client, _ []byte) error {
		t.Fatal("apply must not be called when there are zero nodes")
		return nil
	}

	if err := applyTemplatesPerNode(context.Background(), nil, engine.Options{}, configFile, nil, render, apply); err == nil {
		t.Fatal("expected an error for empty nodes list, got nil")
	}
}
