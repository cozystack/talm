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

// Contract: applyCommandName is the exact engine.CommandNameApply
// value. Drift between the two would cause apply error hints to
// silently start suggesting the non-existent `--offline` flag — the
// apply hint formatter in pkg/engine/lookup_classify.go branches on
// equality with engine.CommandNameApply to omit the `--offline`
// remedy clause.
//
// `applyCommandName` is declared as `= engine.CommandNameApply` in
// pkg/commands/apply.go so a Go compile error catches drift at the
// declaration site already. This test is the belt-and-suspenders
// safety net: a future refactor that re-introduces a literal would
// fail here too.

package commands

import (
	"testing"

	"github.com/cozystack/talm/pkg/engine"
)

func TestContract_ApplyCommandName_MatchesEngineConstant(t *testing.T) {
	if applyCommandName != engine.CommandNameApply {
		t.Errorf("applyCommandName=%q drifted from engine.CommandNameApply=%q — apply hints will silently suggest --offline",
			applyCommandName, engine.CommandNameApply)
	}
}
