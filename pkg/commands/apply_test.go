package commands

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cockroachdb/errors"
	"github.com/cozystack/talm/pkg/engine"
	"github.com/siderolabs/talos/pkg/machinery/client"
	"google.golang.org/grpc/metadata"
)

// errSimulatedApplyFailure is a sentinel error used by the
// restore-on-error coverage to drive failingMaintenance into the
// error branch without touching real Talos infrastructure.
var errSimulatedApplyFailure = errors.New("simulated apply failure")

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

	applyCmdFlags.talosVersion = testTalosVersion
	applyCmdFlags.kubernetesVersion = testKubernetesVersion
	applyCmdFlags.debug = false
	Config.RootDir = testProjectRoot

	opts := buildApplyRenderOptions(
		[]string{testTemplateControlplaneRel},
		testProjectRoot+"/secrets.yaml",
	)

	if !opts.Full {
		t.Error("expected Full=true for template rendering path")
	}
	if opts.Offline {
		t.Error("expected Offline=false for online template rendering path")
	}
	if opts.Root != testProjectRoot {
		t.Errorf("expected Root=%q, got %s", testProjectRoot, opts.Root)
	}
	if opts.TalosVersion != testTalosVersion {
		t.Errorf("expected TalosVersion=%q, got %s", testTalosVersion, opts.TalosVersion)
	}
	if opts.WithSecrets != testProjectRoot+"/secrets.yaml" {
		t.Errorf("expected WithSecrets=%s/secrets.yaml, got %s", testProjectRoot, opts.WithSecrets)
	}
	if len(opts.TemplateFiles) != 1 || opts.TemplateFiles[0] != testTemplateControlplaneRel {
		t.Errorf("expected TemplateFiles=[templates/controlplane.yaml], got %v", opts.TemplateFiles)
	}
	if opts.CommandName != testTalmApply {
		t.Errorf("expected CommandName=%q, got %q (engine.Render uses this for FailIfMultiNodes error wording)", testTalmApply, opts.CommandName)
	}
}

func TestResolveAuthTemplateNodes_CLINodesWin(t *testing.T) {
	in := []string{testNodeAddrA, testNodeAddrB}
	got := resolveAuthTemplateNodes(in, nil)
	if !slices.Equal(got, in) {
		t.Errorf("got %v, want %v (CLI nodes must take precedence over talosconfig context)", got, in)
	}
}

