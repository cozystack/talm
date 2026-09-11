package engine

import (
	"strings"
	"testing"

	"github.com/siderolabs/talos/pkg/machinery/config/configloader"
	"github.com/siderolabs/talos/pkg/machinery/config/encoder"
	"github.com/siderolabs/talos/pkg/machinery/config/machine"
)

// countYAMLDocuments counts non-empty documents in a YAML stream.
func countYAMLDocuments(t *testing.T, data []byte) int {
	t.Helper()

	docs, err := decodeYAMLDocuments(data)
	if err != nil {
		t.Fatalf("decodeYAMLDocuments: %v", err)
	}

	return len(docs)
}

// A full render has to carry every document the bundle produced. From the
// contract where Talos moved the certificate authorities, the service-account
// key, the kubelet and the Kubernetes control-plane settings into documents of
// their own, keeping only the first one silently strips the config down to
// machine and cluster — and machinery validates the remains without complaint.
func TestContract_FullRenderKeepsEveryBundleDocument(t *testing.T) {
	for _, talosVersion := range []string{"", "v1.12", "v1.14"} {
		t.Run("talosVersion="+talosVersion, func(t *testing.T) {
			opts := Options{KubernetesVersion: "v1.34.3", TalosVersion: talosVersion, Full: true}

			configBundle, err := InitializeConfigBundle(opts)
			if err != nil {
				t.Fatalf("InitializeConfigBundle: %v", err)
			}

			fromBundle, err := configBundle.Serialize(encoder.CommentsDisabled, machine.TypeControlPlane)
			if err != nil {
				t.Fatalf("Serialize: %v", err)
			}

			out, err := applyPatchesAndRenderConfig(opts, []string{"machine:\n  type: controlplane\n"})
			if err != nil {
				t.Fatalf("applyPatchesAndRenderConfig: %v", err)
			}

			want := countYAMLDocuments(t, fromBundle)
			if got := countYAMLDocuments(t, out); got != want {
				t.Errorf("render produced %d documents, bundle has %d — documents are being dropped", got, want)
			}
		})
	}
}

// The certificate authorities and the service-account key are what a node needs
// to join; losing them produces a config that still validates.
func TestContract_FullRenderKeepsSecretsOnMultidocContract(t *testing.T) {
	out, err := applyPatchesAndRenderConfig(
		Options{KubernetesVersion: "v1.34.3", TalosVersion: "v1.14", Full: true},
		[]string{"machine:\n  type: controlplane\n"})
	if err != nil {
		t.Fatalf("applyPatchesAndRenderConfig: %v", err)
	}

	for _, kind := range []string{"KubeAPIServerCAConfig", "KubeServiceAccountConfig", "KubeAggregatorCAConfig"} {
		if !strings.Contains(string(out), "kind: "+kind) {
			t.Errorf("render dropped the %s document", kind)
		}
	}
}

// From the contract where Talos moved Kubernetes settings into their own
// documents, the bundle emits KubeAPIServerConfig and its siblings with the
// images already pinned to kubernetesVersion, and those documents survive the
// render. Writing the v1alpha1 fields as well would be rejected by machinery,
// which refuses a config carrying both shapes.
func TestContract_RenderLeavesV1Alpha1ComponentsAloneOnMultidocContract(t *testing.T) {
	patch := `machine:
  type: controlplane
`

	out, err := applyPatchesAndRenderConfig(
		Options{KubernetesVersion: "v1.34.3", TalosVersion: "v1.14", Full: true},
		[]string{patch})
	if err != nil {
		t.Fatalf("applyPatchesAndRenderConfig: %v", err)
	}

	rendered := string(out)

	if !strings.Contains(rendered, "kind: KubeAPIServerConfig") {
		t.Fatalf("expected a KubeAPIServerConfig document in the render\n--- rendered ---\n%s", rendered)
	}

	for _, conflicting := range []string{"\n  apiServer:", "\n  controllerManager:", "\n  proxy:", "\n  scheduler:"} {
		if strings.Contains(rendered, conflicting) {
			t.Errorf("v1alpha1 %q written next to the typed documents; machinery rejects a config with both\n--- rendered ---\n%s", strings.TrimSpace(conflicting), rendered)
		}
	}

	// The images still have to be pinned to kubernetesVersion, just by the bundle.
	if !strings.Contains(rendered, "registry.k8s.io/kube-apiserver:v1.34.3") {
		t.Errorf("typed documents do not carry the cluster's Kubernetes version\n--- rendered ---\n%s", rendered)
	}
}

// An apply sends the serialized bundle, not the node file. Both apply paths go
// through here: the template path renders with Full set, the direct-patch path
// serializes the bundle straight. Either way the node must receive the documents
// it needs to join, which the node file itself does not carry.
func TestContract_ApplyPathCarriesEveryDocument(t *testing.T) {
	opts := Options{KubernetesVersion: "v1.34.3", TalosVersion: "v1.14"}
	patch := `machine:
  type: controlplane
  install:
    disk: /dev/sda
cluster:
  controlPlane:
    endpoint: https://192.0.2.4:6443
`

	configBundle, machineType, err := FullConfigProcess(opts, []string{patch})
	if err != nil {
		t.Fatalf("FullConfigProcess: %v", err)
	}

	out, err := SerializeConfiguration(configBundle, machineType)
	if err != nil {
		t.Fatalf("SerializeConfiguration: %v", err)
	}

	for _, kind := range []string{"KubeAPIServerCAConfig", "KubeServiceAccountConfig"} {
		if !strings.Contains(string(out), "kind: "+kind) {
			t.Errorf("the config an apply sends is missing the %s document", kind)
		}
	}
}

