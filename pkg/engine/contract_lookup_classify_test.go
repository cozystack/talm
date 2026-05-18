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

// Contract: classifyLookupError partitions gRPC failures from the
// talos client into one of six diagnosable classes. Each class drives
// a distinct human-facing hint downstream; misclassification leads the
// operator to a useless remedy ("pass --offline" is dangerous on a
// real chart bug, "verify cert SANs" is irrelevant on a deadline).

package engine

import (
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestClassifyLookupError_TLSHandshake(t *testing.T) {
	err := status.Error(codes.Unavailable, "connection error: desc = \"transport: authentication handshake failed: EOF\"")
	if got := classifyLookupError(err); got != lookupErrTLSHandshake {
		t.Errorf("got %v, want lookupErrTLSHandshake", got)
	}
}

func TestClassifyLookupError_TLSHandshake_TLSPrefix(t *testing.T) {
	err := status.Error(codes.Unavailable, "connection error: desc = \"tls: bad certificate\"")
	if got := classifyLookupError(err); got != lookupErrTLSHandshake {
		t.Errorf("got %v, want lookupErrTLSHandshake", got)
	}
}

func TestClassifyLookupError_Refused(t *testing.T) {
	err := status.Error(codes.Unavailable, "connection error: desc = \"transport: Error while dialing dial tcp 192.0.2.10:50000: connect: connection refused\"")
	if got := classifyLookupError(err); got != lookupErrRefused {
		t.Errorf("got %v, want lookupErrRefused", got)
	}
}

func TestClassifyLookupError_Refused_NoRouteToHost(t *testing.T) {
	err := status.Error(codes.Unavailable, "no route to host")
	if got := classifyLookupError(err); got != lookupErrRefused {
		t.Errorf("got %v, want lookupErrRefused", got)
	}
}

func TestClassifyLookupError_DeadlineExceeded(t *testing.T) {
	err := status.Error(codes.DeadlineExceeded, "context deadline exceeded")
	if got := classifyLookupError(err); got != lookupErrDeadline {
		t.Errorf("got %v, want lookupErrDeadline", got)
	}
}

func TestClassifyLookupError_Authn(t *testing.T) {
	err := status.Error(codes.Unauthenticated, "talosconfig credentials rejected")
	if got := classifyLookupError(err); got != lookupErrAuthn {
		t.Errorf("got %v, want lookupErrAuthn", got)
	}
}

func TestClassifyLookupError_Authn_UnknownAuthority(t *testing.T) {
	err := status.Error(codes.Unavailable, "x509: certificate signed by unknown authority")
	if got := classifyLookupError(err); got != lookupErrAuthn {
		t.Errorf("got %v, want lookupErrAuthn", got)
	}
}

func TestClassifyLookupError_Resource_InternalCode(t *testing.T) {
	err := status.Error(codes.Internal, "no such resource type")
	if got := classifyLookupError(err); got != lookupErrResource {
		t.Errorf("got %v, want lookupErrResource", got)
	}
}

func TestClassifyLookupError_Resource_InvalidArgument(t *testing.T) {
	err := status.Error(codes.InvalidArgument, "invalid resource selector")
	if got := classifyLookupError(err); got != lookupErrResource {
		t.Errorf("got %v, want lookupErrResource", got)
	}
}

// Contract: NotFound from the helpers.ForEachResource return value
// (ResolveResourceKind couldn't find the kind in the target Talos
// version) classifies as Resource, NOT Unknown. The per-node
// callback filters NotFound for missing instances, so any NotFound
// surfacing here is definitionally a chart bug / version mismatch
// signal — the operator needs the "verify with talosctl get" /
// "don't try --offline" hint, not the generic "file an issue" one.
func TestClassifyLookupError_Resource_NotFound_FromResolveResourceKind(t *testing.T) {
	err := status.Error(codes.NotFound, "resource type \"nonexistent\" not registered")
	if got := classifyLookupError(err); got != lookupErrResource {
		t.Errorf("NotFound at the helper level (ResolveResourceKind miss) must classify as Resource; got %v", got)
	}
}

func TestClassifyLookupError_Resource_Unimplemented(t *testing.T) {
	err := status.Error(codes.Unimplemented, "method not implemented in this Talos version")
	if got := classifyLookupError(err); got != lookupErrResource {
		t.Errorf("Unimplemented (older Talos lacking the resource API) must classify as Resource; got %v", got)
	}
}

func TestClassifyLookupError_Unknown_PlainError(t *testing.T) {
	if got := classifyLookupError(errTestNonGRPC); got != lookupErrUnknown {
		t.Errorf("got %v, want lookupErrUnknown", got)
	}
}

// Contract: a bare Unavailable with no diagnostic substring falls
// back to lookupErrRefused — the dominant real-world cause of plain
// Unavailable from gRPC dial. The TLS-handshake and connection-refused
// substrings are the precise discriminators; absence of both means
// "we know the channel isn't up but can't pinpoint why," and
// "verify network path / firewall / port" is a strictly more useful
// remedy than the generic Unknown fallback.
func TestClassifyLookupError_Unavailable_GenericText(t *testing.T) {
	err := status.Error(codes.Unavailable, "something went wrong")
	if got := classifyLookupError(err); got != lookupErrRefused {
		t.Errorf("got %v, want lookupErrRefused (Unavailable default)", got)
	}
}

func TestClassifyLookupError_Nil(t *testing.T) {
	if got := classifyLookupError(nil); got != lookupErrUnknown {
		t.Errorf("got %v, want lookupErrUnknown for nil input", got)
	}
}
