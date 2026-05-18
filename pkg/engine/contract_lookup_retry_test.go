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

// Contract: retryWithFailFast wraps a single `lookup` attempt with a
// bounded retry loop. Transient connectivity classes (refused,
// deadline) are retried up to policy.maxAttempts; deterministic
// classes (TLS handshake, authn, resource, unknown) return after the
// first attempt — the underlying condition won't clear on a second
// try and the operator should not pay the latency.
//
// Context cancellation interrupts the inter-attempt backoff
// immediately so Ctrl+C in `talm template / apply` is responsive.

package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hashicorp/go-multierror"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// testInstantPolicy disables backoff so unit tests don't wait. The
// retry-count contract is tested without sleeping; the actual backoff
// shape is exercised by TestRetryWithFailFast_BoundedTime.
//
//nolint:gochecknoglobals // test fixture, immutable
var testInstantPolicy = retryPolicy{
	maxAttempts: 3,
	backoff:     func(int) time.Duration { return 0 },
}

// grpcTestErr is a tiny test helper that returns a gRPC status error
// verbatim. wrapcheck normally insists on `errors.Wrap` around any
// error returned from an external package; for unit tests that need
// to inject a specific gRPC error shape into the function under test,
// wrapping would defeat the purpose. The one nolint here is paid
// once instead of at every retry-test call site.
//
//nolint:wrapcheck // test fixture: returning a raw gRPC error is the point
func grpcTestErr(c codes.Code, msg string) error {
	return status.Error(c, msg)
}

func TestRetryWithFailFast_NoRetryOnImmediateSuccess(t *testing.T) {
	calls := 0

	err := retryWithFailFast(t.Context(), func() error {
		calls++

		return nil
	}, defaultShouldRetry, testInstantPolicy)
	if err != nil {
		t.Fatalf("expected nil error; got %v", err)
	}

	if calls != 1 {
		t.Errorf("expected 1 attempt on immediate success; got %d", calls)
	}
}

func TestRetryWithFailFast_RetriesOnRefused_SucceedsSecondAttempt(t *testing.T) {
	calls := 0

	err := retryWithFailFast(t.Context(), func() error {
		calls++

		if calls == 1 {
			return grpcTestErr(codes.Unavailable, "connection refused")
		}

		return nil
	}, defaultShouldRetry, testInstantPolicy)
	if err != nil {
		t.Fatalf("expected nil error on retry success; got %v", err)
	}

	if calls != 2 {
		t.Errorf("expected 2 attempts (refused then ok); got %d", calls)
	}
}

func TestRetryWithFailFast_RetriesOnRefused_AllFail(t *testing.T) {
	calls := 0

	err := retryWithFailFast(t.Context(), func() error {
		calls++

		return grpcTestErr(codes.Unavailable, "connection refused")
	}, defaultShouldRetry, testInstantPolicy)
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}

	if calls != testInstantPolicy.maxAttempts {
		t.Errorf("expected %d attempts; got %d", testInstantPolicy.maxAttempts, calls)
	}
}

func TestRetryWithFailFast_RetriesOnDeadline(t *testing.T) {
	calls := 0

	err := retryWithFailFast(t.Context(), func() error {
		calls++

		if calls < testInstantPolicy.maxAttempts {
			return grpcTestErr(codes.DeadlineExceeded, "context deadline exceeded")
		}

		return nil
	}, defaultShouldRetry, testInstantPolicy)
	if err != nil {
		t.Fatalf("expected nil on retry success; got %v", err)
	}

	if calls != testInstantPolicy.maxAttempts {
		t.Errorf("expected %d attempts before success; got %d", testInstantPolicy.maxAttempts, calls)
	}
}

func TestRetryWithFailFast_FailsFastOnTLSHandshake(t *testing.T) {
	calls := 0
	err := retryWithFailFast(t.Context(), func() error {
		calls++

		return grpcTestErr(codes.Unavailable, "transport: authentication handshake failed: EOF")
	}, defaultShouldRetry, testInstantPolicy)
	if err == nil {
		t.Fatal("expected TLS error to bubble up")
	}

	if calls != 1 {
		t.Errorf("TLS handshake must fail fast (deterministic — cert SAN / maintenance — no retry); got %d attempts", calls)
	}
}

func TestRetryWithFailFast_FailsFastOnAuthn(t *testing.T) {
	calls := 0

	err := retryWithFailFast(t.Context(), func() error {
		calls++

		return grpcTestErr(codes.Unauthenticated, "credentials rejected")
	}, defaultShouldRetry, testInstantPolicy)
	if err == nil {
		t.Fatal("expected authn error to bubble up")
	}

	if calls != 1 {
		t.Errorf("authn must fail fast (bad credentials won't clear); got %d attempts", calls)
	}
}

