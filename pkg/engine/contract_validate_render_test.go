package engine

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/cockroachdb/errors"
	"github.com/siderolabs/talos/pkg/machinery/config/configloader"
	"github.com/siderolabs/talos/pkg/machinery/constants"
)

// legacyContract is a Talos contract from before the Kubernetes settings moved
// into documents of their own, where an unpinned Kubernetes version is still
// something the node can decide.
const legacyContract = "v1.13"

// metalMode is the runtime mode a rendered config is validated against: a real
// machine that installs to disk.
type metalMode struct{}

func (metalMode) String() string        { return "metal" }
func (metalMode) RequiresInstall() bool { return true }
func (metalMode) InContainer() bool     { return false }

// validateRendered loads a rendered config the way a node does and returns the
// validation error, if any.
func validateRendered(t *testing.T, rendered []byte) error {
	t.Helper()

	cfg, err := configloader.NewFromBytes(rendered)
	if err != nil {
		t.Fatalf("loading rendered config: %v", err)
	}

	_, err = cfg.ValidateAsClient(metalMode{})

	//nolint:wrapcheck // the caller inspects this error verbatim.
	return err
}

// A render must not hand the operator a config that mixes a v1alpha1 field with
// the document that replaced it: machinery rejects it, so the node refuses the
// apply. The charts write v1alpha1 fields, so a project pinned at or below the
// contract where those fields still live renders cleanly.
//
// Nothing else in the suite loads a rendered config through machinery, which is
// how a major Talos bump could rewrite the output and stay green.
func TestContract_RenderedConfigValidates(t *testing.T) {
	for _, talosVersion := range []string{"v1.12", "v1.13"} {
		t.Run("talosVersion="+talosVersion, func(t *testing.T) {
			out, err := applyPatchesAndRenderConfig(
				Options{KubernetesVersion: "v1.34.3", TalosVersion: talosVersion, Full: true},
				[]string{chartShapedPatch})
			if err != nil {
				t.Fatalf("applyPatchesAndRenderConfig: %v", err)
			}

			if err := validateRendered(t, out); err != nil {
				t.Errorf("rendered config does not validate: %v", err)
			}
		})
	}
}

// An unpinned project renders against the Talos version talm was built from. The
// shipped charts still write the v1alpha1 fields that version superseded, so the
// render has to stop with a way out rather than emit a config the node rejects
// at apply time.
func TestContract_UnpinnedRenderReportsSupersededFields(t *testing.T) {
	_, err := applyPatchesAndRenderConfig(
		Options{KubernetesVersion: "v1.34.3", Full: true},
		[]string{chartShapedPatch})
	if err == nil {
		t.Fatal("expected the render to report the superseded v1alpha1 fields")
	}

	if !strings.Contains(err.Error(), "mixes v1alpha1 fields") {
		t.Errorf("error does not name the conflict: %v", err)
	}

	// The same has to hold when the chart emits a typed document of its own: the
	// bundle emits one too, and a naive concatenation of the two is rejected as a
	// duplicate before the conflict check ever runs.
	_, err = applyPatchesAndRenderConfig(
		Options{KubernetesVersion: "v1.34.3", Full: true},
		[]string{chartShapedPatch + "---\napiVersion: v1alpha1\nkind: HostnameConfig\nhostname: node0\n"})
	if err == nil {
		t.Error("a chart document of the same kind as the bundle's silenced the conflict check")
	} else if !strings.Contains(err.Error(), "mixes v1alpha1 fields") {
		t.Errorf("error does not name the conflict: %v", err)
	}

	hint := errors.FlattenHints(err)
	if !strings.Contains(hint, "talosVersion") {
		t.Errorf("error carries no hint pointing at talosVersion, got hints: %q", hint)
	}

	// Validation reports unrelated incompleteness at the same time; surfacing it
	// here would bury the real cause under false leads the operator is expected
	// to fill in anyway.
	for _, unrelated := range []string{"clusterName must be specified", "endpoint must be specified", "service issuer URL is required"} {
		if strings.Contains(err.Error(), unrelated) {
			t.Errorf("error carries the unrelated %q alongside the conflict:\n%v", unrelated, err)
		}
	}
}