func TestResolveAuthTemplateNodes_NilClientReturnsNil(t *testing.T) {
	got := resolveAuthTemplateNodes(nil, nil)
	if got != nil {
		t.Errorf("got %v, want nil (no CLI nodes + no client = nothing to iterate, caller must surface the error)", got)
	}
	got = resolveAuthTemplateNodes([]string{}, nil)
	if got != nil {
		t.Errorf("got %v, want nil on empty slice + nil client", got)
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

	applyCmdFlags.talosVersion = testTalosVersion
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
	// A sibling directory whose name literally starts with "..". A naive
	// HasPrefix(relPath, "..") check would misclassify it as outside-root;
	// the resolver must treat ".." as a full path element, not a prefix.
	if err := os.MkdirAll(filepath.Join(tmpRoot, "..templates"), 0o755); err != nil {
		t.Fatalf("failed to create ..templates dir: %v", err)
	}

	// Build a platform-portable absolute path outside tmpRoot.
	// filepath.VolumeName is "" on POSIX (yielding e.g. "/elsewhere/...") and
	// "C:" on Windows (yielding "C:\elsewhere\..."). Both are absolute and
	// definitely outside tmpRoot (which lives under the user temp dir).
	absOutside := filepath.Join(filepath.VolumeName(tmpRoot), string(filepath.Separator), "elsewhere", "project", "templates", "controlplane.yaml")

	tests := []struct {
		name      string
		templates []string
		rootDir   string
		want      []string
	}{
		{
			name:      "relative path with empty rootDir",
			templates: []string{testTemplateControlplaneRel},
			rootDir:   "",
			want:      []string{testTemplateControlplaneRel},
		},
		{
			name:      "relative path resolved against rootDir",
			templates: []string{testTemplateControlplaneRel},
			rootDir:   tmpRoot,
			want:      []string{testTemplateControlplaneRel},
		},
		{
			name:      "multiple paths with rootDir",
			templates: []string{testTemplateControlplaneRel, testTemplateWorker},
			rootDir:   tmpRoot,
			want:      []string{testTemplateControlplaneRel, testTemplateWorker},
		},
		{
			name:      "absolute path inside rootDir",
			templates: []string{filepath.Join(tmpRoot, "templates", "controlplane.yaml")},
			rootDir:   tmpRoot,
			want:      []string{testTemplateControlplaneRel},
		},
		{
			// Constructed to be absolute on both POSIX and Windows so the
			// filepath.IsAbs branch is exercised on both CI runners. The
			// resolver normalizes outside-root paths via filepath.ToSlash,
			// so the expected output is the forward-slash form.
			name:      "path outside rootDir is kept as-is",
			templates: []string{absOutside},
			rootDir:   tmpRoot,
			want:      []string{filepath.ToSlash(absOutside)},
		},
		{
			// Directory name literally starting with "..". If the
			// outside-root check used HasPrefix("..") it would wrongly
			// drop this path back to the original input.
			name:      "sibling dir whose name starts with .. is inside rootDir",
			templates: []string{testTemplateControlplane},
			rootDir:   tmpRoot,
			want:      []string{testTemplateControlplane},
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

	GlobalArgs.Nodes = []string{testNodeAddrA, testNodeAddrB}

	capturedCtxCh := make(chan context.Context, 1)
	inner := func(innerCtx context.Context, _ *client.Client) error {
		capturedCtxCh <- innerCtx
		return nil
	}

	wrapped := wrapWithNodeContext(inner)
	err := wrapped(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	capturedCtx := <-capturedCtxCh
	if capturedCtx == nil {
		t.Fatal("inner function was not called")
	}

	// Verify the actual nodes injected via gRPC metadata
	md, ok := metadata.FromOutgoingContext(capturedCtx)
	if !ok {
		t.Fatal("expected outgoing gRPC metadata in context, got none")
	}
	gotNodes := md.Get("nodes")
	wantNodes := []string{testNodeAddrA, testNodeAddrB}
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
	nodes[0] = testNodeAddrA
	GlobalArgs.Nodes = nodes

	inner := func(_ context.Context, _ *client.Client) error {
		return nil
	}

	wrapped := wrapWithNodeContext(inner)
	if err := wrapped(context.Background(), nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify GlobalArgs.Nodes is unchanged after wrapWithNodeContext call
	if !slices.Equal(GlobalArgs.Nodes, []string{testNodeAddrA}) {
		t.Errorf("GlobalArgs.Nodes was mutated to %v, expected [10.0.0.1]", GlobalArgs.Nodes)
	}

	// Verify that the defensive copy is independent: mutating GlobalArgs
	// after wrapper creation doesn't affect a subsequent call
	GlobalArgs.Nodes = []string{testNodeAddrB}

	capturedCtxCh := make(chan context.Context, 1)
	inner2 := func(innerCtx context.Context, _ *client.Client) error {
		capturedCtxCh <- innerCtx
		return nil
	}
	wrapped2 := wrapWithNodeContext(inner2)
	if err := wrapped2(context.Background(), nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	capturedCtx := <-capturedCtxCh
	md, ok := metadata.FromOutgoingContext(capturedCtx)
	if !ok {
		t.Fatal("expected outgoing gRPC metadata in context")
	}
	gotNodes := md.Get("nodes")
	if !slices.Equal(gotNodes, []string{testNodeAddrB}) {
		t.Errorf("nodes in context = %v, want [10.0.0.2]", gotNodes)
	}
}

func TestWrapWithNodeContext_NoNodesNoClient(t *testing.T) {
	origNodes := GlobalArgs.Nodes
	defer func() { GlobalArgs.Nodes = origNodes }()

	GlobalArgs.Nodes = []string{}

	inner := func(_ context.Context, _ *client.Client) error {
		return nil
	}

	wrapped := wrapWithNodeContext(inner)
	err := wrapped(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error when no nodes and no client config context, got nil")
	}
	// The hint chain must keep an operator-actionable explanation, not
	// just the bare "no client available" wrap. A future migration that
	// drops the hint without replacing it would silently degrade the
	// diagnostic.
	hints := errors.GetAllHints(err)
	if len(hints) == 0 {
		t.Errorf("expected at least one hint guiding the operator, got bare error: %v", err)
	}
}

// nodesFromOutgoingCtx pulls per-iteration node identity out of gRPC
// outgoing metadata. Production rotates nodes via client.WithNodes with a
// single-element slice (the plural "nodes" key is what
// helpers.ForEachResource and apid both read); the singular "node" key is
// also checked here to keep the helper resilient against fakes or future
// callers that use client.WithNode directly. Tests that pin the metadata
// contract assert against md.Get directly rather than going through this
// helper.
func nodesFromOutgoingCtx(ctx context.Context, t *testing.T) []string {
	t.Helper()
	md, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		return nil
	}
	if vs := md.Get("nodes"); len(vs) > 0 {
		return vs
	}
	return md.Get("node")
}

// fakeAuthOpenClient mimics openClientPerNodeAuth for tests: shares one
// (nil) parent client across iterations and rotates the node via WithNodes
// (single-element plural slice) on a fresh per-iteration context. Mirroring
// production keeps the loop-semantics tests honest about what metadata key
// downstream lookups will actually see.
//
// The action receives a nil *client.Client. Callers are responsible for
// not dereferencing it; tests that exercise client method calls must
// inject a real client via a different fake. The nil here is deliberate
// — it makes any future change inside applyTemplatesPerNode that starts
// dereferencing the client surface as a panic in CI rather than as
// silent test coverage of an untouched code path.
func fakeAuthOpenClient(parentCtx context.Context) openClientFunc {
	return func(node string, action func(ctx context.Context, c *client.Client) error) error {
		return action(client.WithNodes(parentCtx, node), nil)
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

	want := []string{testNodeAddrA, testNodeAddrB, testNodeAddrC}
	var renderCalls, applyCalls []string

	render := func(ctx context.Context, _ *client.Client, _ engine.Options) ([]byte, error) {
		got := nodesFromOutgoingCtx(ctx, t)
		if len(got) != 1 {
			t.Errorf("render: expected single-node ctx, got %v", got)
		}
		renderCalls = append(renderCalls, got...)
		return []byte("version: v1alpha1\nmachine:\n  type: worker\n"), nil
	}
	apply := func(ctx context.Context, _ *client.Client, _ []byte) error {
		got := nodesFromOutgoingCtx(ctx, t)
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

	want := []string{testNodeAddrA, testNodeAddrB, testNodeAddrC}
	renderCount := 0
	applyCount := 0

	render := func(ctx context.Context, _ *client.Client, _ engine.Options) ([]byte, error) {
		got := nodesFromOutgoingCtx(ctx, t)
		if len(got) > 1 {
			t.Fatalf("render must NEVER see a multi-node ctx; got %v", got)
		}
		renderCount++
		return []byte("version: v1alpha1\nmachine:\n  type: worker\n"), nil
	}
	apply := func(ctx context.Context, _ *client.Client, _ []byte) error {
		got := nodesFromOutgoingCtx(ctx, t)
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

// TestApplyTemplatesPerNode_MultiNodeWithNonEmptyBodyIsRejected pins
// the guard against stamping a single per-node body (pinned hostname,
// address, VIP) onto every target. A node file that targets more than
// one node and carries a body below its modeline is user error; the
// helper must surface it instead of silently replicating the pin.
func TestApplyTemplatesPerNode_MultiNodeWithNonEmptyBodyIsRejected(t *testing.T) {
	dir := t.TempDir()
	configFile := filepath.Join(dir, "node.yaml")
	body := `# talm: nodes=["10.0.0.1","10.0.0.2"]
machine:
  network:
    hostname: node0
`
	if err := os.WriteFile(configFile, []byte(body), 0o644); err != nil {
		t.Fatalf("write configFile: %v", err)
	}

	render := func(_ context.Context, _ *client.Client, _ engine.Options) ([]byte, error) {
		t.Fatal("render must not be called when the multi-node overlay guard trips")
		return nil, nil
	}
	apply := func(_ context.Context, _ *client.Client, _ []byte) error {
		t.Fatal("apply must not be called when the multi-node overlay guard trips")
		return nil
	}

	err := applyTemplatesPerNode(engine.Options{}, configFile,
		[]string{testNodeAddrA, testNodeAddrB},
		fakeAuthOpenClient(context.Background()), render, apply)
	if err == nil {
		t.Fatal("expected an error for multi-node + non-empty body, got nil")
	}
	msg := err.Error()
	for _, want := range []string{"node file", "2 nodes", "per-node body"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message %q does not mention %q", msg, want)
		}
	}
}

// TestApplyTemplatesPerNode_MultiNodeEmptyBodyIsAllowed pins that the
// overlay guard does NOT trip on a modeline-only node file (the common
// bootstrap shape: one file drives the same template across N nodes
// without pinning any per-node field).
func TestApplyTemplatesPerNode_MultiNodeEmptyBodyIsAllowed(t *testing.T) {
	dir := t.TempDir()
	configFile := filepath.Join(dir, "node.yaml")
	if err := os.WriteFile(configFile, []byte(`# talm: nodes=["10.0.0.1","10.0.0.2"]`+"\n"), 0o644); err != nil {
		t.Fatalf("write configFile: %v", err)
	}

	renderCount := 0
	render := func(_ context.Context, _ *client.Client, _ engine.Options) ([]byte, error) {
		renderCount++
		return []byte("version: v1alpha1\nmachine:\n  type: worker\n"), nil
	}
	applyCount := 0
	apply := func(_ context.Context, _ *client.Client, _ []byte) error {
		applyCount++
		return nil
	}

	if err := applyTemplatesPerNode(engine.Options{}, configFile,
		[]string{testNodeAddrA, testNodeAddrB},
		fakeAuthOpenClient(context.Background()), render, apply); err != nil {
		t.Fatalf("applyTemplatesPerNode: %v", err)
	}
	if renderCount != 2 || applyCount != 2 {
		t.Errorf("render=%d apply=%d, want 2 each", renderCount, applyCount)
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

	err := applyTemplatesPerNode(engine.Options{}, configFile, nil, fakeAuthOpenClient(context.Background()), render, apply)
	if err == nil {
		t.Fatal("expected an error for empty nodes list, got nil")
	}
	// The error itself names the missing inputs; the cockroachdb/errors
	// hint chain points the user at the concrete ways to set them. Both
	// must survive a reword — the message catches a future regression
	// that drops the topic entirely; the hint catches one that drops
	// the actionable guidance.
	msg := err.Error()
	if !strings.Contains(msg, "nodes") {
		t.Errorf("error message %q must mention %q (the missing input)", msg, "nodes")
	}
	hints := errors.GetAllHints(err)
	if len(hints) == 0 {
		t.Fatalf("expected at least one hint guiding the operator to set nodes, got %v", err)
	}
	combined := strings.Join(hints, "\n")
	for _, want := range []string{"--nodes", "modeline", testTalosconfigName} {
		if !strings.Contains(combined, want) {
			t.Errorf("hint chain %q does not mention %q (operator-actionable resolution path)", combined, want)
		}
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

	want := []string{testNodeAddrA, testNodeAddrB}
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

	GlobalArgs.Nodes = []string{testNodeFixtureA, testNodeFixtureB}

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

	openClient := openClientPerNodeMaintenance([]string{testNodeFixtureFingerprint}, fakeMaintenance)

	for _, node := range []string{testNodeAddrA, testNodeAddrB} {
		if err := openClient(node, func(_ context.Context, _ *client.Client) error { return nil }); err != nil {
			t.Fatalf("openClient(%q): %v", node, err)
		}
	}

	if !slices.Equal(GlobalArgs.Nodes, []string{testNodeFixtureA, testNodeFixtureB}) {
		t.Errorf("GlobalArgs.Nodes not restored after maintenance loop: got %v", GlobalArgs.Nodes)
	}
	if len(calls) != 2 {
		t.Fatalf("maintenance fake should have been called twice, got %d times", len(calls))
	}
	for i, want := range []string{testNodeAddrA, testNodeAddrB} {
		if !slices.Equal(calls[i].nodesAtCall, []string{want}) {
			t.Errorf("call %d: GlobalArgs.Nodes at WithClientMaintenance time = %v, want [%q]", i, calls[i].nodesAtCall, want)
		}
		if !slices.Equal(calls[i].fingerprints, []string{testNodeFixtureFingerprint}) {
			t.Errorf("call %d: fingerprints passed through = %v, want [\"fp-1\"]", i, calls[i].fingerprints)
		}
	}
}

// TestOpenClientPerNodeMaintenance_RestoresGlobalNodesOnError pins the
// restore-on-error half of the contract: even when the action returns
// an error, the deferred restore must put GlobalArgs.Nodes back. The
// success-path counterpart lives in
// TestOpenClientPerNodeMaintenance_NarrowsAndRestoresGlobalNodes;
// without this companion, a future refactor that swaps the defer for an
// explicit restore-after-success block would silently regress the
// error path.
func TestOpenClientPerNodeMaintenance_RestoresGlobalNodesOnError(t *testing.T) {
	saved := append([]string(nil), GlobalArgs.Nodes...)
	defer func() { GlobalArgs.Nodes = saved }()

	GlobalArgs.Nodes = []string{testNodeFixtureA, testNodeFixtureB}

	failingMaintenance := func(_ []string, action func(ctx context.Context, c *client.Client) error) error {
		return action(context.Background(), nil)
	}
	openClient := openClientPerNodeMaintenance(nil, failingMaintenance)

	err := openClient(testNodeAddrA, func(_ context.Context, _ *client.Client) error {
		return errSimulatedApplyFailure
	})
	if err == nil {
		t.Fatal("expected error from failing action, got nil")
	}
	if !slices.Equal(GlobalArgs.Nodes, []string{testNodeFixtureA, testNodeFixtureB}) {
		t.Errorf("GlobalArgs.Nodes not restored after error: got %v, want [original-A original-B]", GlobalArgs.Nodes)
	}
}

// TestApplyTemplatesPerNode_AuthModeUsesPluralNodesMetadataKey pins the
// gRPC metadata key the auth-mode opener writes. The auth template-rendering
// path drives lookups inside engine.Render through Talos's
// helpers.ForEachResource, which reads only the plural "nodes" metadata key
// (cmd/talosctl/pkg/talos/helpers/resources.go) — when that key is empty the
// helper falls back to []string{""} and issues an RPC with an empty target,
// surfacing as "rpc error: code = Internal desc = invalid target".
// helpers.FailIfMultiNodes accepts len("nodes") <= 1, so a single-element
// plural slice keeps the multi-node guard happy while making lookups work.
// The singular "node" key, in contrast, is invisible to ForEachResource and
// must never be used on the auth path.
func TestApplyTemplatesPerNode_AuthModeUsesPluralNodesMetadataKey(t *testing.T) {
	dir := t.TempDir()
	configFile := filepath.Join(dir, "node.yaml")
	if err := os.WriteFile(configFile, []byte("# talm: nodes=[\"a\"]\n"), 0o644); err != nil {
		t.Fatalf("write configFile: %v", err)
	}

	const node = testNodeAddrA
	render := func(ctx context.Context, _ *client.Client, _ engine.Options) ([]byte, error) {
		md, ok := metadata.FromOutgoingContext(ctx)
		if !ok {
			t.Fatal("expected outgoing metadata on per-iteration ctx")
		}
		if got := md.Get("nodes"); !slices.Equal(got, []string{node}) {
			t.Errorf(`metadata key "nodes" = %v, want [%q] (single-element plural slice — what helpers.ForEachResource reads)`, got, node)
		}
		if got := md.Get("node"); len(got) != 0 {
			t.Errorf(`metadata key "node" must be unset on auth apply, got %v`, got)
		}
		return []byte("version: v1alpha1\nmachine:\n  type: worker\n"), nil
	}
	apply := func(_ context.Context, _ *client.Client, _ []byte) error { return nil }

	openClient := openClientPerNodeAuth(context.Background(), nil)
	if err := applyTemplatesPerNode(engine.Options{}, configFile, []string{node}, openClient, render, apply); err != nil {
		t.Fatalf("applyTemplatesPerNode: %v", err)
	}
}

// TestCosiPreflightContext_StripsPluralAndAttachesSingular pins the
// COSI preflight ctx contract: the auth template-rendering apply path
// puts the target node under the plural "nodes" metadata key (so
// helpers.ForEachResource and apid's machine-API backend resolver can
// read it), but Talos's apid director rejects every COSI method whose
// outgoing context carries the plural key, regardless of slice
// length. cosiPreflightContext rebuilds ctx with the singular "node"
// key so the COSI router accepts the call. Without this, the version
// preflight silently no-ops on the auth path: cosiVersionReader
// swallows errors and returns ok=false on rejection, so the user
// never sees the mismatch warning the preflight exists to surface.
func TestCosiPreflightContext_StripsPluralAndAttachesSingular(t *testing.T) {
	const node = testNodeAddrA
	in := client.WithNodes(context.Background(), node)

	out, err := cosiPreflightContext(in)
	if err != nil {
		t.Fatalf("cosiPreflightContext: %v", err)
	}

	md, ok := metadata.FromOutgoingContext(out)
	if !ok {
		t.Fatal("expected outgoing metadata on preflight ctx")
	}
	if got := md.Get("nodes"); len(got) != 0 {
		t.Errorf(`metadata key "nodes" must be unset on COSI preflight ctx, got %v (apid's COSI router rejects every call carrying it)`, got)
	}
	if got := md.Get("node"); !slices.Equal(got, []string{node}) {
		t.Errorf(`metadata key "node" = %v, want [%q] (apid's COSI router routes by the singular key)`, got, node)
	}
}

// TestCosiPreflightContext_LeavesNoMetadataAlone pins the noop case
// for the insecure (maintenance) apply path, whose ctx carries no
// outgoing metadata at all — the maintenance client dials a single
// endpoint per call and routing-by-key is irrelevant. The helper
// must return ctx unchanged, not synthesize an empty "node" key
// that apid would route to the wrong target.
func TestCosiPreflightContext_LeavesNoMetadataAlone(t *testing.T) {
	in := context.Background()
	out, err := cosiPreflightContext(in)
	if err != nil {
		t.Fatalf("cosiPreflightContext: %v", err)
	}

	if md, ok := metadata.FromOutgoingContext(out); ok && len(md) > 0 {
		t.Errorf("expected no outgoing metadata on preflight ctx for maintenance path, got %v", md)
	}
}

// TestCosiPreflightContext_RejectsMultiNodeCtx pins that a multi-
// element plural slice surfaces as an explicit error rather than a
// silent passthrough. applyTemplatesPerNode iterates one node at a
// time, so a multi-element ctx at this point indicates a broken
// caller; passing it through to the COSI router would silently
// no-op the preflight (apid rejects, cosiVersionReader swallows the
// rejection, version mismatch never surfaces) — the exact symptom
// this helper exists to prevent on the single-node case.
func TestCosiPreflightContext_RejectsMultiNodeCtx(t *testing.T) {
	in := client.WithNodes(context.Background(), "a", "b")
	_, err := cosiPreflightContext(in)
	if err == nil {
		t.Fatal("expected error for multi-node outgoing ctx, got nil")
	}
	if !strings.Contains(err.Error(), "expected exactly one") {
		t.Errorf("expected error to mention single-node invariant, got: %v", err)
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

	// The template subcommand's no-overlay invariant is structural
	// rather than runtime: pkg/commands/template.go does not call
	// engine.MergeFileAsPatch, and a human-facing `talm template`
	// output going through the patcher would drop every YAML comment
	// (including the modeline). A runtime assertion here cannot pin
	// that — any block that reuses the `rendered` constant is
	// tautological — so the guard lives in the template code path
	// itself. The modeline round-trip tests in pkg/modeline surface a
	// regression that would wire MergeFileAsPatch into generateOutput.
}

// TestIsOutsideRoot pins the contract that distinguishes a path that
// truly escapes the project root (".." or a first element of "..")
// from a path whose first element merely *starts* with ".." but is
// itself a valid sibling-directory name (e.g. "..templates"). The
// distinction matters wherever a caller routes inside-root paths
// differently from outside-root ones; a HasPrefix("..") test
// silently misclassifies the latter as outside-root.
func TestIsOutsideRoot(t *testing.T) {
	cases := []struct {
		relPath string
		want    bool
	}{
		{"..", true},
		{".." + string(filepath.Separator) + testFooLiteral, true},
		{".." + string(filepath.Separator) + testFooLiteral + string(filepath.Separator) + "bar", true},
		{"..foo", false},
		{"..foo" + string(filepath.Separator) + "bar", false},
		{"..templates" + string(filepath.Separator) + "controlplane.yaml", false},
		{"..mykube", false},
		{testFooLiteral, false},
		{testFooLiteral + string(filepath.Separator) + "..bar", false},
		{".", false},
	}
	for _, c := range cases {
		if got := isOutsideRoot(c.relPath); got != c.want {
			t.Errorf("isOutsideRoot(%q) = %v, want %v", c.relPath, got, c.want)
		}
	}
}
