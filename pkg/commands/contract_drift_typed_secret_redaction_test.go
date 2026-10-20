package commands

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/cockroachdb/errors"
	machineapi "github.com/siderolabs/talos/pkg/machinery/api/machine"
	"github.com/siderolabs/talos/pkg/machinery/config/configdiff"
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

	return append(controlPlaneWithSecrets(t, "v1.14"), []byte(typedSecretDocs)...)
}

// controlPlaneWithSecrets renders a control-plane config on the given
// contract with its own freshly generated secrets.
func controlPlaneWithSecrets(t *testing.T, talosVersion string) []byte {
	t.Helper()

	configBundle, err := engine.InitializeConfigBundle(engine.Options{TalosVersion: talosVersion, KubernetesVersion: "v1.34.3"})
	if err != nil {
		t.Fatalf("InitializeConfigBundle: %v", err)
	}

	out, err := configBundle.Serialize(encoder.CommentsDisabled, machine.TypeControlPlane)
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}

	return out
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

// The etcd encryption config is a whole Kubernetes EncryptionConfiguration,
// and only its `secret` leaves are secret. Collecting the rest (provider key
// names, the `secrets` resource) would mask those words everywhere in the
// dry-run diff, as Talos itself does not.
func TestModeDetails_EtcdEncryptionCollectsOnlySecretLeaves(t *testing.T) {
	values, err := collectConfigSecretValues(v114ControlPlaneWithSecrets(t))
	if err != nil {
		t.Fatalf("collectConfigSecretValues: %v", err)
	}

	for _, plain := range []string{"secrets", "key2"} {
		if _, ok := values[plain]; ok {
			t.Errorf("%q is collected as a secret value", plain)
		}
	}

	printed := redactValuesInText("+    - hostPath: /var/lib/secrets\n+      name: key2-volume\n", values)
	if strings.Contains(printed, modeDetailsRedactionSentinel) {
		t.Errorf("plain diff lines were masked:\n%s", printed)
	}
}

// The dry-run diff the node returns shows both its own config and the one
// being applied. Every secret Talos redacts must be masked on both sides, as
// printed: from v1.14 the CA and service-account keys are multi-line PEM,
// which the diff splits into one prefixed line per line of the key.
func TestModeDetails_PrintedDiffMasksSecretsOnBothSides(t *testing.T) {
	for _, talosVersion := range []string{"v1.13", "v1.14"} {
		t.Run(talosVersion, func(t *testing.T) {
			onNode := controlPlaneWithSecrets(t, talosVersion)
			rendered := controlPlaneWithSecrets(t, talosVersion)

			diff := nodeDiff(t, onNode, rendered)

			nodeValues, err := collectConfigSecretValues(onNode)
			if err != nil {
				t.Fatalf("collectConfigSecretValues: %v", err)
			}

			values, err := modeDetailsSecretValues(rendered, nodeValues)
			if err != nil {
				t.Fatalf("modeDetailsSecretValues: %v", err)
			}

			buf := &bytes.Buffer{}
			printApplyResultsRedacted(&machineapi.ApplyConfigurationResponse{
				Messages: []*machineapi.ApplyConfiguration{{ModeDetails: diff}},
			}, values, buf)
			printed := buf.String()

			if !strings.Contains(printed, "@@") {
				t.Fatalf("the two configs produced no diff to check:\n%s", printed)
			}

			for side, cfg := range map[string][]byte{"node": onNode, "rendered": rendered} {
				for kind, leaves := range talosSecretLeaves(t, cfg) {
					for path, value := range leaves {
						if line, leaked := leakedSecretLine(printed, value); leaked {
							t.Errorf("%s side: %s %s printed in the dry-run diff (%q)", side, kind, path, line)
						}
					}
				}
			}
		})
	}
}

// The node's secrets are collected before the apply, so a node config talm
// cannot read or decode costs the masking of that side, never the apply. A
// dry run says so instead of leaking silently; a maintenance connection has
// no config to read and stays quiet.
func TestNodeSecretValues(t *testing.T) {
	origShow, origDryRun := applyCmdFlags.showSecretsInDrift, applyCmdFlags.dryRun
	t.Cleanup(func() { applyCmdFlags.showSecretsInDrift, applyCmdFlags.dryRun = origShow, origDryRun })

	applyCmdFlags.dryRun = true

	onNode := controlPlaneWithSecrets(t, "v1.14")

	reader := func(data []byte, ok bool, err error) machineConfigReader {
		return func(context.Context) ([]byte, bool, error) { return data, ok, err }
	}

	for _, tc := range []struct {
		name        string
		show        bool
		read        machineConfigReader
		wantValues  bool
		wantWarning bool
	}{
		{name: "read", read: reader(onNode, true, nil), wantValues: true},
		{name: "read error", read: reader(nil, false, errors.New("rpc timeout")), wantWarning: true},
		{name: "maintenance connection", read: reader(nil, false, nil)},
		{name: "undecodable config", read: reader([]byte("machine: [unterminated"), true, nil), wantWarning: true},
		{name: "show secrets", show: true, read: func(context.Context) ([]byte, bool, error) {
			t.Error("--show-secrets-in-drift must not read the node's config")

			return nil, false, nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			applyCmdFlags.showSecretsInDrift = tc.show

			buf := &bytes.Buffer{}
			values := nodeSecretValues(context.Background(), tc.read, buf)

			if got := len(values) > 0; got != tc.wantValues {
				t.Errorf("collected secrets = %v, want %v", got, tc.wantValues)
			}

			if got := strings.Contains(buf.String(), "not masked"); got != tc.wantWarning {
				t.Errorf("warning printed = %v, want %v: %q", got, tc.wantWarning, buf.String())
			}
		})
	}

	// Talos puts the config diff into the response of a dry run only, so a
	// real apply has nothing of the node's to mask and skips the read.
	t.Run("no read outside a dry run", func(t *testing.T) {
		applyCmdFlags.showSecretsInDrift, applyCmdFlags.dryRun = false, false

		buf := &bytes.Buffer{}
		values := nodeSecretValues(context.Background(), func(context.Context) ([]byte, bool, error) {
			t.Error("a real apply must not read the node's config for redaction")

			return onNode, true, nil
		}, buf)

		if values != nil || buf.Len() != 0 {
			t.Errorf("a real apply collected %d values and printed %q", len(values), buf.String())
		}
	})
}

func nodeDiff(t *testing.T, oldBytes, newBytes []byte) string {
	t.Helper()

	oldCfg, err := configloader.NewFromBytes(oldBytes)
	if err != nil {
		t.Fatalf("loading node config: %v", err)
	}

	newCfg, err := configloader.NewFromBytes(newBytes)
	if err != nil {
		t.Fatalf("loading rendered config: %v", err)
	}

	diff, err := configdiff.DiffConfigs(oldCfg, newCfg)
	if err != nil {
		t.Fatalf("DiffConfigs: %v", err)
	}

	return diff
}

// leakedSecretLine reports a line of value that shows up in printed. A
// multi-line value is checked line by line, as the diff prints it; the PEM
// armor lines are not secret.
func leakedSecretLine(printed, value string) (string, bool) {
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "-----") {
			continue
		}

		for _, out := range strings.Split(printed, "\n") {
			if strings.TrimSpace(strings.TrimLeft(out, "+- ")) == line || (len(line) >= 16 && strings.Contains(out, line)) {
				return line, true
			}
		}
	}

	return "", false
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
