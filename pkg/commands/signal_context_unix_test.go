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

//go:build !windows

// Raising a signal at the running process needs syscall.Kill, which Windows
// does not have. signalContext itself builds everywhere: syscall.SIGTERM is
// defined on Windows too, it just cannot be delivered this way.

package commands

import (
	"os"
	"syscall"
	"testing"
	"time"
)

// TestSignalContext_CancelsOnSignal pins the first half of the interrupt
// contract: a signal cancels the context the client call runs under. The second
// half (a second Ctrl+C killing the process) follows from unregistering the
// handler, which Go's default disposition then handles.
//
// The raise is process-scope, so this test must not run in parallel and the
// package must not gain a second test that installs its own SIGTERM handler.
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