// chartShapedPatch carries the v1alpha1 fields the shipped charts write and
// Talos later moved into documents of their own.
const chartShapedPatch = `machine:
  type: controlplane
  kubelet:
    extraArgs:
      rotate-server-certificates: "true"
  network:
    nameservers:
      - 192.0.2.53
  install:
    disk: /dev/sda
cluster:
  controlPlane:
    endpoint: https://192.0.2.4:6443
`

func TestKubeVersion(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"unset falls back to the machinery default", "", strings.TrimPrefix(constants.DefaultKubernetesVersion, "v")},
		{"v prefix is stripped", "v1.30.0", "1.30.0"},
		{"bare version is passed through", "1.30.0", "1.30.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := kubeVersion(tc.in); got != tc.want {
				t.Errorf("kubeVersion(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// On a contract where the node can still choose, a project that pins no
// kubernetesVersion must not have one chosen for it: the fallback that satisfies
// v1.14's generator would otherwise write this binary's Kubernetes version into
// the config, invisibly, since the render's diff drops fields equal to the
// bundle default.
func TestContract_UnsetKubernetesVersionEmitsNoImages(t *testing.T) {
	patch := `machine:
  type: controlplane
  install:
    disk: /dev/sda
cluster:
  controlPlane:
    endpoint: https://192.0.2.4:6443
`

	assertNoKubeImages := func(t *testing.T, what string, out []byte) {
		t.Helper()

		for _, line := range strings.Split(string(out), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "image:") && (strings.Contains(trimmed, "kube") || strings.Contains(trimmed, "kubelet")) {
				t.Errorf("%s pinned a Kubernetes version the project never set: %s", what, trimmed)
			}
		}
	}

	for _, talosVersion := range []string{"v1.12", legacyContract} {
		t.Run("talosVersion="+talosVersion, func(t *testing.T) {
			rendered, err := applyPatchesAndRenderConfig(
				Options{TalosVersion: talosVersion, Full: true},
				[]string{patch})
			if err != nil {
				t.Fatalf("applyPatchesAndRenderConfig: %v", err)
			}

			assertNoKubeImages(t, "the render", rendered)

			// The direct-patch apply path serializes the bundle itself and never
			// goes through the render, so it needs its own assertion.
			configBundle, machineType, err := FullConfigProcess(Options{TalosVersion: talosVersion}, []string{patch})
			if err != nil {
				t.Fatalf("FullConfigProcess: %v", err)
			}

			sent, err := SerializeConfiguration(configBundle, machineType, talosVersion, "")
			if err != nil {
				t.Fatalf("SerializeConfiguration: %v", err)
			}

			assertNoKubeImages(t, "the apply path", sent)
		})
	}
}

// From the contract where the Kubernetes settings live in documents of their
// own, those documents require an image and there is no node-side default left.
// Leaving the version unset has to be reported, not papered over with a config
// machinery rejects for an empty image.
func TestContract_UnsetKubernetesVersionRefusedOnMultidocContract(t *testing.T) {
	patch := `machine:
  type: controlplane
  install:
    disk: /dev/sda
cluster:
  controlPlane:
    endpoint: https://192.0.2.4:6443
`

	for _, talosVersion := range []string{"", "v1.14"} {
		t.Run("talosVersion="+talosVersion, func(t *testing.T) {
			_, err := applyPatchesAndRenderConfig(
				Options{TalosVersion: talosVersion, Full: true},
				[]string{patch})
			if err == nil {
				t.Fatal("an unset Kubernetes version must be reported on this contract")
			}

			if !strings.Contains(errors.FlattenHints(err), "kubernetesVersion") {
				t.Errorf("error carries no hint naming the key to pin: %v", err)
			}
		})
	}
}

// An image the operator wrote themselves is theirs, pinned version or not.
// Stripping it would silently redirect an airgapped cluster away from its
// mirror, and the drift preview would compare against the stripped config.
func TestContract_UnsetKubernetesVersionKeepsOperatorImages(t *testing.T) {
	patch := `machine:
  type: controlplane
  install:
    disk: /dev/sda
  kubelet:
    image: ghcr.io/example/kubelet:v1.31.0-custom
cluster:
  controlPlane:
    endpoint: https://192.0.2.4:6443
  apiServer:
    image: registry.example.com/mirror/kube-apiserver:v1.31.0
`

	out, err := applyPatchesAndRenderConfig(
		Options{TalosVersion: "v1.13", Full: true},
		[]string{patch})
	if err != nil {
		t.Fatalf("applyPatchesAndRenderConfig: %v", err)
	}

	for _, want := range []string{
		"ghcr.io/example/kubelet:v1.31.0-custom",
		"registry.example.com/mirror/kube-apiserver:v1.31.0",
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("the operator's own image %q was stripped\n--- rendered ---\n%s", want, out)
		}
	}
}