func TestRetryWithFailFast_FailsFastOnResource(t *testing.T) {
	calls := 0

	err := retryWithFailFast(t.Context(), func() error {
		calls++

		return grpcTestErr(codes.Internal, "no such resource type")
	}, defaultShouldRetry, testInstantPolicy)
	if err == nil {
		t.Fatal("expected resource error to bubble up")
	}

	if calls != 1 {
		t.Errorf("resource-class must fail fast (chart bug, deterministic); got %d attempts", calls)
	}
}

func TestRetryWithFailFast_FailsFastOnUnknown(t *testing.T) {
	calls := 0

	err := retryWithFailFast(t.Context(), func() error {
		calls++

		return errTestNonGRPC
	}, defaultShouldRetry, testInstantPolicy)
	if err == nil {
		t.Fatal("expected unknown error to bubble up")
	}

	if calls != 1 {
		t.Errorf("unknown-class must fail fast (cannot assume transience); got %d attempts", calls)
	}
}

func TestRetryWithFailFast_HonoursContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())

	slowPolicy := retryPolicy{
		maxAttempts: 3,
		backoff:     func(int) time.Duration { return 10 * time.Second },
	}

	calls := 0

	go func() {
		// Give the first attempt time to enter the backoff sleep
		// before cancelling.
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	start := time.Now()

	err := retryWithFailFast(ctx, func() error {
		calls++

		return grpcTestErr(codes.Unavailable, "connection refused")
	}, defaultShouldRetry, slowPolicy)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error on cancellation")
	}

	// Backoff is 10s; we should be back well under that if cancellation
	// interrupted the sleep correctly.
	if elapsed > 2*time.Second {
		t.Errorf("cancellation did not interrupt backoff; elapsed=%v", elapsed)
	}

	if calls < 1 || calls > 2 {
		t.Errorf("expected 1-2 attempts before cancellation; got %d", calls)
	}
}

func TestRetryWithFailFast_BoundedTime(t *testing.T) {
	policy := retryPolicy{
		maxAttempts: 3,
		backoff:     interAttemptBackoff,
	}

	calls := 0
	start := time.Now()

	err := retryWithFailFast(t.Context(), func() error {
		calls++

		return grpcTestErr(codes.Unavailable, "connection refused")
	}, defaultShouldRetry, policy)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}

	if calls != policy.maxAttempts {
		t.Errorf("expected %d attempts; got %d", policy.maxAttempts, calls)
	}

	// Production backoff: 200ms · 2^n between attempts 0→1 and 1→2.
	// 200ms + 400ms = 600ms; allow up to 2s for scheduling slack.
	if elapsed > 2*time.Second {
		t.Errorf("production retry budget exceeded 2s; elapsed=%v", elapsed)
	}

	if elapsed < 500*time.Millisecond {
		t.Errorf("production retry budget too short — backoff not actually waiting; elapsed=%v", elapsed)
	}
}

// Contract: firstLookupError surfaces per-node multierror failures
// to the retry classifier, which closes the gap that would otherwise
// leave the "brief partition against one node in a multi-node
// lookup" case unretried. helpers.ForEachResource itself returns nil
// when only callback (per-node) errors occurred; pre-fix the
// closure returned that nil, retryWithFailFast exited successfully,
// and the multiErr was wrapped+hinted without a second attempt.
func TestFirstLookupError_PrefersHelperErrOverMultiErr(t *testing.T) {
	helperErr := grpcTestErr(codes.Unavailable, "transport: handshake failed")
	mErr := &multierror.Error{}
	mErr = multierror.Append(mErr, grpcTestErr(codes.Unavailable, "connection refused"))

	got := firstLookupError(helperErr, mErr)
	if !errors.Is(got, helperErr) {
		t.Errorf("expected helperErr to win over multiErr; got %v", got)
	}
}

func TestFirstLookupError_SurfacesMultiErrWhenHelperErrNil(t *testing.T) {
	mErr := &multierror.Error{}
	mErr = multierror.Append(mErr, grpcTestErr(codes.Unavailable, "connection refused"))

	got := firstLookupError(nil, mErr)
	if got == nil {
		t.Fatal("multiErr-only failure must surface (so retry can see it); got nil")
	}

	if classifyLookupError(got) != lookupErrRefused {
		t.Errorf("classifier must see Refused class through multierror wrap; got class %v", classifyLookupError(got))
	}
}

func TestFirstLookupError_NilBoth(t *testing.T) {
	if got := firstLookupError(nil, nil); got != nil {
		t.Errorf("nil/nil must return nil; got %v", got)
	}
}

