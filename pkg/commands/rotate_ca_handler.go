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
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/cosi-project/runtime/pkg/safe"
	"github.com/cozystack/talm/pkg/age"
	"github.com/cozystack/talm/pkg/secureperm"
	"github.com/siderolabs/crypto/x509"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/siderolabs/talos/pkg/machinery/client"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	secretsres "github.com/siderolabs/talos/pkg/machinery/resources/secrets"
)

// cobraDefValueFalse is the cobra flag default-value rendering of false.
const cobraDefValueFalse = "false"

// wrapRotateCACommand adds talm-specific handling to the rotate-ca command.
//
//nolint:gocognit,gocyclo,cyclop,funlen,nestif // cobra command wrapper with linear PreRunE+RunE branching over (single-node validation, auto-discover nodes, set --output, set --k8s-endpoint, post-rotate updates); each branch is short, splitting would lose the documented dispatch order.
func wrapRotateCACommand(wrappedCmd *cobra.Command, originalRunE func(*cobra.Command, []string) error) {
	// Update command description
	wrappedCmd.Long = `Rotates Talos and/or Kubernetes root Certificate Authorities.

This command must be run against a SINGLE control-plane node. The specified node
will be used to coordinate the CA rotation across the entire cluster.

The command works by:
1. Auto-discovering all cluster nodes (control-plane and workers) from Kubernetes API
2. Generating new CA certificates
3. Gracefully rolling out the new CAs to all nodes
4. Updating local configs (talosconfig, secrets.yaml, kubeconfig)

IMPORTANT: You must specify exactly ONE control-plane node via --endpoints/-e or --nodes
flags, or through a single config file (-f). The node must be a control-plane node.

By default, both Talos API CA and Kubernetes API CA are rotated. Use --talos=false
or --kubernetes=false to rotate only one of them.

The command runs in dry-run mode by default. Use --dry-run=false to perform actual rotation.`

	wrappedCmd.Example = `  # Dry-run CA rotation (recommended first step)
  talm rotate-ca -f nodes/controlplane-1.yaml

  # Actually perform the rotation
  talm rotate-ca -f nodes/controlplane-1.yaml --dry-run=false

  # Rotate only Talos API CA
  talm rotate-ca -f nodes/controlplane-1.yaml --kubernetes=false --dry-run=false

  # Rotate only Kubernetes API CA
  talm rotate-ca -f nodes/controlplane-1.yaml --talos=false --dry-run=false`

	// Disable --with-docs and --with-examples by default
	if f := wrappedCmd.Flags().Lookup("with-docs"); f != nil {
		f.DefValue = cobraDefValueFalse
		_ = wrappedCmd.Flags().Set("with-docs", "false")
	}

	if f := wrappedCmd.Flags().Lookup("with-examples"); f != nil {
		f.DefValue = cobraDefValueFalse
		_ = wrappedCmd.Flags().Set("with-examples", "false")
	}

	// Store original PreRunE to chain it
	originalPreRunE := wrappedCmd.PreRunE

	wrappedCmd.PreRunE = func(cmd *cobra.Command, args []string) error {
		// Populate GlobalArgs.Nodes from upstream's per-class node
		// flags (--init-node / --control-plane-nodes / --worker-nodes)
		// BEFORE chaining the original PreRunE. See
		// populateNodesFromPerClassFlags godoc for the chain-order
		// rationale (upstream WithClient guard reads from
		// taloscommands.GlobalArgs after the wrapper's sync; populating
		// after the sync leaves upstream blind to the per-class lists).
		// Upstream rotate-ca's contract is "exactly one CP node" — the
		// multi-node guard below catches the case where the populated
		// list ends up with more than one entry.
		populateNodesFromPerClassFlags(cmd)

		// Run original PreRunE first (processes modeline, syncs GlobalArgs, etc.)
		if originalPreRunE != nil {
			if err := originalPreRunE(cmd, args); err != nil {
				return err
			}
		}

		// Validate that only one endpoint/node is provided
		if len(GlobalArgs.Endpoints) > 1 {
			return errors.WithHint(
				errors.Newf("rotate-ca requires exactly one control-plane node, but %d endpoints were provided", len(GlobalArgs.Endpoints)),
				"the rotate-ca command coordinates CA rotation across the entire cluster from a single control-plane node; specify only one endpoint via --endpoints or a single config file",
			)
		}

		if len(GlobalArgs.Nodes) > 1 {
			return errors.WithHint(
				errors.Newf("rotate-ca requires exactly one control-plane node, but %d nodes were provided", len(GlobalArgs.Nodes)),
				"the rotate-ca command coordinates CA rotation across the entire cluster from a single control-plane node; specify only one node via --nodes, --control-plane-nodes with a single IP, or a single config file",
			)
		}

		return nil
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
				return errors.Wrap(err, "failed to auto-discover nodes")
			}

			if err := cmd.Flags().Set("control-plane-nodes", strings.Join(cpNodes, ",")); err != nil {
				return errors.Wrap(err, "failed to set control-plane-nodes")
			}

			if len(wNodes) > 0 {
				if err := cmd.Flags().Set("worker-nodes", strings.Join(wNodes, ",")); err != nil {
					return errors.Wrap(err, "failed to set worker-nodes")
				}
			}

			fmt.Fprintf(os.Stderr, "  Control plane: %v\n", cpNodes)
			fmt.Fprintf(os.Stderr, "  Workers: %v\n", wNodes)
		}

		// Set --output to project talosconfig
		if !cmd.Flags().Changed("output") {
			talosconfigPath := GlobalArgs.Talosconfig
			if talosconfigPath == "" {
				talosconfigPath = filepath.Join(Config.RootDir, "talosconfig")
			}

			if err := cmd.Flags().Set("output", talosconfigPath); err != nil {
				return errors.Wrap(err, "failed to set output")
			}
		}

		// Set --k8s-endpoint from GlobalArgs.Endpoints. Delegate to
		// normalizeEndpoint so the canonical form (including the IPv6
		// `[host]` no-port branch) matches the rest of the package
		// instead of re-implementing the trim-and-rejoin logic and
		// silently dropping the bracket-stripping branch.
		if !cmd.Flags().Changed("k8s-endpoint") && len(GlobalArgs.Endpoints) > 0 {
			k8sEndpoint := normalizeEndpoint(GlobalArgs.Endpoints[0])
			if err := cmd.Flags().Set("k8s-endpoint", k8sEndpoint); err != nil {
				return errors.Wrap(err, "failed to set k8s-endpoint")
			}
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

		// Use control plane node for COSI requests (not the external endpoint)
		cpNodes, _ := cmd.Flags().GetStringSlice("control-plane-nodes")

		var targetNode string
		if len(cpNodes) > 0 {
			targetNode = cpNodes[0]
		}

		// Update secrets.yaml with new CA from cluster
		if err := updateSecretsFromCluster(rotateTalos, rotateKubernetes, targetNode); err != nil {
			return errors.Wrap(err, "failed to update secrets.yaml")
		}

		// Update talosconfig.encrypted if it exists (talosconfig already updated by upstream)
		if rotateTalos {
			if err := updateTalosconfigEncryption(); err != nil {
				return errors.Wrap(err, "failed to update talosconfig.encrypted")
			}
		}

		// Update kubeconfig using talm kubeconfig
		if rotateKubernetes {
			fmt.Fprintf(os.Stderr, "> Updating kubeconfig...\n")

			if err := runKubeconfigCmd(); err != nil {
				return errors.Wrap(err, "failed to update kubeconfig")
			}
		}

		fmt.Fprintf(os.Stderr, "\n> CA rotation completed successfully!\n")

		return nil
	}
}

