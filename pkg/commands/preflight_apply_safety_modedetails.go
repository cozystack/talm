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
	"sort"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/siderolabs/talos/pkg/cli"
	machineapi "github.com/siderolabs/talos/pkg/machinery/api/machine"
	yaml "gopkg.in/yaml.v3"
)

// modeDetailsRedactionSentinel masks a secret value inside the server-returned
// dry-run diff (ModeDetails). The diff is free-form text, not structured YAML,
// so the by-value masking used here cannot carry the length tell that the
// structured drift preview emits — a fixed sentinel keeps the diff readable
// while disclosing nothing. Matches the `***` `talm template` prints to stdout.
const modeDetailsRedactionSentinel = "***"

// secretLeafNames are leaf field names whose value is always secret regardless
// of position in the config tree. The Talos bootstrap allowlist (isSecretPath)
// is keyed by full dotted path, which works for fixed-location material
// (cluster.ca.key) but not for Wireguard key material that lives under
// machine.network.interfaces[].wireguard — collecting those by leaf name avoids
// dragging the surrounding public fields (publicKey, endpoint, allowedIPs) into
// the redaction set, where their low entropy could clobber unrelated diff text.
const (
	secretLeafPrivateKey   = "privateKey"
	secretLeafPresharedKey = "presharedKey"
)

//nolint:gochecknoglobals // static set of always-secret leaf names.
var secretLeafNames = map[string]struct{}{
	secretLeafPrivateKey:   {},
	secretLeafPresharedKey: {},
}

// collectConfigSecretValues walks the rendered (multi-doc) MachineConfig and
// returns the set of secret string VALUES it carries: every leaf whose dotted
// path is on the Talos bootstrap allowlist or a typed document's secret field
// (isSecretFieldOf) plus every Wireguard key leaf (secretLeafNames). It exists
// to redact the server-computed dry-run diff (ModeDetails), which talm prints
// verbatim and which would otherwise leak ca.key / token / encryption-secret
// material by value — the structured drift preview redacts these by PATH,
// but ModeDetails is opaque diff text, so the only handle talm has on it is
// the value itself.
//
// Over-collection is bounded and harmless: an acceptedCAs entry contributes its
// public crt alongside the key, so a cert rotation renders as the sentinel in
// the dry-run diff. That trades a little readability for never leaking the
// adjacent key, and --show-secrets-in-drift restores the verbatim diff.
func collectConfigSecretValues(rendered []byte) (map[string]struct{}, error) {
	out := make(map[string]struct{})

	if len(bytes.TrimSpace(rendered)) == 0 {
		return out, nil
	}

	dec := yaml.NewDecoder(bytes.NewReader(rendered))

	for {
		var doc map[string]any

		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return nil, errors.Wrap(err, "decoding rendered config for dry-run diff redaction")
		}

		kind, _ := doc["kind"].(string)
		walkConfigSecretValues(doc, kind, "", out)
	}

	return out, nil
}

// walkConfigSecretValues recurses the parsed config, building dotted paths, and
// collects every string leaf reachable under a secret path or a secret leaf
// name. When a node matches, the whole subtree's string leaves are taken
// (collectStringLeaves) so a slice-valued secret path like cluster.acceptedCAs
// is covered without descending further.
func walkConfigSecretValues(node any, kind, path string, out map[string]struct{}) {
	switch typed := node.(type) {
	case map[string]any:
		for key, val := range typed {
			child := key
			if path != "" {
				child = path + "." + key
			}

			if _, ok := secretLeafNames[key]; ok {
				collectStringLeaves(val, out)

				continue
			}

			if isSecretFieldOf(kind, child) {
				if leafKey, ok := secretTypedLeafKeys[kind]; ok {
					collectNamedStringLeaves(val, leafKey, out)
				} else {
					collectStringLeaves(val, out)
				}

				continue
			}

			walkConfigSecretValues(val, kind, child, out)
		}
	case []any:
		for i, item := range typed {
			walkConfigSecretValues(item, kind, fmt.Sprintf("%s[%d]", path, i), out)
		}
	}
}

// secretTypedLeafKeys narrows the values collected under a kind's secret
// field to the leaves with this key. The etcd encryption config is a whole
// EncryptionConfiguration whose provider key names and resource names are
// plain words; masking them by value would cut them out of every diff line.
// Talos redacts only its `secret` leaves.
//
//nolint:gochecknoglobals // static map of secret leaf keys per typed document.
var secretTypedLeafKeys = map[string]string{
	kindKubeEtcdEncryptionConfig: "secret",
}

// collectNamedStringLeaves adds every string leaf under value whose map key
// is leafKey.
func collectNamedStringLeaves(value any, leafKey string, out map[string]struct{}) {
	switch typed := value.(type) {
	case map[string]any:
		for key, item := range typed {
			if s, ok := item.(string); ok && key == leafKey && s != "" {
				out[s] = struct{}{}

				continue
			}

			collectNamedStringLeaves(item, leafKey, out)
		}
	case []any:
		for _, item := range typed {
			collectNamedStringLeaves(item, leafKey, out)
		}
	}
}

