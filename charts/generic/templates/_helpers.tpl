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
  {{- with .Values.timeServers }}
  time:
    servers:
      {{- toYaml . | nindent 6 }}
  {{- end }}
  {{- /* carry the running node's machine.network.interfaces
         verbatim when network.preserveExisting is set, ONLY on the v1.12+
         multi-doc schema. On legacy, talos.config.network.legacy already
         emits machine.network natively, so a second one here would be a
         duplicate key yaml.v3 rejects. */}}
  {{- $multidoc := and .TalosVersion (not (semverCompare "<1.12.0-0" .TalosVersion)) }}
  {{- if and $multidoc .Values.network .Values.network.preserveExisting }}
  {{- $existing := include "talm.discovered.existing_interfaces_configuration" . }}
  {{- if $existing }}
  network:
    interfaces:
      {{- $existing | nindent 6 }}
  {{- end }}
  {{- end }}
  {{- /* extraLinks is multi-doc only; fail fast on legacy rather
         than silently dropping the operator's declared links. */}}
  {{- if and (not $multidoc) .Values.network .Values.network.extraLinks }}
  {{- fail "talm: network.extraLinks is only supported on the v1.12+ multi-doc schema. Pin templateOptions.talosVersion to v1.12 or later, or declare the extra links via a per-node body overlay for legacy renders." }}
  {{- end }}
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
  {{- with .Values.extraControllerManagerArgs }}
  controllerManager:
    extraArgs:
      {{- /* Talos component extraArgs is map[string]string: coerce every
             value to a quoted string so an unquoted numeric is not emitted
             as a YAML int Talos rejects. range sorts keys for determinism. */}}
      {{- range $k, $v := . }}
      {{- if kindIs "invalid" $v }}
      {{- fail (printf "values.yaml: extraControllerManagerArgs.%s has no value. A bare `key:` renders as the literal string \"<nil>\" onto the component command line; give it a value or drop the key." $k) }}
      {{- end }}
      {{ $k }}: {{ $v | toString | quote }}
      {{- end }}
  {{- end }}
  {{- with .Values.extraSchedulerArgs }}
  scheduler:
    extraArgs:
      {{- range $k, $v := . }}
      {{- if kindIs "invalid" $v }}
      {{- fail (printf "values.yaml: extraSchedulerArgs.%s has no value. A bare `key:` renders as the literal string \"<nil>\" onto the component command line; give it a value or drop the key." $k) }}
      {{- end }}
      {{ $k }}: {{ $v | toString | quote }}
      {{- end }}
  {{- end }}
  apiServer:
    {{- with .Values.extraApiServerArgs }}
    extraArgs:
      {{- range $k, $v := . }}
      {{- if kindIs "invalid" $v }}
      {{- fail (printf "values.yaml: extraApiServerArgs.%s has no value. A bare `key:` renders as the literal string \"<nil>\" onto the component command line; give it a value or drop the key." $k) }}
      {{- end }}
      {{ $k }}: {{ $v | toString | quote }}
      {{- end }}
    {{- end }}
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
    {{- $existingLinkNames := fromJsonArray (include "talm.discovered.existing_interface_names" .) }}
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
    {{- if or $existingInterfacesConfiguration $defaultLinkName $vipOverride .Values.vips }}
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
    {{- if has (.Values.vipLink | toString) $existingLinkNames }}
    {{- /* The preserved interfaces block already emits this link verbatim;
           a second entry for the vipLink override would be a duplicate
           device Talos won't merge. */}}
    {{- fail (printf "talm: vipLink %q is already present in the node's running machine.network.interfaces (emitted verbatim on the legacy schema); the VIP override would produce a duplicate device Talos won't merge. Add the vip inline to that interface via a per-node body overlay, or pin templateOptions.talosVersion to v1.12+ where VIPs are separate Layer2VIPConfig documents." .Values.vipLink) }}
    {{- end }}
    - interface: {{ .Values.vipLink }}
      vip:
        ip: {{ $fipStr }}
    {{- end }}
    {{- /* one interface entry with an inline vip per vips entry
           (legacy schema has no Layer2VIPConfig document). Seed the seen
           link/ip sets with the vipLink override and floatingIP so a vips
           entry colliding with either is caught, matching multidoc. */}}
    {{- $seenVipLinks := list }}
    {{- if $vipOverride }}
    {{- $seenVipLinks = append $seenVipLinks (.Values.vipLink | toString) }}
    {{- end }}
    {{- $seenVipIPs := list }}
    {{- if $fipIsSet }}
    {{- $seenVipIPs = append $seenVipIPs $fipStr }}
    {{- end }}
    {{- range .Values.vips }}
    {{- if not (ipIsValid (.ip | toString)) }}
    {{- fail (printf "talm: vips[].ip %q is not a valid IPv4 / IPv6 literal. Edit values.yaml and re-run." .ip) }}
    {{- end }}
    {{- if not .link }}
    {{- fail (printf "talm: a vips entry (ip %q) has no link. Each vips entry must name the link the VIP is pinned to." (.ip | toString)) }}
    {{- end }}
    {{- /* Only meaningful when the rebuild actually emitted the primary
           link. When the preserved block was used instead, the primary has
           no rebuild-generated entry and the preserved-name check below is
           the one that applies (with the accurate message). */}}
    {{- if and (not $existingInterfacesConfiguration) (eq (.link | toString) $defaultLinkName) }}
    {{- /* The primary link already has its own interfaces[] entry; a
           second one for the same link is a duplicate device Talos does
           not merge. Refuse it and point at the right tool. */}}
    {{- fail (printf "talm: vips entry link %q is the discovered primary link, which already has an interfaces[] entry on the legacy schema; a second entry for it produces a duplicate device Talos won't merge. Use floatingIP for a VIP on the primary link, or pin templateOptions.talosVersion to v1.12+ where VIPs are separate Layer2VIPConfig documents." .link) }}
    {{- end }}
    {{- if has (.link | toString) $existingLinkNames }}
    {{- /* The preserved interfaces block (a re-apply of a legacy-applied
           node, or preserveExisting) already emits this link verbatim, so
           a vips entry naming it would double-declare the device. */}}
    {{- fail (printf "talm: vips entry link %q is already present in the node's running machine.network.interfaces (emitted verbatim on the legacy schema); a second entry for it produces a duplicate device Talos won't merge. Add the vip inline to that interface via a per-node body overlay, or pin templateOptions.talosVersion to v1.12+ where VIPs are separate Layer2VIPConfig documents." .link) }}
    {{- end }}
    {{- if has (.link | toString) $seenVipLinks }}
    {{- /* One inline vip per interface on legacy; two VIPs on one link
           (including the vipLink override) cannot both be expressed. */}}
    {{- fail (printf "talm: link %q already carries a VIP (via vipLink or another vips entry) on the legacy schema, which holds at most one inline vip per interface. Pin templateOptions.talosVersion to v1.12+ where each VIP is a separate Layer2VIPConfig document." .link) }}
    {{- end }}
    {{- if has (.ip | toString) $seenVipIPs }}
    {{- /* The same VIP ip on two links loses arbitration on apply; fail
           fast, matching the multidoc ip-uniqueness check. */}}
    {{- fail (printf "talm: VIP ip %q is declared more than once (across floatingIP and vips) on the legacy schema. Each VIP ip must be unique." (.ip | toString)) }}
    {{- end }}
    {{- $seenVipLinks = append $seenVipLinks (.link | toString) }}
    {{- $seenVipIPs = append $seenVipIPs (.ip | toString) }}
    - interface: {{ .link }}
      vip:
        ip: {{ .ip }}
    {{- end }}
    {{- end }}
{{- end }}

{{- define "talos.config.legacy" }}
{{- include "talos.config.machine.common" . }}
{{- include "talm.config.registries.legacy" . }}
{{- include "talos.config.network.legacy" . }}

{{- include "talos.config.cluster" . }}
{{- end }}

{{- define "talos.config.multidoc" }}
{{- include "talos.config.machine.common" . }}

{{- include "talos.config.cluster" . }}
{{- include "talm.config.registries.multidoc" . }}
{{- include "talos.config.network.multidoc" . }}
{{- end }}