// discoverClusterNodes discovers control plane and worker nodes from the Kubernetes API.
//
//nolint:nonamedreturns // named returns document semantics (control-plane vs workers); naked returns are not used so renaming would only lose the documentation.
func discoverClusterNodes() (controlPlane, workers []string, err error) {
	// Get kubeconfig from cluster via talos API
	kubeconfigData, err := getKubeconfigFromTalos()
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to get kubeconfig")
	}

	// Update kubeconfig server endpoint to use our endpoint instead of VIP
	if len(GlobalArgs.Endpoints) > 0 {
		kubeconfigData, err = updateKubeconfigEndpoint(kubeconfigData, GlobalArgs.Endpoints[0])
		if err != nil {
			return nil, nil, errors.Wrap(err, "failed to update kubeconfig endpoint")
		}
	}

	// Create kubernetes client
	config, err := clientcmd.RESTConfigFromKubeConfig(kubeconfigData)
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to create kubernetes config")
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to create kubernetes client")
	}

	// List nodes
	nodes, err := clientset.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{})
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to list nodes")
	}

	//nolint:gocritic,varnamelen // 768-byte v1.Node copy is acceptable on rare CA rotation; loop variable name preserved for documentation.
	for _, node := range nodes.Items {
		// Get internal IP
		var ip string

		for _, addr := range node.Status.Addresses {
			if addr.Type == v1.NodeInternalIP {
				ip = addr.Address

				break
			}
		}

		if ip == "" {
			continue
		}

		// Check if control plane
		_, isControlPlane := node.Labels["node-role.kubernetes.io/control-plane"]
		if isControlPlane {
			controlPlane = append(controlPlane, ip)
		} else {
			workers = append(workers, ip)
		}
	}

	if len(controlPlane) == 0 {
		return nil, nil, errors.New("no control plane nodes found")
	}

	return controlPlane, workers, nil
}

