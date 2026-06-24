package engine

import (
	"strings"
	"testing"
)

// A chart that patches only extraArgs on the control-plane components (the
// common case — it does not re-state the component images) must still produce a
// config that pins every component image to kubernetesVersion. Otherwise the
// render diff drops the images (they equal the bundle default) and the node
// re-defaults them to the Talos release's Kubernetes version, skewing
// controller-manager/scheduler away from kube-apiserver/kubelet.
func TestContract_RenderPinsControlPlaneComponentImages(t *testing.T) {
	const kubernetesVersion = "v1.34.3"

	patch := `machine:
  type: controlplane
cluster:
  controllerManager:
    extraArgs:
      bind-address: 0.0.0.0
  scheduler:
    extraArgs:
      bind-address: 0.0.0.0
  apiServer:
    certSANs:
      - 127.0.0.1
`

	out, err := applyPatchesAndRenderConfig(Options{KubernetesVersion: kubernetesVersion}, []string{patch})
	if err != nil {
		t.Fatalf("applyPatchesAndRenderConfig: %v", err)
	}

	for _, want := range []string{
		"registry.k8s.io/kube-apiserver:" + kubernetesVersion,
		"registry.k8s.io/kube-controller-manager:" + kubernetesVersion,
		"registry.k8s.io/kube-scheduler:" + kubernetesVersion,
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("rendered config missing pinned image %q; node would re-default it\n--- rendered ---\n%s", want, out)
		}
	}
}