// A chart emits typed documents of its own, and from contract v1.12 the bundle
// emits some of the same kinds. Talos rejects a config carrying two documents
// with one identity, so the chart's document has to replace the bundle's rather
// than join it — and the render has to stay loadable.
func TestContract_ChartDocumentReplacesBundleDocument(t *testing.T) {
	patch := `machine:
  type: controlplane
  install:
    disk: /dev/sda
cluster:
  controlPlane:
    endpoint: https://192.0.2.4:6443
---
apiVersion: v1alpha1
kind: HostnameConfig
hostname: node0
`

	for _, talosVersion := range []string{"v1.12", "v1.13"} {
		t.Run("talosVersion="+talosVersion, func(t *testing.T) {
			out, err := applyPatchesAndRenderConfig(
				Options{KubernetesVersion: "v1.34.3", TalosVersion: talosVersion, Full: true},
				[]string{patch})
			if err != nil {
				t.Fatalf("applyPatchesAndRenderConfig: %v", err)
			}

			if got := strings.Count(string(out), "kind: HostnameConfig"); got != 1 {
				t.Errorf("render carries %d HostnameConfig documents, want 1\n--- rendered ---\n%s", got, out)
			}

			if !strings.Contains(string(out), "hostname: node0") {
				t.Errorf("the chart's hostname was dropped in favour of the bundle's\n--- rendered ---\n%s", out)
			}

			if _, err := configloader.NewFromBytes(out); err != nil {
				t.Errorf("Talos refuses the rendered config: %v", err)
			}
		})
	}
}

// An extra document with neither apiVersion nor kind shares the identity
// sentinel with the v1alpha1 document itself. Treating that as a match would
// let a stray `---\nfoo: bar` in a chart delete machine, cluster, the tokens
// and the CAs, and the render would report nothing.
func TestContract_UntypedExtraDocumentKeepsMachineConfig(t *testing.T) {
	patch := `machine:
  type: controlplane
  install:
    disk: /dev/sda
cluster:
  controlPlane:
    endpoint: https://192.0.2.4:6443
---
someChartOutput: true
`

	for _, talosVersion := range []string{"v1.11", "v1.12", "v1.13"} {
		t.Run("talosVersion="+talosVersion, func(t *testing.T) {
			out, err := applyPatchesAndRenderConfig(
				Options{KubernetesVersion: "v1.34.3", TalosVersion: talosVersion, Full: true},
				[]string{patch})
			if err != nil {
				t.Fatalf("applyPatchesAndRenderConfig: %v", err)
			}

			rendered := string(out)

			if !strings.Contains(rendered, "machine:") || !strings.Contains(rendered, "cluster:") {
				t.Errorf("the v1alpha1 document was dropped by an untyped extra document\n--- rendered ---\n%s", rendered)
			}

			if !strings.Contains(rendered, "someChartOutput") {
				t.Errorf("the extra document itself went missing\n--- rendered ---\n%s", rendered)
			}
		})
	}
}

// A non-full render returns a patch, so it does not carry every bundle
// document — but it must not manufacture directives against fields the
// contract moved out of v1alpha1, and the operator's own typed documents have
// to survive it. Both halves are what makes the node file reproduce the
// cluster on the next apply.
func TestContract_NonFullRenderOnMultidocContract(t *testing.T) {
	typed := `apiVersion: v1alpha1
kind: KubeClusterConfig
clusterName: prod
endpoint: https://192.0.2.10:6443
`

	out, err := applyPatchesAndRenderConfig(
		Options{KubernetesVersion: "v1.34.3", TalosVersion: "v1.14"},
		[]string{"machine:\n  type: controlplane\n", typed})
	if err != nil {
		t.Fatalf("applyPatchesAndRenderConfig: %v", err)
	}

	rendered := string(out)

	if !strings.Contains(rendered, "kind: KubeClusterConfig") || !strings.Contains(rendered, "clusterName: prod") {
		t.Errorf("render dropped the operator's KubeClusterConfig:\n%s", rendered)
	}

	// cluster.clusterName and cluster.controlPlane.endpoint live in
	// KubeClusterConfig from this contract on, so nothing should be deleting
	// them from a v1alpha1 document that no longer declares them.
	if strings.Contains(rendered, "$patch: delete") {
		t.Errorf("render emitted a delete directive against fields the contract moved:\n%s", rendered)
	}
}

// The non-full render diffs the serialized bundle against itself through
// yamltools.DiffYAMLs, which compares the first document of each side and
// ignores the rest. That is only correct while the first document is the
// v1alpha1 one on every contract. Nothing in machinery promises the order, and
// the cost of it changing grew with this bump: the bundle went from two
// documents to twenty-eight, so a reordering would silently diff v1alpha1
// against a typed document instead of failing.
func TestContract_BundleSerializesV1Alpha1First(t *testing.T) {
	for _, talosVersion := range []string{"", "v1.12", "v1.13", "v1.14"} {
		t.Run("talosVersion="+talosVersion, func(t *testing.T) {
			configBundle, err := InitializeConfigBundle(
				Options{KubernetesVersion: "v1.34.3", TalosVersion: talosVersion})
			if err != nil {
				t.Fatalf("InitializeConfigBundle: %v", err)
			}

			serialized, err := configBundle.Serialize(encoder.CommentsDisabled, machine.TypeControlPlane)
			if err != nil {
				t.Fatalf("Serialize: %v", err)
			}

			docs, err := decodeYAMLDocuments(serialized)
			if err != nil {
				t.Fatalf("decodeYAMLDocuments: %v", err)
			}

			if len(docs) == 0 {
				t.Fatal("bundle serialized to no documents")
			}

			if id := documentIdentityFromNode(docs[0]); id != legacyRootIdentity {
				t.Errorf("first serialized document is %q, want the v1alpha1 root: the non-full render diffs doc[0]", id)
			}
		})
	}
}
