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

package commands

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
	"time"

	"google.golang.org/grpc/metadata"

	"github.com/siderolabs/talos/pkg/machinery/client"
	clientconfig "github.com/siderolabs/talos/pkg/machinery/client/config"
)

func withNodesReset(t *testing.T) {
	t.Helper()

	orig := GlobalArgs.Nodes
	t.Cleanup(func() { GlobalArgs.Nodes = orig })
}

// TestWithClientMaintenance_SpansEveryNode pins that the maintenance client
// carries every --nodes entry as an endpoint. client.WithMaintenanceMode narrows
// the client to the single node it is handed, so a regression here would silently
// reduce a multi-node insecure apply to one node.
func TestWithClientMaintenance_SpansEveryNode(t *testing.T) {
	withNodesReset(t)

	GlobalArgs.Nodes = []string{"192.0.2.1", "192.0.2.2"}

	var got []string

	err := WithClientMaintenance(nil, func(_ context.Context, c *client.Client) error {
		got = c.GetEndpoints()

		return nil
	})
	if err != nil {
		t.Fatalf("WithClientMaintenance: %v", err)
	}

	if !slices.Equal(got, GlobalArgs.Nodes) {
		t.Errorf("maintenance client endpoints = %v, want %v", got, GlobalArgs.Nodes)
	}
}

// TestWithClientMaintenance_RejectsMalformedFingerprint pins that a bad
// --cert-fingerprint fails the connection instead of silently dropping the
// pinning, which would leave the insecure connection unauthenticated.
func TestWithClientMaintenance_RejectsMalformedFingerprint(t *testing.T) {
	withNodesReset(t)

	GlobalArgs.Nodes = []string{"192.0.2.1"}

	err := WithClientMaintenance([]string{"not-a-fingerprint"}, func(context.Context, *client.Client) error {
		t.Error("action ran despite a malformed fingerprint")

		return nil
	})
	if err == nil {
		t.Fatal("expected an error for a malformed certificate fingerprint")
	}
}

// TestWithClientNoNodes_UsesContextEndpointsWithoutNodes pins the two properties
// of the locally built client: endpoints come from the named talosconfig
// context, and no node metadata is attached — callers that want nodes add them
// in their own layer. Talos v1.14 replaced the talosctl wrapper with a factory
// that refuses to build without nodes, so this construction is talm's own.
func TestWithClientNoNodes_UsesContextEndpointsWithoutNodes(t *testing.T) {
	withNodesReset(t)

	cfg := &clientconfig.Config{
		Context: "present",
		Contexts: map[string]*clientconfig.Context{
			"present": {Endpoints: []string{"192.0.2.1"}, Nodes: []string{"192.0.2.9"}},
		},
	}

	cfgPath := filepath.Join(t.TempDir(), "talosconfig")
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatalf("save talosconfig fixture: %v", err)
	}

	origTalosconfig, origCmdContext, origEndpoints, origSkipVerify := GlobalArgs.Talosconfig, GlobalArgs.CmdContext, GlobalArgs.Endpoints, SkipVerify

	t.Cleanup(func() {
		GlobalArgs.Talosconfig, GlobalArgs.CmdContext, GlobalArgs.Endpoints, SkipVerify = origTalosconfig, origCmdContext, origEndpoints, origSkipVerify
	})

	GlobalArgs.Talosconfig = cfgPath
	GlobalArgs.CmdContext = "present"
	GlobalArgs.Endpoints = nil
	GlobalArgs.Nodes = nil
	SkipVerify = false

	var (
		endpoints []string
		hasNodes  bool
	)

	err := WithClientNoNodes(func(ctx context.Context, c *client.Client) error {
		endpoints = c.GetEndpoints()

		md, ok := metadata.FromOutgoingContext(ctx)
		hasNodes = ok && len(md.Get("nodes")) > 0

		return nil
	})
	if err != nil {
		t.Fatalf("WithClientNoNodes: %v", err)
	}

	if want := []string{"192.0.2.1"}; !slices.Equal(endpoints, want) {
		t.Errorf("endpoints = %v, want %v from the talosconfig context", endpoints, want)
	}

	if hasNodes {
		t.Error("WithClientNoNodes attached node metadata; callers add nodes themselves")
	}
}

// TestSignalContext_CancelsOnSignal pins the first half of the interrupt
// contract: a signal cancels the context the client call runs under. The second
// half (a second Ctrl+C killing the process) follows from unregistering the
// handler, which Go's default disposition then handles.
func TestSignalContext_CancelsOnSignal(t *testing.T) {
	ctx, stop := signalContext()
	defer stop()

	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("raise SIGTERM: %v", err)
	}

	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("context was not cancelled by SIGTERM")
	}
}
