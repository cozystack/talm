{{- define "talos.config" }}
{{- if and .TalosVersion (not (semverCompare "<1.12.0-0" .TalosVersion)) }}
{{- include "talos.config.multidoc" . }}
{{- else }}
{{- include "talos.config.legacy" . }}
{{- end }}
{{- end }}

{{- /* Shared machine section: type, kubelet, certSANs, install */ -}}
{{- define "talos.config.machine.common" }}
machine:
  type: {{ .MachineType }}
  kubelet:
    nodeIP:
      validSubnets:
        {{- if .Values.advertisedSubnets }}
        {{- toYaml .Values.advertisedSubnets | nindent 8 }}
        {{- else }}
        {{- /* Fall back to the subnet of the node's default-gateway-bearing
               link. cidrNetwork masks host bits so the emitted YAML is the
               canonical network form (192.168.201.0/24) rather than the
               host form (192.168.201.10/24). Dedupe after masking because
               a link with a secondary address in the same subnet would
               otherwise produce duplicate list entries. */ -}}
        {{- $addrs := fromJsonArray (include "talm.discovered.default_addresses_by_gateway" .) }}
        {{- if not $addrs }}
        {{- fail "values.yaml: `advertisedSubnets` was left empty and talm could not derive a default from discovery. No default-gateway-bearing link was found on the node. This field is a cluster-wide subnet selector fed to kubelet and etcd; `talm template` is invoked once per node and cannot merge per-node values into one cluster value. Either set advertisedSubnets explicitly in values.yaml, or ensure the node has a default route before running `talm template`." }}
        {{- end }}
        {{- $subnets := list }}
        {{- range $addrs }}
        {{- $subnets = append $subnets (. | cidrNetwork) }}
        {{- end }}
        {{- range uniq $subnets }}
        - {{ . }}
        {{- end }}
        {{- end }}
  {{- with .Values.certSANs }}
  certSANs:
  {{- toYaml . | nindent 2 }}
  {{- end }}
  install:
    {{- (include "talm.discovered.disks_info" .) | nindent 4 }}
    disk: {{ include "talm.discovered.system_disk_name" . | quote }}
{{- end }}

{{- /* Shared cluster section */ -}}
{{- define "talos.config.cluster" }}

cluster:
  network:
    podSubnets:
      {{- toYaml .Values.podSubnets | nindent 6 }}
    serviceSubnets:
      {{- toYaml .Values.serviceSubnets | nindent 6 }}
  clusterName: {{ include "talm.validate.dns1123subdomain" (dict "value" (.Values.clusterName | default .Chart.Name) "field" "clusterName") | quote }}
  controlPlane:
    endpoint: {{ required "values.yaml: `endpoint` must be set to the cluster control-plane URL (e.g. https://<vip>:6443). This field is cluster-wide: every node's kubelet and kube-proxy dials it, so it cannot be auto-derived from the current node's IP -- `talm template` runs once per node and has no way to reconcile per-node IPs into a single shared endpoint. For multi-node setups use a VIP or an external load balancer; for single-node clusters the node's routable IP works." .Values.endpoint | quote }}
  {{- if eq .MachineType "controlplane" }}
  apiServer:
    {{- with .Values.certSANs }}
    certSANs:
    {{- toYaml . | nindent 4 }}
    {{- end }}
  etcd:
    advertisedSubnets:
      {{- if .Values.advertisedSubnets }}
      {{- toYaml .Values.advertisedSubnets | nindent 6 }}
      {{- else }}
      {{- /* Fall back to the subnet of the node's default-gateway-bearing
             link; cidrNetwork masks host bits to emit canonical network
             form. Dedupe handled the same way as validSubnets above.
             Empty discovery already errored via validSubnets' required()
             guard, so we reach this block only when at least one address
             was resolved. */ -}}
      {{- $subnets := list }}
      {{- range fromJsonArray (include "talm.discovered.default_addresses_by_gateway" .) }}
      {{- $subnets = append $subnets (. | cidrNetwork) }}
      {{- end }}
      {{- range uniq $subnets }}
      - {{ . }}
      {{- end }}
      {{- end }}
  {{- end }}
{{- end }}

