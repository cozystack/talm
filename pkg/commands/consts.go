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

// Shared string constants used across the commands package. Centralized
// here so the goconst linter has a single canonical reference and so a
// rename touches one location.
const (
	// defaultKubeconfigName is the default basename used when
	// Config.GlobalOptions.Kubeconfig is unset; it doubles as the
	// .gitignore entry and the value compared against
	// filepath.Base(kubeconfigPath) to gate kubeconfig-specific
	// post-processing.
	defaultKubeconfigName = "kubeconfig"

	// chartYamlName is the on-disk name of the Helm chart manifest at
	// the project root.
	chartYamlName = "Chart.yaml"

	// defaultLocalEndpoint is the loopback endpoint baked into freshly
	// generated talosconfig contexts when no real endpoint is known yet.
	defaultLocalEndpoint = "127.0.0.1"

	// initSubcommand is the canonical name of the init subcommand,
	// used when the dispatcher needs to special-case it.
	initSubcommand = "init"
)
