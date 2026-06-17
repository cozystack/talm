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
	"github.com/cockroachdb/errors"
	"gopkg.in/yaml.v3"
)

// redactionSentinel is the placeholder a secret value is replaced with in the
// stdout / preview stream so it does not leak to terminal scrollback or CI
// logs. It is only ever used for human-facing output, never written to a node
// file (the in-place path omits secrets entirely — see OmitSecretValues).
const redactionSentinel = "***"

// OmitSecretValues removes every field whose scalar value is a known secret
// from the rendered config so the value never lands in a committed node file.
// The real value is re-rendered at apply time, where the node-file body is
// merged as a patch on top of a fresh render — so a path the body omits is
// left to the render, which holds the genuine value.
//
// Two granularities, dictated by how `talm apply`'s patch merge treats the
// node body (see MergeFileAsPatch / pruneBodyIdentitiesAgainstRendered):
//
//   - A secret that is a direct map value: drop the key. Maps merge by key, so
//     the rendered value survives untouched.
//   - A secret anywhere inside a sequence: drop the WHOLE KEY, never a partial
//     list. A partial list is unsafe two ways. Under default sequence
//     semantics a stripped element no longer deep-equals the rendered element
//     and would be appended as a spurious duplicate. Under `merge:"replace"`
//     semantics (configpatcher's replaceSemanticPaths — cluster podSubnets /
//     serviceSubnets, apiServer auditPolicy, ingress, portSelector/ports) a
//     partial body list OVERWRITES the rendered list, silently dropping the
//     secret-bearing element from the applied config. Dropping the whole key
//     sidesteps both: the body omits the list entirely, so the render (which
//     holds every element, secret included) is authoritative under any merge
//     semantics.
//
// A mapping emptied by dropping a secret key is left as an empty map (e.g.
// `auth: {}`), NOT key-dropped. An empty-map patch merges as a no-op (maps
// merge by key), so the rendered value survives — no clobber risk, unlike the
// sequence case.
//
// Returns rendered unchanged when secrets is empty.
func OmitSecretValues(rendered []byte, secrets map[string]struct{}) ([]byte, error) {
	return transformSecretValues(rendered, secrets, omitSecretsFromNode)
}

// RedactSecretValues replaces every known-secret scalar value with a fixed
// sentinel, preserving structure. Used only for the stdout / preview stream
// of `talm template` (default-on when encrypted value files are in scope,
// bypassed by --show-secrets) so a preview never prints secret material.
// Returns rendered unchanged when secrets is empty.
func RedactSecretValues(rendered []byte, secrets map[string]struct{}) ([]byte, error) {
	return transformSecretValues(rendered, secrets, redactSecretsInNode)
}

// transformSecretValues decodes every YAML document in rendered, applies fn to
// each document's tree, and re-encodes. An empty secret set is a no-op that
// returns the input verbatim (so the common no-encrypted-values path keeps the
// raw render bytes and their exact formatting).
func transformSecretValues(rendered []byte, secrets map[string]struct{}, transform func(*yaml.Node, map[string]struct{})) ([]byte, error) {
	if len(secrets) == 0 {
		return rendered, nil
	}

	docs, err := decodeAllYAMLDocuments(rendered)
	if err != nil {
		return nil, errors.Wrap(err, "decoding rendered config before sealing secret values")
	}

	for _, doc := range docs {
		transform(doc, secrets)
	}

	out, err := encodeAllYAMLDocuments(docs)
	if err != nil {
		return nil, errors.Wrap(err, "re-encoding config after sealing secret values")
	}

	return out, nil
}

// isSecretScalar reports whether node is a scalar whose decoded value is in the
// secret set. It returns false for an AliasNode even if the anchor it points at
// holds a secret — that is safe here only because the input is always a
// freshly yaml.Encoder-produced Talos MachineConfig, which never emits anchors
// or aliases. If aliased input could ever reach the omit path, a secret behind
// an alias would not be detected; the input contract guarantees it cannot.
func isSecretScalar(node *yaml.Node, secrets map[string]struct{}) bool {
	if node.Kind != yaml.ScalarNode {
		return false
	}

	_, ok := secrets[node.Value]

	return ok
}

// nodeContainsSecret reports whether node or any of its descendants is a secret
// scalar. Used to decide whether a whole sequence element must be dropped.
func nodeContainsSecret(node *yaml.Node, secrets map[string]struct{}) bool {
	if isSecretScalar(node, secrets) {
		return true
	}

	for _, child := range node.Content {
		if nodeContainsSecret(child, secrets) {
			return true
		}
	}

	return false
}

// omitSecretsFromNode walks node and removes secret-bearing fields per the
// granularity rules documented on OmitSecretValues.
func omitSecretsFromNode(node *yaml.Node, secrets map[string]struct{}) {
	switch node.Kind {
	case yaml.DocumentNode:
		for _, child := range node.Content {
			omitSecretsFromNode(child, secrets)
		}
	case yaml.MappingNode:
		node.Content = omitSecretsFromMapping(node.Content, secrets)
	case yaml.SequenceNode:
		node.Content = keepNonSecretElements(node.Content, secrets)
	case yaml.ScalarNode, yaml.AliasNode:
		// A bare scalar / alias has no enclosing key to drop here; a
		// secret scalar is removed by its parent mapping or sequence.
	}
}

// omitSecretsFromMapping rebuilds a mapping's key/value list, dropping entries
// whose value carries a secret. A scalar-secret value drops the key; a
// sequence value that contains a secret anywhere drops the whole key (never a
// partial list — see OmitSecretValues for why a partial list is unsafe under
// both default and replace merge semantics); any other value is recursed into
// and kept.
func omitSecretsFromMapping(content []*yaml.Node, secrets map[string]struct{}) []*yaml.Node {
	kept := make([]*yaml.Node, 0, len(content))

	for i := 0; i+1 < len(content); i += 2 {
		key, value := content[i], content[i+1]

		if isSecretScalar(value, secrets) {
			continue // C1: drop the key holding a secret scalar.
		}

		if value.Kind == yaml.SequenceNode {
			if nodeContainsSecret(value, secrets) {
				continue // C2: any secret in the list drops the whole key.
			}

			kept = append(kept, key, value)

			continue
		}

		omitSecretsFromNode(value, secrets)
		kept = append(kept, key, value)
	}

	return kept
}

// keepNonSecretElements returns the sequence elements that contain no secret
// anywhere. An element carrying a secret (at any depth) is dropped whole — a
// partial element would not deep-equal the rendered element and would be
// appended as a duplicate at apply time.
func keepNonSecretElements(elements []*yaml.Node, secrets map[string]struct{}) []*yaml.Node {
	kept := make([]*yaml.Node, 0, len(elements))

	for _, element := range elements {
		if nodeContainsSecret(element, secrets) {
			continue
		}

		kept = append(kept, element)
	}

	return kept
}

// redactSecretsInNode walks node and replaces every secret scalar value with
// the redaction sentinel, leaving structure intact.
func redactSecretsInNode(node *yaml.Node, secrets map[string]struct{}) {
	switch node.Kind {
	case yaml.DocumentNode, yaml.SequenceNode:
		for _, child := range node.Content {
			redactSecretsInNode(child, secrets)
		}
	case yaml.MappingNode:
		for i := 1; i < len(node.Content); i += 2 {
			redactSecretsInNode(node.Content[i], secrets)
		}
	case yaml.ScalarNode:
		if isSecretScalar(node, secrets) {
			node.SetString(redactionSentinel)
		}
	case yaml.AliasNode:
		// Aliases reference an already-visited anchor; the anchored
		// node is redacted at its definition site.
	}
}
