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
	"context"
	"strings"
	"testing"

	"github.com/cockroachdb/errors"
)

func TestEvaluateVersionMismatch(t *testing.T) {
	const (
		// versionCurrentSubstring is the rendering of machinery's
		// TalosVersionCurrent (nil contract); the warning embeds it
		// when no configured version is supplied.
		versionCurrentSubstring = "current"
		// unparseableVersion is a sentinel that
		// machineryconfig.ParseContractFromVersion rejects.
		unparseableVersion = "garbage"
	)

	tests := []struct {
		name        string
		configured  string
		running     string
		wantWarning bool
		wantInMsg   string
	}{
		{"configured newer than running", "v1.12", "v1.11.6", true, "v1.11.6"},
		{"configured equals running minor", "v1.12", "v1.12.6", false, ""},
		{"configured older than running", "v1.10", "v1.12.6", false, ""},
		{"configured equal exact", "v1.11", "v1.11.0", false, ""},
		// Empty configured = TalosVersionCurrent (nil contract = newest); the warning
		// must fire because that's the documented reproduction case for an unset
		// talosVersion against an older node.
		{"configured empty means current and warns", "", "v1.11.6", true, versionCurrentSubstring},
		{"configured empty equal-modern still warns", "", "v1.12.6", true, versionCurrentSubstring},
		{"running unparseable", "v1.12", unparseableVersion, false, ""},
		{"configured unparseable", unparseableVersion, "v1.12.6", false, ""},
		{"both empty silent", "", "", false, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := evaluateVersionMismatch(tc.configured, tc.running)
			if (got != nil) != tc.wantWarning {
				t.Fatalf("evaluateVersionMismatch(%q, %q) = %v, want warning=%v", tc.configured, tc.running, got, tc.wantWarning)
			}
			if !tc.wantWarning {
				return
			}
			hints := errors.GetAllHints(got)
			if len(hints) == 0 {
				t.Fatalf("warning has no hint attached")
			}
			if tc.wantInMsg != "" && !strings.Contains(got.Error(), tc.wantInMsg) {
				t.Errorf("warning message %q does not contain %q", got.Error(), tc.wantInMsg)
			}
		})
	}
}

func TestAnnotateApplyConfigError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantHint bool
	}{
		{"nil error", nil, false},
		{
			"unrelated error",
			errors.New("connection refused"),
			false,
		},
		{
			"strict decoder error",
			errors.New("rpc error: code = Unknown desc = failed to parse config: unknown keys found during decoding:\nmachine:\n    install:\n        grubUseUKICmdline: true"),
			true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := annotateApplyConfigError(tc.err)
			if tc.err == nil {
				if got != nil {
					t.Fatalf("annotateApplyConfigError(nil) = %v, want nil", got)
				}
				return
			}
			hints := errors.GetAllHints(got)
			hasHint := len(hints) > 0
			if hasHint != tc.wantHint {
				t.Fatalf("annotateApplyConfigError hint=%v, want %v (hints=%v)", hasHint, tc.wantHint, hints)
			}
		})
	}
}

// stubReader is a versionReader that returns a fixed value, matching the
// contract preflightCheckTalosVersion uses (string, ok). Tests use it to
// drive the function without standing up a live COSI server.
func stubReader(version string, ok bool) versionReader {
	return func(context.Context) (string, bool) { return version, ok }
}

func TestPreflightCheckTalosVersion(t *testing.T) {
	tests := []struct {
		name           string
		configured     string
		readerVersion  string
		readerOK       bool
		wantWarnPrefix string
		wantHint       bool
	}{
		{
			name:           "configured newer than running emits warning and hint",
			configured:     "v1.12",
			readerVersion:  "v1.11.6",
			readerOK:       true,
			wantWarnPrefix: "warning: pre-flight: configured talosVersion=v1.12 is newer",
			wantHint:       true,
		},
		{
			// Reproduction case from cozystack/talm#132: unset talosVersion + older node.
			name:           "empty configured treated as current and warns",
			configured:     "",
			readerVersion:  "v1.11.6",
			readerOK:       true,
			wantWarnPrefix: "warning: pre-flight: configured talosVersion=current is newer",
			wantHint:       true,
		},
		{
			name:           "versions match silently",
			configured:     "v1.12",
			readerVersion:  "v1.12.6",
			readerOK:       true,
			wantWarnPrefix: "",
			wantHint:       false,
		},
		{
			name:           "reader failure is silent",
			configured:     "v1.12",
			readerVersion:  "",
			readerOK:       false,
			wantWarnPrefix: "",
			wantHint:       false,
		},
		{
			name:           "running unparseable is silent",
			configured:     "v1.12",
			readerVersion:  "weird-build-2026-01",
			readerOK:       true,
			wantWarnPrefix: "",
			wantHint:       false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			buf := &bytes.Buffer{}
			preflightCheckTalosVersion(context.Background(), stubReader(tc.readerVersion, tc.readerOK), tc.configured, buf)

			got := buf.String()
			if tc.wantWarnPrefix == "" {
				if got != "" {
					t.Fatalf("expected silent output, got %q", got)
				}
				return
			}
			if !strings.Contains(got, tc.wantWarnPrefix) {
				t.Fatalf("output %q does not start with warning prefix %q", got, tc.wantWarnPrefix)
			}
			if tc.wantHint && !strings.Contains(got, "hint:") {
				t.Errorf("expected a hint line in output %q", got)
			}
		})
	}
}
