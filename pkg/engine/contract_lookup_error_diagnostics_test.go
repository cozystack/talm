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

// Contract: wrapLookupError enriches every lookup failure with two
// layers of operator-facing context:
//
//  1. ALWAYS: the wrapped message names the resource kind, namespace,
//     id, and dialed endpoints. The pre-fix chain stripped this and
//     left the operator with a generic "iterating resources: rpc
//     error: …" trail.
//
//  2. ALWAYS: an errors.WithHint annotation steers the operator
//     toward the right remedy based on classifyLookupError's verdict.
//     The hint distinguishes connectivity-class failures (TLS /
//     refused / deadline / authn — where `--offline` is a safe
//     escape for `talm template`) from resource-class failures
//     (`--offline` would mask a real chart bug — explicitly NOT
//     suggested in that branch).
//
// For `talm apply`, hints never mention `--offline` because apply has
// no such flag; suggesting it would teach a non-existent workflow.

package engine

import (
	"strings"
	"testing"

	cockroachErrors "github.com/cockroachdb/errors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// errTestNonGRPC is a sentinel returned by tests that need a
// non-gRPC, non-status error to exercise the unknown-class branch.
// Package-level so err113 sees it as a wrapped static error rather
// than a dynamic `errors.New` at the call site.
var errTestNonGRPC = cockroachErrors.New("some non-grpc thing went wrong")

const (
	testLookupKind        = "disks"
	testLookupNamespace   = ""
	testLookupDocID       = ""
	testLookupCmdTemplate = "talm template"
	// Pinned to the exported engine constant so any drift between
	// pkg/commands/apply.go (which also reads CommandNameApply) and
	// the apply-no-offline contract surfaces here at compile / run
	// time rather than as a silent hint regression.
	testLookupCmdApply = CommandNameApply
)

var testLookupEndpoints = []string{"192.0.2.10"} //nolint:gochecknoglobals // table-test fixture; immutable

// === Level 1: wrap context (always-on, class-independent) ===

func TestContract_WrapLookupError_Nil(t *testing.T) {
	got := wrapLookupError(nil, testLookupKind, testLookupNamespace, testLookupDocID, testLookupEndpoints, testLookupCmdTemplate)
	if got != nil {
		t.Errorf("nil input must produce nil output; got %v", got)
	}
}

func TestContract_WrapLookupError_NamesKindNamespaceIdEndpoints(t *testing.T) {
	inner := status.Error(codes.Unavailable, "connection error: desc = \"transport: authentication handshake failed: EOF\"")
	got := wrapLookupError(inner, "disks", "system", "system-disk", []string{"192.0.2.10", "192.0.2.11"}, testLookupCmdTemplate)

	if got == nil {
		t.Fatal("expected non-nil error")
	}

	msg := got.Error()
	for _, want := range []string{
		`kind="disks"`,
		`namespace="system"`,
		`id="system-disk"`,
		`192.0.2.10`,
		`192.0.2.11`,
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("wrapped message missing %q; got: %s", want, msg)
		}
	}

	if !strings.Contains(msg, "handshake failed") {
		t.Errorf("wrapped message must preserve the underlying error; got: %s", msg)
	}
}

// === Level 2: class-specific hints ===

func hintsOf(t *testing.T, err error) string {
	t.Helper()

	hints := cockroachErrors.GetAllHints(err)

	return strings.Join(hints, "\n")
}

func TestContract_WrapLookupError_TLSHandshake_HintMentionsCertSANsAndOffline(t *testing.T) {
	inner := status.Error(codes.Unavailable, "connection error: desc = \"transport: authentication handshake failed: EOF\"")
	got := wrapLookupError(inner, testLookupKind, testLookupNamespace, testLookupDocID, testLookupEndpoints, testLookupCmdTemplate)
	hint := hintsOf(t, got)

	for _, want := range []string{"TLS handshake", "cert SAN", "maintenance mode", "--offline"} {
		if !strings.Contains(hint, want) {
			t.Errorf("TLS-handshake hint missing %q; got hint:\n%s", want, hint)
		}
	}

	if !strings.Contains(hint, "192.0.2.10") {
		t.Errorf("hint must name the dialed endpoint; got:\n%s", hint)
	}

	if !strings.Contains(hint, "talosctl --endpoints 192.0.2.10 get nodeaddress") {
		t.Errorf("hint must point at `talosctl --endpoints X get nodeaddress`; got:\n%s", hint)
	}
}

// Contract: the talosctl suggestion lists ALL configured endpoints
// comma-separated, not just the first one. Naming only the first
// would mislead an operator whose actual failing node is somewhere
// in the middle of the list (e.g. 3-node modeline, middle node
// down).
func TestContract_WrapLookupError_TLSHandshake_MultipleEndpoints_AllListed(t *testing.T) {
	inner := status.Error(codes.Unavailable, "connection error: desc = \"transport: authentication handshake failed: EOF\"")
	got := wrapLookupError(inner, testLookupKind, testLookupNamespace, testLookupDocID, []string{"192.0.2.10", "192.0.2.11", "192.0.2.12"}, testLookupCmdTemplate)
	hint := hintsOf(t, got)

	if !strings.Contains(hint, "talosctl --endpoints 192.0.2.10,192.0.2.11,192.0.2.12 get nodeaddress") {
		t.Errorf("hint must list all endpoints comma-separated; got:\n%s", hint)
	}
}

