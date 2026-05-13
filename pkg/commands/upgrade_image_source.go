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
	"os"
	"path/filepath"

	"github.com/cockroachdb/errors"
	"gopkg.in/yaml.v3"
)

// resolveUpgradeImageFromValues reads the cluster-default installer
// image from values.yaml. Per #176, values.yaml is the source of
// truth for cluster-wide knobs; the upgrade target reads from there
// directly instead of running engine.FullConfigProcess on the
// rendered node body (which returned whatever image was baked into
// the LAST `talm template` run, silently ignoring later values.yaml
// edits).
//
// Per-node image override is still available via --image; the
// resolver only fires when --image is unset.
func resolveUpgradeImageFromValues(rootDir string) (string, error) {
	valuesPath := filepath.Join(rootDir, "values.yaml")

	data, err := os.ReadFile(valuesPath)
	if err != nil {
		//nolint:wrapcheck // cockroachdb/errors.WithHint attaches operator-facing guidance.
		return "", errors.WithHintf(
			errors.Wrapf(err, "reading values.yaml from project root %s", rootDir),
			"talm upgrade resolves the target installer image from values.yaml; the project root is detected from the first -f file's directory walking up to the nearest Chart.yaml + secrets.yaml, or from --root explicitly. Ensure the -f file lives inside a `talm init`'d project, pass --root <dir>, or pass --image <ref> to skip resolution entirely.",
		)
	}

	var values struct {
		Image string `yaml:"image"`
	}

	if err := yaml.Unmarshal(data, &values); err != nil {
		return "", errors.Wrapf(err, "parsing values.yaml at %s", valuesPath)
	}

	if values.Image == "" {
		//nolint:wrapcheck // cockroachdb/errors.WithHint attaches operator-facing guidance.
		return "", errors.WithHint(
			errors.New("image not set in values.yaml"),
			"set `image: <installer-ref>` in values.yaml (the cluster-wide default) or pass --image <ref> to override per-invocation",
		)
	}

	return values.Image, nil
}
