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
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/cosi-project/runtime/pkg/safe"
	"github.com/siderolabs/crypto/x509"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/siderolabs/talos/pkg/machinery/client"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	configres "github.com/siderolabs/talos/pkg/machinery/resources/config"
	secretsres "github.com/siderolabs/talos/pkg/machinery/resources/secrets"
)

// wrapRotateCACommand adds special handling for rotate-ca command
func wrapRotateCACommand(wrappedCmd *cobra.Command, originalRunE func(*cobra.Command, []string) error) {
	// Disable --with-docs and --with-examples by default
	if flag := wrappedCmd.Flags().Lookup("with-docs"); flag != nil {
		flag.DefValue = "false"
		flag.Value.Set("false")
	}
	if flag := wrappedCmd.Flags().Lookup("with-examples"); flag != nil {
		flag.DefValue = "false"
		flag.Value.Set("false")
	}

	wrappedCmd.RunE = func(cmd *cobra.Command, args []string) error {
		// Ensure project root is detected
		if !Config.RootDirExplicit {
			detectedRoot, err := detectRootFromCWD()
			if err == nil && detectedRoot != "" {
				Config.RootDir = detectedRoot
			}
		}

		// Check flags
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		rotateTalos, _ := cmd.Flags().GetBool("talos")
		rotateKubernetes, _ := cmd.Flags().GetBool("kubernetes")

		// PRE-PROCESSING: Auto-discover nodes if not specified
		controlPlaneNodes, _ := cmd.Flags().GetStringSlice("control-plane-nodes")
		workerNodes, _ := cmd.Flags().GetStringSlice("worker-nodes")

		if len(controlPlaneNodes) == 0 && len(workerNodes) == 0 {
			fmt.Fprintf(os.Stderr, "> Auto-discovering cluster nodes...\n")
			cpNodes, wNodes, err := discoverClusterNodes()
			if err != nil {
				return fmt.Errorf("failed to auto-discover nodes: %w", err)
			}
			cmd.Flags().Set("control-plane-nodes", strings.Join(cpNodes, ","))
			if len(wNodes) > 0 {
				cmd.Flags().Set("worker-nodes", strings.Join(wNodes, ","))
			}
			fmt.Fprintf(os.Stderr, "  - Control plane nodes: %v\n", cpNodes)
			fmt.Fprintf(os.Stderr, "  - Worker nodes: %v\n", wNodes)
		}

		// Set --output to project talosconfig
		if !cmd.Flags().Changed("output") {
			talosconfigPath := GlobalArgs.Talosconfig
			if talosconfigPath == "" {
				talosconfigPath = filepath.Join(Config.RootDir, "talosconfig")
			}
			cmd.Flags().Set("output", talosconfigPath)
		}

		// Set --k8s-endpoint from GlobalArgs.Endpoints
		if !cmd.Flags().Changed("k8s-endpoint") && len(GlobalArgs.Endpoints) > 0 {
			host := GlobalArgs.Endpoints[0]
			host = strings.TrimPrefix(host, "https://")
			host = strings.TrimPrefix(host, "http://")
			if h, _, err := net.SplitHostPort(host); err == nil {
				host = h
			}
			k8sEndpoint := fmt.Sprintf("https://%s:6443", host)
			cmd.Flags().Set("k8s-endpoint", k8sEndpoint)
		}

		// Run the original rotate-ca command
		if err := originalRunE(cmd, args); err != nil {
			return err
		}

		// POST-PROCESSING (only if not dry-run)
		if dryRun {
			return nil
		}

		fmt.Fprintf(os.Stderr, "\n> Updating local configuration files...\n")

		// Update secrets.yaml with new CA from cluster
		if err := updateSecretsFromCluster(rotateTalos, rotateKubernetes); err != nil {
			return fmt.Errorf("failed to update secrets.yaml: %w", err)
		}

		// Update talosconfig.encrypted if it exists (talosconfig already updated by upstream)
		if rotateTalos {
			if err := UpdateTalosconfigEncryption(); err != nil {
				return fmt.Errorf("failed to update talosconfig.encrypted: %w", err)
			}
		}

		// Update kubeconfig using talm kubeconfig
		if rotateKubernetes {
			fmt.Fprintf(os.Stderr, "\n> Updating kubeconfig...\n")
			if err := runKubeconfigCmd(); err != nil {
				return fmt.Errorf("failed to update kubeconfig: %w", err)
			}
		}

		fmt.Fprintf(os.Stderr, "\n> CA rotation completed successfully!\n")
		return nil
	}
}

