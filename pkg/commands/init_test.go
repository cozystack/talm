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
	"os"
	"path/filepath"
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
	path := filepath.Join(dir, "ok.txt")

	if err := writeToDestination([]byte("x"), path, 0o644); err != nil {
		t.Fatalf("writeToDestination: %v", err)
	}
	if !strings.Contains(buf.String(), "Created "+path) {
		t.Errorf("expected 'Created %s' in output, got %q", path, buf.String())
	}
}

// compile-time assert createdSink is writer-shaped.
var _ io.Writer = (*bytes.Buffer)(nil)

// TestWriteSecretsBundleToFile_StillRefusesOverwrite pins that
// writeSecretsBundleToFile still honors the --force gate after the
// redundant validateFileExists call was dropped — the gate now lives
// only inside writeSecureToDestination, and this test would fail if
// that inner check were ever removed too.
func TestWriteSecretsBundleToFile_StillRefusesOverwrite(t *testing.T) {
	forceOrig := initCmdFlags.force
	rootOrig := Config.RootDir
	t.Cleanup(func() {
		initCmdFlags.force = forceOrig
		Config.RootDir = rootOrig
	})

	dir := t.TempDir()
	Config.RootDir = dir
	initCmdFlags.force = false

	// Seed a pre-existing secrets.yaml.
	existing := filepath.Join(dir, "secrets.yaml")
	if err := os.WriteFile(existing, []byte("preserve-me"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	err := writeSecretsBundleToFile(nil)
	if err == nil {
		t.Fatal("expected error refusing to overwrite existing secrets.yaml")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error should mention existing-file gate, got: %v", err)
	}

	// Original content must be intact.
	got, readErr := os.ReadFile(existing)
	if readErr != nil {
		t.Fatalf("ReadFile: %v", readErr)
	}
	if string(got) != "preserve-me" {
		t.Errorf("original content changed: %q", got)
	}
}