// Contract: when endpoints is empty (e.g. no --endpoints flag, no
// modeline endpoints, no talosconfig context), the `talosctl`
// suggestion must remain grammatical — no double space, no dangling
// `--endpoints` with no value, no literal `[]` leaking through
// fmt.Sprintf("%v", nil). Reaches the engine through
// `engine.Options.TalosEndpoints` populated by `append([]string(nil),
// GlobalArgs.Endpoints...)`; an empty GlobalArgs.Endpoints flows
// through as a nil/empty slice.
func TestContract_WrapLookupError_TLSHandshake_EmptyEndpoints(t *testing.T) {
	inner := status.Error(codes.Unavailable, "connection error: desc = \"transport: authentication handshake failed: EOF\"")
	got := wrapLookupError(inner, testLookupKind, testLookupNamespace, testLookupDocID, nil, testLookupCmdTemplate)
	hint := hintsOf(t, got)

	if strings.Contains(hint, "talosctl  get") || strings.Contains(hint, "talosctl  --") {
		t.Errorf("hint has a double space — empty endpoints leaked into the suggestion; got:\n%s", hint)
	}

	if strings.Contains(hint, "--endpoints get") || strings.Contains(hint, "--endpoints \n") {
		t.Errorf("hint has a dangling --endpoints with no value; got:\n%s", hint)
	}

	if !strings.Contains(hint, "talosctl get nodeaddress") {
		t.Errorf("hint must fall back to a clean `talosctl get nodeaddress` form; got:\n%s", hint)
	}

	if strings.Contains(hint, "endpoint []") || strings.Contains(hint, "aborted  ") {
		t.Errorf("empty endpoints leaked into the hint lead text (literal `[]` or double space); got:\n%s", hint)
	}
}

// Contract: every hint — including resource-class and unknown-class
// — must read grammatically with empty endpoints. No leading-bracket
// leak, no double space before the em-dash. The resource branch
// historically never names endpoints (chart bug is endpoint-
// independent) but the test still pins absence of `[]` so a future
// refactor that introduces an endpoint reference there is forced to
// handle the empty case.
func TestContract_WrapLookupError_AllClasses_EmptyEndpointsGrammatical(t *testing.T) {
	cases := []struct {
		name  string
		inner error
	}{
		{"tls", status.Error(codes.Unavailable, "transport: authentication handshake failed: EOF")},
		{"refused", status.Error(codes.Unavailable, "connection refused")},
		{"deadline", status.Error(codes.DeadlineExceeded, "context deadline exceeded")},
		{"authn", status.Error(codes.Unauthenticated, "rejected")},
		{"resource", status.Error(codes.Internal, "no such resource type")},
		{"unknown_class", errTestNonGRPC},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := wrapLookupError(tc.inner, testLookupKind, testLookupNamespace, testLookupDocID, nil, testLookupCmdTemplate)
			hint := hintsOf(t, got)

			if strings.Contains(hint, "[]") {
				t.Errorf("literal `[]` leaked into hint (fmt.Sprintf %%v on nil slice); got:\n%s", hint)
			}

			if strings.Contains(hint, "  ") {
				t.Errorf("double space in hint (empty endpoints leaked); got:\n%s", hint)
			}
		})
	}
}

func TestContract_WrapLookupError_Refused_HintMentionsFirewallAndOffline(t *testing.T) {
	inner := status.Error(codes.Unavailable, "connection error: desc = \"transport: Error while dialing dial tcp 192.0.2.10:50000: connect: connection refused\"")
	got := wrapLookupError(inner, testLookupKind, testLookupNamespace, testLookupDocID, testLookupEndpoints, testLookupCmdTemplate)
	hint := hintsOf(t, got)

	for _, want := range []string{"refused", "firewall", "--offline"} {
		if !strings.Contains(hint, want) {
			t.Errorf("refused hint missing %q; got hint:\n%s", want, hint)
		}
	}
}

func TestContract_WrapLookupError_Deadline_HintMentionsTimeoutAndOffline(t *testing.T) {
	inner := status.Error(codes.DeadlineExceeded, "context deadline exceeded")
	got := wrapLookupError(inner, testLookupKind, testLookupNamespace, testLookupDocID, testLookupEndpoints, testLookupCmdTemplate)
	hint := hintsOf(t, got)

	for _, want := range []string{"timed out", "--offline"} {
		if !strings.Contains(hint, want) {
			t.Errorf("deadline hint missing %q; got hint:\n%s", want, hint)
		}
	}
}

func TestContract_WrapLookupError_Authn_HintMentionsTalosconfigAndOffline(t *testing.T) {
	inner := status.Error(codes.Unauthenticated, "talosconfig credentials rejected")
	got := wrapLookupError(inner, testLookupKind, testLookupNamespace, testLookupDocID, testLookupEndpoints, testLookupCmdTemplate)
	hint := hintsOf(t, got)

	for _, want := range []string{"talosconfig", "--offline"} {
		if !strings.Contains(hint, want) {
			t.Errorf("authn hint missing %q; got hint:\n%s", want, hint)
		}
	}
}