{{- /* Shared network document generation for v1.12+ multi-doc format */ -}}
{{- define "talos.config.network.multidoc" }}
{{- /* Multi-doc format reconstructs network config from discovery resources.
       Every configurable link on the node (physical NIC, bond, VLAN, bridge)
       gets its own document so a multi-NIC node ends up with all NICs
       configured rather than only the gateway-bearing one. The gateway-
       link's IPv4 default-route gateway is emitted only on that link's
       document; every other link gets its addresses without a default route.
       MTU is surfaced when discovery reports a value so non-default-MTU
       links (jumbo frames, GRE) survive a re-render.

       existing_interfaces_configuration is not consulted here: v1.12 nodes
       store network config in separate documents (LinkConfig, BondConfig,
       VLANConfig), not in the legacy machine.network.interfaces field. The
       guardrail below catches the upgrade case where a node was originally
       bootstrapped on a chart that emitted the legacy schema and still
       carries non-empty machine.network.interfaces[] in its running
       MachineConfig — the renderer cannot translate those entries today
       and would otherwise silently drop them on the next apply. */ -}}
{{- $legacyInterfaces := include "talm.discovered.existing_interfaces_configuration" . }}
{{- if $legacyInterfaces }}
{{- fail (printf "talm: the multi-doc renderer cannot translate legacy machine.network.interfaces[] from the running MachineConfig. Move the interfaces, vlans, and addresses below into per-node body overlays as v1.12 typed documents (LinkConfig, VLANConfig, BondConfig, RouteConfig) before re-running talm apply, or pin templateOptions.talosVersion to v1.11 in Chart.yaml until the translator lands.\n\nDetected legacy block:\n%s" $legacyInterfaces) }}
{{- end }}
{{- (include "talm.discovered.physical_links_info" .) }}
---
apiVersion: v1alpha1
kind: HostnameConfig
hostname: {{ include "talm.discovered.hostname" . | quote }}
---
apiVersion: v1alpha1
kind: ResolverConfig
nameservers:
{{- $resolvers := include "talm.discovered.default_resolvers" . }}
{{- if $resolvers }}
{{- range fromJsonArray $resolvers }}
  - address: {{ . | quote }}
{{- end }}
{{- else }}
  []
{{- end }}
{{- /* Validate floatingIP before either VIP-emission branch
       below depends on it. A typo here would otherwise silently
       fall through cidrContains (lenient on parse failure) into
       the default-link fallback, ship a Layer2VIPConfig with a
       nonsense `name:` value, and surface only at apply time.
       Render-time `fail` with the bad value is much cheaper to
       debug.

       Coerce through `toString` BEFORE the predicate. An unquoted
       numeric YAML scalar (`floatingIP: 192168`) parses as int,
       and ipIsValid is a Go function with a string parameter —
       passing an int would raise the Go-template
       "wrong type for value; expected string; got int" panic
       instead of the friendly fail message. The toString is also
       the safety net for any future operator yaml shape we have
       not yet thought of. */}}
{{- $fipStr := .Values.floatingIP | toString }}
{{- if and $fipStr (not (ipIsValid $fipStr)) (eq .MachineType "controlplane") }}
{{- fail (printf "talm: floatingIP %q is not a valid IPv4 / IPv6 literal. Edit values.yaml and re-run." $fipStr) }}
{{- end }}
{{- /* Operator-declared vipLink override: emit Layer2VIPConfig
       regardless of discovery state. Useful when the target link
       does not yet exist on the live system at first apply (typical
       case: a VLAN sub-interface this template is about to bring up).
       The discovery-derived block below skips its own Layer2VIPConfig
       when this branch fires, so we never emit duplicates. */}}
{{- if and .Values.floatingIP .Values.vipLink (eq .MachineType "controlplane") }}
---
apiVersion: v1alpha1
kind: Layer2VIPConfig
name: {{ .Values.floatingIP | quote }}
link: {{ .Values.vipLink }}
{{- end }}
{{- $defaultLinkName := include "talm.discovered.default_link_name_by_gateway" . }}
{{- $configurableLinks := fromJsonArray (include "talm.discovered.configurable_link_names" .) }}
{{- range $linkName := $configurableLinks }}
{{- $link := lookup "links" "" $linkName }}
{{- if $link }}
{{- $kind := $link.spec.kind | toString }}
{{- $isGatewayLink := eq $linkName $defaultLinkName }}
{{- $rawAddresses := fromJsonArray (include "talm.discovered.addresses_by_link" $linkName) }}
{{- /* Strip the operator-declared floatingIP from per-link addresses
       so the VIP currently held by this leader does not leak into
       LinkConfig.addresses. Talos's VIP operator installs the VIP
       as a regular global-scope address indistinguishable from a
       permanent one in COSI; without the filter, a re-render against
       the VIP-active node would declare the VIP both as a permanent
       address and as the Layer2VIPConfig target, putting the leader
       and follower configs out of sync. */}}
{{- $addresses := list }}
{{- range $rawAddresses }}
{{- if not (and $.Values.floatingIP (hasPrefix (printf "%s/" $.Values.floatingIP) .)) }}
{{- $addresses = append $addresses . }}
{{- end }}
{{- end }}
{{- $linkGateway := "" }}
{{- if $isGatewayLink }}
{{- $linkGateway = include "talm.discovered.gateway_by_link" $linkName }}
{{- end }}
{{- if eq $kind "bridge" }}
{{- /* BridgeConfig emission. Discovers bridge ports (members) via
       talm.discovered.bridge_slaves and emits a typed v1.12+
       BridgeConfig document with the same address / route / mtu
       shape as the other branches. STP and VLAN filtering are
       opt-in: they are emitted only when the bridge controller
       reported a non-nil spec.bridgeMaster.stp / spec.bridgeMaster
       value, so a default-state bridge stays minimal. */ -}}
{{- $bridgeMaster := $link.spec.bridgeMaster }}
{{- $bridgePorts := fromJsonArray (include "talm.discovered.bridge_slaves" $link.spec.index) }}
---
apiVersion: v1alpha1
kind: BridgeConfig
name: {{ $linkName }}
{{- if $bridgePorts }}
links:
{{- range $bridgePorts }}
  - {{ . }}
{{- end }}
{{- end }}
{{- if $bridgeMaster }}
{{- if $bridgeMaster.stp }}
{{- if hasKey $bridgeMaster.stp "enabled" }}
stp:
  enabled: {{ $bridgeMaster.stp.enabled }}
{{- end }}
{{- end }}
{{- /* COSI's BridgeVLANSpec serialises FilteringEnabled as
       yaml:"filteringEnabled" (verified against
       siderolabs/talos pkg/machinery/resources/network/link.go).
       The output-side BridgeConfig schema uses the shorter
       yaml:"filtering,omitempty" key — so we read the long form
       from discovery and emit the short form into the rendered
       document. */ -}}
{{- if $bridgeMaster.vlan }}
{{- if hasKey $bridgeMaster.vlan "filteringEnabled" }}
vlan:
  filtering: {{ $bridgeMaster.vlan.filteringEnabled }}
{{- end }}
{{- end }}
{{- end }}
{{- if $addresses }}
addresses:
{{- range $addresses }}
  - address: {{ . }}
{{- end }}
{{- end }}
{{- if $linkGateway }}
routes:
  - gateway: {{ $linkGateway }}
{{- end }}
{{- if $link.spec.mtu }}
mtu: {{ $link.spec.mtu }}
{{- end }}
{{- else if eq $kind "bond" }}
{{- $bondMaster := $link.spec.bondMaster }}
{{- $slaves := fromJsonArray (include "talm.discovered.bond_slaves" $link.spec.index) }}
---
apiVersion: v1alpha1
kind: BondConfig
name: {{ $linkName }}
links:
{{- range $slaves }}
  - {{ . }}
{{- end }}
{{- if $bondMaster }}
{{- if $bondMaster.mode }}
bondMode: {{ $bondMaster.mode }}
{{- end }}
{{- if $bondMaster.xmitHashPolicy }}
xmitHashPolicy: {{ $bondMaster.xmitHashPolicy }}
{{- end }}
{{- if $bondMaster.lacpRate }}
lacpRate: {{ $bondMaster.lacpRate }}
{{- end }}
{{- if $bondMaster.miimon }}
miimon: {{ $bondMaster.miimon }}
{{- end }}
{{- if $bondMaster.updelay }}
updelay: {{ $bondMaster.updelay }}
{{- end }}
{{- if $bondMaster.downdelay }}
downdelay: {{ $bondMaster.downdelay }}
{{- end }}
{{- end }}
{{- if $addresses }}
addresses:
{{- range $addresses }}
  - address: {{ . }}
{{- end }}
{{- end }}
{{- if $linkGateway }}
routes:
  - gateway: {{ $linkGateway }}
{{- end }}
{{- if $link.spec.mtu }}
mtu: {{ $link.spec.mtu }}
{{- end }}
{{- else if eq $kind "vlan" }}
{{- $parentLinkName := include "talm.discovered.parent_link_name" $linkName }}
{{- $vlanID := include "talm.discovered.vlan_id" $linkName }}
{{- if not $parentLinkName }}
{{- /* VLANConfig requires the parent field on the wire. Emitting one
       without it produces a document Talos rejects on apply. Treat the
       partial-discovery case as fail-fast — a VLAN with an unresolvable
       linkIndex is a discovery bug, not a config we can render. */ -}}
{{- fail (printf "talm: discovered VLAN %q has no resolvable parent link (spec.linkIndex points at a non-existent link). VLANConfig requires the parent field; refusing to emit an invalid document. Fix the discovery state or declare the VLAN explicitly via a per-node body overlay." $linkName) }}
{{- end }}
{{- if not $vlanID }}
{{- /* VLANConfig also requires vlanID. Symmetric guardrail to the
       missing-parent case above — discovery without spec.vlan.vlanID
       cannot produce a valid VLANConfig. */ -}}
{{- fail (printf "talm: discovered VLAN %q has no resolvable vlanID (spec.vlan.vlanID is unset). VLANConfig requires vlanID; refusing to emit an invalid document. Fix the discovery state or declare the VLAN explicitly via a per-node body overlay." $linkName) }}
{{- end }}
---
apiVersion: v1alpha1
kind: VLANConfig
name: {{ $linkName }}
vlanID: {{ $vlanID }}
parent: {{ $parentLinkName }}
{{- if $addresses }}
addresses:
{{- range $addresses }}
  - address: {{ . }}
{{- end }}
{{- end }}
{{- if $linkGateway }}
routes:
  - gateway: {{ $linkGateway }}
{{- end }}
{{- if $link.spec.mtu }}
mtu: {{ $link.spec.mtu }}
{{- end }}
{{- else }}
---
apiVersion: v1alpha1
kind: LinkConfig
name: {{ $linkName }}
{{- if $addresses }}
addresses:
{{- range $addresses }}
  - address: {{ . }}
{{- end }}
{{- end }}
{{- if $linkGateway }}
routes:
  - gateway: {{ $linkGateway }}
{{- end }}
{{- if $link.spec.mtu }}
mtu: {{ $link.spec.mtu }}
{{- end }}
{{- end }}
{{- end }}
{{- end }}
{{- /* Discovery-derived Layer2VIPConfig: skipped when the operator
       has set .Values.vipLink, since the override-path block above
       has already emitted the document with the operator's chosen
       link.

       Link selection prefers the link whose discovered addresses
       contain the floatingIP (talm.discovered.link_name_for_address),
       so a VIP in a private subnet hosted on a VLAN child lands on
       that VLAN — not on the IPv4-default-route NIC. The
       default-gateway link stays as the fallback for topologies
       where the VIP isn't on any discovered subnet (typical for
       upstream-routable VIPs that arrive via the default-route
       link). When neither resolves a link, no Layer2VIPConfig is
       emitted, matching the prior behaviour. */}}
{{- if and .Values.floatingIP (not .Values.vipLink) (eq .MachineType "controlplane") }}
{{- $vipLink := include "talm.discovered.link_name_for_address" .Values.floatingIP }}
{{- /* Default-gateway fallback must also point at a configurable
       link — otherwise an unmanaged default-route NIC (Wireguard,
       a slave link) would silently win selection and the rendered
       Layer2VIPConfig would dangle on a link the chart never emits
       a per-link document for. Mirror the same configurable-link
       gate link_name_for_address applies inside its own iteration. */ -}}
{{- if not $vipLink }}
{{- if has $defaultLinkName $configurableLinks }}
{{- $vipLink = $defaultLinkName }}
{{- end }}
{{- end }}
{{- if $vipLink }}
---
apiVersion: v1alpha1
kind: Layer2VIPConfig
name: {{ .Values.floatingIP | quote }}
link: {{ $vipLink }}
{{- end }}
{{- end }}
{{- end }}

