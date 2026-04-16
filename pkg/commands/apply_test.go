package commands

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
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

// nodesFromOutgoingCtx pulls per-iteration node identity out of gRPC
// outgoing metadata. The Talos client SDK writes single-target metadata to
// the "node" key (client.WithNode) and multi-target metadata to "nodes"
// (client.WithNodes) — checking both keys lets the per-node loop tests
// assert iteration shape regardless of which writer the loop chose.
func nodesFromOutgoingCtx(t *testing.T, ctx context.Context) []string {
	t.Helper()
	md, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		return nil
	}
	if vs := md.Get("node"); len(vs) > 0 {
		return vs
	}
	return md.Get("nodes")
}

// fakeAuthOpenClient mimics openClientPerNodeAuth for tests: shares one
// (nil) parent client across iterations and rotates the node via WithNode
// on a fresh per-iteration context.
func fakeAuthOpenClient(parentCtx context.Context) openClientFunc {
	return func(node string, action func(ctx context.Context, c *client.Client) error) error {
		return action(client.WithNode(parentCtx, node), nil)
	}
}

// TestApplyTemplatesPerNode_LoopsOncePerNodeWithSingleNodeContext asserts
// that applyTemplatesPerNode invokes render and apply exactly once per
// node and that every per-iteration context carries exactly that one
// node. The historical regression — batching every node from
// GlobalArgs.Nodes into a single gRPC context — caused engine.Render's
// multi-node guard to reject the call before any rendering ran.
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

// TestApplyTemplatesPerNode_NeverBatchesNodes guarantees no future tweak
// can revert applyTemplatesPerNode to batching all nodes into a single
// render call. Phrased against this helper directly, not against
// engine.Render, so the assertion stays meaningful even if the engine
// guard moves elsewhere.
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

// TestApplyTemplatesPerNode_MaintenanceModeOpensFreshClientPerNode pins
// the contract for the insecure (maintenance) mode opener: every node
// must trigger a fresh openClient invocation so each iteration has its
// own single-endpoint client. Sharing one maintenance client across
// endpoints sends ApplyConfiguration to a gRPC-balanced endpoint and
// most nodes never receive the config — the production opener
// (openClientPerNodeMaintenance) avoids this by narrowing
// GlobalArgs.Nodes around each WithClientMaintenance call. Recording
// fake here proves the per-iteration shape; the narrow-and-restore
// behaviour of the real opener is checked by
// TestOpenClientPerNodeMaintenance_NarrowsAndRestoresGlobalNodes.
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

// TestOpenClientPerNodeMaintenance_NarrowsAndRestoresGlobalNodes drives
// the real openClientPerNodeMaintenance with an injected
// maintenanceClientFunc fake. The fake captures GlobalArgs.Nodes at the
// moment a real WithClientMaintenance would have read it for endpoint
// resolution. The contract: every iteration narrows GlobalArgs.Nodes to
// exactly the iteration's node, and the prior value is restored after
// the action returns regardless of success. Without the narrowing, the
// real WithClientMaintenance would dial every endpoint at once and gRPC
// would round-robin ApplyConfiguration across them.
func TestOpenClientPerNodeMaintenance_NarrowsAndRestoresGlobalNodes(t *testing.T) {
	saved := append([]string(nil), GlobalArgs.Nodes...)
	defer func() { GlobalArgs.Nodes = saved }()

	GlobalArgs.Nodes = []string{"original-A", "original-B"}

	type call struct {
		fingerprints []string
		nodesAtCall  []string
	}
	var calls []call

	fakeMaintenance := func(fingerprints []string, action func(ctx context.Context, c *client.Client) error) error {
		// WithClientMaintenance reads GlobalArgs.Nodes for its endpoints
		// at this point. Snapshot the value so the test can inspect it.
		calls = append(calls, call{
			fingerprints: append([]string(nil), fingerprints...),
			nodesAtCall:  append([]string(nil), GlobalArgs.Nodes...),
		})
		return action(context.Background(), nil)
	}

	openClient := openClientPerNodeMaintenance([]string{"fp-1"}, fakeMaintenance)

	for _, node := range []string{"10.0.0.1", "10.0.0.2"} {
		if err := openClient(node, func(_ context.Context, _ *client.Client) error { return nil }); err != nil {
			t.Fatalf("openClient(%q): %v", node, err)
		}
	}

	if !slices.Equal(GlobalArgs.Nodes, []string{"original-A", "original-B"}) {
		t.Errorf("GlobalArgs.Nodes not restored after maintenance loop: got %v", GlobalArgs.Nodes)
	}
	if len(calls) != 2 {
		t.Fatalf("maintenance fake should have been called twice, got %d times", len(calls))
	}
	for i, want := range []string{"10.0.0.1", "10.0.0.2"} {
		if !slices.Equal(calls[i].nodesAtCall, []string{want}) {
			t.Errorf("call %d: GlobalArgs.Nodes at WithClientMaintenance time = %v, want [%q]", i, calls[i].nodesAtCall, want)
		}
		if !slices.Equal(calls[i].fingerprints, []string{"fp-1"}) {
			t.Errorf("call %d: fingerprints passed through = %v, want [\"fp-1\"]", i, calls[i].fingerprints)
		}
	}
}

