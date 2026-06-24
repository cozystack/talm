package engine

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/cosi-project/runtime/pkg/resource"
	"github.com/cosi-project/runtime/pkg/state"
	"github.com/cosi-project/runtime/pkg/state/impl/inmem"
	"github.com/cosi-project/runtime/pkg/state/impl/namespaced"

	"github.com/siderolabs/talos/pkg/machinery/client"
	"github.com/siderolabs/talos/pkg/machinery/resources/network"
)

// withNodesMetadata attaches the plural "nodes" key the way the talm commands do,
// which is the key forEachResource and failIfMultiNodes read back. Upstream
// deprecated client.WithNodes in favor of the singular WithNode; until those two
// helpers move off the metadata, tests have to write what they read.
//
//nolint:staticcheck // SA1019: see above — the plural key is what the helpers under test consume.
func withNodesMetadata(ctx context.Context, nodes ...string) context.Context {
	return client.WithNodes(ctx, nodes...)
}

// collectWalk runs the walker over one node and returns what the callback saw:
// "node/resource-id" per resource, plus the per-node errors handed to it rather
// than returned.
func collectWalk(t *testing.T, st state.State, namespace, resourceType, resourceID, node string) ([]string, []error) {
	t.Helper()

	c := &client.Client{COSI: st}

	var (
		seen     []string
		callErrs []error
	)

	err := walkNodeResources(context.Background(), c,
		func(_ context.Context, hostname string, r resource.Resource, callErr error) error {
			if callErr != nil {
				callErrs = append(callErrs, callErr)

				return nil
			}

			seen = append(seen, hostname+"/"+r.Metadata().ID())

			return nil
		},
		namespace, resourceType, resourceID, node)
	if err != nil {
		t.Fatalf("walkNodeResources: %v", err)
	}

	return seen, callErrs
}

// An empty resource ID means "every resource of this kind", and the node name
// is reported back for each one. Template lookups rely on both: the chart asks
// for a whole kind and attributes results per node.
func TestWalkNodeResources_ListsEveryResourceWithNode(t *testing.T) {
	t.Parallel()

	st := state.WrapCore(namespaced.NewState(inmem.Build))

	for _, id := range []string{"eth0", "eth1"} {
		if err := st.Create(context.Background(), network.NewHostnameSpec(network.NamespaceName, id)); err != nil {
			t.Fatalf("seed %q: %v", id, err)
		}
	}

	seen, callErrs := collectWalk(t, st, network.NamespaceName, network.HostnameSpecType, "", "node0")

	if len(callErrs) != 0 {
		t.Fatalf("unexpected callback errors: %v", callErrs)
	}

	want := []string{"node0/eth0", "node0/eth1"}
	if !slices.Equal(seen, want) {
		t.Errorf("walk produced %v, want %v", seen, want)
	}
}

// A missing resource reaches the callback as callError rather than aborting the
// walk. That is what lets a caller ignore NotFound for one node and carry on
// with the rest, which is how template lookups treat absent resources.
func TestWalkNodeResources_RoutesMissingResourceToCallback(t *testing.T) {
	t.Parallel()

	st := state.WrapCore(namespaced.NewState(inmem.Build))

	seen, callErrs := collectWalk(t, st, network.NamespaceName, network.HostnameSpecType, "absent", "node0")

	if len(seen) != 0 {
		t.Errorf("walk reported resources that do not exist: %v", seen)
	}

	if len(callErrs) != 1 {
		t.Fatalf("expected exactly one callback error, got %v", callErrs)
	}
}

// failIfMultiNodes guards the commands that only make sense against one node.
// One node is fine, several are not, and a context with no node metadata at all
// (an offline render) must not trip the guard.
func TestFailIfMultiNodes(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		nodes   []string
		wantErr bool
	}{
		{"no metadata", nil, false},
		{"one node", []string{"node0"}, false},
		{"two nodes", []string{"node0", "node1"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			if tc.nodes != nil {
				ctx = withNodesMetadata(ctx, tc.nodes...)
			}

			err := failIfMultiNodes(ctx, "talm template")
			if gotErr := err != nil; gotErr != tc.wantErr {
				t.Fatalf("failIfMultiNodes(%v) error = %v, want error: %v", tc.nodes, err, tc.wantErr)
			}

			if tc.wantErr && !errors.Is(err, ErrMultiNodeUnsupported) {
				t.Errorf("error = %v, want ErrMultiNodeUnsupported", err)
			}
		})
	}
}

// forEachResource needs a resource kind to walk. The guard fires before the
// client is touched, which is what makes it testable without one.
func TestForEachResource_RequiresResourceType(t *testing.T) {
	t.Parallel()

	err := forEachResource(context.Background(), nil, nil, nil, "ns")
	if !errors.Is(err, ErrNoResourceType) {
		t.Fatalf("forEachResource with no kind = %v, want ErrNoResourceType", err)
	}
}
