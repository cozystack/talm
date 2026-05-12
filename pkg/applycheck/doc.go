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

// Package applycheck implements the apply-time safety gates: extracting
// host-resource references from a rendered Talos MachineConfig, evaluating
// disk selectors against a host snapshot, and diffing two MachineConfig
// snapshots by (kind, name) identity. The walker is YAML-only; no Talos
// machinery types leak through its surface, so the package can be exercised
// from contract tests without standing up a Talos client.
package applycheck
