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
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"math/big"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cockroachdb/errors"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"github.com/siderolabs/talos/pkg/machinery/client"
	clientconfig "github.com/siderolabs/talos/pkg/machinery/client/config"
)

// genTestCertKeyB64 generates an ephemeral self-signed ECDSA cert/key pair and
// returns each PEM block base64-encoded — the exact storage format a talosconfig
// context uses for its Crt and Key fields.
func genTestCertKeyB64(t *testing.T) (string, string) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "talm-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}

	crtPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}

	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	return base64.StdEncoding.EncodeToString(crtPEM), base64.StdEncoding.EncodeToString(keyPEM)
}

// TestSkipVerifyTLSConfig_NoCertKeyInsecureOnly documents that with an empty
// talosconfig context (no client cert/key) the config still skips server-cert
// verification but carries no client certificate.
func TestSkipVerifyTLSConfig_NoCertKeyInsecureOnly(t *testing.T) {
	cfg, err := skipVerifyTLSConfig(&clientconfig.Context{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !cfg.InsecureSkipVerify {
		t.Error("InsecureSkipVerify must be true — skipping server-cert verification is the point of --skip-verify")
	}

	if len(cfg.Certificates) != 0 {
		t.Errorf("expected no client certificate when context has none, got %d", len(cfg.Certificates))
	}
}

// TestSkipVerifyTLSConfig_WithCertKeyPreservesClientAuth is the core contract:
// server-cert verification is skipped WHILE client-certificate authentication is
// preserved from the talosconfig context.
func TestSkipVerifyTLSConfig_WithCertKeyPreservesClientAuth(t *testing.T) {
	crtB64, keyB64 := genTestCertKeyB64(t)

	cfg, err := skipVerifyTLSConfig(&clientconfig.Context{Crt: crtB64, Key: keyB64})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !cfg.InsecureSkipVerify {
		t.Error("InsecureSkipVerify must remain true even when a client cert is present")
	}

	if len(cfg.Certificates) != 1 {
		t.Fatalf("expected the client certificate to be preserved, got %d certificates", len(cfg.Certificates))
	}
}

// TestSkipVerifyTLSConfig_InvalidBase64Cert asserts a malformed base64 crt is a
// hard error rather than a silently insecure connection.
func TestSkipVerifyTLSConfig_InvalidBase64Cert(t *testing.T) {
	_, keyB64 := genTestCertKeyB64(t)

	if _, err := skipVerifyTLSConfig(&clientconfig.Context{Crt: "!!not-base64!!", Key: keyB64}); err == nil {
		t.Fatal("expected an error for an invalid base64 certificate")
	}
}

// TestSkipVerifyTLSConfig_InvalidBase64Key asserts a malformed base64 key errors.
func TestSkipVerifyTLSConfig_InvalidBase64Key(t *testing.T) {
	crtB64, _ := genTestCertKeyB64(t)

	if _, err := skipVerifyTLSConfig(&clientconfig.Context{Crt: crtB64, Key: "!!not-base64!!"}); err == nil {
		t.Fatal("expected an error for an invalid base64 key")
	}
}

// TestSkipVerifyTLSConfig_MismatchedKeyPair asserts a cert paired with a
// different private key fails instead of producing a broken client cert.
func TestSkipVerifyTLSConfig_MismatchedKeyPair(t *testing.T) {
	crtB64, _ := genTestCertKeyB64(t)
	_, otherKeyB64 := genTestCertKeyB64(t)

	if _, err := skipVerifyTLSConfig(&clientconfig.Context{Crt: crtB64, Key: otherKeyB64}); err == nil {
		t.Fatal("expected an error when the cert and key belong to different pairs")
	}
}

// stageMissingContextTalosconfig writes a talosconfig whose only context is
// "present", points GlobalArgs at it while requesting the absent context
// "absent", and toggles SkipVerify — restoring every mutated global on cleanup.
//
// It is a routing probe: a wrapper that reaches the local WithClientSkipVerify
// fails with errContextNotFound before dialing, whereas one that falls through
// to upstream global.Args surfaces a different error. That lets a test assert
// which path a client wrapper took without needing a live node.
func stageMissingContextTalosconfig(t *testing.T, skipVerify bool) {
	t.Helper()

	cfg := &clientconfig.Config{
		Context: "present",
		Contexts: map[string]*clientconfig.Context{
			"present": {Endpoints: []string{"127.0.0.1"}},
		},
	}

	cfgPath := filepath.Join(t.TempDir(), "talosconfig")
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatalf("save talosconfig fixture: %v", err)
	}

	origTalosconfig := GlobalArgs.Talosconfig
	origCmdContext := GlobalArgs.CmdContext
	origSkipVerify := SkipVerify
	t.Cleanup(func() {
		GlobalArgs.Talosconfig = origTalosconfig
		GlobalArgs.CmdContext = origCmdContext
		SkipVerify = origSkipVerify
	})

	GlobalArgs.Talosconfig = cfgPath
	GlobalArgs.CmdContext = "absent"
	SkipVerify = skipVerify
}

// TestWithClientSkipVerify_ContextNotFound covers the early-return branch: when
// the requested context is absent from the talosconfig, the wrapper fails with
// errContextNotFound before ever dialing a node.
func TestWithClientSkipVerify_ContextNotFound(t *testing.T) {
	stageMissingContextTalosconfig(t, true)

	err := WithClientSkipVerify(func(context.Context, *client.Client) error {
		t.Fatal("action must not run when the context is not found")

		return nil
	})

	if !errors.Is(err, errContextNotFound) {
		t.Fatalf("expected errContextNotFound, got %v", err)
	}
}

// TestWithClientNoNodes_SkipVerifyRoutes proves that with SkipVerify set the
// shared talm choke point reroutes through WithClientSkipVerify. This is the
// coverage the dropped cozystack/talos fork used to provide at the library
// level for every talm-native command; errContextNotFound is emitted only by
// the local WithClientSkipVerify, so seeing it proves the route was taken.
func TestWithClientNoNodes_SkipVerifyRoutes(t *testing.T) {
	stageMissingContextTalosconfig(t, true)

	err := WithClientNoNodes(func(context.Context, *client.Client) error {
		t.Fatal("action must not run — routing should hit WithClientSkipVerify's missing-context guard")

		return nil
	})

	if !errors.Is(err, errContextNotFound) {
		t.Fatalf("expected WithClientNoNodes to route through WithClientSkipVerify (errContextNotFound), got %v", err)
	}
}

// TestWithClient_SkipVerifyRoutes proves the same for WithClient — the wrapper
// every talm-native command (upgrade, rotate-ca, ...) funnels through — so
// --skip-verify is honored for them, not just apply/template.
func TestWithClient_SkipVerifyRoutes(t *testing.T) {
	stageMissingContextTalosconfig(t, true)

	err := WithClient(func(context.Context, *client.Client) error {
		t.Fatal("action must not run — routing should hit WithClientSkipVerify's missing-context guard")

		return nil
	})

	if !errors.Is(err, errContextNotFound) {
		t.Fatalf("expected WithClient to route through WithClientSkipVerify (errContextNotFound), got %v", err)
	}
}

// withGlobalArgsReset saves and restores the GlobalArgs fields the option-builder
// tests mutate, so they don't leak into other tests sharing the package global.
func withGlobalArgsReset(t *testing.T) {
	t.Helper()

	origCluster := GlobalArgs.Cluster
	origEndpoints := GlobalArgs.Endpoints
	t.Cleanup(func() {
		GlobalArgs.Cluster = origCluster
		GlobalArgs.Endpoints = origEndpoints
	})

	GlobalArgs.Cluster = ""
	GlobalArgs.Endpoints = nil
}

// TestSkipVerifyClientOptions_ClusterThreaded pins that --cluster is not lost on
// the skip-verify path: setting GlobalArgs.Cluster adds exactly one option
// (client.WithCluster), matching upstream global.Args.WithClientNoNodes. The
// option slice is the only observable surface — client.Options fields are
// unexported — so coverage is by option count.
func TestSkipVerifyClientOptions_ClusterThreaded(t *testing.T) {
	withGlobalArgsReset(t)

	base := skipVerifyClientOptions(&clientconfig.Context{}, &tls.Config{}, nil)
	if len(base) != 4 {
		t.Fatalf("baseline expected 4 options (config context + TLS + default gRPC + SideroV1 keys dir), got %d", len(base))
	}

	GlobalArgs.Cluster = "proxy-cluster"

	withCluster := skipVerifyClientOptions(&clientconfig.Context{}, &tls.Config{}, nil)
	if len(withCluster) != len(base)+1 {
		t.Errorf("--cluster must add one option: baseline %d, with cluster %d", len(base), len(withCluster))
	}
}

// TestSkipVerifyClientOptions_DialOptionsThreaded pins that caller dial options
// reach the skip-verify client instead of being silently dropped.
func TestSkipVerifyClientOptions_DialOptionsThreaded(t *testing.T) {
	withGlobalArgsReset(t)

	base := skipVerifyClientOptions(&clientconfig.Context{}, &tls.Config{}, nil)
	withDial := skipVerifyClientOptions(&clientconfig.Context{}, &tls.Config{}, []grpc.DialOption{grpc.WithUserAgent("talm-test")})

	if len(withDial) != len(base)+1 {
		t.Errorf("caller dial options must add one option: baseline %d, with dial %d", len(base), len(withDial))
	}
}

// TestSkipVerifyClientOptions_Endpoints pins endpoint selection: flag endpoints
// or, failing that, context endpoints add exactly one option; neither adds none.
func TestSkipVerifyClientOptions_Endpoints(t *testing.T) {
	withGlobalArgsReset(t)

	none := skipVerifyClientOptions(&clientconfig.Context{}, &tls.Config{}, nil)

	GlobalArgs.Endpoints = []string{"192.0.2.1"}
	fromFlag := skipVerifyClientOptions(&clientconfig.Context{Endpoints: []string{"192.0.2.2"}}, &tls.Config{}, nil)

	GlobalArgs.Endpoints = nil
	fromContext := skipVerifyClientOptions(&clientconfig.Context{Endpoints: []string{"192.0.2.2"}}, &tls.Config{}, nil)

	if len(fromFlag) != len(none)+1 || len(fromContext) != len(none)+1 {
		t.Errorf("endpoints must add exactly one option: none=%d flag=%d context=%d", len(none), len(fromFlag), len(fromContext))
	}
}

// TestWarnSkipVerifyUnsupported pins the passthrough scope-reduction warning:
// silent when the flag is off, and naming both the flag and the command when on.
func TestWarnSkipVerifyUnsupported(t *testing.T) {
	origSkipVerify := SkipVerify
	t.Cleanup(func() { SkipVerify = origSkipVerify })

	SkipVerify = false

	var offBuf bytes.Buffer

	warnSkipVerifyUnsupported(&offBuf, "health")

	if offBuf.Len() != 0 {
		t.Errorf("no warning expected when --skip-verify is off, got %q", offBuf.String())
	}

	SkipVerify = true

	var onBuf bytes.Buffer

	warnSkipVerifyUnsupported(&onBuf, "health")

	out := onBuf.String()
	if !strings.Contains(out, "--skip-verify") || !strings.Contains(out, "health") {
		t.Errorf("warning must name both --skip-verify and the command, got %q", out)
	}
	// Pin the full native-command list as one substring so the warning
	// text and the manual-test-plan D4 expected string cannot drift apart
	// (rotate-ca was dropped from the doc once already).
	if !strings.Contains(out, "apply, template, upgrade, rotate-ca") {
		t.Errorf("warning must list the full native command set, got %q", out)
	}
}

// stageValidSkipVerifyContext writes a talosconfig with a single reachable
// context "present" (carrying the given nodes), points GlobalArgs at it with
// SkipVerify on and no explicit --nodes / --endpoints, and restores every
// mutated global on cleanup. Unlike stageMissingContextTalosconfig, the context
// resolves — so client construction succeeds and the action closure runs,
// letting a test inspect the context the wrapper actually built.
func stageValidSkipVerifyContext(t *testing.T, nodes []string) {
	t.Helper()

	cfg := &clientconfig.Config{
		Context: "present",
		Contexts: map[string]*clientconfig.Context{
			"present": {
				Endpoints: []string{"127.0.0.1"},
				Nodes:     nodes,
			},
		},
	}

	cfgPath := filepath.Join(t.TempDir(), "talosconfig")
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatalf("save talosconfig fixture: %v", err)
	}

	origTalosconfig := GlobalArgs.Talosconfig
	origCmdContext := GlobalArgs.CmdContext
	origNodes := GlobalArgs.Nodes
	origEndpoints := GlobalArgs.Endpoints
	origSkipVerify := SkipVerify
	t.Cleanup(func() {
		GlobalArgs.Talosconfig = origTalosconfig
		GlobalArgs.CmdContext = origCmdContext
		GlobalArgs.Nodes = origNodes
		GlobalArgs.Endpoints = origEndpoints
		SkipVerify = origSkipVerify
	})

	GlobalArgs.Talosconfig = cfgPath
	GlobalArgs.CmdContext = ""
	GlobalArgs.Nodes = nil
	GlobalArgs.Endpoints = nil
	SkipVerify = true
}

