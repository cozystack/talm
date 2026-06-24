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

// Contract: the hard case for secret sealing — a secret nested inside a list
// element. machine.pods[] has no patch merge key, so apply matches its
// elements by whole-element deep-equal. A node-file body carrying a partial
// element (secret stripped) would NOT match the rendered element and would be
// appended as a spurious duplicate. OmitSecretValues therefore drops the whole
// element; the round-trip through MergeFileAsPatch must then leave the real
// rendered pod intact and un-duplicated.

package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// configWithPodSecret carries a secret two levels deep inside a machine.pods[]
// element (spec.containers[].env[].value).
const configWithPodSecret = `version: v1alpha1
machine:
  type: controlplane
  install:
    disk: /dev/sda
  pods:
    - apiVersion: v1
      kind: Pod
      metadata:
        name: kms-plugin
      spec:
        containers:
          - name: kms
            env:
              - name: SECRET_ID
                value: vault-secret-id
cluster:
  controlPlane:
    endpoint: https://10.0.0.10:6443
`

// TestContract_OmitSecretValues_DropsWholeListElement pins C2: a secret nested
// anywhere inside a sequence element drops the entire element, and a sequence
// emptied by that removal has its key dropped too (so an empty-list patch
// cannot clobber the rendered list).
func TestContract_OmitSecretValues_DropsWholeListElement(t *testing.T) {
	out, err := OmitSecretValues([]byte(configWithPodSecret), secretSet("vault-secret-id"))
	if err != nil {
		t.Fatalf("OmitSecretValues: %v", err)
	}

	got := string(out)
	for _, leak := range []string{"vault-secret-id", "SECRET_ID", "kms-plugin"} {
		if strings.Contains(got, leak) {
			t.Errorf("the whole secret-bearing pod element must be omitted, found %q in:\n%s", leak, got)
		}
	}
	if strings.Contains(got, "pods:") {
		t.Errorf("a sequence emptied by secret removal must have its key dropped (no empty-list patch):\n%s", got)
	}
	// A non-secret sibling field must survive so the body is still a valid patch.
	if !strings.Contains(got, "type: controlplane") {
		t.Errorf("non-secret fields must survive:\n%s", got)
	}
}

// TestContract_SealOmitThenMerge_RealSecretSurvivesNoDuplicate is the headline
// correctness test: it reproduces the apply pipeline. The rendered config holds
// the REAL secret; the node-file body is the sealed (omitted) form. Merging the
// body as a patch on top of the render must yield the real secret exactly once
// — proving the omitted body neither clobbers nor duplicates the rendered pod.
func TestContract_SealOmitThenMerge_RealSecretSurvivesNoDuplicate(t *testing.T) {
	rendered := []byte(configWithPodSecret)

	sealedBody, err := OmitSecretValues(rendered, secretSet("vault-secret-id"))
	if err != nil {
		t.Fatalf("OmitSecretValues: %v", err)
	}

	dir := t.TempDir()
	bodyFile := filepath.Join(dir, "node0.yaml")
	// Prepend a modeline so the file is a well-formed talm node file; the
	// body below it is what gets merged as a patch.
	bodyWithModeline := "# talm: nodes=[\"10.0.0.1\"], templates=[\"templates/controlplane.yaml\"]\n" + string(sealedBody)
	if err := os.WriteFile(bodyFile, []byte(bodyWithModeline), 0o644); err != nil {
		t.Fatalf("write body: %v", err)
	}

	merged, err := MergeFileAsPatch(rendered, bodyFile)
	if err != nil {
		t.Fatalf("MergeFileAsPatch: %v", err)
	}

	out := string(merged)
	if !strings.Contains(out, "vault-secret-id") {
		t.Errorf("real secret must be present after apply re-render + merge:\n%s", out)
	}
	if n := strings.Count(out, "kms-plugin"); n != 1 {
		t.Errorf("pod must appear exactly once (no append-duplicate), got %d:\n%s", n, out)
	}
	if n := strings.Count(out, "vault-secret-id"); n != 1 {
		t.Errorf("secret env must appear exactly once, got %d:\n%s", n, out)
	}
}

// configWithReplaceListSecret puts a secret as one element of a multi-element
// cluster.network.podSubnets list. podSubnets is a configpatcher
// replace-semantic path, so a partial body list would OVERWRITE (not merge
// into) the rendered list — silently dropping the secret element from the
// applied config. The non-secret serviceSubnets sibling exercises that a
// sibling replace-list key is left untouched.
const configWithReplaceListSecret = `version: v1alpha1
machine:
  type: controlplane
  install:
    disk: /dev/sda
cluster:
  controlPlane:
    endpoint: https://10.0.0.10:6443
  network:
    podSubnets:
      - 10.244.0.0/16
      - 10.99.0.0/16
    serviceSubnets:
      - 10.96.0.0/12
`

