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
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// After a values.yaml bump every node body still names the previous image:
// the command's own help calls that the canonical way to raise the cluster's
// Talos version and says re-templating first is not required. So a body older
// than the target is the normal shape, and telling that operator to "bump
// values.yaml" is advice to repeat what they just did.
//
// The incident worth prescribing for is the other direction: values.yaml left
// on an old preset default while the node file was edited to something
// current. There the upgrade goes somewhere older than the file says, and the
// file is then rewritten to match it.
func TestContract_UpgradeDivergenceMessageMatchesTheDirection(t *testing.T) {
	const target = "ghcr.io/cozystack/cozystack/talos:v1.12.6"

	write := func(t *testing.T, image string) string {
		t.Helper()

		path := filepath.Join(t.TempDir(), "node0.yaml")
		body := "machine:\n  type: controlplane\n  install:\n    disk: /dev/sda\n    image: " + image + "\n"

		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}

		return path
	}

	t.Run("body behind the target is reported without prescribing", func(t *testing.T) {
		var buf bytes.Buffer

		warnNodeBodyImageDivergence(&buf, []string{write(t, "ghcr.io/cozystack/cozystack/talos:v1.11.0")}, target)

		out := buf.String()
		if !strings.Contains(out, "v1.11.0") || !strings.Contains(out, "v1.12.6") {
			t.Errorf("the stale body was not reported at all:\n%s", out)
		}

		if strings.Contains(out, "Bump values.yaml") {
			t.Errorf("told the operator to bump values.yaml, which is what put the body behind:\n%s", out)
		}

		if strings.Contains(out, "warning:") {
			t.Errorf("labelled the canonical flow as a warning:\n%s", out)
		}
	})

	t.Run("body ahead of the target warns and prescribes", func(t *testing.T) {
		var buf bytes.Buffer

		warnNodeBodyImageDivergence(&buf, []string{write(t, "ghcr.io/cozystack/cozystack/talos:v1.13.0")}, target)

		out := buf.String()
		if !strings.Contains(out, "warning:") {
			t.Errorf("a body the upgrade will not honour was not flagged:\n%s", out)
		}

		if !strings.Contains(out, "values.yaml") {
			t.Errorf("the way out was not named:\n%s", out)
		}
	})

	// A patch ahead is the same incident one granularity down: the node moves
	// backwards, and the pre-upgrade guard lets it through because Talos allows
	// patch moves inside a minor. Ordering below the minor is what catches it.
	t.Run("body a patch ahead of the target warns", func(t *testing.T) {
		var buf bytes.Buffer

		warnNodeBodyImageDivergence(&buf, []string{write(t, "ghcr.io/cozystack/cozystack/talos:v1.12.9")}, target)

		if out := buf.String(); !strings.Contains(out, "warning:") {
			t.Errorf("a body a patch ahead was reported as the canonical flow:\n%s", out)
		}
	})

	t.Run("body a patch behind the target is the canonical flow", func(t *testing.T) {
		var buf bytes.Buffer

		warnNodeBodyImageDivergence(&buf, []string{write(t, "ghcr.io/cozystack/cozystack/talos:v1.12.1")}, target)

		if out := buf.String(); strings.Contains(out, "warning:") {
			t.Errorf("a trailing patch was flagged as going somewhere else:\n%s", out)
		}
	})

	t.Run("a body already on the target says nothing", func(t *testing.T) {
		var buf bytes.Buffer

		warnNodeBodyImageDivergence(&buf, []string{write(t, target)}, target)

		if out := buf.String(); out != "" {
			t.Errorf("reported a body that is already on the target, which is every node on an ordinary upgrade:\n%s", out)
		}
	})

	t.Run("an empty image declares nothing", func(t *testing.T) {
		var buf bytes.Buffer

		warnNodeBodyImageDivergence(&buf, []string{write(t, `""`)}, target)

		if out := buf.String(); out != "" {
			t.Errorf("reported an empty image, which would print a message with a hole in it:\n%s", out)
		}
	})

	t.Run("a body with no tag warns", func(t *testing.T) {
		var buf bytes.Buffer

		warnNodeBodyImageDivergence(&buf, []string{write(t, "ghcr.io/cozystack/cozystack/talos")}, target)

		if out := buf.String(); !strings.Contains(out, "warning:") {
			t.Errorf("an untagged body cannot be shown to trail the target, so it is a divergence:\n%s", out)
		}
	})

	t.Run("a body naming another image warns", func(t *testing.T) {
		var buf bytes.Buffer

		warnNodeBodyImageDivergence(&buf, []string{write(t, "registry.example.com/custom/talos:v1.12.6")}, target)

		if out := buf.String(); !strings.Contains(out, "warning:") {
			t.Errorf("an image the upgrade will not go to was not flagged:\n%s", out)
		}
	})
}

