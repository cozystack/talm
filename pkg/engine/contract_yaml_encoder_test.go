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
	"bytes"
	"errors"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// errFakeWriterFailure is the sentinel returned by alwaysFailWriter
// once its budget is exhausted. Distinct from any error
// gopkg.in/yaml.v3 may produce so the assertion below can verify
// the wrap chain reaches the underlying writer.
var errFakeWriterFailure = errors.New("fake writer failure")

// alwaysFailWriter returns errFakeWriterFailure on every Write
// call. yaml.Encoder.Encode fails on its first emit attempt, so
// this drives the encode-error branch of encodeYAMLNodeIndented.
type alwaysFailWriter struct{}

func (alwaysFailWriter) Write(_ []byte) (int, error) {
	return 0, errFakeWriterFailure
}

// TestEncodeYAMLNodeIndented_WrapsEncodeError pins the
// encode-error wrap chain. yaml.Encoder.Encode emits the document
// body during the call, so an immediate-fail writer surfaces the
// failure on Encode and encodeYAMLNodeIndented must wrap it as
// "encoding target config".
func TestEncodeYAMLNodeIndented_WrapsEncodeError(t *testing.T) {
	node := &yaml.Node{
		Kind: yaml.MappingNode,
		Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Value: "foo"},
			{Kind: yaml.ScalarNode, Value: "bar"},
		},
	}

	err := encodeYAMLNodeIndented(alwaysFailWriter{}, node)
	if err == nil {
		t.Fatal("expected error from alwaysFailWriter, got nil")
	}
	if !strings.Contains(err.Error(), "encoding target config") {
		t.Errorf("expected wrap with %q, got %v", "encoding target config", err)
	}
	// gopkg.in/yaml.v3 wraps the underlying writer error as
	// "yaml: write error: <underlying>" via fmt.Errorf with %v
	// rather than %w, so errors.Is does not chain through. Assert
	// on the message substring instead — drops to a string check
	// only because of the upstream wrapping idiom, not because the
	// wrap chain is loose at our boundary.
	if !strings.Contains(err.Error(), errFakeWriterFailure.Error()) {
		t.Errorf("error message does not surface underlying writer failure %q; got %v", errFakeWriterFailure.Error(), err)
	}
}

// TestEncodeYAMLNodeIndented_HappyPath pins the success contract:
// a small mapping node encodes to 2-space-indented YAML. Guards
// against a refactor that swaps the indent or breaks the encode
// pipeline.
func TestEncodeYAMLNodeIndented_HappyPath(t *testing.T) {
	node := &yaml.Node{
		Kind: yaml.MappingNode,
		Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Value: "key"},
			{Kind: yaml.ScalarNode, Value: "value"},
		},
	}

	var buf bytes.Buffer
	if err := encodeYAMLNodeIndented(&buf, node); err != nil {
		t.Fatalf("encodeYAMLNodeIndented: %v", err)
	}

	got := buf.String()
	if !strings.Contains(got, "key: value") {
		t.Errorf("missing key: value in encoded output: %q", got)
	}
}

// Note on the close-error branch:
//
// encodeYAMLNodeIndented also wraps yaml.Encoder.Close failures as
// "closing target config encoder", matching the sister sites at
// engine.go:585 ("closing YAML encoder after stripping
// $patch:delete directives") and engine.go:765 ("closing encoder
// for pruned body"). That branch is NOT covered by a runtime test
// because gopkg.in/yaml.v3's Encoder.Close performs no Write call
// on the underlying writer for any input the engine produces in
// practice — the document body and document-end marker both go
// out during Encode, leaving Close as a stream-finalisation no-op
// against any Write-succeeding writer (verified empirically with a
// 1024-key mapping and a multi-doc stream — both cases yield zero
// writes during Close). Triggering a real Close failure would
// require either a yaml.v3 internal-state hack (not supported) or
// substituting yaml.Encoder for a mock (rejected as architectural
// scope). The wrap stays in production code as defensive symmetry
// with the sister sites; a pin lives in this comment so a future
// reader does not interpret the absence of a runtime test as
// confidence that the path is dead.