// TestContract_SealOmitThenMerge_ReplaceListSecretSurvives is the regression
// guard for the merge:"replace" path: a secret in a multi-element replace list
// must not produce a partial body list (which would clobber the rendered list
// on apply). OmitSecretValues drops the whole key; the round-trip then leaves
// both the secret element AND its non-secret sibling intact, re-rendered from
// the chart at apply.
func TestContract_SealOmitThenMerge_ReplaceListSecretSurvives(t *testing.T) {
	rendered := []byte(configWithReplaceListSecret)

	sealedBody, err := OmitSecretValues(rendered, secretSet("10.99.0.0/16"))
	if err != nil {
		t.Fatalf("OmitSecretValues: %v", err)
	}

	// The sealed body must not carry a partial podSubnets list (the whole key
	// is dropped); leaving "10.244.0.0/16" in a body podSubnets would clobber
	// the rendered list under replace semantics.
	if strings.Contains(string(sealedBody), "podSubnets") {
		t.Errorf("a replace-list with a secret must have its whole key dropped, not a partial list:\n%s", sealedBody)
	}

	dir := t.TempDir()
	bodyFile := filepath.Join(dir, "node0.yaml")
	bodyWithModeline := "# talm: nodes=[\"10.0.0.1\"], templates=[\"templates/controlplane.yaml\"]\n" + string(sealedBody)
	if err := os.WriteFile(bodyFile, []byte(bodyWithModeline), 0o644); err != nil {
		t.Fatalf("write body: %v", err)
	}

	merged, err := MergeFileAsPatch(rendered, bodyFile)
	if err != nil {
		t.Fatalf("MergeFileAsPatch: %v", err)
	}

	out := string(merged)
	if !strings.Contains(out, "10.99.0.0/16") {
		t.Errorf("the secret subnet must survive the replace merge (re-rendered at apply):\n%s", out)
	}
	if !strings.Contains(out, "10.244.0.0/16") {
		t.Errorf("the non-secret sibling subnet must survive too:\n%s", out)
	}
}

// configWithSoleSecretMapKey has a secret as the ONLY key under a parent map
// (machine.registries.config.r\.example.auth.password). Omitting it empties
// the auth map. The seal layer leaves an empty map (not a dropped key); this
// pins the load-bearing claim that an empty-map patch merges as a no-op, so
// the rendered secret survives — the map analogue of the emptied-sequence
// case.
const configWithSoleSecretMapKey = `version: v1alpha1
machine:
  type: controlplane
  install:
    disk: /dev/sda
  registries:
    config:
      r.example:
        auth:
          password: topsecret
cluster:
  controlPlane:
    endpoint: https://10.0.0.10:6443
`

// TestContract_SealOmitThenMerge_EmptiedMapSecretSurvives is the round-trip
// guard for the empty-map branch: when a secret is the only key under its
// parent map, OmitSecretValues leaves an empty map in the body, and merging
// that empty map as a patch must leave the rendered secret authoritative
// (no clobber).
func TestContract_SealOmitThenMerge_EmptiedMapSecretSurvives(t *testing.T) {
	rendered := []byte(configWithSoleSecretMapKey)

	sealedBody, err := OmitSecretValues(rendered, secretSet("topsecret"))
	if err != nil {
		t.Fatalf("OmitSecretValues: %v", err)
	}
	if strings.Contains(string(sealedBody), "topsecret") {
		t.Errorf("the sole secret key must be dropped from the body:\n%s", sealedBody)
	}

	dir := t.TempDir()
	bodyFile := filepath.Join(dir, "node0.yaml")
	bodyWithModeline := "# talm: nodes=[\"10.0.0.1\"], templates=[\"templates/controlplane.yaml\"]\n" + string(sealedBody)
	if err := os.WriteFile(bodyFile, []byte(bodyWithModeline), 0o644); err != nil {
		t.Fatalf("write body: %v", err)
	}

	merged, err := MergeFileAsPatch(rendered, bodyFile)
	if err != nil {
		t.Fatalf("MergeFileAsPatch: %v", err)
	}

	if !strings.Contains(string(merged), "topsecret") {
		t.Errorf("the rendered secret must survive an empty-map patch merge:\n%s", merged)
	}
}