// The godoc promises silence on a file the walker cannot make sense of. Each
// of these reaches a different surrender: an unreadable path, a stream that
// does not parse, and a structurally wrong machine.install.image.
func TestContract_UpgradeDivergenceWarningSurrendersQuietly(t *testing.T) {
	dir := t.TempDir()

	badYAML := filepath.Join(dir, "broken.yaml")
	if err := os.WriteFile(badYAML, []byte("machine:\n\tinstall: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	wrongShape := filepath.Join(dir, "wrong-shape.yaml")
	if err := os.WriteFile(wrongShape, []byte("machine:\n  install: \"oops\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		path string
	}{
		{name: "unreadable file", path: filepath.Join(dir, "does-not-exist.yaml")},
		{name: "unparseable stream", path: badYAML},
		{name: "structurally wrong install", path: wrongShape},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer

			warnNodeBodyImageDivergence(&buf, []string{tc.path}, "ghcr.io/cozystack/cozystack/talos:v1.12.6")

			if out := buf.String(); out != "" {
				t.Errorf("warned instead of surrendering:\n%s", out)
			}
		})
	}
}

// The report only exists if the handler still calls it. Replacing the call
// with `_ = filesToProcess` leaves every test above green, because they all
// drive the function directly — so this one goes through cmd.RunE.
//
// The downgrade guard has the same pin for the same reason; without this the
// two halves of the branch are covered unevenly.
func TestContract_UpgradeDivergenceReportReachesTheOperator(t *testing.T) {
	root := t.TempDir()

	// DetectAndSetRootFromFiles anchors on these two, so the -f file has to sit
	// inside something shaped like a talm project.
	for name, content := range map[string]string{
		"Chart.yaml":   "apiVersion: v2\nname: t\nversion: 0.1.0\n",
		"secrets.yaml": "cluster:\n  id: x\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if err := os.WriteFile(filepath.Join(root, "values.yaml"),
		[]byte("image: ghcr.io/cozystack/cozystack/talos:v1.12.6\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	nodeFile := filepath.Join(root, "node0.yaml")
	body := "# talm: nodes=[\"192.0.2.10\"]\nmachine:\n  type: controlplane\n  install:\n    image: ghcr.io/cozystack/cozystack/talos:v1.13.0\n"

	if err := os.WriteFile(nodeFile, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	savedRoot, savedVerify, savedNodes := Config.RootDir, upgradeCmdFlags.skipPostUpgradeVerify, GlobalArgs.Nodes

	t.Cleanup(func() {
		Config.RootDir = savedRoot
		upgradeCmdFlags.skipPostUpgradeVerify = savedVerify
		GlobalArgs.Nodes = savedNodes
	})

	Config.RootDir = root

	cmd := &cobra.Command{Use: upgradeCmdName}
	cmd.Flags().StringP("image", "i", "", "")
	cmd.Flags().Bool("stage", false, "")
	cmd.Flags().StringSliceP("file", "f", nil, "")
	wrapUpgradeCommand(cmd, func(*cobra.Command, []string) error { return nil })

	if err := cmd.Flags().Set("file", nodeFile); err != nil {
		t.Fatal(err)
	}

	upgradeCmdFlags.skipPostUpgradeVerify = true

	// The RunE error is deliberately ignored: the report is written during
	// image resolution, before anything that needs a cluster, so whatever
	// happens later does not affect what the operator already saw.
	out := captureStderr(t, func() { _ = cmd.RunE(cmd, nil) })

	if !strings.Contains(out, "node0.yaml") || !strings.Contains(out, "v1.13.0") {
		t.Errorf("the divergence never reached the operator through the real flow:\n%s", out)
	}
}
