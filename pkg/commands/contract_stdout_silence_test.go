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

// Contract: `talm` never emits human-facing progress / status text to
// stdout. stdout is reserved for the rendered config stream (so
// `talm template --file X > Y` produces a clean Y). Progress lines —
// the `- talm: file=…` lines printed during multi-file template /
// apply runs — must go to stderr.
//
// Pinning the rule in two layers:
//
//  1. Source-level invariant: walk pkg/commands/*.go and assert that
//     no `fmt.Print` / `fmt.Println` / `fmt.Printf` call starts a
//     literal with "- talm: ". The only acceptable channel for that
//     prefix is `fmt.Fprintf(os.Stderr, …)`. This locks all four
//     known sites at once (template.go:225 + apply.go:422/425/571)
//     and prevents regression at any future call site.
//
//  2. Integration: run `templateOneFile` in --offline mode against a
//     synthetic chart, capture stdout and stderr separately, and
//     assert stdout contains only the rendered config (modeline +
//     warn banner + body) while the `- talm: file=…` progress line
//     lands on stderr.

package commands

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestContract_NoTalmProgressOnStdout walks every .go source file in
// pkg/commands via go/ast and asserts no call expression with the
// shape `fmt.Print*("- talm: …", …)` OR `fmt.Fprint*(os.Stdout, "-
// talm: …", …)` exists. AST-level matching catches multi-line call
// sites that a line-by-line text grep would miss (e.g.
// `fmt.Printf(\n\t"- talm: …",\n\t…)`).
//
// The progress prefix MUST only be emitted via stderr writers
// (`fmt.Fprint*(os.Stderr, "- talm: …", …)`); the test inspects the
// first non-writer string-literal argument and rejects any call that
// targets stdout (explicit or default).
func TestContract_NoTalmProgressOnStdout(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read pkg/commands directory: %v", err)
	}

	fset := token.NewFileSet()

	var violations []string

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}

		path := filepath.Join(".", entry.Name())

		file, parseErr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", path, parseErr)
		}

		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}

			if v := violationForFmtCall(call, fset); v != "" {
				violations = append(violations, path+":"+v)
			}

			return true
		})
	}

	if len(violations) > 0 {
		t.Errorf("found %d stdout-progress violation(s):\n%s\nfix: rewrite as `fmt.Fprintf(os.Stderr, \"- talm: …\", …)`",
			len(violations), strings.Join(violations, "\n"))
	}
}

// violationForFmtCall returns a human-readable position+description
// when `call` matches the forbidden shape: a fmt.{Print,Printf,Println}
// invocation or fmt.{Fprint,Fprintf,Fprintln}(os.Stdout, …) where the
// first non-writer argument is a string literal starting with `- talm:`.
// Returns the empty string otherwise.
func violationForFmtCall(call *ast.CallExpr, fset *token.FileSet) string {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}

	ident, ok := sel.X.(*ast.Ident)
	if !ok || ident.Name != "fmt" {
		return ""
	}

	args := call.Args

	switch sel.Sel.Name {
	case "Print", "Printf", "Println":
		// stdout by default — first arg is the format / value.
	case "Fprint", "Fprintf", "Fprintln":
		// stdout only if first arg is os.Stdout; skip os.Stderr & writers.
		if len(args) == 0 || !isOsStdout(args[0]) {
			return ""
		}

		args = args[1:]
	default:
		return ""
	}

	if len(args) == 0 {
		return ""
	}

	lit, ok := args[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return ""
	}

	if !strings.HasPrefix(lit.Value, `"- talm:`) {
		return ""
	}

	pos := fset.Position(call.Pos())

	return pos.String() + ": forbidden fmt." + sel.Sel.Name + "(… " + lit.Value + " …) on stdout"
}

func isOsStdout(e ast.Expr) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok {
		return false
	}

	ident, ok := sel.X.(*ast.Ident)

	return ok && ident.Name == "os" && sel.Sel.Name == "Stdout"
}

