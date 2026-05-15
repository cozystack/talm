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
	"fmt"
	"io"
	"os"

	"github.com/cockroachdb/errors"
	"gopkg.in/yaml.v3"
)

// nodeBodyYAMLIndent pins the YAML indent the writeback emits at.
// talm-rendered node bodies use 2-space indent throughout (see the
// `nindent 2/4/6` ladder in charts/cozystack/templates/_helpers.tpl).
// yaml.v3's default Marshal uses 4-space indent, which would
// re-indent every line under `machine:` on round-trip and flood
// `git diff` with cosmetic noise on every upgrade. Pinning to 2 keeps
// the writeback diff to the single image scalar line.
const nodeBodyYAMLIndent = 2

// writeBackInstallImageToNodeBody patches machine.install.image inside
// the YAML at filePath, replacing whatever value sits there with newImage.
// Preserves the file's comments, indentation, AND multi-document shape
// via yaml.v3 streaming round-trip; the encoder is pinned to
// nodeBodyYAMLIndent (2) so a follow-up `git diff` shows only the
// single image scalar line.
//
// Multi-document support is load-bearing for the cozystack v1.12+
// render path: talos.config.multidoc emits machine.config + cluster
// alongside RegistryMirrorConfig, LinkConfig, Layer2VIPConfig and
// VLANConfig documents separated by `---`. A naive yaml.Unmarshal +
// yaml.Marshal would drop every document after the first one,
// silently erasing the registry / network config from the body file
// on disk. Read with a streaming decoder, patch the first document
// that carries machine.install.image, re-emit all documents in order.
//
// Returns (true, nil) on a successful patch, (false, nil) when no
// document has machine.install.image OR the located document is
// already on newImage (both shapes are silent-skip — the caller
// distinguishes the two via the stderr line written here, but the
// return shape is uniform). Returns an error only when the file is
// unreadable, unparseable, the YAML structure conflicts with the
// expected shape (e.g. machine: is a scalar), or the write-back
// itself fails.
//
// Called by talm upgrade after a successful upgrade RPC + post-upgrade
// verify so the cluster's nodes/*.yaml stays consistent with the image
// the node now runs.
func writeBackInstallImageToNodeBody(filePath, newImage string) (bool, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return false, errors.Wrapf(err, "reading node body %s", filePath)
	}

	docs, err := decodeAllYAMLDocs(data)
	if err != nil {
		return false, errors.Wrapf(err, "parsing node body %s", filePath)
	}

	imageNode, found, err := locateInstallImageNode(docs)
	if err != nil {
		return false, errors.Wrapf(err, "locating machine.install.image in %s", filePath)
	}

	if !found {
		// No document carries the key — orphan / side-patch /
		// truncated file. Surface the skip explicitly so an operator
		// who passed a file by mistake sees the no-op.
		fmt.Fprintf(os.Stderr, "Skipped %s: no machine.install.image key (orphan or side-patch shape)\n", filePath)

		return false, nil
	}

	if imageNode.Value == newImage {
		// Already on the target image. Skip the disk write so file
		// mtime stays stable.
		return false, nil
	}

	imageNode.Value = newImage

	out, err := encodeAllYAMLDocs(docs)
	if err != nil {
		return false, errors.Wrapf(err, "re-marshalling node body %s", filePath)
	}

	// Resolve the file's mode bits. os.WriteFile applies its mode
	// argument ONLY when it creates the file; on the truncate-and-
	// rewrite path (the common case here — the file already exists)
	// the OS preserves the existing permissions regardless of what
	// we pass. So this Stat-and-pass dance covers the rare case
	// where the file was deleted between the read above and the
	// write below: a fresh file would otherwise pick up the process
	// umask. presetFileMode (0o644) is the fallback when Stat
	// itself fails (the node body would be unreadable in that case,
	// so further hardening would serve no purpose).
	mode := presetFileMode
	if info, err := os.Stat(filePath); err == nil {
		mode = info.Mode().Perm()
	}

	//nolint:gosec // filePath is an operator-supplied -f argument; we must write to exactly that path
	if err := os.WriteFile(filePath, out, mode); err != nil {
		return false, errors.Wrapf(err, "writing back node body %s", filePath)
	}

	return true, nil
}

// decodeAllYAMLDocs streams every document in the buffer through
// yaml.NewDecoder. yaml.Unmarshal reads only the first document, so
// using it would silently truncate multi-doc inputs — exactly the
// shape the cozystack v1.12+ render emits. Returns the documents in
// source order; an empty input returns an empty slice.
func decodeAllYAMLDocs(data []byte) ([]*yaml.Node, error) {
	var docs []*yaml.Node

	dec := yaml.NewDecoder(bytes.NewReader(data))

	for {
		var doc yaml.Node
		if err := dec.Decode(&doc); err != nil {
			if errors.Is(err, io.EOF) {
				return docs, nil
			}

			//nolint:wrapcheck // caller wraps with the file path
			return nil, err
		}

		docs = append(docs, &doc)
	}
}