// Pinning the version is what puts the images in, and every component lands on
// the version that was pinned.
func TestContract_PinnedKubernetesVersionReachesEveryComponent(t *testing.T) {
	patch := `machine:
  type: controlplane
  install:
    disk: /dev/sda
cluster:
  controlPlane:
    endpoint: https://192.0.2.4:6443
`

	out, err := applyPatchesAndRenderConfig(
		Options{KubernetesVersion: "v1.34.3", TalosVersion: "v1.13", Full: true},
		[]string{patch})
	if err != nil {
		t.Fatalf("applyPatchesAndRenderConfig: %v", err)
	}

	for _, want := range []string{
		"ghcr.io/siderolabs/kubelet:v1.34.3",
		"registry.k8s.io/kube-apiserver:v1.34.3",
		"registry.k8s.io/kube-controller-manager:v1.34.3",
		"registry.k8s.io/kube-proxy:v1.34.3",
		"registry.k8s.io/kube-scheduler:v1.34.3",
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("rendered config missing %q\n--- rendered ---\n%s", want, out)
		}
	}
}

// Machinery phrases the conflict two ways, and the k8s documents use the form
// with an article. Matching only the other one leaves the check disabled for
// .cluster.controlPlane.endpoint and .cluster.clusterName, which every chart
// writes — the exact fields an operator is most likely to hit.
func TestContract_ClusterEndpointConflictIsReported(t *testing.T) {
	patch := `machine:
  type: controlplane
  install:
    disk: /dev/sda
cluster:
  controlPlane:
    endpoint: https://192.0.2.4:6443
`

	_, err := applyPatchesAndRenderConfig(
		Options{KubernetesVersion: "v1.34.3", TalosVersion: "v1.14", Full: true},
		[]string{patch})
	if err == nil {
		t.Fatal("a superseded cluster endpoint must stop the render")
	}

	if !strings.Contains(err.Error(), "mixes v1alpha1 fields") {
		t.Errorf("error is not recognised as the conflict class: %v", err)
	}
}

// The check asks each document whether it conflicts, rather than reading
// validation text, so a kind whose message is phrased differently is still
// caught. KubeSpanConfig and KubeProxyConfig both word it their own way.
func TestContract_SupersededFieldsCaughtRegardlessOfWording(t *testing.T) {
	for _, tc := range []struct {
		name  string
		patch string
	}{
		{
			name: "kubespan",
			patch: `machine:
  type: controlplane
  install:
    disk: /dev/sda
  network:
    kubespan:
      enabled: true
---
apiVersion: v1alpha1
kind: KubeSpanConfig
enabled: true
`,
		},
		{
			name: "proxy",
			patch: `machine:
  type: controlplane
  install:
    disk: /dev/sda
cluster:
  proxy:
    disabled: true
---
apiVersion: v1alpha1
kind: KubeProxyConfig
enabled: false
image: registry.k8s.io/kube-proxy:v1.34.3
`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := applyPatchesAndRenderConfig(
				Options{KubernetesVersion: "v1.34.3", TalosVersion: "v1.14", Full: true},
				[]string{tc.patch})
			if err == nil {
				t.Fatal("a document superseding a v1alpha1 field must stop the render")
			}

			if !strings.Contains(err.Error(), "mixes v1alpha1 fields") {
				t.Errorf("error is not the conflict class: %v", err)
			}
		})
	}
}

