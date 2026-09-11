package engine

import (
	"context"
	"errors"
	"fmt"

	"github.com/cosi-project/runtime/pkg/resource"
	"github.com/cosi-project/runtime/pkg/resource/meta"
	"github.com/cosi-project/runtime/pkg/state"
	"google.golang.org/grpc/metadata"

	"github.com/siderolabs/talos/pkg/machinery/client"
)

// Talos v1.14.0 dropped FailIfMultiNodes and ForEachResource from
// cmd/talosctl/pkg/talos/helpers: talosctl inlined the resource loop into its
// own get command and left no exported replacement. Both are thin wrappers over
// the public client API, so talm carries its own, the same way it carries
// --skip-verify since the fork was dropped. Behaviour matches what the helpers
// did before they were dropped, which is what the callers in engine.go expect.
//
// These follow the structure of siderolabs/talos
// cmd/talosctl/pkg/talos/helpers/resources.go (the resource walk) and
// checks.go (the multi-node guard), both licensed under MPL-2.0:
// https://github.com/siderolabs/talos/tree/v1.13.7/cmd/talosctl/pkg/talos/helpers

// ErrMultiNodeUnsupported is returned for a command that only makes sense
// against a single node when the context names more than one.
var ErrMultiNodeUnsupported = errors.New("command is not supported with multiple nodes")

// ErrNoResourceType is returned when no resource type was given to walk.
var ErrNoResourceType = errors.New("not enough arguments: at least 1 is expected")

// failIfMultiNodes reports an error when the context carries more than one node
// in its outgoing metadata.
func failIfMultiNodes(ctx context.Context, command string) error {
	md, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		return nil
	}

	if len(md.Get("nodes")) <= 1 {
		return nil
	}

	return fmt.Errorf("%w: %q", ErrMultiNodeUnsupported, command)
}

// forEachResource resolves a resource kind and runs callback for every resource
// of that kind, on every node named in the context's outgoing metadata.
//
// The per-node loop is not unit-tested: resolving the kind goes through
// client.ResolveResourceKind, which is a concrete method on the Talos client
// and cannot be substituted. walkNodeResources below carries the per-node body
// and is tested directly. The loop cannot run more than one iteration from talm
// anyway: Render calls failIfMultiNodes before installing the lookup function,
// so a render is always single-node.
//
// The kind is resolved once, against the first node: a resource definition is
// cluster-wide, so which node answers does not matter. A per-node lookup failure
// is handed to the callback rather than returned, which is what lets a caller
// report an unreachable node and carry on with the rest; an error the callback
// itself returns stops the walk.
func forEachResource(
	ctx context.Context,
	c *client.Client,
	callbackRD func(rd *meta.ResourceDefinition) error,
	callback func(ctx context.Context, hostname string, r resource.Resource, callError error) error,
	namespace string,
	args ...string,
) error {
	if len(args) == 0 {
		return ErrNoResourceType
	}

	resourceType := args[0]

	var resourceID string

	if len(args) > 1 {
		resourceID = args[1]
	}

	md, _ := metadata.FromOutgoingContext(ctx)

	nodes := md.Get("nodes")
	if len(nodes) == 0 {
		nodes = []string{""}
	}

	resourceDefinition, err := c.ResolveResourceKind(client.WithNode(ctx, nodes[0]), &namespace, resourceType)
	if err != nil {
		return fmt.Errorf("resolving resource kind %q: %w", resourceType, err)
	}

	if callbackRD != nil {
		if cbErr := callbackRD(resourceDefinition); cbErr != nil {
			return cbErr
		}
	}

	resourceType = resourceDefinition.TypedSpec().Type

	for _, node := range nodes {
		if err := walkNodeResources(ctx, c, callback, namespace, resourceType, resourceID, node); err != nil {
			return err
		}
	}

	return nil
}

// walkNodeResources runs callback over one node's resources of a single kind:
// the one named by resourceID, or every resource of the kind when it is empty.
func walkNodeResources(
	ctx context.Context,
	c *client.Client,
	callback func(ctx context.Context, hostname string, r resource.Resource, callError error) error,
	namespace, resourceType, resourceID, node string,
) error {
	nodeCtx := ctx
	if node != "" {
		nodeCtx = client.WithNode(ctx, node)
	}

	if resourceID != "" {
		r, callErr := c.COSI.Get(
			nodeCtx,
			resource.NewMetadata(namespace, resourceType, resourceID, resource.VersionUndefined),
			state.WithGetUnmarshalOptions(state.WithSkipProtobufUnmarshal()),
		)

		return callback(ctx, node, r, callErr)
	}

	items, callErr := c.COSI.List(
		nodeCtx,
		resource.NewMetadata(namespace, resourceType, "", resource.VersionUndefined),
		state.WithListUnmarshalOptions(state.WithSkipProtobufUnmarshal()),
	)
	if callErr != nil {
		return callback(ctx, node, nil, callErr)
	}

	for _, r := range items.Items {
		if err := callback(ctx, node, r, nil); err != nil {
			return err
		}
	}

	return nil
}
