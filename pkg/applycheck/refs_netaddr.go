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

package applycheck

import (
	"bytes"
	"fmt"
	"io"
	"net/netip"
	"strconv"

	"github.com/cockroachdb/errors"
	yaml "gopkg.in/yaml.v3"
)

// netAddrHandler emits syntactic net-addr findings for one v1alpha1
// multidoc kind. Handlers are registered in multidocNetAddrHandlers
// and dispatched by WalkNetAddrFindings.
type netAddrHandler func(doc map[string]any, basePath string) []Finding

//nolint:gochecknoglobals // dispatch table for syntactic net-addr handlers; static after init.
var multidocNetAddrHandlers = map[string]netAddrHandler{
	"StaticHostConfig":  handleStaticHostConfigName,
	"NetworkRuleConfig": handleNetworkRuleConfigIngress,
	"WireguardConfig":   handleWireguardEndpoints,
}

// WalkNetAddrFindings parses the rendered MachineConfig bytes and
// returns a Finding for every malformed net-addr field in the three
// kinds registered above. Pure syntactic — no host snapshot
// required — so the walker runs in Phase 1 alongside (not inside)
// the Ref-based walker.
//
// Empty input and YAML decode of a nil document are no-ops; an
// actual YAML parse error is wrapped and returned. Unknown kinds
// are ignored so future Talos kinds and vendor extensions do not
// trip the gate.
func WalkNetAddrFindings(rendered []byte) ([]Finding, error) {
	if len(bytes.TrimSpace(rendered)) == 0 {
		return nil, nil
	}

	dec := yaml.NewDecoder(bytes.NewReader(rendered))

	var findings []Finding

	for docIndex := 0; ; docIndex++ {
		var doc map[string]any

		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return nil, errors.Wrapf(err, "applycheck: decoding YAML document %d for net-addr walk", docIndex)
		}

		if doc == nil {
			continue
		}

		kind, ok := doc["kind"].(string)
		if !ok {
			continue
		}

		handler, ok := multidocNetAddrHandlers[kind]
		if !ok {
			continue
		}

		findings = append(findings, handler(doc, fmt.Sprintf("doc[%d]", docIndex))...)
	}

	return findings, nil
}

// handleStaticHostConfigName validates StaticHostConfig.name as a
// parseable IP literal (IPv4 or IPv6). In Talos's v1alpha1 schema
// `StaticHostConfigV1Alpha1`, the IP literal lives in the `name`
// field (the document's meta-name doubling as the IP to which
// the host entries point) — there is NO separate `address` field,
// despite what one might assume from the docname.
//
// Missing or non-string names are silently skipped — Talos rejects
// those at the RPC layer with a clearer kind-specific message; the
// walker only catches present-but-malformed values.
func handleStaticHostConfigName(doc map[string]any, basePath string) []Finding {
	nameAny, ok := doc["name"]
	if !ok {
		return nil
	}

	nameStr, ok := nameAny.(string)
	if !ok {
		return nil
	}

	if nameStr == "" {
		return nil
	}

	if _, err := netip.ParseAddr(nameStr); err == nil {
		return nil
	}

	path := basePath + ".name"

	return []Finding{{
		Ref: Ref{
			Name:   nameStr,
			Source: path,
		},
		Severity: SeverityBlocker,
		Reason:   "StaticHostConfig.name is not a valid IP literal: " + quote(nameStr),
		Hint:     "expected IPv4 (e.g. 192.0.2.10) or IPv6 (e.g. 2001:db8::1); the `name` field on StaticHostConfig is the IP literal the hostnames map to",
	}}
}

