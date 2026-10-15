package engine

import (
	"bytes"
	"io"
	"maps"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cockroachdb/errors"
	"gopkg.in/yaml.v3"
	"helm.sh/helm/v4/pkg/chart/common"
	"helm.sh/helm/v4/pkg/chart/v2/loader"

	helmEngine "github.com/cozystack/talm/pkg/engine/helm"
)

// The shipped charts must render a config the node accepts on every contract
// they support, through the whole engine: the chart output is a patch on the
// generated bundle, and only the merged result tells whether Talos takes it.
// From v1.14 that means the Kubernetes settings travel in typed documents and
// none of the v1alpha1 fields they replaced survive.
func TestContract_ShippedChartsRenderValidConfig(t *testing.T) {
	charts := []struct {
		name string
		path string
	}{
		{"cozystack", cozystackChartPath},
		{"generic", genericChartPath},
	}

	// Every knob that lands in a Kubernetes document, so each branch of the
	// typed rendering goes through machinery validation at least once.
	knobs := map[string]any{
		"certSANs":                   []any{"api.example.com"},
		"extraKubeletExtraArgs":      map[string]any{"imageGCHighThresholdPercent": 80},
		"extraApiServerArgs":         map[string]any{"max-requests-inflight": "2000"},
		"extraControllerManagerArgs": map[string]any{"concurrent-gc-syncs": "30"},
		"extraSchedulerArgs":         map[string]any{"kube-api-qps": "100"},
		"oidcIssuerUrl":              "https://issuer.example.com",
	}

	for _, chart := range charts {
		for _, machineType := range []string{"controlplane", "worker"} {
			for _, talosVersion := range []string{"v1.12", "v1.13", "v1.14"} {
				for _, values := range []string{"defaults", "knobs"} {
					overrides := map[string]any{}
					if values == "knobs" {
						overrides = knobs
					}

					t.Run(chart.name+"/"+machineType+"/"+talosVersion+"/"+values, func(t *testing.T) {
						rendered := renderShippedChart(t, chart.path, "templates/"+machineType+".yaml", talosVersion, overrides)
						full, applied := renderNodeFileAndApply(t, talosVersion, rendered)

						// The node file restates what the render produced; laying
						// it back over the render must change nothing, or every
						// apply grows the lists that merge by appending.
						if !reflect.DeepEqual(decodeDocuments(t, applied), decodeDocuments(t, full)) {
							t.Errorf("applying the node file changes the rendered config\n--- full ---\n%s\n--- applied ---\n%s", full, applied)
						}

						for label, out := range map[string][]byte{"full render": full, "applied node file": applied} {
							if err := validateRendered(t, out); err != nil {
								t.Errorf("%s does not validate: %v", label, err)
							}

							if values == "knobs" && machineType == "controlplane" {
								for _, want := range []string{"api.example.com", "max-requests-inflight", "concurrent-gc-syncs", "kube-api-qps", "imageGCHighThresholdPercent"} {
									if !strings.Contains(string(out), want) {
										t.Errorf("%s lost %q", label, want)
									}
								}
							}

							if talosVersion != "v1.14" {
								continue
							}

							assertNoSupersededV1Alpha1Fields(t, label, out)

							if chart.name == "cozystack" && machineType == "controlplane" {
								assertCozystackControlPlaneV114(t, label, string(out))

								if values == "knobs" {
									assertCozystackOIDCAuthenticatorV114(t, label, string(out))
								}
							}
						}
					})
				}
			}
		}
	}
}

// From v1.14 Talos refuses kube-apiserver authentication and authorization
// flags in KubeAPIServerConfig. The node would reject the apply, so the render
// refuses them first and names the document that replaced them.
func TestContract_ApiServerAuthFlagsRefusedFromV114(t *testing.T) {
	for _, chartPath := range []string{cozystackChartPath, genericChartPath} {
		for _, flag := range []string{"oidc-issuer-url", "anonymous-auth", "authentication-config", "authorization-config", "authorization-mode", "authorization-webhook-version"} {
			t.Run(filepath.Base(chartPath)+"/"+flag, func(t *testing.T) {
				err := renderExpectingError(t, chartPath, "v1.14", helmEngineEmptyLookup, map[string]any{
					"endpoint":           testEndpoint,
					"advertisedSubnets":  []any{testAdvertisedSubnet},
					"extraApiServerArgs": map[string]any{flag: "x"},
				})
				if err == nil {
					t.Fatalf("extraApiServerArgs.%s rendered on v1.14", flag)
				}

				if !strings.Contains(err.Error(), "extraApiServerArgs."+flag) {
					t.Errorf("error does not name the flag: %v", err)
				}
			})
		}

		// Talos denies a fixed list, not the whole authorization- family.
		t.Run(filepath.Base(chartPath)+"/authorization-policy-file allowed", func(t *testing.T) {
			renderShippedChart(t, chartPath, "templates/controlplane.yaml", "v1.14", map[string]any{
				"extraApiServerArgs": map[string]any{"authorization-policy-file": "/etc/policy.jsonl"},
			})
		})
	}
}