// Contract: a resource-class error (chart bug / unsupported resource)
// must NEVER suggest --offline — `--offline` would render the chart
// against an empty discovery surface and produce broken output that
// looks valid. The hint instead steers the operator at `talosctl get
// <kind>` to verify the resource exists.
func TestContract_WrapLookupError_Resource_HintWarnsAgainstOffline(t *testing.T) {
	inner := status.Error(codes.Internal, "no such resource type")
	got := wrapLookupError(inner, testLookupKind, testLookupNamespace, testLookupDocID, testLookupEndpoints, testLookupCmdTemplate)
	hint := hintsOf(t, got)

	if !strings.Contains(hint, "chart") {
		t.Errorf("resource-class hint must mention chart bug; got:\n%s", hint)
	}

	if !strings.Contains(hint, "talosctl get") {
		t.Errorf("resource-class hint must point at `talosctl get`; got:\n%s", hint)
	}

	if strings.Contains(hint, "--offline") {
		t.Errorf("resource-class hint must NOT suggest --offline (would mask chart bug); got:\n%s", hint)
	}
}

// Contract: for `talm apply`, no hint ever mentions --offline because
// apply has no such flag. The diagnosis itself stays the same; only
// the remedy phrasing flips ("verify reachability" instead).
func TestContract_WrapLookupError_Apply_HintNeverMentionsOffline(t *testing.T) {
	cases := []struct {
		name  string
		inner error
	}{
		{"tls", status.Error(codes.Unavailable, "connection error: desc = \"transport: authentication handshake failed: EOF\"")},
		{"refused", status.Error(codes.Unavailable, "connection refused")},
		{"deadline", status.Error(codes.DeadlineExceeded, "context deadline exceeded")},
		{"authn", status.Error(codes.Unauthenticated, "rejected")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := wrapLookupError(tc.inner, testLookupKind, testLookupNamespace, testLookupDocID, testLookupEndpoints, testLookupCmdApply)
			hint := hintsOf(t, got)

			if hint == "" {
				t.Errorf("apply hint must not be empty; got error: %v", got)
			}

			if strings.Contains(hint, "--offline") {
				t.Errorf("apply hint must NOT mention --offline (flag does not exist for apply); got:\n%s", hint)
			}
		})
	}
}

// Contract: when CommandName is empty (caller forgot to set it,
// untested call path, future subcommand) the hint MUST NOT suggest
// `--offline`. The remedy clause is allow-listed against
// CommandNameTemplate: anything else falls through to the safe
// generic "fix reachability" form. Reviewer flagged this as a real
// failure mode — engine.go defaults cmdName to "talm" when empty,
// and "talm" was previously falling into the template branch and
// suggesting --offline incorrectly.
func TestContract_WrapLookupError_EmptyCommandName_NoOfflineSuggestion(t *testing.T) {
	inner := status.Error(codes.Unavailable, "transport: authentication handshake failed: EOF")
	got := wrapLookupError(inner, testLookupKind, testLookupNamespace, testLookupDocID, testLookupEndpoints, "")
	hint := hintsOf(t, got)

	if strings.Contains(hint, "--offline") {
		t.Errorf("empty commandName must not get --offline suggestion (no such flag exists on unknown subcommands); got hint:\n%s", hint)
	}

	if hint == "" {
		t.Errorf("empty commandName must still receive a fallback hint; got error: %v", got)
	}
}

// Contract: an unrecognized commandName (e.g. a future subcommand
// not yet allow-listed) MUST also fall to the safe remedy. Mirrors
// the apply branch's safety semantics.
func TestContract_WrapLookupError_UnknownCommandName_NoOfflineSuggestion(t *testing.T) {
	inner := status.Error(codes.Unavailable, "connection refused")
	got := wrapLookupError(inner, testLookupKind, testLookupNamespace, testLookupDocID, testLookupEndpoints, "talm future-subcommand")
	hint := hintsOf(t, got)

	if strings.Contains(hint, "--offline") {
		t.Errorf("unknown commandName must not get --offline suggestion; got hint:\n%s", hint)
	}
}

// Contract: unknown class still gets a hint, but a generic one that
// guides toward filing an issue rather than guessing a specific
// remedy. Pinning so a refactor that drops the fallback hint
// surfaces here.
func TestContract_WrapLookupError_Unknown_HasFallbackHint(t *testing.T) {
	got := wrapLookupError(errTestNonGRPC, "machinetype", testLookupNamespace, testLookupDocID, testLookupEndpoints, testLookupCmdTemplate)
	hint := hintsOf(t, got)

	if hint == "" {
		t.Errorf("unknown-class error must still carry a fallback hint; got error: %v", got)
	}

	if !strings.Contains(got.Error(), `kind="machinetype"`) {
		t.Errorf("wrap must name the kind passed by caller; got: %s", got)
	}
}