// outgoingNodes returns the plural "nodes" gRPC metadata talos sets via
// client.WithNodes, or nil when none is present.
func outgoingNodes(ctx context.Context) []string {
	md, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		return nil
	}

	return md.Get("nodes")
}

// TestWithClient_SkipVerifyPreservesConfigContext pins that the skip-verify
// client carries the resolved talosconfig context, so downstream
// client.GetConfigContext() honors --talosconfig / --context instead of falling
// back to the default ~/.talos/config. Without WithConfigContext, WithClient's
// node-resolution closure (and upgrade/rotate-ca's own GetConfigContext calls)
// silently read the wrong config when no explicit --nodes is given.
func TestWithClient_SkipVerifyPreservesConfigContext(t *testing.T) {
	stageValidSkipVerifyContext(t, []string{"192.0.2.50"})

	var gotContext *clientconfig.Context

	err := WithClient(func(_ context.Context, c *client.Client) error {
		gotContext = c.GetConfigContext()

		return nil
	})
	if err != nil {
		t.Fatalf("WithClient returned error (config context likely fell back to default): %v", err)
	}

	if gotContext == nil {
		t.Fatal("GetConfigContext returned nil — the resolved config context was not threaded onto the skip-verify client")
	}

	if len(gotContext.Nodes) != 1 || gotContext.Nodes[0] != "192.0.2.50" {
		t.Errorf("expected the specified talosconfig context nodes [192.0.2.50], got %v", gotContext.Nodes)
	}
}

