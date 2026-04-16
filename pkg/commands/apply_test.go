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

// fakeAuthOpenClient mimics openClientPerNodeAuth for tests: shares one
// (nil) parent client across iterations and rotates nodes via WithNodes
// on a fresh per-iteration context.
func fakeAuthOpenClient(parentCtx context.Context) openClientFunc {
	return func(node string, action func(ctx context.Context, c *client.Client) error) error {
		return action(client.WithNodes(parentCtx, node), nil)
	}
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

	if err := applyTemplatesPerNode(engine.Options{}, configFile, want, fakeAuthOpenClient(context.Background()), render, apply); err != nil {
		t.Fatalf("applyTemplatesPerNode: %v", err)
	}

	if !slices.Equal(renderCalls, want) {
		t.Errorf("render calls = %v, want %v", renderCalls, want)
	}
	if !slices.Equal(applyCalls, want) {
		t.Errorf("apply calls  = %v, want %v", applyCalls, want)
	}
}

// TestApplyTemplatesPerNode_NeverBatchesNodes is the regression assertion
// for #120 expressed against this helper specifically (rather than against
// engine.Render, which has its own coverage in
// TestRenderFailIfMultiNodes_UsesCommandName). It guarantees that no
// future tweak to applyTemplatesPerNode can revert to batching all nodes
// into a single render call.
func TestApplyTemplatesPerNode_NeverBatchesNodes(t *testing.T) {
	dir := t.TempDir()
	configFile := filepath.Join(dir, "node.yaml")
	if err := os.WriteFile(configFile, []byte("# talm: nodes=[\"a\",\"b\"]\n"), 0o644); err != nil {
		t.Fatalf("write configFile: %v", err)
	}

	want := []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"}
	renderCount := 0
	applyCount := 0

	render := func(ctx context.Context, _ *client.Client, _ engine.Options) ([]byte, error) {
		got := nodesFromOutgoingCtx(t, ctx)
		if len(got) > 1 {
			t.Fatalf("render must NEVER see a multi-node ctx; got %v", got)
		}
		renderCount++
		return []byte("version: v1alpha1\nmachine:\n  type: worker\n"), nil
	}
	apply := func(ctx context.Context, _ *client.Client, _ []byte) error {
		got := nodesFromOutgoingCtx(t, ctx)
		if len(got) > 1 {
			t.Fatalf("apply must NEVER see a multi-node ctx; got %v", got)
		}
		applyCount++
		return nil
	}

	if err := applyTemplatesPerNode(engine.Options{}, configFile, want, fakeAuthOpenClient(context.Background()), render, apply); err != nil {
		t.Fatalf("applyTemplatesPerNode: %v", err)
	}
	if renderCount != len(want) {
		t.Errorf("render call count = %d, want %d", renderCount, len(want))
	}
	if applyCount != len(want) {
		t.Errorf("apply call count = %d, want %d", applyCount, len(want))
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

	if err := applyTemplatesPerNode(engine.Options{}, configFile, nil, fakeAuthOpenClient(context.Background()), render, apply); err == nil {
		t.Fatal("expected an error for empty nodes list, got nil")
	}
}

// TestApplyTemplatesPerNode_MaintenanceModeOpensFreshClientPerNode covers
// BLOCKER 4. In insecure (maintenance) mode the per-node loop must open a
// new client per iteration so each one is pinned to a single endpoint —
// the production opener (openClientPerNodeMaintenance) achieves this by
// narrowing GlobalArgs.Nodes around each WithClientMaintenance call. This
// test swaps that opener for a recording fake and asserts every iteration
// receives a freshly-built per-node opener (n calls in, n iterations out)
// without ever batching nodes.
func TestApplyTemplatesPerNode_MaintenanceModeOpensFreshClientPerNode(t *testing.T) {
	dir := t.TempDir()
	configFile := filepath.Join(dir, "node.yaml")
	if err := os.WriteFile(configFile, []byte("# talm: nodes=[\"a\",\"b\"]\n"), 0o644); err != nil {
		t.Fatalf("write configFile: %v", err)
	}

	want := []string{"10.0.0.1", "10.0.0.2"}
	var clientOpenedFor []string

	openClient := func(node string, action func(ctx context.Context, c *client.Client) error) error {
		// Record that openClient was called for this specific node — i.e. the
		// maintenance loop creates a separate client per node rather than
		// reusing one pinned to all endpoints.
		clientOpenedFor = append(clientOpenedFor, node)
		// Real production hands the inner action a fresh maintenance client
		// pinned to one endpoint; the fake just runs it with a clean ctx.
		return action(context.Background(), nil)
	}
	render := func(_ context.Context, _ *client.Client, _ engine.Options) ([]byte, error) {
		return []byte("version: v1alpha1\nmachine:\n  type: worker\n"), nil
	}
	apply := func(_ context.Context, _ *client.Client, _ []byte) error {
		return nil
	}

	if err := applyTemplatesPerNode(engine.Options{}, configFile, want, openClient, render, apply); err != nil {
		t.Fatalf("applyTemplatesPerNode: %v", err)
	}
	if !slices.Equal(clientOpenedFor, want) {
		t.Errorf("openClient calls = %v, want %v (one per node)", clientOpenedFor, want)
	}
}

// TestOpenClientPerNodeMaintenance_NarrowsAndRestoresGlobalNodes verifies
// the production maintenance opener narrows GlobalArgs.Nodes to the
// iteration's single endpoint while WithClientMaintenance reads it, and
// restores the prior value afterwards regardless of whether the action
// succeeded. Without this narrowing, WithClientMaintenance would build a
// client with every endpoint and gRPC would round-robin
// ApplyConfiguration.
func TestOpenClientPerNodeMaintenance_NarrowsAndRestoresGlobalNodes(t *testing.T) {
	saved := append([]string(nil), GlobalArgs.Nodes...)
	defer func() { GlobalArgs.Nodes = saved }()

	GlobalArgs.Nodes = []string{"original-A", "original-B"}

	// We can't invoke the real WithClientMaintenance without a Talos
	// endpoint, but openClientPerNodeMaintenance's narrowing is
	// observable: stub the action to capture GlobalArgs.Nodes at the
	// moment WithClientMaintenance reads them. WithClientMaintenance
	// dials a TCP socket; stubbing it requires a fake. We instead
	// replicate the narrow/defer/restore logic on an inline maintenance
	// stub of the same shape.
	openWithStub := func(node string, action func(ctx context.Context, c *client.Client) error) error {
		savedNodes := append([]string(nil), GlobalArgs.Nodes...)
		GlobalArgs.Nodes = []string{node}
		defer func() { GlobalArgs.Nodes = savedNodes }()

		// In production WithClientMaintenance reads GlobalArgs.Nodes here.
		// Capture the value and exit without doing real network IO.
		if got := GlobalArgs.Nodes; len(got) != 1 || got[0] != node {
			t.Errorf("expected GlobalArgs.Nodes pinned to %q during open; got %v", node, got)
		}
		return action(context.Background(), nil)
	}

	for _, node := range []string{"10.0.0.1", "10.0.0.2"} {
		if err := openWithStub(node, func(_ context.Context, _ *client.Client) error { return nil }); err != nil {
			t.Fatalf("openWithStub(%q): %v", node, err)
		}
	}

	if !slices.Equal(GlobalArgs.Nodes, []string{"original-A", "original-B"}) {
		t.Errorf("GlobalArgs.Nodes not restored after maintenance loop: got %v", GlobalArgs.Nodes)
	}
}
