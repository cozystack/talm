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

import "testing"

func TestCheckChartVersion(t *testing.T) {
	tests := []struct {
		name         string
		binary       string
		chart        string
		wantMismatch bool
	}{
		{
			name:         "equal release versions stay silent",
			binary:       "v0.30.0",
			chart:        "0.30.0",
			wantMismatch: false,
		},
		{
			name:         "binary newer than vendored chart warns",
			binary:       "v0.30.0",
			chart:        "0.27.0",
			wantMismatch: true,
		},
		{
			name:         "binary older than vendored chart warns",
			binary:       "v0.27.0",
			chart:        "0.30.0",
			wantMismatch: true,
		},
		{
			name:         "v prefix on chart version is normalized",
			binary:       "v0.30.0",
			chart:        "v0.30.0",
			wantMismatch: false,
		},
		{
			name:         "dev binary build is skipped",
			binary:       "dev",
			chart:        "0.27.0",
			wantMismatch: false,
		},
		{
			name:         "chart stamped by dev build (0.1.0 sentinel) is skipped",
			binary:       "v0.30.0",
			chart:        "0.1.0",
			wantMismatch: false,
		},
		{
			name:         "empty chart version is skipped",
			binary:       "v0.30.0",
			chart:        "",
			wantMismatch: false,
		},
		{
			name:         "unparseable chart version is skipped",
			binary:       "v0.30.0",
			chart:        "not-a-version",
			wantMismatch: false,
		},
		{
			name:         "unparseable binary version is skipped",
			binary:       "garbage",
			chart:        "0.27.0",
			wantMismatch: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotMismatch, gotMsg := CheckChartVersion(tt.binary, tt.chart)
			if gotMismatch != tt.wantMismatch {
				t.Errorf("CheckChartVersion(%q, %q) mismatch = %v, want %v",
					tt.binary, tt.chart, gotMismatch, tt.wantMismatch)
			}

			if gotMismatch && gotMsg == "" {
				t.Errorf("CheckChartVersion(%q, %q) reported a mismatch but returned an empty message",
					tt.binary, tt.chart)
			}

			if !gotMismatch && gotMsg != "" {
				t.Errorf("CheckChartVersion(%q, %q) returned no mismatch but a non-empty message %q",
					tt.binary, tt.chart, gotMsg)
			}
		})
	}
}