// getKubeconfigFromTalos fetches kubeconfig from Talos API.
func getKubeconfigFromTalos() ([]byte, error) {
	var kubeconfigData []byte

	err := GlobalArgs.WithClient(func(ctx context.Context, c *client.Client) error {
		var err error

		kubeconfigData, err = c.Kubeconfig(ctx)
		if err != nil {
			return errors.Wrap(err, "failed to get kubeconfig")
		}

		return nil
	})

	//nolint:wrapcheck // forwarding talos/cobra error verbatim per the wrapper contract.
	return kubeconfigData, err
}

// updateKubeconfigEndpoint updates the server endpoint in kubeconfig bytes.
func updateKubeconfigEndpoint(kubeconfigData []byte, endpoint string) ([]byte, error) {
	config, err := clientcmd.Load(kubeconfigData)
	if err != nil {
		return nil, errors.Wrap(err, "failed to parse kubeconfig")
	}

	// Delegate to normalizeEndpoint so the canonical form matches the
	// k8s-endpoint flag set above (and every other normaliser in the
	// package). Re-implementing the trim-and-rejoin logic here used to
	// drop the IPv6 `[host]` no-port branch — same class of bug
	// nosprintfhostport surfaced for the talosctl wrapper.
	k8sEndpoint := normalizeEndpoint(endpoint)

	// Update server for all clusters
	for _, cluster := range config.Clusters {
		cluster.Server = k8sEndpoint
	}

	// Marshal back to bytes
	//nolint:wrapcheck // forwarding talos/cobra error verbatim per the wrapper contract.
	return clientcmd.Write(*config)
}

