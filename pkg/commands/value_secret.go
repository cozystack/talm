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
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/cozystack/talm/pkg/age"
	"github.com/cozystack/talm/pkg/engine"
)

// sealRenderedSecrets removes or hides values that came from encrypted value
// files in `talm template` output. With -I (writing a committed node file) the
// secret fields are omitted entirely — the real value is re-rendered at apply.
// On the stdout/preview stream they are redacted to a sentinel unless the
// operator passes --show-secrets. When no encrypted value file is in scope the
// render bytes are returned verbatim.
//
// persistedValueFiles is the subset of valueFiles that `talm apply` will read
// back on its own (the Chart.yaml templateOptions.valueFiles set). The
// modeline does not persist value files, so an encrypted file passed only via
// `template --values` is invisible to a later `apply` — omitting its secret at
// -I time without that file in Chart.yaml would silently drop the field from
// the applied config. warnUnpersistedEncryptedFiles surfaces that foot-gun.
func sealRenderedSecrets(rendered []byte, valueFiles, persistedValueFiles []string, rootDir string, inplace, showSecrets bool) ([]byte, error) {
	secrets, err := collectEncryptedValueLeaves(valueFiles, rootDir)
	if err != nil {
		return nil, err
	}

	if len(secrets) == 0 {
		return rendered, nil
	}

	switch {
	case inplace:
		warnUnpersistedEncryptedFiles(valueFiles, persistedValueFiles, os.Stderr)

		sealed, sealErr := engine.OmitSecretValues(rendered, secrets)
		if sealErr != nil {
			return nil, errors.Wrap(sealErr, "omitting secret values from rendered config")
		}

		return sealed, nil
	case !showSecrets:
		sealed, sealErr := engine.RedactSecretValues(rendered, secrets)
		if sealErr != nil {
			return nil, errors.Wrap(sealErr, "redacting secret values in rendered config")
		}

		return sealed, nil
	default:
		// --show-secrets on the stdout stream: print verbatim.
		return rendered, nil
	}
}

// warnUnpersistedEncryptedFiles emits a stderr warning for each encrypted
// value file whose secrets `template -I` just omitted from a node file but
// which `apply` will NOT re-read on its own — i.e. it is not in the Chart.yaml
// templateOptions.valueFiles set (persisted). The modeline does not record
// value files, so such a file passed only via `template --values` leaves the
// omitted secret permanently absent from the applied config unless the
// operator re-passes it to apply. Surfacing this turns a silent data-loss
// foot-gun into a visible, actionable warning.
func warnUnpersistedEncryptedFiles(valueFiles, persistedValueFiles []string, w io.Writer) {
	for _, filePath := range valueFiles {
		if !strings.HasSuffix(filePath, age.EncryptedFileSuffix) {
			continue
		}

		if slices.Contains(persistedValueFiles, filePath) {
			continue
		}

		fmt.Fprintf(w, "warning: %s holds encrypted values that were omitted from the rendered node file, "+
			"but it is not in Chart.yaml templateOptions.valueFiles; `talm apply` will re-render WITHOUT these "+
			"secrets and the fields will be absent from the applied config. Add it to templateOptions.valueFiles "+
			"(or re-pass --values %s to apply).\n", filePath, filePath)
	}
}

// collectEncryptedValueLeaves decrypts every *.encrypted.yaml entry in
// valueFiles and returns the set of their plaintext string-leaf values — the
// "secret set" `talm template` seals out of node files (-I), redacts in the
// preview stream, and `talm apply` redacts in the drift preview. rootDir
// locates talm.key. Plain (non-encrypted) value files are ignored: only values
// the operator chose to encrypt are treated as secret.
//
// An empty result (no encrypted value files in scope) means sealing is a
// no-op, so the common path keeps the raw render verbatim.
//
// Sharp edge — value-based matching: sealing matches by exact value across the
// whole rendered config, so a secret whose plaintext coincides with an
// ordinary structural string (e.g. a password literally set to "controlplane"
// or a bare port like "6443") would also seal/redact that unrelated field. Do
// not encrypt values that collide with non-secret config strings; prefer
// high-entropy values. This is inherent to matching secrets that templates can
// place at arbitrary rendered paths.
func collectEncryptedValueLeaves(valueFiles []string, rootDir string) (map[string]struct{}, error) {
	secrets := make(map[string]struct{})

	for _, filePath := range valueFiles {
		if !strings.HasSuffix(filePath, age.EncryptedFileSuffix) {
			continue
		}

		decrypted, err := age.DecryptYAMLToMap(rootDir, filePath)
		if err != nil {
			return nil, errors.Wrapf(err, "collecting secret values from %s", filePath)
		}

		collectStringLeaves(decrypted, secrets)
	}

	return secrets, nil
}

// collectStringLeaves walks an arbitrary decoded YAML value and adds every
// string leaf to secrets. Empty strings are skipped — sealing on an empty
// value would match unrelated empty fields across the rendered config.
func collectStringLeaves(value any, secrets map[string]struct{}) {
	switch typed := value.(type) {
	case map[string]any:
		for _, item := range typed {
			collectStringLeaves(item, secrets)
		}
	case []any:
		for _, item := range typed {
			collectStringLeaves(item, secrets)
		}
	case string:
		if typed != "" {
			secrets[typed] = struct{}{}
		}
	}
}
