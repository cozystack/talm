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

package engine

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/hashicorp/go-multierror"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// CommandNameApply is the CommandName value `talm apply` passes to
// engine.Render. Exported so pkg/commands/apply.go uses it directly
// as the source of truth — eliminates the drift class that would
// otherwise let apply error hints silently start suggesting the
// non-existent `--offline` flag.
const CommandNameApply = "talm apply"

// CommandNameTemplate is the CommandName value `talm template` passes
// to engine.Render. Pinning to a constant lets offlineRemedyFor
// allow-list `--offline` suggestions (rather than deny-list against
// known no-offline subcommands) — that way any future subcommand or
// untested caller that forgets to set CommandName falls through to
// the safe "fix reachability" remedy instead of getting an
// `--offline` hint that may not exist for its flow.
const CommandNameTemplate = "talm template"

// lookupErrorClass partitions failures from the talos client `lookup`
// path into actionable categories. Each class drives a distinct
// remedy in hintForClass; the partition is intentionally lossy
// (substring-matched on gRPC code + description) — the goal is
// "right-enough hint 95% of the time", not exhaustive taxonomy.
type lookupErrorClass int

const (
	lookupErrUnknown lookupErrorClass = iota
	lookupErrTLSHandshake
	lookupErrRefused
	lookupErrDeadline
	lookupErrAuthn
	lookupErrResource
)

// classifyLookupError inspects err and returns the most specific
// class it can identify. Discriminators are checked in priority
// order; `Unavailable` with no diagnostic substring falls back to
// `lookupErrRefused` (the dominant real-world cause of bare
// Unavailable from gRPC dial).
func classifyLookupError(err error) lookupErrorClass {
	if err == nil {
		return lookupErrUnknown
	}

	desc := strings.ToLower(err.Error())

	if strings.Contains(desc, "certificate signed by unknown authority") ||
		strings.Contains(desc, "x509:") {
		return lookupErrAuthn
	}

	if strings.Contains(desc, "handshake failed") || strings.Contains(desc, "tls:") {
		return lookupErrTLSHandshake
	}

	if strings.Contains(desc, "connection refused") ||
		strings.Contains(desc, "no route to host") ||
		strings.Contains(desc, "network is unreachable") {
		return lookupErrRefused
	}

	switch status.Code(err) {
	case codes.DeadlineExceeded:
		return lookupErrDeadline
	case codes.Unauthenticated:
		return lookupErrAuthn
	case codes.Internal, codes.InvalidArgument, codes.FailedPrecondition, codes.NotFound, codes.Unimplemented:
		// NotFound and Unimplemented at this level come from the
		// ResolveResourceKind path inside helpers.ForEachResource —
		// the operator asked for a resource kind the target Talos
		// version doesn't know about. The per-node callback already
		// filters NotFound for missing instances (engine.go), so any
		// NotFound reaching the classifier is definitionally a
		// resource-class problem (chart bug, version mismatch).
		return lookupErrResource
	case codes.Unavailable:
		// Diagnostic substrings already checked above; reaching here
		// means "channel down, root cause unidentified". Operators
		// expect "refused / firewall / wrong port" guidance for this
		// class far more often than "TLS / authn / chart bug" — the
		// refused-class remedy is a strict superset of useful
		// suggestions for the unknown-Unavailable case.
		return lookupErrRefused
	case codes.OK,
		codes.Canceled,
		codes.Unknown,
		codes.AlreadyExists,
		codes.PermissionDenied,
		codes.ResourceExhausted,
		codes.Aborted,
		codes.OutOfRange,
		codes.DataLoss:
		// Not connectivity-class; fall through to substring fallback.
	}

	if strings.Contains(desc, "context deadline exceeded") {
		return lookupErrDeadline
	}

	return lookupErrUnknown
}

// wrapLookupError enriches a raw `lookup` error with two layers of
// operator-facing context:
//
//  1. The wrap message names the resource kind, namespace, id, and
//     dialed endpoints — the pre-fix chain stripped these and forced
//     the operator to read the chart template trace to recover them.
//  2. An errors.WithHint annotation steers the operator toward the
//     right remedy for the classified failure mode.
//
// Returns nil when err is nil so call sites can use it
// unconditionally after `helpers.ForEachResource`.
func wrapLookupError(err error, kind, namespace, docID string, endpoints []string, commandName string) error {
	if err == nil {
		return nil
	}

	enriched := errors.Wrapf(err,
		"looking up resource kind=%q namespace=%q id=%q on endpoints=%v",
		kind, namespace, docID, endpoints)

	class := classifyLookupError(err)

	//nolint:wrapcheck // WithHint annotates the already-wrapped error chain; double-wrap would obscure the chain.
	return errors.WithHint(enriched, hintForClass(class, commandName, kind, endpoints))
}