// The warning belongs to the render, which serializes the bundle twice on the
// default path. Emitting it from the serializer said it twice.
func TestContract_UnpinnedKubernetesVersionWarnsOnce(t *testing.T) {
	contract, err := renderContract(legacyContract)
	if err != nil {
		t.Fatalf("renderContract: %v", err)
	}

	var buf bytes.Buffer

	warnUnpinnedKubernetesVersion(&buf, contract, "")

	if got := strings.Count(buf.String(), "warning:"); got != 1 {
		t.Errorf("warning emitted %d times, want 1:\n%s", got, buf.String())
	}

	buf.Reset()
	warnUnpinnedKubernetesVersion(&buf, contract, "v1.34.3")

	if buf.Len() != 0 {
		t.Errorf("a pinned version must not warn, got:\n%s", buf.String())
	}
}

// The direct-patch apply path never goes through the render, so it carries its
// own copy of both guards. Without them a node file with no templates would be
// serialized with the component images stripped and sent on, silently.
func TestContract_DirectPatchPathWarnsAndChecks(t *testing.T) {
	patch := `machine:
  type: controlplane
  install:
    disk: /dev/sda
`

	configBundle, machineType, err := FullConfigProcess(Options{TalosVersion: legacyContract}, []string{patch})
	if err != nil {
		t.Fatalf("FullConfigProcess: %v", err)
	}

	stderr := captureStderr(t, func() {
		if _, err := SerializeConfiguration(configBundle, machineType, legacyContract, ""); err != nil {
			t.Fatalf("SerializeConfiguration: %v", err)
		}
	})

	if !strings.Contains(stderr, "kubernetesVersion is not set") {
		t.Errorf("the direct-patch path stripped the component images without saying so, stderr:\n%s", stderr)
	}
}

// captureStderr runs fn with os.Stderr redirected and returns what it wrote.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}

	saved := os.Stderr
	os.Stderr = w

	// fn may call t.Fatalf, which ends the goroutine before the restore below.
	// Without this the next test writes into a pipe nobody reads.
	t.Cleanup(func() {
		os.Stderr = saved
		_ = w.Close()
		_ = r.Close()
	})

	fn()

	os.Stderr = saved

	if err := w.Close(); err != nil {
		t.Fatalf("close pipe: %v", err)
	}

	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read pipe: %v", err)
	}

	return string(out)
}

// The conflict check fires on any document that supersedes a v1alpha1 field,
// including on a contract that still keeps those settings in v1alpha1 — a chart
// emitting both shapes at once. Telling that operator to pin the contract lower
// sends them nowhere: they are already below the boundary, and the fix is to
// drop one of the two shapes.
func TestContract_SupersededFieldsHintMatchesTheContract(t *testing.T) {
	rendered := `machine:
  type: controlplane
  network:
    hostname: node0
---
apiVersion: v1alpha1
kind: HostnameConfig
hostname: node0
`

	for _, tc := range []struct {
		name         string
		talosVersion string
		wantPinLower bool
	}{
		{name: "above the boundary", talosVersion: "v1.14", wantPinLower: true},
		{name: "at the boundary", talosVersion: "v1.13", wantPinLower: false},
		{name: "below the boundary", talosVersion: "v1.12", wantPinLower: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkSupersededFields([]byte(rendered), tc.talosVersion)
			if err == nil {
				t.Fatalf("talosVersion=%s: expected a conflict", tc.talosVersion)
			}

			hints := strings.Join(errors.GetAllHints(err), "\n")

			if got := strings.Contains(hints, "Pin it to"); got != tc.wantPinLower {
				t.Errorf("talosVersion=%s: hint offers a lower pin = %v, want %v:\n%s",
					tc.talosVersion, got, tc.wantPinLower, hints)
			}
		})
	}
}
