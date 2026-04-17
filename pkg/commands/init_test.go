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
	"bytes"
	"io"
	"strings"
	"testing"
)

// writeToDestinationSilentOnFailureCases asserts neither writeTo-
// Destination nor writeSecureToDestination emits a misleading
// "Created <path>" line when the underlying write fails. The failure
// is induced by pointing destination at an existing directory, which
// makes os.WriteFile (and therefore secureperm.WriteFile) fail.
func TestWriteToDestination_SilentOnFailure(t *testing.T) {
	forceOrig := initCmdFlags.force
	sinkOrig := createdSink
	t.Cleanup(func() {
		initCmdFlags.force = forceOrig
		createdSink = sinkOrig
	})

	initCmdFlags.force = true

	cases := []struct {
		name string
		call func(data []byte, dest string) error
	}{
		{
			name: "writeToDestination",
			call: func(data []byte, dest string) error {
				return writeToDestination(data, dest, 0o644)
			},
		},
		{
			name: "writeSecureToDestination",
			call: func(data []byte, dest string) error {
				return writeSecureToDestination(data, dest)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			createdSink = &buf

			// Destination is an existing directory — the underlying write
			// call fails without touching the filesystem content.
			dir := t.TempDir()

			err := tc.call([]byte("data"), dir)
			if err == nil {
				t.Fatal("expected error writing to directory, got nil")
			}
			if strings.Contains(buf.String(), "Created") {
				t.Errorf("output contains 'Created' despite failure: %q", buf.String())
			}
		})
	}
}

// TestWriteToDestination_AnnouncesOnSuccess pins that the happy path
// still prints "Created <path>" — i.e. the new err==nil guard didn't
// accidentally silence the normal success message.
func TestWriteToDestination_AnnouncesOnSuccess(t *testing.T) {
	forceOrig := initCmdFlags.force
	sinkOrig := createdSink
	t.Cleanup(func() {
		initCmdFlags.force = forceOrig
		createdSink = sinkOrig
	})

	initCmdFlags.force = true

	var buf bytes.Buffer
	createdSink = &buf

	dir := t.TempDir()
	path := dir + "/ok.txt"

	if err := writeToDestination([]byte("x"), path, 0o644); err != nil {
		t.Fatalf("writeToDestination: %v", err)
	}
	if !strings.Contains(buf.String(), "Created "+path) {
		t.Errorf("expected 'Created %s' in output, got %q", path, buf.String())
	}
}

// compile-time assert createdSink is writer-shaped.
var _ io.Writer = (*bytes.Buffer)(nil)