// hintForClass returns the human-facing remedy for a classified
// lookup failure. The `talm apply` branch never mentions `--offline`
// because that flag is template-only; suggesting it would teach a
// non-existent workflow.
func hintForClass(class lookupErrorClass, commandName, kind string, endpoints []string) string {
	offlineRemedy := offlineRemedyFor(commandName)

	switch class {
	case lookupErrTLSHandshake:
		return fmt.Sprintf(
			"TLS handshake aborted%s — common causes: "+
				"(1) node is not running Talos or is in maintenance mode, "+
				"(2) cert SANs do not include the dialed address, "+
				"(3) wrong port. "+
				"Verify with `talosctl%s get nodeaddress` first. %s",
			endpointPhrase(endpoints), endpointsSuggestionSuffix(endpoints), offlineRemedy)

	case lookupErrRefused:
		return fmt.Sprintf(
			"connection refused%s — node down, wrong port, or blocked by firewall. "+
				"Verify the network path. %s",
			endpointPhrase(endpoints), offlineRemedy)

	case lookupErrDeadline:
		return fmt.Sprintf(
			"request timed out%s — node is slow to respond or the network is partitioned. %s",
			endpointPhrase(endpoints), offlineRemedy)

	case lookupErrAuthn:
		return fmt.Sprintf(
			"talosconfig credentials rejected%s — verify the talosconfig context and the node's PKI. %s",
			endpointPhrase(endpoints), offlineRemedy)

	case lookupErrResource:
		return fmt.Sprintf(
			"lookup of resource kind=%q failed with a non-network error — likely a chart bug or unsupported Talos resource. "+
				"Verify the resource exists with `talosctl get %s`. "+
				"Skipping live discovery would not help here: chart helpers depend on this value and an empty result would produce broken output that looks valid.",
			kind, kind)

	case lookupErrUnknown:
		// fall through to the generic remedy below.
	}

	return fmt.Sprintf(
		"lookup failed%s with an unrecognized error — "+
			"file an issue at https://github.com/cozystack/talm/issues with the full output.",
		endpointPhrase(endpoints))
}

// offlineRemedyFor returns the closing sentence of a connectivity-
// class hint. Allow-listed: only callers that explicitly identify as
// `CommandNameTemplate` get the `--offline` escape clause, because
// only `talm template` ships the flag. Every other caller (apply,
// untested callers, callers that forgot to set CommandName, future
// subcommands) gets the safe generic "fix reachability" remedy. The
// allow-list avoids the failure mode where a missing CommandName
// would silently leak `--offline` into a hint for a flow that has
// no such flag.
func offlineRemedyFor(commandName string) string {
	if commandName == CommandNameTemplate {
		return "If live discovery is not required for this render, pass --offline to skip it."
	}

	return "Fix node reachability before re-running."
}

// endpointsSuggestionSuffix builds the ` --endpoints X,Y,…` suffix
// appended to a `talosctl` suggestion in hints. Returns the empty
// string when endpoints is empty so the rendered suggestion stays
// grammatical (`talosctl get nodeaddress` rather than `talosctl
// --endpoints  get nodeaddress` with a double space). Lists all
// configured endpoints comma-separated — `talosctl --endpoints`
// accepts multiple, and naming only the first would mislead the
// operator when the failing one is somewhere in the middle of the
// list.
func endpointsSuggestionSuffix(endpoints []string) string {
	if len(endpoints) == 0 {
		return ""
	}

	return " --endpoints " + strings.Join(endpoints, ",")
}

// endpointPhrase returns the operator-facing phrase that names the
// dialed endpoint set in hint leads. When endpoints is empty (no
// --endpoints flag, no modeline endpoints, no talosconfig context)
// returns the empty string so the hint reads grammatically without
// a literal `[]` bracket pair leaking through fmt.Sprintf("%v", nil).
// Includes the leading space so the call site can write "verb%s —"
// and get clean output in both the populated and empty cases.
func endpointPhrase(endpoints []string) string {
	if len(endpoints) == 0 {
		return ""
	}

	return fmt.Sprintf(" by endpoint %v", endpoints)
}

// retryPolicy parameterises retryWithFailFast so tests can drop the
// backoff to zero while production keeps the exponential schedule.
// Both fields are required; a zero maxAttempts skips the loop body
// entirely and would silently swallow any error.
type retryPolicy struct {
	maxAttempts int
	backoff     func(attempt int) time.Duration
}

// defaultMaxAttempts is the upper bound on `lookup` retries in
// production. Three attempts with 200ms · 2^n backoff = total
// inter-attempt wait of 600ms (200 + 400). Keeps the CLI responsive
// while catching genuinely transient failures (connection blip,
// brief partition, controller restart).
const defaultMaxAttempts = 3