// handleNetworkRuleConfigIngress validates the CIDR-shaped fields
// inside NetworkRuleConfig.ingress[]. In Talos's v1alpha1 schema
// `RuleConfigV1Alpha1`, each entry of `ingress` is an `IngressRule`
// with `subnet` (required CIDR) and `except` (optional CIDR). There
// is NO top-level `matchSourceAddress[]` field, despite operator
// intuition.
//
// A bare IP without /N is NOT accepted — Talos's contract is
// CIDR-shaped at this position. Missing ingress / empty list /
// missing subnet on a rule are no-ops at this layer (Talos
// validates structural requireds at the RPC).
func handleNetworkRuleConfigIngress(doc map[string]any, basePath string) []Finding {
	listAny, ok := doc["ingress"]
	if !ok {
		return nil
	}

	list, ok := listAny.([]any)
	if !ok {
		return nil
	}

	var findings []Finding

	for i, entry := range list {
		ruleMap, ok := entry.(map[string]any)
		if !ok {
			continue
		}

		findings = appendCIDRFindingIfPresent(findings, ruleMap, "subnet", basePath, i)
		findings = appendCIDRFindingIfPresent(findings, ruleMap, "except", basePath, i)
	}

	return findings
}

// appendCIDRFindingIfPresent reads ingress[i].<field> from the rule
// map and appends a finding when the value is a present-but-
// malformed string. Missing or empty values are no-ops (Talos
// requires `subnet`; absence triggers a clearer RPC error). Used
// for both `subnet` and `except`.
func appendCIDRFindingIfPresent(findings []Finding, rule map[string]any, field, basePath string, i int) []Finding {
	valAny, ok := rule[field]
	if !ok {
		return findings
	}

	cidrStr, ok := valAny.(string)
	if !ok {
		return findings
	}

	if cidrStr == "" {
		return findings
	}

	if _, err := netip.ParsePrefix(cidrStr); err == nil {
		return findings
	}

	path := fmt.Sprintf("%s.ingress[%d].%s", basePath, i, field)

	return append(findings, Finding{
		Ref: Ref{
			Name:   cidrStr,
			Source: path,
		},
		Severity: SeverityBlocker,
		Reason:   "NetworkRuleConfig.ingress[" + strconv.Itoa(i) + "]." + field + " is not a valid CIDR: " + quote(cidrStr),
		Hint:     "expected CIDR like 192.0.2.0/24 or 2001:db8::/32; a bare IP without /N is not accepted by Talos's NetworkRuleConfig schema",
	})
}

// handleWireguardEndpoints validates each peer's endpoint as a
// parseable host:port literal. Empty or missing endpoint values
// describe a listener-only remote peer (this side accepts but does
// not initiate) and are intentionally NOT findings.
//
// netip.ParseAddrPort accepts both IPv4 host:port and bracketed
// IPv6 [host]:port — the canonical Wireguard endpoint shapes.
func handleWireguardEndpoints(doc map[string]any, basePath string) []Finding {
	peersAny, ok := doc["peers"]
	if !ok {
		return nil
	}

	peers, ok := peersAny.([]any)
	if !ok {
		return nil
	}

	var findings []Finding

	for i, peer := range peers {
		peerMap, ok := peer.(map[string]any)
		if !ok {
			continue
		}

		endpointAny, ok := peerMap["endpoint"]
		if !ok {
			continue
		}

		endpointStr, ok := endpointAny.(string)
		if !ok {
			continue
		}

		if endpointStr == "" {
			continue
		}

		if _, err := netip.ParseAddrPort(endpointStr); err == nil {
			continue
		}

		path := fmt.Sprintf("%s.peers[%d].endpoint", basePath, i)

		findings = append(findings, Finding{
			Ref: Ref{
				Name:   endpointStr,
				Source: path,
			},
			Severity: SeverityBlocker,
			Reason:   "WireguardConfig.peers[" + strconv.Itoa(i) + "].endpoint is not a valid host:port: " + quote(endpointStr),
			Hint:     "expected IPv4:port (e.g. 192.0.2.10:51820) or [IPv6]:port (e.g. [2001:db8::1]:51820); hostnames must be resolved to a literal IP in the rendered config",
		})
	}

	return findings
}