// TestWithClientNoNodes_SkipVerifyOmitsNodeMetadata pins the no-nodes contract:
// WithClientNoNodes (the skip-verify backing for COSI reads like rotate-ca's
// updateSecretsFromCluster) must NOT attach the plural `nodes` metadata that
// apid's director rejects — even when the talosconfig context carries nodes.
func TestWithClientNoNodes_SkipVerifyOmitsNodeMetadata(t *testing.T) {
	stageValidSkipVerifyContext(t, []string{"192.0.2.50"})

	var gotNodes []string

	err := WithClientNoNodes(func(ctx context.Context, _ *client.Client) error {
		gotNodes = outgoingNodes(ctx)

		return nil
	})
	if err != nil {
		t.Fatalf("WithClientNoNodes returned error: %v", err)
	}

	if len(gotNodes) != 0 {
		t.Errorf("WithClientNoNodes under --skip-verify must not set node metadata (no-nodes contract), got nodes=%v", gotNodes)
	}
}

// TestWithClient_SkipVerifySetsResolvedNodes is the complement: WithClient (the
// with-nodes layer) MUST inject the resolved context nodes so commands that
// need node targeting still work under --skip-verify without explicit --nodes.
func TestWithClient_SkipVerifySetsResolvedNodes(t *testing.T) {
	stageValidSkipVerifyContext(t, []string{"192.0.2.50"})

	var gotNodes []string

	err := WithClient(func(ctx context.Context, _ *client.Client) error {
		gotNodes = outgoingNodes(ctx)

		return nil
	})
	if err != nil {
		t.Fatalf("WithClient returned error: %v", err)
	}

	if len(gotNodes) != 1 || gotNodes[0] != "192.0.2.50" {
		t.Errorf("WithClient under --skip-verify must set the resolved context nodes [192.0.2.50], got %v", gotNodes)
	}
}