// discoverClusterNodes discovers control plane and worker nodes from Kubernetes API
func discoverClusterNodes() (controlPlane []string, workers []string, err error) {
	if len(GlobalArgs.Nodes) == 0 {
		return nil, nil, fmt.Errorf("no nodes specified: use --nodes/-n flag or --file/-f with modeline")
	}

	node := GlobalArgs.Nodes[0]

	err = WithClientAuto(func(ctx context.Context, c *client.Client) error {
		ctx = client.WithNode(ctx, node)

		// Get kubeconfig from node
		r, err := c.KubeconfigRaw(ctx)
		if err != nil {
			return fmt.Errorf("failed to get kubeconfig: %w", err)
		}
		defer r.Close()

		data, err := extractKubeconfigFromTarGz(r)
		if err != nil {
			return fmt.Errorf("failed to extract kubeconfig: %w", err)
		}

		// Create Kubernetes client
		restConfig, err := clientcmd.RESTConfigFromKubeConfig(data)
		if err != nil {
			return fmt.Errorf("failed to create REST config: %w", err)
		}

		// Override server endpoint if specified
		if len(GlobalArgs.Endpoints) > 0 {
			endpoint := GlobalArgs.Endpoints[0]
			host := endpoint
			host = strings.TrimPrefix(host, "https://")
			host = strings.TrimPrefix(host, "http://")
			if h, _, err := net.SplitHostPort(host); err == nil {
				host = h
			}
			restConfig.Host = fmt.Sprintf("https://%s:6443", host)
		}

		clientset, err := kubernetes.NewForConfig(restConfig)
		if err != nil {
			return fmt.Errorf("failed to create Kubernetes client: %w", err)
		}

		nodeList, err := clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
		if err != nil {
			return fmt.Errorf("failed to list nodes: %w", err)
		}

		// Get node IPs and check their roles via Talos API
		for _, k8sNode := range nodeList.Items {
			var internalIP string
			for _, addr := range k8sNode.Status.Addresses {
				if addr.Type == v1.NodeInternalIP {
					internalIP = addr.Address
					break
				}
			}
			if internalIP == "" {
				continue
			}

			// Check node type via Talos API
			nodeCtx := client.WithNode(ctx, internalIP)
			mt, err := safe.StateGetByID[*configres.MachineType](nodeCtx, c.COSI, configres.MachineTypeID)
			if err != nil {
				return fmt.Errorf("failed to get machine type for %s: %w", internalIP, err)
			}

			if mt.MachineType().IsControlPlane() {
				controlPlane = append(controlPlane, internalIP)
			} else {
				workers = append(workers, internalIP)
			}
		}

		return nil
	})

	if err != nil {
		return nil, nil, err
	}

	if len(controlPlane) == 0 {
		return nil, nil, fmt.Errorf("no control plane nodes found")
	}

	return controlPlane, workers, nil
}

// updateSecretsFromCluster fetches new CA from cluster and updates secrets.yaml
func updateSecretsFromCluster(updateTalos, updateKubernetes bool) error {
	secretsPath := ResolveSecretsPath(Config.TemplateOptions.WithSecrets)
	if !fileExists(secretsPath) {
		fmt.Fprintf(os.Stderr, "  - secrets.yaml not found, skipping\n")
		return nil
	}

	// Read existing secrets.yaml
	data, err := os.ReadFile(secretsPath)
	if err != nil {
		return fmt.Errorf("failed to read secrets.yaml: %w", err)
	}

	var bundle secrets.Bundle
	if err := yaml.Unmarshal(data, &bundle); err != nil {
		return fmt.Errorf("failed to parse secrets.yaml: %w", err)
	}

	// Get first node
	if len(GlobalArgs.Nodes) == 0 {
		return fmt.Errorf("no nodes available")
	}
	node := GlobalArgs.Nodes[0]

	// Fetch new CA from cluster
	err = WithClientAuto(func(ctx context.Context, c *client.Client) error {
		ctx = client.WithNode(ctx, node)

		if updateTalos {
			osRoot, err := safe.StateGetByID[*secretsres.OSRoot](ctx, c.COSI, secretsres.OSRootID)
			if err != nil {
				return fmt.Errorf("failed to get OSRoot: %w", err)
			}
			bundle.Certs.OS = &x509.PEMEncodedCertificateAndKey{
				Crt: osRoot.TypedSpec().IssuingCA.Crt,
				Key: osRoot.TypedSpec().IssuingCA.Key,
			}
			fmt.Fprintf(os.Stderr, "  - Updated Talos CA in secrets.yaml\n")
		}

		if updateKubernetes {
			k8sRoot, err := safe.StateGetByID[*secretsres.KubernetesRoot](ctx, c.COSI, secretsres.KubernetesRootID)
			if err != nil {
				return fmt.Errorf("failed to get KubernetesRoot: %w", err)
			}
			bundle.Certs.K8s = &x509.PEMEncodedCertificateAndKey{
				Crt: k8sRoot.TypedSpec().IssuingCA.Crt,
				Key: k8sRoot.TypedSpec().IssuingCA.Key,
			}
			fmt.Fprintf(os.Stderr, "  - Updated Kubernetes CA in secrets.yaml\n")
		}

		return nil
	})
	if err != nil {
		return err
	}

	// Save secrets.yaml and update encrypted version
	return SaveSecretsBundleWithEncryption(&bundle)
}

// runKubeconfigCmd runs the wrapped talosctl kubeconfig command
func runKubeconfigCmd() error {
	for _, cmd := range Commands {
		if cmd.Name() == "kubeconfig" {
			// Set --force to avoid interactive prompt
			cmd.Flags().Set("force", "true")
			return cmd.RunE(cmd, []string{})
		}
	}
	return fmt.Errorf("kubeconfig command not found")
}

// extractKubeconfigFromTarGz extracts kubeconfig from tar.gz archive
func extractKubeconfigFromTarGz(r io.Reader) ([]byte, error) {
	gzr, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("error creating gzip reader: %w", err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("error reading tar: %w", err)
		}

		if hdr.Name == "kubeconfig" {
			return io.ReadAll(tr)
		}
	}

	return nil, fmt.Errorf("kubeconfig not found in archive")
}