// redactValuesInText masks every occurrence of every secret value in text with
// the sentinel. Values are applied longest-first so that when one secret is a
// substring of another, the longer match is consumed first and no fragment of
// the longer value survives. An empty value set returns text unchanged, which
// is exactly the --show-secrets-in-drift behaviour (no values collected).
//
// A multi-line value, such as a PEM key, never appears whole: the diff prints
// each of its lines on a line of its own, behind a `+`/`-` marker and the
// indentation. Its lines are matched one diff line at a time instead.
func redactValuesInText(text string, values map[string]struct{}) string {
	if len(values) == 0 {
		return text
	}

	ordered := make([]string, 0, len(values))
	secretLines := map[string]struct{}{}

	for value := range values {
		if value == "" {
			continue
		}

		if strings.Contains(value, "\n") {
			addSecretLines(secretLines, value)

			continue
		}

		ordered = append(ordered, value)
	}

	text = redactSecretLines(text, secretLines)

	sort.Slice(ordered, func(i, j int) bool {
		if len(ordered[i]) != len(ordered[j]) {
			return len(ordered[i]) > len(ordered[j])
		}

		return ordered[i] < ordered[j]
	})

	for _, value := range ordered {
		text = strings.ReplaceAll(text, value, modeDetailsRedactionSentinel)
	}

	return text
}

// addSecretLines adds the lines of a multi-line secret to set. PEM armor
// lines name the key type and are not secret.
func addSecretLines(set map[string]struct{}, value string) {
	for line := range strings.SplitSeq(value, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "-----") {
			continue
		}

		set[line] = struct{}{}
	}
}

// redactSecretLines masks every diff line whose content, after the `+`, `-`
// or space marker and the indentation, is a line of a multi-line secret.
// Exactly one marker character is dropped: base64 lines can start with `+`.
func redactSecretLines(text string, secretLines map[string]struct{}) string {
	if len(secretLines) == 0 {
		return text
	}

	lines := strings.Split(text, "\n")
	for i, line := range lines {
		body := line
		if body != "" && strings.ContainsRune("+- ", rune(body[0])) {
			body = body[1:]
		}

		content := strings.TrimSpace(body)
		if _, secret := secretLines[content]; !secret {
			continue
		}

		indent := len(body) - len(strings.TrimLeft(body, " \t"))
		lines[i] = line[:len(line)-len(body)+indent] + modeDetailsRedactionSentinel
	}

	return strings.Join(lines, "\n")
}

// printApplyResultsRedacted mirrors talosctl's helpers.PrintApplyResults
// (warnings to stderr, ModeDetails to w) but runs ModeDetails through
// redactValuesInText first. ModeDetails is the server-returned dry-run /
// apply-mode diff; talosctl prints it raw, which leaks any secret-bearing
// field that appears in or adjacent to a change hunk. w receives the (possibly
// redacted) diff; warnings keep going through cli.Warning for parity.
func printApplyResultsRedacted(resp *machineapi.ApplyConfigurationResponse, values map[string]struct{}, w io.Writer) {
	for _, message := range resp.GetMessages() {
		for _, warning := range message.GetWarnings() {
			cli.Warning("%s", warning)
		}

		if details := message.GetModeDetails(); details != "" {
			_, _ = fmt.Fprintln(w, redactValuesInText(details, values))
		}
	}
}

// emitApplyResults is the redacting replacement for the two
// helpers.PrintApplyResults call sites on the apply paths. It collects the
// secret value set the dry-run diff must hide — Talos bootstrap material from
// the rendered config always, plus user secrets from encrypted value files when
// the path rendered them — and prints the results with those values masked.
// --show-secrets-in-drift collects nothing, so the diff prints verbatim.
//
// rendersUserValues mirrors buildDriftRedactor: only the template-rendering
// path feeds value files into the applied config, so only it can leak a user
// secret through ModeDetails; the direct-patch path renders none, so collecting
// them there would be pure overhead and would wrongly require a talm.key.
func emitApplyResults(resp *machineapi.ApplyConfigurationResponse, rendered []byte, rendersUserValues bool) error {
	if applyCmdFlags.showSecretsInDrift {
		printApplyResultsRedacted(resp, nil, os.Stderr)

		return nil
	}

	values, err := collectConfigSecretValues(rendered)
	if err != nil {
		return err
	}

	if rendersUserValues {
		userSecrets, err := collectEncryptedValueLeaves(applyValueFilePaths(), Config.RootDir)
		if err != nil {
			return errors.Wrap(err, "collecting user secret values for dry-run diff redaction")
		}

		for value := range userSecrets {
			values[value] = struct{}{}
		}
	}

	printApplyResultsRedacted(resp, values, os.Stderr)

	return nil
}
