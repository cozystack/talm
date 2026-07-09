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
    {{- with .Values.extraKubeletExtraArgs }}
    extraConfig:
      {{- toYaml . | nindent 6 }}
    {{- end }}
  {{- with .Values.certSANs }}
  certSANs:
  {{- toYaml . | nindent 2 }}
  {{- end }}
  {{- with .Values.extraSysctls }}
  sysctls:
    {{- toYaml . | nindent 4 }}
  {{- end }}
  {{- with .Values.extraKernelModules }}
  kernel:
    modules:
      {{- toYaml . | nindent 6 }}
  {{- end }}
  {{- with .Values.extraMachineFiles }}
  files:
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
{{- include "talm.config.network.multidoc" . }}
{{- end }}
{{- define "talos.config.network.legacy" }}
{{- /* Coerce floatingIP through toString and call the shared
       talm.validate_floatingIP partial so legacy renders fail at
       template time on a malformed value, same as the multi-doc
       path. $fipStr / $fipIsSet are reused below in place of every
       direct .Values.floatingIP reference. */ -}}
{{- $fipStr := .Values.floatingIP | toString }}
{{- $fipIsSet := and (ne $fipStr "") (ne $fipStr "<nil>") }}
{{- include "talm.validate_floatingIP" . }}
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
    {{- $vipOverride := and $fipIsSet .Values.vipLink (eq .MachineType "controlplane") (ne .Values.vipLink $defaultLinkName) }}
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
          {{- if and $fipIsSet (eq .MachineType "controlplane") (not $suppressInlineVip) }}
          vip:
            ip: {{ $fipStr }}
          {{- end }}
      {{- else }}
      addresses: {{ include "talm.discovered.default_addresses_by_gateway" . }}
      routes:
        - network: 0.0.0.0/0
          gateway: {{ include "talm.discovered.default_gateway" . }}
      {{- if and $fipIsSet (eq .MachineType "controlplane") (not $suppressInlineVip) }}
      vip:
        ip: {{ $fipStr }}
      {{- end }}
      {{- end }}
    {{- end }}
    {{- if $vipOverride }}
    - interface: {{ .Values.vipLink }}
      vip:
        ip: {{ $fipStr }}
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