// updateSecretsFromCluster fetches new CA certificates from cluster and updates secrets.yaml.
// The third parameter (target node) is reserved for the future per-node
// mode but unused today (the call goes through WithClientNoNodes to
// avoid the multi-node proxying COSI does not support).
//
//nolint:funlen // single linear secrets.yaml regeneration (load bundle, fetch CA via WithClient, encode, write) — extracting helpers would split the error-context wrapping across files.
func updateSecretsFromCluster(updateTalos, updateKubernetes bool, _ string) error {
	secretsPath := ResolveSecretsPath(Config.TemplateOptions.WithSecrets)

	// Load existing secrets
	bundle, err := secrets.LoadBundle(secretsPath)
	if err != nil {
		return errors.Wrap(err, "failed to load secrets bundle")
	}

	// Use WithClientNoNodes to avoid automatic node setting - COSI doesn't support multi-node proxying
	err = WithClientNoNodes(func(ctx context.Context, c *client.Client) error {
		// Fetch Talos CA if needed
		if updateTalos {
			fmt.Fprintf(os.Stderr, "  Fetching Talos CA from cluster...\n")

			osRoot, err := safe.StateGetByID[*secretsres.OSRoot](ctx, c.COSI, secretsres.OSRootID)
			if err != nil {
				return errors.Wrap(err, "failed to get OSRoot")
			}

			bundle.Certs.OS = &x509.PEMEncodedCertificateAndKey{
				Crt: osRoot.TypedSpec().IssuingCA.Crt,
				Key: osRoot.TypedSpec().IssuingCA.Key,
			}
		}

		// Fetch Kubernetes CA if needed
		if updateKubernetes {
			fmt.Fprintf(os.Stderr, "  Fetching Kubernetes CA from cluster...\n")

			k8sRoot, err := safe.StateGetByID[*secretsres.KubernetesRoot](ctx, c.COSI, secretsres.KubernetesRootID)
			if err != nil {
				return errors.Wrap(err, "failed to get KubernetesRoot")
			}

			bundle.Certs.K8s = &x509.PEMEncodedCertificateAndKey{
				Crt: k8sRoot.TypedSpec().IssuingCA.Crt,
				Key: k8sRoot.TypedSpec().IssuingCA.Key,
			}
		}

		return nil
	})
	if err != nil {
		return err
	}

	// Save secrets.yaml
	data, err := yaml.Marshal(bundle)
	if err != nil {
		return errors.Wrap(err, "failed to marshal secrets")
	}

	if err := secureperm.WriteFile(secretsPath, data); err != nil {
		return errors.Wrap(err, "failed to write secrets.yaml")
	}

	fmt.Fprintf(os.Stderr, "  Updated secrets.yaml\n")

	// Update secrets.encrypted.yaml if it exists
	encryptedPath := filepath.Join(Config.RootDir, "secrets.encrypted.yaml")

	keyFile := filepath.Join(Config.RootDir, "talm.key")
	if fileExists(encryptedPath) && fileExists(keyFile) {
		if err := age.EncryptSecretsFile(Config.RootDir); err != nil {
			return errors.Wrap(err, "failed to encrypt secrets.yaml")
		}

		fmt.Fprintf(os.Stderr, "  Updated secrets.encrypted.yaml\n")
	}

	return nil
}

// updateTalosconfigEncryption updates talosconfig.encrypted if it exists.
func updateTalosconfigEncryption() error {
	encryptedPath := filepath.Join(Config.RootDir, "talosconfig.encrypted")
	keyFile := filepath.Join(Config.RootDir, "talm.key")

	if !fileExists(encryptedPath) || !fileExists(keyFile) {
		return nil
	}

	fmt.Fprintf(os.Stderr, "  Updating talosconfig.encrypted...\n")

	if err := age.EncryptYAMLFile(Config.RootDir, "talosconfig", "talosconfig.encrypted"); err != nil {
		return errors.Wrap(err, "failed to encrypt talosconfig")
	}

	return nil
}

// runKubeconfigCmd runs the wrapped talosctl kubeconfig command.
func runKubeconfigCmd() error {
	for _, cmd := range Commands {
		if cmd.Name() == defaultKubeconfigName {
			// Set --force to avoid interactive prompt
			if cmd.Flags().Lookup("force") != nil {
				if err := cmd.Flags().Set("force", "true"); err != nil {
					return errors.Wrap(err, "failed to set force flag")
				}
			}

			//nolint:wrapcheck // forwarding talos/cobra error verbatim per the wrapper contract.
			return cmd.RunE(cmd, []string{})
		}
	}

	return errors.New("kubeconfig command not found")
}