func TestFirstLookupError_NilMultiErr(t *testing.T) {
	if got := firstLookupError(nil, (*multierror.Error)(nil)); got != nil {
		t.Errorf("nil/nil-multierror must return nil; got %v", got)
	}
}

// Contract: when multiErr has accumulated errors from multiple nodes
// with DIFFERENT classes (e.g. node A: x509 / authn; node B: refused),
// firstLookupError returns the LAST appended error rather than the
// multierror envelope. classifyLookupError downstream substring-
// matches err.Error(); if we returned the multierror, the x509 from
// node A would mis-flip the whole batch to lookupErrAuthn and node
// B's refused (the more relevant signal — latest outcome) would be
// masked.
func TestFirstLookupError_PicksLastAppended_NotMultierror(t *testing.T) {
	mErr := &multierror.Error{}
	mErr = multierror.Append(mErr, grpcTestErr(codes.Unavailable, "x509: certificate signed by unknown authority"))
	mErr = multierror.Append(mErr, grpcTestErr(codes.Unavailable, "connection refused"))

	got := firstLookupError(nil, mErr)
	if got == nil {
		t.Fatal("expected non-nil error from multiErr; got nil")
	}

	if classifyLookupError(got) != lookupErrRefused {
		t.Errorf("classifier must see the LAST error (refused) — not whole multierror concat which would match x509 first; got class %v", classifyLookupError(got))
	}
}

// Contract: a multierror-only refused failure on attempt 1 triggers
// a second attempt (proving the retry path sees per-node errors).
// The pre-fix closure returned only helperErr (nil here) so retry
// exited on attempt 1 and the operator never got the retry budget
// for the dominant transient class.
func TestRetryWithFailFast_RetriesOnMultiErrOnly(t *testing.T) {
	calls := 0

	err := retryWithFailFast(t.Context(), func() error {
		calls++

		if calls == 1 {
			mErr := &multierror.Error{}
			mErr = multierror.Append(mErr, grpcTestErr(codes.Unavailable, "connection refused"))

			return firstLookupError(nil, mErr)
		}

		return nil
	}, defaultShouldRetry, testInstantPolicy)
	if err != nil {
		t.Fatalf("expected nil after retry success; got %v", err)
	}

	if calls != 2 {
		t.Errorf("expected 2 attempts (multierror refused then ok); got %d", calls)
	}
}

func TestRetryableLookupError(t *testing.T) {
	cases := []struct {
		class lookupErrorClass
		want  bool
	}{
		{lookupErrRefused, true},
		{lookupErrDeadline, true},
		{lookupErrTLSHandshake, false},
		{lookupErrAuthn, false},
		{lookupErrResource, false},
		{lookupErrUnknown, false},
	}

	for _, tc := range cases {
		if got := retryableLookupError(tc.class); got != tc.want {
			t.Errorf("retryableLookupError(%v): got %v, want %v", tc.class, got, tc.want)
		}
	}
}

// Contract: when the closure-derived `shouldRetry` reports false
// because partial data has been collected, retry stops on attempt 1
// even with a connection-refused error. Mirrors the stale-modeline
// case in newLookupFunction: one node responds with data, another
// is persistently down. Retry would discard the good data and pay
// up to 600ms while reaching the same final verdict — fast-path
// avoids both costs.
func TestRetryWithFailFast_PartialSuccessFastPath(t *testing.T) {
	calls := 0
	partialSuccessReached := false

	shouldRetry := func(err error) bool {
		if partialSuccessReached {
			return false
		}

		return retryableLookupError(classifyLookupError(err))
	}

	err := retryWithFailFast(t.Context(), func() error {
		calls++
		partialSuccessReached = true

		return grpcTestErr(codes.Unavailable, "connection refused")
	}, shouldRetry, testInstantPolicy)
	if err == nil {
		t.Fatal("expected error to bubble up; partial-success fast-path returns the error, not nil")
	}

	if calls != 1 {
		t.Errorf("partial-success closure must short-circuit retry on attempt 1; got %d attempts", calls)
	}
}

// Contract: a future class added to the iota set without being
// threaded into retryableLookupError's switch MUST panic at first
// reach, not silently fall to "not retryable". Pinned via a
// synthetic class value past the known range; the panic recovery
// proves the default branch fires loudly so the regression is caught
// in CI rather than producing a silent UX degradation.
func TestRetryableLookupError_UnknownClassPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on unhandled class; got none")
		}
	}()

	// Synthetic class value beyond the iota set — emulates a future
	// class added to the type without switch update.
	_ = retryableLookupError(lookupErrorClass(999))
}