// defaultRetryPolicy is the production retry budget for `lookup`
// against the live Talos API. Returned by function rather than held
// as a var so test-time policy injection is obviously local and the
// gochecknoglobals lint suppression is not paid module-wide.
func defaultRetryPolicy() retryPolicy {
	return retryPolicy{
		maxAttempts: defaultMaxAttempts,
		backoff:     interAttemptBackoff,
	}
}

// interAttemptBackoff is the wait between attempt N and attempt N+1
// (200ms · 2^N). Caller passes the just-finished attempt's zero-
// based index, so the maximum value actually slept is
// `interAttemptBackoff(maxAttempts - 2)` — the last attempt's value
// is computed by the loop but never slept on because the loop breaks
// at `i == maxAttempts - 1`. Renamed from the earlier
// `productionBackoff` to make the inter-attempt semantics explicit.
func interAttemptBackoff(attempt int) time.Duration {
	return 200 * time.Millisecond * time.Duration(1<<attempt)
}

// firstLookupError picks the most operator-relevant error from the
// two paths through which `helpers.ForEachResource` reports failure:
// the direct return value (resource-definition resolution against
// the first endpoint) and the per-node multierror that accumulates
// callback failures (per-node dial issues against the rest of a
// multi-node lookup). Resource-definition errors take precedence
// because they are categorically more severe — a failed resolve
// means NO lookup result is possible, while per-node failures may
// still produce a partial result the chart could use.
//
// When surfacing from multiErr, returns the LAST appended error
// rather than the multierror envelope. classifyLookupError uses
// substring matching on err.Error(); the multierror's concatenated
// text would mix discriminators across nodes (e.g. one node's `x509:`
// failure would mis-flip the whole batch to lookupErrAuthn). The
// last-appended error is the most recent dial outcome and yields the
// cleanest classification signal.
//
// Returns nil only when both inputs are nil. Surfaces the original
// errors verbatim so classifyLookupError downstream can read the
// gRPC status code without unwrapping noise.
func firstLookupError(helperErr error, multiErr *multierror.Error) error {
	if helperErr != nil {
		return helperErr
	}

	if multiErr == nil || len(multiErr.Errors) == 0 {
		return nil
	}

	return multiErr.Errors[len(multiErr.Errors)-1]
}

// retryableLookupError reports whether a failure class is worth
// retrying. Only transient connectivity failures qualify: refused
// connections frequently clear within milliseconds (controller
// restart, brief partition), deadline-exceeded points at a slow
// hop that may recover. TLS handshake, authn, resource-class, and
// unknown errors are deterministic — retrying buys nothing and
// costs operator latency.
//
// `default` panics rather than returning false so a future class
// added to the iota set without being threaded through here is loud
// at first reach, not silent (a silently-non-retryable new class
// would degrade UX without any signal).
func retryableLookupError(class lookupErrorClass) bool {
	switch class {
	case lookupErrRefused, lookupErrDeadline:
		return true
	case lookupErrTLSHandshake, lookupErrAuthn, lookupErrResource, lookupErrUnknown:
		return false
	default:
		panic(fmt.Sprintf("retryableLookupError: unhandled lookupErrorClass %d — add it to the switch", class))
	}
}

// retryWithFailFast invokes attempt up to policy.maxAttempts times.
// After each failure shouldRetry decides whether the loop continues;
// false returns immediately so the operator does not wait through
// pointless retries for deterministic failures OR for partial-success
// scenarios where retrying would discard already-collected data
// without curing the persistent-failure node (the stale-modeline
// case). Honours ctx cancellation between attempts — Ctrl+C in
// `talm template / apply` interrupts the backoff sleep instantly.
//
// shouldRetry receives the raw error so callers that need to consider
// closure state (e.g. "partial success collected → don't retry") can
// close over local variables in addition to consulting the error
// class. The package-level convenience defaultShouldRetry implements
// the pure class-based predicate.
//
// Returns the LAST error observed (not a multierror) so the downstream
// wrap+hint sees the most recent failure for classification purposes.
func retryWithFailFast(ctx context.Context, attempt func() error, shouldRetry func(error) bool, policy retryPolicy) error {
	var lastErr error

	for i := range policy.maxAttempts {
		lastErr = attempt()
		if lastErr == nil {
			return nil
		}

		if !shouldRetry(lastErr) {
			return lastErr
		}

		if i == policy.maxAttempts-1 {
			break
		}

		select {
		case <-ctx.Done():
			//nolint:wrapcheck // context error is the operator-meaningful one when cancellation interrupts retry; preserving it lets the chart-template trace point at "context cancelled" rather than the last transport error.
			return ctx.Err()
		case <-time.After(policy.backoff(i)):
		}
	}

	return lastErr
}

// defaultShouldRetry is the pure class-based retry predicate: an
// error is retried iff its class is retryable. Convenient for
// callers that have no state-dependent shortcut (the test suite,
// future callers without partial-success semantics).
func defaultShouldRetry(err error) bool {
	return retryableLookupError(classifyLookupError(err))
}