{{- /* Shared legacy network section for machine.network */ -}}
{{- define "talos.config.network.legacy" }}
  network:
    hostname: {{ include "talm.discovered.hostname" . | quote }}
    nameservers: {{ include "talm.discovered.default_resolvers" . }}
    {{- (include "talm.discovered.physical_links_info" .) | nindent 4 }}
    {{- $existingInterfacesConfiguration := include "talm.discovered.existing_interfaces_configuration" . }}
    {{- $defaultLinkName := include "talm.discovered.default_link_name_by_gateway" . }}
    {{- /* vipLink override on the legacy schema: legacy Talos has no
       Layer2VIPConfig document, so the override is expressed as a
       top-level interfaces[] entry that carries only the vip block.
       When vipLink == $defaultLinkName the inline vip below already
       lands on the right link, so no override entry is needed. */}}
    {{- $vipOverride := and .Values.floatingIP .Values.vipLink (eq .MachineType "controlplane") (ne .Values.vipLink $defaultLinkName) }}
    {{- /* Suppress the inline (discovery-derived) vip when the operator
       has redirected it to a different link; otherwise the VIP would
       be pinned twice on different interfaces. */}}
    {{- $suppressInlineVip := and .Values.vipLink (ne .Values.vipLink $defaultLinkName) }}
    {{- if or $existingInterfacesConfiguration $defaultLinkName $vipOverride }}
    interfaces:
    {{- if $existingInterfacesConfiguration }}
    {{- $existingInterfacesConfiguration | nindent 4 }}
    {{- else if $defaultLinkName }}
    {{- $isVlan := include "talm.discovered.is_vlan" $defaultLinkName }}
    {{- $parentLinkName := "" }}
    {{- if $isVlan }}
    {{- $parentLinkName = include "talm.discovered.parent_link_name" $defaultLinkName }}
    {{- end }}
    {{- $interfaceName := $defaultLinkName }}
    {{- if and $isVlan $parentLinkName }}
    {{- $interfaceName = $parentLinkName }}
    {{- end }}
    - interface: {{ $interfaceName }}
      {{- $bondConfig := include "talm.discovered.bond_config" $interfaceName }}
      {{- if $bondConfig }}
      {{- $bondConfig | nindent 6 }}
      {{- end }}
      {{- if $isVlan }}
      vlans:
        - vlanId: {{ include "talm.discovered.vlan_id" $defaultLinkName }}
          addresses: {{ include "talm.discovered.default_addresses_by_gateway" . }}
          routes:
            - network: 0.0.0.0/0
              gateway: {{ include "talm.discovered.default_gateway" . }}
          {{- if and .Values.floatingIP (eq .MachineType "controlplane") (not $suppressInlineVip) }}
          vip:
            ip: {{ .Values.floatingIP }}
          {{- end }}
      {{- else }}
      addresses: {{ include "talm.discovered.default_addresses_by_gateway" . }}
      routes:
        - network: 0.0.0.0/0
          gateway: {{ include "talm.discovered.default_gateway" . }}
      {{- if and .Values.floatingIP (eq .MachineType "controlplane") (not $suppressInlineVip) }}
      vip:
        ip: {{ .Values.floatingIP }}
      {{- end }}
      {{- end }}
    {{- end }}
    {{- if $vipOverride }}
    - interface: {{ .Values.vipLink }}
      vip:
        ip: {{ .Values.floatingIP }}
    {{- end }}
    {{- end }}
{{- end }}

{{- define "talos.config.legacy" }}
{{- include "talos.config.machine.common" . }}
{{- include "talos.config.network.legacy" . }}

{{- include "talos.config.cluster" . }}
{{- end }}

{{- define "talos.config.multidoc" }}
{{- include "talos.config.machine.common" . }}

{{- include "talos.config.cluster" . }}
{{- include "talos.config.network.multidoc" . }}
{{- end }}
