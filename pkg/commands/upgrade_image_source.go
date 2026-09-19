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
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/blang/semver/v4"
	"github.com/cockroachdb/errors"
	"gopkg.in/yaml.v3"
)

// resolveUpgradeImageFromValues reads the cluster-default installer
// image from values.yaml. values.yaml is the source of truth for
// cluster-wide knobs; the upgrade target reads from there directly
// instead of re-extracting from the rendered node body (which would
// return whatever image was baked into the last `talm template`
// run, silently ignoring later values.yaml edits).
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

// warnNodeBodyImageDivergence reports every -f body whose
// machine.install.image is not the resolved target.
//
// Two things happen silently to such a file: the upgrade ignores its image
// (the target is values.yaml::image) and the post-upgrade write-back then
// rewrites it. Both are worth stating, but only one direction is a problem.
//
// A body naming an OLDER Talos is the canonical shape: the command's help
// calls bumping values.yaml the way to raise the cluster's version and says
// re-templating first is not required, so every body trails the target until
// the write-back resyncs it. Reported as a plain fact, with no advice.
//
// A body naming a NEWER Talos, or an image whose version cannot be compared,
// is the incident: the upgrade goes somewhere the file does not name, and the
// file is then overwritten to match. That one carries the warning label and
// the way out.
//
// Only reached when the target was resolved from values.yaml. With an explicit
// --image the body is still ignored and still rewritten, but the operator named
// the target themselves, so the half of the surprise worth reporting is gone.
//
// Best-effort by design: a file that does not parse, or carries no install
// image at all, is skipped rather than reported. Side-patches and orphans in
// the -f list legitimately have neither, and a parse error surfaces from the
// write-back with a better message than a pre-flight warning could give.
func warnNodeBodyImageDivergence(w io.Writer, files []string, target string) {
	if target == "" {
		return
	}

	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		docs, err := decodeAllYAMLDocs(data)
		if err != nil {
			continue
		}

		node, ok, err := locateInstallImageNode(docs)
		if err != nil || !ok {
			continue
		}

		// An empty value declares nothing to diverge from, and reporting it
		// would print a message with a hole where the image should be.
		if node.Value == "" || node.Value == target {
			continue
		}

		if bodyTrailsTarget(node.Value, target) {
			fmt.Fprintf(w,
				"%s declares machine.install.image %s; the upgrade installs %s and rewrites the file to match "+
					"after it succeeds.\n",
				path, node.Value, target)

			continue
		}

		fmt.Fprintf(w,
			"warning: %s declares machine.install.image %s, but the upgrade target is %s. "+
				"The file's image is not used; it will be rewritten to the target after a successful upgrade. "+
				"If the file is the one you meant, put that image in values.yaml or pass --image.\n",
			path, node.Value, target)
	}
}

// bodyTrailsTarget reports whether the body's image names an older Talos than
// the target, which is what a values.yaml bump leaves behind.
//
// Ordered down to the patch. Comparing at major.minor would call a body on
// v1.12.9 against a v1.12.6 target "trailing", but that is the incident this
// report exists for, one granularity down: the node moves backwards and the
// pre-upgrade guard lets it through, because Talos allows patch moves inside a
// minor. Anything that cannot be compared — a different repository, a digest
// pin, an unparseable tag — is deliberately not treated as trailing, because
// then the upgrade really is going somewhere the file does not name.
func bodyTrailsTarget(bodyImage, target string) bool {
	bodyVersion, targetVersion := parseTargetVersion(bodyImage), parseTargetVersion(target)
	if bodyVersion == "" || targetVersion == "" {
		return false
	}

	if repositoryOf(bodyImage) != repositoryOf(target) {
		return false
	}

	body, err := semver.ParseTolerant(bodyVersion)
	if err != nil {
		return false
	}

	want, err := semver.ParseTolerant(targetVersion)
	if err != nil {
		return false
	}

	return body.LT(want)
}

// repositoryOf strips the tag from an image reference, leaving the part that
// says where the image comes from. A body pointing at another repository is a
// divergence even when the versions line up.
func repositoryOf(image string) string {
	tag := parseTargetVersion(image)
	if tag == "" {
		return image
	}

	return strings.TrimSuffix(image, ":"+tag)
}
