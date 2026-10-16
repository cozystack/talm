package commands

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/cockroachdb/errors"
	"github.com/siderolabs/talos/pkg/machinery/config/configloader"
	"github.com/siderolabs/talos/pkg/machinery/config/encoder"
	"github.com/siderolabs/talos/pkg/machinery/config/machine"
	"gopkg.in/yaml.v3"

	"github.com/cozystack/talm/pkg/applycheck"
	"github.com/cozystack/talm/pkg/engine"
)

const talosRedactionMarker = "TALM-TEST-REDACTED"

// typedSecretDocs carries one secret in every typed document kind whose
// Redact method masks something, beyond the ones the generator emits.
const typedSecretDocs = `---
apiVersion: v1alpha1
kind: RegistryAuthConfig
name: registry.example.com
username: operator
password: registry-password-value
---
apiVersion: v1alpha1
kind: RegistryTLSConfig
name: tls.example.com
clientIdentity:
  cert: registry-client-crt-value
  key: registry-client-key-value
---
apiVersion: v1alpha1
kind: HCloudVIPConfig
name: hcloud-vip
link: eth0
apiToken: hcloud-token-value
---
apiVersion: v1alpha1
kind: UserVolumeConfig
name: data
provisioning:
  diskSelector:
    match: "!system_disk"
encryption:
  provider: luks2
  keys:
    - slot: 0
      static:
        passphrase: volume-passphrase-value
`

// v114ControlPlaneWithSecrets renders a v1.14 control-plane config the way
// talm does, with its own freshly generated secrets, plus typedSecretDocs.
func v114ControlPlaneWithSecrets(t *testing.T) []byte {
	t.Helper()

	configBundle, err := engine.InitializeConfigBundle(engine.Options{TalosVersion: "v1.14", KubernetesVersion: "v1.34.3"})
	if err != nil {
		t.Fatalf("InitializeConfigBundle: %v", err)
	}

	out, err := configBundle.Serialize(encoder.CommentsDisabled, machine.TypeControlPlane)
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}

	return append(out, []byte(typedSecretDocs)...)
}

// talosSecretLeaves returns every string leaf Talos itself masks in cfgBytes,
// as kind -> dotted path -> value, by diffing the config against its own
// RedactSecrets copy.
func talosSecretLeaves(t *testing.T, cfgBytes []byte) map[string]map[string]string {
	t.Helper()

	cfg, err := configloader.NewFromBytes(cfgBytes)
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}

	redactedBytes, err := cfg.RedactSecrets(talosRedactionMarker).EncodeBytes(encoder.WithComments(encoder.CommentsDisabled))
	if err != nil {
		t.Fatalf("encoding redacted config: %v", err)
	}

	originalBytes, err := cfg.EncodeBytes(encoder.WithComments(encoder.CommentsDisabled))
	if err != nil {
		t.Fatalf("encoding config: %v", err)
	}

	original, redacted := decodeAll(t, originalBytes), decodeAll(t, redactedBytes)
	if len(original) != len(redacted) {
		t.Fatalf("redaction changed the document count: %d -> %d", len(original), len(redacted))
	}

	out := map[string]map[string]string{}

	for i := range original {
		kind, _ := original[i]["kind"].(string)
		if kind == "" {
			kind = "MachineConfig"
		}

		collectChangedLeaves(original[i], redacted[i], "", func(path, value string) {
			if out[kind] == nil {
				out[kind] = map[string]string{}
			}

			out[kind][path] = value
		})
	}

	return out
}

func decodeAll(t *testing.T, data []byte) []map[string]any {
	t.Helper()

	var docs []map[string]any

	dec := yaml.NewDecoder(bytes.NewReader(data))

	for {
		var doc map[string]any

		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			return docs
		}

		if err != nil {
			t.Fatalf("decoding config: %v", err)
		}

		if doc != nil {
			docs = append(docs, doc)
		}
	}
}

func collectChangedLeaves(original, redacted any, path string, add func(path, value string)) {
	switch o := original.(type) {
	case map[string]any:
		r, _ := redacted.(map[string]any)
		for key, value := range o {
			child := key
			if path != "" {
				child = path + "." + key
			}

			collectChangedLeaves(value, r[key], child, add)
		}
	case []any:
		r, _ := redacted.([]any)
		for i, value := range o {
			var rv any
			if i < len(r) {
				rv = r[i]
			}

			collectChangedLeaves(value, rv, fmt.Sprintf("%s[%d]", path, i), add)
		}
	case string:
		if r, _ := redacted.(string); r != o {
			add(path, o)
		}
	}
}

// Every field Talos itself treats as secret has to be masked in the drift
// preview, whatever document carries it. From contract v1.14 the CA keys,
// the service-account key, the etcd encryption keys and the discovery
// secret live in typed documents, and a path list written for v1alpha1
// printed them in the clear.
func TestDriftPreview_MasksEverySecretTalosRedacts(t *testing.T) {
	current := v114ControlPlaneWithSecrets(t)
	desired := v114ControlPlaneWithSecrets(t)

	changes, err := applycheck.Diff(current, desired)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}

	buf := &bytes.Buffer{}
	printDriftPreview(buf, "drift:", changes, secretRedactor{})
	preview := buf.String()

	for _, cfg := range [][]byte{current, desired} {
		for kind, leaves := range talosSecretLeaves(t, cfg) {
			for path, value := range leaves {
				if strings.Contains(preview, value) {
					t.Errorf("%s %s printed in the clear by the drift preview", kind, path)
				}
			}
		}
	}

	// The fixture must actually differ in its generated secrets, or the
	// preview would have nothing to leak and the check above proves nothing.
	if !strings.Contains(preview, "KubeServiceAccountConfig") {
		t.Fatalf("expected the two configs to differ in their service-account keys:\n%s", preview)
	}
}

// The dry-run diff the node returns is opaque text, so talm masks it by the
// secret values the rendered config carries; those have to include every
// secret Talos redacts, typed documents included.
func TestModeDetails_CollectsEverySecretTalosRedacts(t *testing.T) {
	cfg := v114ControlPlaneWithSecrets(t)

	collected, err := collectConfigSecretValues(cfg)
	if err != nil {
		t.Fatalf("collectConfigSecretValues: %v", err)
	}

	for kind, leaves := range talosSecretLeaves(t, cfg) {
		for path, value := range leaves {
			if _, ok := collected[value]; !ok {
				t.Errorf("%s %s is not collected for dry-run diff redaction", kind, path)
			}
		}
	}
}

// The same field name is secret on one kind and harmless on another:
// KubeEtcdEncryptionConfig.config holds the encryption keys,
// KubeletConfig.config holds kubelet settings an operator needs to see.
func TestSecretRedactor_TypedPathsAreKeyedByKind(t *testing.T) {
	if !(secretRedactor{}).forKind("KubeEtcdEncryptionConfig").redactPath("config.resources") {
		t.Error("KubeEtcdEncryptionConfig config.resources is not redacted")
	}

	if (secretRedactor{}).forKind("KubeletConfig").redactPath("config.maxPods") {
		t.Error("KubeletConfig config.maxPods is redacted")
	}

	if (secretRedactor{show: true}).forKind("KubeEtcdEncryptionConfig").redactPath("config.resources") {
		t.Error("--show-secrets-in-drift does not reveal typed secrets")
	}
}