// TestContract_TemplateProgress_GoesToStderr runs `templateOneFile`
// in offline mode against a synthetic chart, captures stdout and
// stderr separately, and asserts:
//   - stdout contains the rendered config (modeline banner + body)
//   - stdout does NOT contain the `- talm: file=` progress line
//   - stderr DOES contain the progress line
//
// Without --offline, templateOneFile would dial a Talos node; --offline
// short-circuits that and exercises the in-process rendering path
// alone. The chart fixture is the same minimal layout used by the
// other generateOutput-level tests (Chart.yaml + values.yaml +
// secrets.yaml + templates/config.yaml).
func TestContract_TemplateProgress_GoesToStderr(t *testing.T) {
	withTemplateFlagsSnapshot(t)

	root := makeMinimalChart(t)

	nodeFile := filepath.Join(root, "node0.yaml")
	nodeBody := "# talm: nodes=[\"" + testNodeIP + "\"], endpoints=[\"" + testNodeIP + "\"], templates=[\"templates/config.yaml\"]\n" +
		"machine:\n  type: worker\n"

	if err := os.WriteFile(nodeFile, []byte(nodeBody), 0o600); err != nil {
		t.Fatalf("write node file: %v", err)
	}

	Config.RootDir = root
	templateCmdFlags.configFiles = []string{nodeFile}
	templateCmdFlags.offline = true
	templateCmdFlags.templatesFromArgs = false
	templateCmdFlags.nodesFromArgs = false
	templateCmdFlags.endpointsFromArgs = false

	stdout, stderr := captureStdoutAndStderr(t, func() {
		runErr := templateWithFiles(nil)(context.Background(), nil)
		if runErr != nil {
			t.Fatalf("templateWithFiles failed: %v", runErr)
		}
	})

	if strings.Contains(stdout, "- talm: file=") {
		t.Errorf("stdout MUST NOT contain `- talm: file=` progress line; got stdout:\n%s", stdout)
	}

	if !strings.Contains(stderr, "- talm: file=") {
		t.Errorf("stderr MUST contain `- talm: file=` progress line; got stderr:\n%s", stderr)
	}

	if !strings.Contains(stdout, "# talm: nodes=") {
		t.Errorf("stdout MUST contain rendered modeline `# talm: nodes=…`; got stdout:\n%s", stdout)
	}

	if !strings.Contains(stdout, "machine:") {
		t.Errorf("stdout MUST contain rendered config body (`machine:`); got stdout:\n%s", stdout)
	}
}

const testNodeIP = "192.0.2.10" // RFC 5737 TEST-NET-1

// captureStdoutAndStderr redirects both os.Stdout and os.Stderr to
// pipes for the duration of fn, returns whatever fn printed on each
// channel. Companion to captureStdout in contract_template_test.go;
// extracted as a sibling helper so future stdout/stderr-discipline
// tests can reuse the two-channel form without duplicating the
// goroutine plumbing.
func captureStdoutAndStderr(t *testing.T, fn func()) (string, string) {
	t.Helper()

	rOut, wOut, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe stdout: %v", err)
	}

	rErr, wErr, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe stderr: %v", err)
	}

	origOut := os.Stdout
	origErr := os.Stderr
	os.Stdout = wOut
	os.Stderr = wErr

	t.Cleanup(func() {
		os.Stdout = origOut
		os.Stderr = origErr
	})

	doneOut := drainPipe(rOut)
	doneErr := drainPipe(rErr)

	fn()
	_ = wOut.Close()
	_ = wErr.Close()

	return <-doneOut, <-doneErr
}

func drainPipe(r *os.File) <-chan string {
	out := make(chan string, 1)

	go func() {
		buf := make([]byte, 0, 64*1024)
		readBuf := make([]byte, 4096)

		for {
			n, err := r.Read(readBuf)
			if n > 0 {
				buf = append(buf, readBuf[:n]...)
			}

			if err != nil {
				break
			}
		}

		out <- string(buf)
	}()

	return out
}