// The OIDC flags the cozystack preset used to put on the kube-apiserver
// command line, as the JWT authenticator Talos takes from v1.14. A username
// claim other than email gets the "<issuer>#" prefix the flags defaulted to,
// so existing RBAC bindings keep matching.
func assertCozystackOIDCAuthenticatorV114(t *testing.T, label, out string) {
	t.Helper()

	if strings.Contains(out, "oidc-") {
		t.Errorf("%s: OIDC is still passed as kube-apiserver flags", label)
	}

	for _, want := range []string{
		"kind: KubeAuthenticationConfig",
		"url: https://issuer.example.com",
		"claim: preferred_username",
		"prefix: https://issuer.example.com#",
		"claim: groups",
		"- path: /healthz",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("%s: OIDC authenticator is missing %q", label, want)
		}
	}
}

func renderShippedChart(t *testing.T, chartPath, templateFile, talosVersion string, overrides map[string]any) string {
	t.Helper()

	origLookup := helmEngine.LookupFunc
	t.Cleanup(func() { helmEngine.LookupFunc = origLookup })
	helmEngine.LookupFunc = simpleNicLookup()

	chrt, err := loader.LoadDir(chartPath)
	if err != nil {
		t.Fatalf("load chart: %v", err)
	}

	values := cloneValues(chrt.Values)
	values["endpoint"] = testEndpoint
	values["advertisedSubnets"] = []any{testAdvertisedSubnet}
	maps.Copy(values, overrides)

	out, err := (&helmEngine.Engine{}).Render(chrt, common.Values{"Values": values, "TalosVersion": talosVersion})
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	return out[path.Join(chrt.Name(), templateFile)]
}

// renderNodeFileAndApply returns the full render and what `talm apply` sends
// for the node file a non-full render writes: the templates rendered in full
// with the node file laid over them.
func renderNodeFileAndApply(t *testing.T, talosVersion, rendered string) ([]byte, []byte) {
	t.Helper()

	opts := Options{KubernetesVersion: "v1.34.3", TalosVersion: talosVersion}

	nodeFile, err := applyPatchesAndRenderConfig(opts, []string{rendered})
	if err != nil {
		t.Fatalf("non-full render: %v", err)
	}

	opts.Full = true

	full, err := applyPatchesAndRenderConfig(opts, []string{rendered})
	if err != nil {
		t.Fatalf("full render: %v", err)
	}

	nodeFilePath := filepath.Join(t.TempDir(), "node.yaml")
	if err := os.WriteFile(nodeFilePath, nodeFile, 0o600); err != nil {
		t.Fatalf("writing node file: %v", err)
	}

	applied, err := MergeFileAsPatch(full, nodeFilePath)
	if err != nil {
		t.Fatalf("merging the node file: %v", err)
	}

	return full, applied
}

func decodeDocuments(t *testing.T, data []byte) []map[string]any {
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
			t.Fatalf("decoding rendered config: %v", err)
		}

		if doc != nil {
			docs = append(docs, doc)
		}
	}
}

func assertNoSupersededV1Alpha1Fields(t *testing.T, label string, out []byte) {
	t.Helper()

	var v1alpha1 struct {
		Machine map[string]any `yaml:"machine"`
		Cluster map[string]any `yaml:"cluster"`
	}

	if err := yaml.Unmarshal(out, &v1alpha1); err != nil {
		t.Fatalf("%s: decoding the v1alpha1 document: %v", label, err)
	}

	for _, key := range []string{"kubelet", "nodeLabels", "nodeTaints"} {
		if _, ok := v1alpha1.Machine[key]; ok {
			t.Errorf("%s: v1alpha1 machine.%s is set on the v1.14 contract", label, key)
		}
	}

	for _, key := range []string{"clusterName", "controlPlane", "network", "apiServer", "controllerManager", "scheduler", "proxy", "discovery", "allowSchedulingOnControlPlanes"} {
		if _, ok := v1alpha1.Cluster[key]; ok {
			t.Errorf("%s: v1alpha1 cluster.%s is set on the v1.14 contract", label, key)
		}
	}
}

// What the cozystack preset turns off or strips on a control plane: Cozystack
// ships its own CNI and kube-proxy replacement, schedules workloads on control
// planes, and lets external load balancers reach them.
func assertCozystackControlPlaneV114(t *testing.T, label, out string) {
	t.Helper()

	for _, unwanted := range []string{
		"kind: KubeFlannelCNIConfig",
		"kind: DiscoveryServiceConfig",
		"node.kubernetes.io/exclude-from-external-load-balancers",
		"node-role.kubernetes.io/control-plane: NoSchedule",
		"$patch",
	} {
		if strings.Contains(out, unwanted) {
			t.Errorf("%s: cozystack control plane still carries %q", label, unwanted)
		}
	}

	for _, want := range []string{
		"kind: KubeProxyConfig\nenabled: false",
		"cpuManagerPolicy: static",
		"bind-address: 0.0.0.0",
		"quota-backend-bytes:",
		"registry.k8s.io/kube-apiserver:v1.34.3",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("%s: cozystack control plane is missing %q", label, want)
		}
	}
}
