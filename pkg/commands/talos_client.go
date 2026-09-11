// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

// The code in this file is derived from siderolabs/talos, which is licensed
// under MPL-2.0, and is therefore kept under MPL-2.0 itself. The rest of talm
// stays under Apache-2.0; MPL-2.0 §3.3 covers that combination.
//
// signalContext follows pkg/cli/context.go:
// https://github.com/siderolabs/talos/blob/v1.14.0/pkg/cli/context.go
//
// newClientNoNodes and WithClientMaintenance follow
// cmd/talosctl/pkg/talos/global/client.go, whose exported entry points v1.14
// removed:
// https://github.com/siderolabs/talos/blob/v1.13.7/cmd/talosctl/pkg/talos/global/client.go

package commands

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/cockroachdb/errors"
	"github.com/siderolabs/talos/pkg/machinery/client"
	clientconfig "github.com/siderolabs/talos/pkg/machinery/client/config"
	"google.golang.org/grpc"
)

// signalContext returns a context cancelled on SIGINT/SIGTERM, mirroring the
// wrappers talosctl builds its own clients with.
//
// It unregisters the handler on the first signal, so a second Ctrl+C kills the
// process outright. signal.NotifyContext would keep the registration, and the
// second signal would land in a full channel and be discarded, leaving a stuck
// call with no way out from the keyboard.
func signalContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	go func() {
		select {
		case <-sigCh:
			signal.Stop(sigCh)
			fmt.Fprintln(os.Stderr, "Signal received, aborting, press Ctrl+C once again to abort immediately...")
			cancel()
		case <-ctx.Done():
		}
	}()

	return ctx, func() {
		signal.Stop(sigCh)
		cancel()
	}
}

// newClientNoNodes constructs a client that carries no node metadata and runs
// action against it.
func newClientNoNodes(action func(context.Context, *client.Client) error, dialOptions ...grpc.DialOption) error {
	ctx, stop := signalContext()
	defer stop()

	// Built on pkg/machinery/client rather than the talosctl wrapper: Talos
	// v1.14 replaced that wrapper with a ClientFactory which refuses to
	// construct without nodes, which is the one thing this function allows.
	cfg, err := clientconfig.Open(GlobalArgs.Talosconfig)
	if err != nil {
		return errors.Wrapf(err, "opening talosconfig %q", GlobalArgs.Talosconfig)
	}

	opts := []client.OptionFunc{
		client.WithConfig(cfg),
		client.WithDefaultGRPCDialOptions(),
		client.WithGRPCDialOptions(dialOptions...),
		client.WithSideroV1KeysDir(clientconfig.CustomSideroV1KeysDirPath(GlobalArgs.SideroV1KeysDir)),
	}

	if GlobalArgs.CmdContext != "" {
		opts = append(opts, client.WithContextName(GlobalArgs.CmdContext))
	}

	if len(GlobalArgs.Endpoints) > 0 {
		opts = append(opts, client.WithEndpoints(GlobalArgs.Endpoints...))
	}

	if GlobalArgs.Cluster != "" {
		opts = append(opts, client.WithCluster(GlobalArgs.Cluster))
	}

	c, err := client.New(ctx, opts...)
	if err != nil {
		return errors.Wrap(err, "constructing Talos client")
	}

	defer func() { _ = c.Close() }()

	return action(ctx, c)
}

// WithClientMaintenance wraps common code to initialize Talos client in maintenance (insecure mode).
//
// One client spans every node in GlobalArgs.Nodes, as the talosctl wrapper did
// before v1.14 hid it behind a per-node ClientFactory; callers that need a
// single-endpoint client narrow the list themselves (openClientPerNodeMaintenance).
// GlobalArgs.Nodes is read synchronously because that narrowing restores the
// saved list as soon as action returns.
func WithClientMaintenance(enforceFingerprints []string, action func(context.Context, *client.Client) error) error {
	ctx, stop := signalContext()
	defer stop()

	nodes := GlobalArgs.Nodes

	c, err := client.New(ctx,
		client.WithDefaultGRPCDialOptions(),
		// Taken for the insecure TLS config and the fingerprint pinning. Its node
		// argument is inert here: options are applied in order, and WithEndpoints
		// below overwrites the single endpoint it sets.
		client.WithMaintenanceMode("", enforceFingerprints),
		client.WithEndpoints(nodes...),
	)
	if err != nil {
		return errors.Wrap(err, "constructing maintenance client")
	}

	defer func() { _ = c.Close() }()

	return action(ctx, c)
}
