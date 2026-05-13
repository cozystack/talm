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
	"strings"
	"testing"

	"github.com/cockroachdb/errors"
)

// TestResolveUpgradeImageFromValues_ReadsImage pins the canonical
// path for #176: `talm upgrade` reads its target installer image
// from values.yaml (the source of truth for cluster-wide knobs),
// not from the rendered node body. The previous implementation
// called engine.FullConfigProcess on the node body and extracted
// machine.install.image from the result — silently stuck on the
// LAST templated image, even after the operator bumped
// values.yaml::image to upgrade the cluster.
func TestResolveUpgradeImageFromValues_ReadsImage(t *testing.T) {
	dir := t.TempDir()

	values := "image: ghcr.io/siderolabs/installer:v1.13.0\n" +
		"endpoint: https://192.0.2.10:6443\n"
	if err := os.WriteFile(filepath.Join(dir, "values.yaml"), []byte(values), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := resolveUpgradeImageFromValues(dir)
	if err != nil {
		t.Fatalf("resolveUpgradeImageFromValues: %v", err)
	}

	want := "ghcr.io/siderolabs/installer:v1.13.0"
	if got != want {
		t.Errorf("upgrade image must come from values.yaml; got %q want %q", got, want)
	}
}

// TestResolveUpgradeImageFromValues_MissingImage_ErrorWithHint
// pins the operator-facing failure mode: when values.yaml exists
// but has no `image` key, the error must name the file AND point
// at the recovery path (set the key OR pass --image explicitly).
// Without the hint, the operator sees a bare "image not set" and
// has to grep the codebase to find where to fix it.
func TestResolveUpgradeImageFromValues_MissingImage_ErrorWithHint(t *testing.T) {
	dir := t.TempDir()

	values := "endpoint: https://192.0.2.10:6443\n"
	if err := os.WriteFile(filepath.Join(dir, "values.yaml"), []byte(values), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := resolveUpgradeImageFromValues(dir)
	if err == nil {
		t.Fatal("expected error when values.yaml lacks `image`; got nil")
	}

	if !strings.Contains(err.Error(), "image") {
		t.Errorf("error must name the missing field; got: %v", err)
	}

	if !strings.Contains(err.Error(), "values.yaml") {
		t.Errorf("error must name values.yaml so the operator locates the file to edit; got: %v", err)
	}

	hints := errors.GetAllHints(err)
	if len(hints) == 0 {
		t.Fatalf("expected at least one hint guiding the operator; got bare error: %v", err)
	}

	combined := strings.Join(hints, "\n")
	if !strings.Contains(combined, "--image") {
		t.Errorf("hint chain must mention the --image escape hatch; got: %s", combined)
	}
}

// TestResolveUpgradeImageFromValues_MissingValuesYAML_Error pins
// the missing-file error path. The operator-facing error must
// surface the path so they can locate the project root mismatch.
func TestResolveUpgradeImageFromValues_MissingValuesYAML_Error(t *testing.T) {
	dir := t.TempDir() // no values.yaml inside

	_, err := resolveUpgradeImageFromValues(dir)
	if err == nil {
		t.Fatal("expected error when values.yaml is missing; got nil")
	}

	if !strings.Contains(err.Error(), "values.yaml") {
		t.Errorf("error must name the missing values.yaml file; got: %v", err)
	}
}

// TestResolveUpgradeImageFromValues_EmptyImage_ErrorSameAsMissing
// pins the empty-string case: values.yaml with `image: ""` is
// indistinguishable from missing image — both must error out
// rather than silently passing an empty string downstream to
// talosctl, where the failure mode is opaque. Beyond non-nil, the
// error/hint shape must match the MissingImage path so operators
// get one consistent recovery story regardless of which precise
// shape (key absent vs. key empty) their values.yaml has.
func TestResolveUpgradeImageFromValues_EmptyImage_ErrorSameAsMissing(t *testing.T) {
	dir := t.TempDir()

	values := "image: \"\"\nendpoint: https://192.0.2.10:6443\n"
	if err := os.WriteFile(filepath.Join(dir, "values.yaml"), []byte(values), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := resolveUpgradeImageFromValues(dir)
	if err == nil {
		t.Fatal("expected error when values.yaml has empty image; got nil")
	}

	if !strings.Contains(err.Error(), "image") || !strings.Contains(err.Error(), "values.yaml") {
		t.Errorf("empty image must match missing-image error shape (names both `image` and `values.yaml`); got: %v", err)
	}

	hints := errors.GetAllHints(err)
	if len(hints) == 0 {
		t.Fatalf("expected at least one hint for the empty-image case; got bare error: %v", err)
	}

	if !strings.Contains(strings.Join(hints, "\n"), "--image") {
		t.Errorf("empty-image hint chain must mention the --image escape hatch (same as missing-image); got: %v", hints)
	}
}