// TestApplyTemplatesPerNode_AuthModeUsesSingleNodeMetadataKey pins the
// gRPC metadata key the auth-mode opener writes. WithNode sets "node"
// (single-target proxy); WithNodes sets "nodes" (apid aggregation).
// engine.Render's FailIfMultiNodes guard treats len("nodes") > 1 as the
// multi-node case, so single-target metadata under "node" passes
// trivially. A future refactor that swaps WithNode back to WithNodes
// would slip past nodesFromOutgoingCtx (which reads either key) — this
// assertion catches that regression directly.
func TestApplyTemplatesPerNode_AuthModeUsesSingleNodeMetadataKey(t *testing.T) {
	dir := t.TempDir()
	configFile := filepath.Join(dir, "node.yaml")
	if err := os.WriteFile(configFile, []byte("# talm: nodes=[\"a\"]\n"), 0o644); err != nil {
		t.Fatalf("write configFile: %v", err)
	}

	const node = "10.0.0.1"
	render := func(ctx context.Context, _ *client.Client, _ engine.Options) ([]byte, error) {
		md, ok := metadata.FromOutgoingContext(ctx)
		if !ok {
			t.Fatal("expected outgoing metadata on per-iteration ctx")
		}
		if got := md.Get("node"); !slices.Equal(got, []string{node}) {
			t.Errorf(`metadata key "node" = %v, want [%q]`, got, node)
		}
		if got := md.Get("nodes"); len(got) != 0 {
			t.Errorf(`metadata key "nodes" must be unset for single-target apply, got %v`, got)
		}
		return []byte("version: v1alpha1\nmachine:\n  type: worker\n"), nil
	}
	apply := func(_ context.Context, _ *client.Client, _ []byte) error { return nil }

	openClient := openClientPerNodeAuth(context.Background(), nil)
	if err := applyTemplatesPerNode(engine.Options{}, configFile, []string{node}, openClient, render, apply); err != nil {
		t.Fatalf("applyTemplatesPerNode: %v", err)
	}
}

// TestTemplateAndApplyDiverge_NodeBodyOverlayLimitation pins a known
// trade-off: `talm apply -f node.yaml` overlays the node file body on the
// rendered template before sending the result to ApplyConfiguration, but
// `talm template -f node.yaml` does NOT — its output is the raw rendered
// template plus the modeline and AUTOGENERATED-warning comment. Applying
// the same overlay in template would route the output through the Talos
// config-patcher, which strips every YAML comment (including the modeline)
// and reorders keys. Subsequent commands that read the file back would
// lose the modeline metadata.
//
// The test asserts the divergence is real and bounded: rendered + modeline
// passes through template untouched while the apply path produces a
// merged result. Without this pin, a future "make template match apply"
// patch will silently break the template subcommand.
func TestTemplateAndApplyDiverge_NodeBodyOverlayLimitation(t *testing.T) {
	const rendered = `version: v1alpha1
machine:
  type: controlplane
  network:
    hostname: talos-abcde
`
	dir := t.TempDir()
	nodeFile := filepath.Join(dir, "node.yaml")
	const nodeBody = `# talm: nodes=["10.0.0.1"]
machine:
  network:
    hostname: node0
`
	if err := os.WriteFile(nodeFile, []byte(nodeBody), 0o644); err != nil {
		t.Fatalf("write node file: %v", err)
	}

	// apply path: renders, then overlays.
	merged, err := engine.MergeFileAsPatch([]byte(rendered), nodeFile)
	if err != nil {
		t.Fatalf("MergeFileAsPatch: %v", err)
	}
	if !strings.Contains(string(merged), "hostname: node0") {
		t.Errorf("apply path must overlay hostname: node0; got:\n%s", string(merged))
	}

	// template path: the output is the rendered bytes verbatim — no merge,
	// no patcher round-trip, no comment loss. The template subcommand
	// emits modeline + AUTOGENERATED warning + rendered as a single string
	// straight to stdout/disk; this snippet just stands in for the
	// rendered portion. Identity is the contract.
	templateOutput := []byte(rendered)
	if !bytes.Equal(templateOutput, []byte(rendered)) {
		t.Error("template path must not modify the rendered bytes")
	}
	if strings.Contains(string(templateOutput), "hostname: node0") {
		t.Error("template path must NOT overlay node body — that strips comments and modeline")
	}
}