// locateInstallImageNode scans docs in order and returns the first
// scalar node behind machine.install.image. Returns (node, true, nil)
// when found, (nil, false, nil) when no document has the key (the
// silent-skip path), and an error when a document's structural shape
// conflicts with the expected mapping chain (e.g. machine.install is
// a scalar).
func locateInstallImageNode(docs []*yaml.Node) (*yaml.Node, bool, error) {
	for _, doc := range docs {
		node, found, err := findMachineInstallImageNode(doc)
		if err != nil {
			return nil, false, err
		}

		if found {
			return node, true, nil
		}
	}

	return nil, false, nil
}

// encodeAllYAMLDocs re-emits docs through a single yaml.NewEncoder
// pinned to nodeBodyYAMLIndent. The encoder writes `---\n` between
// successive Encode calls, reproducing the source's multi-doc shape.
func encodeAllYAMLDocs(docs []*yaml.Node) ([]byte, error) {
	var buf bytes.Buffer

	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(nodeBodyYAMLIndent)

	for _, doc := range docs {
		if err := enc.Encode(doc); err != nil {
			_ = enc.Close()

			//nolint:wrapcheck // caller wraps with the file path
			return nil, err
		}
	}

	if err := enc.Close(); err != nil {
		//nolint:wrapcheck // caller wraps with the file path
		return nil, err
	}

	return buf.Bytes(), nil
}

// findMachineInstallImageNode walks a parsed YAML document looking
// for the scalar node behind .machine.install.image. Returns
// (node, true, nil) when found, (nil, false, nil) when any link in
// the chain is missing, and an error when the chain exists but the
// node shape is wrong (e.g. machine.install is a scalar).
//
// An empty document (zero content) is treated as "key absent" and
// silent-skipped. The caller logs the skip via a stderr line so an
// operator who passed a truncated body sees the no-op explicitly
// instead of wondering why the file was left alone.
func findMachineInstallImageNode(doc *yaml.Node) (*yaml.Node, bool, error) {
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil, false, nil
	}

	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, false, nil
	}

	machine, ok, err := childByKey(root, "machine")
	if err != nil || !ok {
		return nil, false, err
	}

	install, ok, err := childByKey(machine, "install")
	if err != nil || !ok {
		return nil, false, err
	}

	image, ok, err := childByKey(install, "image")
	if err != nil || !ok {
		return nil, false, err
	}

	if image.Kind != yaml.ScalarNode {
		//nolint:wrapcheck // sentinel constructed in-place; caller wraps with the file path
		return nil, false, errors.Newf("expected scalar at machine.install.image, got %s", yamlKindName(image.Kind))
	}

	return image, true, nil
}

// yamlKindName renders a yaml.Kind constant as the human-readable
// node-shape name used in Talos / YAML docs. The yaml.v3 constants
// are an iota enum whose numeric values mean nothing to an operator
// reading a structural error — "got kind 4" is opaque, "got mapping"
// is actionable.
func yamlKindName(kind yaml.Kind) string {
	switch kind {
	case yaml.DocumentNode:
		return "document"
	case yaml.SequenceNode:
		return "sequence"
	case yaml.MappingNode:
		return "mapping"
	case yaml.ScalarNode:
		return "scalar"
	case yaml.AliasNode:
		return "alias"
	default:
		return fmt.Sprintf("unknown (kind %d)", kind)
	}
}

// childByKey returns the value node for `key` inside a mapping node.
// Returns (nil, false, nil) when the key is absent (legitimate skip
// path), and an error when the surrounding node is not a mapping at
// all (the path the caller is walking is structurally wrong).
func childByKey(parent *yaml.Node, key string) (*yaml.Node, bool, error) {
	if parent.Kind != yaml.MappingNode {
		//nolint:wrapcheck // sentinel constructed in-place; caller wraps with the file path
		return nil, false, errors.Newf("expected mapping while walking to %q, got %s", key, yamlKindName(parent.Kind))
	}

	for i := 0; i < len(parent.Content)-1; i += 2 {
		k := parent.Content[i]
		if k.Kind == yaml.ScalarNode && k.Value == key {
			return parent.Content[i+1], true, nil
		}
	}

	return nil, false, nil
}

// writeBackInstallImageToFiles applies writeBackInstallImageToNodeBody
// to every entry in files and prints a short progress line per file
// (matching the rest of the upgrade-handler output style). Returns
// the first error encountered; on success every file is patched (or
// silently skipped if it has no install.image key).
//
// Called from the upgrade handler after the talosctl RPC + post-upgrade
// verify return success — see upgrade_handler.go.
func writeBackInstallImageToFiles(files []string, newImage string) error {
	if newImage == "" {
		return nil
	}

	for _, filePath := range files {
		patched, err := writeBackInstallImageToNodeBody(filePath, newImage)
		if err != nil {
			return err
		}

		// Print "Synced" only when the body was actually rewritten.
		// The skip path (orphan / side-patch shape) already wrote its
		// own "Skipped %s: no machine.install.image key …" line from
		// writeBackInstallImageToNodeBody; the no-op-target-match path
		// is silent (nothing changed on disk, no operator-facing
		// signal needed). A "Skipped … Synced …" pair on the same
		// file would be misleading.
		if patched {
			fmt.Fprintf(os.Stderr, "Synced machine.install.image in %s to %s\n", filePath, newImage)
		}
	}

	return nil
}
