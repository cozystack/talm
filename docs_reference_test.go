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

package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"
)

// referenceDir is the published CLI reference, rendered from the cobra
// command tree and committed so the docs site can build without a Go
// toolchain.
const referenceDir = "docs/reference"

// updateReference reports whether to rewrite the committed reference
// instead of comparing against it. Mirrors TALM_UPDATE_GOLDEN in
// pkg/engine:
//
//	TALM_UPDATE_DOCS=1 go test . -run TestReferenceDocs
func updateReference() bool {
	return os.Getenv("TALM_UPDATE_DOCS") != ""
}

// referencePage maps a cobra command path to the page that documents it.
// "talm" is the site's reference index; everything below it drops the
// binary name, so `talm etcd status` lands at etcd_status.md and is
// served from /reference/etcd_status/.
func referencePage(commandPath string) string {
	trimmed := strings.TrimPrefix(commandPath, "talm")
	trimmed = strings.TrimSpace(trimmed)
	if trimmed == "" {
		return "index.md"
	}

	return strings.ReplaceAll(trimmed, " ", "_") + ".md"
}

// referenceLink rewrites the cross-references cobra emits under SEE ALSO
// (talm_etcd_status.md) into the page names used here.
func referenceLink(name string) string {
	return referencePage(strings.ReplaceAll(strings.TrimSuffix(name, ".md"), "_", " "))
}

// renderReference walks the command tree and returns page name -> content.
//
// It renders each command with doc.GenMarkdownCustom rather than calling
// doc.GenMarkdownTree, which hardcodes both the file names and an opening
// H2. MkDocs takes a page's title from its first H1 and falls back to the
// file name, so the heading is promoted here.
func renderReference(root *cobra.Command) (map[string]string, error) {
	pages := map[string]string{}

	var walk func(*cobra.Command) error
	walk = func(cmd *cobra.Command) error {
		// Cobra only propagates this from a parent inside its SEE ALSO
		// branch. Without it every command stamps the current date into
		// its page and no two runs ever agree.
		cmd.DisableAutoGenTag = true

		buf := &bytes.Buffer{}
		if err := doc.GenMarkdownCustom(cmd, buf, referenceLink); err != nil {
			return fmt.Errorf("render %s: %w", cmd.CommandPath(), err)
		}

		pages[referencePage(cmd.CommandPath())] = promoteHeadings(buf.String())

		for _, child := range cmd.Commands() {
			if !child.IsAvailableCommand() || child.IsAdditionalHelpTopicCommand() {
				continue
			}
			if err := walk(child); err != nil {
				return err
			}
		}

		return nil
	}

	if err := walk(root); err != nil {
		return nil, err
	}

	return pages, nil
}

// cobraHeadings are the section headings doc.GenMarkdownCustom emits. It
// opens a page at H2 and puts every section under it at H3, which leaves
// MkDocs with no H1 to title the page from and a table of contents that
// starts one level too deep. Promote the whole document by one level.
//
//nolint:gochecknoglobals // immutable lookup table read by promoteHeadings.
var cobraHeadings = []string{
	"### Synopsis",
	"### Examples",
	"### Options",
	"### Options inherited from parent commands",
	"### SEE ALSO",
}

func promoteHeadings(body string) string {
	body = strings.Replace(body, "## ", "# ", 1)

	for _, heading := range cobraHeadings {
		body = strings.ReplaceAll(body, "\n"+heading+"\n", "\n"+heading[1:]+"\n")
	}

	return body
}

// referenceRoot returns the command tree as an operator sees it.
func referenceRoot(t *testing.T) *cobra.Command {
	t.Helper()

	// Persistent flags supply the "Options inherited from parent commands"
	// section of every page. registerRootFlags is single-call by contract
	// (pflag panics on a redefined flag), and rootCmd is a package global
	// that survives between tests, so guard on a flag it registers rather
	// than calling it blind — otherwise `go test -count=2` panics.
	if rootCmd.PersistentFlags().Lookup("talosconfig") == nil {
		registerRootFlags(rootCmd)
	}

	// Cobra builds `completion` lazily inside Execute(), so it is absent
	// from the tree a test walks. Operators have it, so document it. The
	// call is idempotent: it returns early once the command exists.
	rootCmd.InitDefaultCompletionCmd()

	return rootCmd
}

// TestReferenceDocs pins docs/reference/ to the cobra command tree, so
// adding a command — including one inherited from a Talos bump, since talm
// wraps talosctl's commands — fails here until the reference is
// regenerated. Cobra's own `help` command is not documented:
// IsAvailableCommand reports false for it, so both the walk here and
// cobra's SEE ALSO loop skip it.
func TestReferenceDocs(t *testing.T) {
	// Flag defaults are built with filepath.Join (the --talosconfig
	// default among them), so the rendered pages carry backslashes on
	// Windows and a byte comparison fails for reasons unrelated to
	// drift. Linux CI is what enforces freshness.
	if runtime.GOOS == "windows" {
		t.Skip("reference pages embed OS-specific path separators; generated and verified on Linux")
	}

	pages, err := renderReference(referenceRoot(t))
	if err != nil {
		t.Fatalf("render reference: %v", err)
	}

	if updateReference() {
		writeReference(t, pages)

		return
	}

	compareReference(t, pages)
}

func writeReference(t *testing.T, pages map[string]string) {
	t.Helper()

	existing, err := filepath.Glob(filepath.Join(referenceDir, "*.md"))
	if err != nil {
		t.Fatalf("list reference dir: %v", err)
	}

	for _, path := range existing {
		if _, kept := pages[filepath.Base(path)]; !kept {
			if err := os.Remove(path); err != nil {
				t.Fatalf("remove stale page %s: %v", path, err)
			}
		}
	}

	if err := os.MkdirAll(referenceDir, 0o755); err != nil {
		t.Fatalf("create reference dir: %v", err)
	}

	for name, body := range pages {
		if err := os.WriteFile(filepath.Join(referenceDir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

func compareReference(t *testing.T, pages map[string]string) {
	t.Helper()

	const regenerate = "regenerate with: TALM_UPDATE_DOCS=1 go test . -run TestReferenceDocs"

	onDisk, err := filepath.Glob(filepath.Join(referenceDir, "*.md"))
	if err != nil {
		t.Fatalf("list reference dir: %v", err)
	}

	seen := map[string]bool{}

	for _, path := range onDisk {
		name := filepath.Base(path)
		seen[name] = true

		want, documented := pages[name]
		if !documented {
			t.Errorf("%s documents a command that no longer exists — %s", path, regenerate)

			continue
		}

		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}

		if string(got) != want {
			t.Errorf("%s differs from the command tree — %s", path, regenerate)
		}
	}

	missing := []string{}

	for name := range pages {
		if !seen[name] {
			missing = append(missing, name)
		}
	}

	sort.Strings(missing)

	if len(missing) > 0 {
		t.Errorf("%d command(s) have no page: %s — %s", len(missing), strings.Join(missing, ", "), regenerate)
	}
}
