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
	"strings"
	"testing"

	"github.com/cockroachdb/errors"
)

func TestEvaluateVersionMismatch(t *testing.T) {
	tests := []struct {
		name        string
		configured  string
		running     string
		wantWarning bool
	}{
		{"configured newer than running", "v1.12", "v1.11.6", true},
		{"configured equals running minor", "v1.12", "v1.12.6", false},
		{"configured older than running", "v1.10", "v1.12.6", false},
		{"configured equal exact", "v1.11", "v1.11.0", false},
		{"configured empty", "", "v1.12.6", false},
		{"running unparseable", "v1.12", "garbage", false},
		{"configured unparseable", "garbage", "v1.12.6", false},
		{"both empty", "", "", false},
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
			if !strings.Contains(got.Error(), tc.running) {
				t.Errorf("warning message %q does not mention running version %q", got.Error(), tc.running)
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
			errors.New("rpc error: code = Unknown desc = failed to parse config: unknown keys found during decoding:\nmachine:\n    install:\n        grubUseUKICmdline: true\n"),
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
