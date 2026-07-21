{{- define "talos.config" }}
{{- if and .TalosVersion (not (semverCompare "<1.12.0-0" .TalosVersion)) }}
{{- include "talos.config.multidoc" . }}
{{- else }}
{{- include "talos.config.legacy" . }}
{{- end }}
{{- end }}

{{- /* Shared machine section: type, nodeLabels (controlplane), kubelet, sysctls, kernel, certSANs, files, install */ -}}
{{- define "talos.config.machine.common" }}
machine:
  {{- if eq .MachineType "controlplane" }}
  nodeLabels:
    node.kubernetes.io/exclude-from-external-load-balancers:
      $patch: delete
  {{- end }}
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
    {{- /* extraKubeletExtraArgs MUST NOT collide with the preset's
           built-in extraConfig keys — yaml.v3 (used by Talos config
           decode and by the upgrade-time body write-back) rejects
           duplicate map keys, so a silent merge would emit a config
           that cannot decode. Fail at render with a precise hint
           naming the offending key; operators wanting a different
           default fork the preset. */ -}}
    {{- range $k, $_ := .Values.extraKubeletExtraArgs }}
      {{- if or (eq $k "cpuManagerPolicy") (eq $k "maxPods") }}
        {{- fail (printf "values.yaml: extraKubeletExtraArgs.%s collides with the cozystack preset's built-in kubelet.extraConfig — keys never override (yaml.v3 rejects duplicate map keys on decode). Remove the entry from extraKubeletExtraArgs, or fork the chart preset if you need a different default." $k) }}
      {{- end }}
    {{- end }}
    extraConfig:
      cpuManagerPolicy: static
      maxPods: 512
      {{- with .Values.extraKubeletExtraArgs }}
      {{- toYaml . | nindent 6 }}
      {{- end }}
  {{- /* extraSysctls MUST NOT collide with the preset's built-in
         sysctls; same rationale as extraKubeletExtraArgs. $builtinSysctls
         is the single source of truth for the preset-owned keys — keep
         it in sync with the literal sysctls block rendered further down.

         Always-on DRBD/LINSTOR tuning: Cozystack always runs DRBD (the
         drbd module is loaded unconditionally below), and these knobs
         resolve the TCP-port exhaustion the Cozystack team observed on
         production clusters under DRBD reconnect storms (node reboots,
         resync). tcp_orphan_retries/tcp_fin_timeout speed up reclamation
         of orphaned and FIN-WAIT sockets so a reconnect storm cannot
         outrun cleanup; netdev_* widen the receive backlog so bursty
         replication traffic isn't dropped under load.

         vm.nr_hugepages is treated as preset-owned even when its gate
         (.Values.nr_hugepages) is inactive, so operators always route it
         through the dedicated `nr_hugepages` key. The tcp_keepalive_*
         triplet is preset-owned only while .Values.tcpKeepaliveTuning is
         set (see below), so it can be operator-supplied via extraSysctls
         when the toggle is off. */ -}}
  {{- $builtinSysctls := list
        "vm.nr_hugepages"
        "net.ipv4.neigh.default.gc_thresh1"
        "net.ipv4.neigh.default.gc_thresh2"
        "net.ipv4.neigh.default.gc_thresh3"
        "net.ipv4.tcp_orphan_retries"
        "net.ipv4.tcp_fin_timeout"
        "net.core.netdev_max_backlog"
        "net.core.netdev_budget"
        "net.core.netdev_budget_usecs" }}
  {{- if $.Values.tcpKeepaliveTuning }}
  {{- $builtinSysctls = concat $builtinSysctls (list
        "net.ipv4.tcp_keepalive_time"
        "net.ipv4.tcp_keepalive_intvl"
        "net.ipv4.tcp_keepalive_probes") }}
  {{- end }}
  {{- range $k, $_ := .Values.extraSysctls }}
    {{- if has $k $builtinSysctls }}
      {{- fail (printf "values.yaml: extraSysctls.%s collides with the cozystack preset's built-in machine.sysctls — keys never override (yaml.v3 rejects duplicate map keys on decode). Remove the entry from extraSysctls, or fork the chart preset if you need a different default." $k) }}
    {{- end }}
  {{- end }}
  sysctls:
    {{- with $.Values.nr_hugepages }}
    vm.nr_hugepages: {{ . | quote }}
    {{- end }}
    net.ipv4.neigh.default.gc_thresh1: "4096"
    net.ipv4.neigh.default.gc_thresh2: "8192"
    net.ipv4.neigh.default.gc_thresh3: "16384"
    net.ipv4.tcp_orphan_retries: "3"
    net.ipv4.tcp_fin_timeout: "30"
    net.core.netdev_max_backlog: "5000"
    net.core.netdev_budget: "600"
    net.core.netdev_budget_usecs: "8000"
    {{- if $.Values.tcpKeepaliveTuning }}
    net.ipv4.tcp_keepalive_time: "600"
    net.ipv4.tcp_keepalive_intvl: "10"
    net.ipv4.tcp_keepalive_probes: "6"
    {{- end }}
    {{- with .Values.extraSysctls }}
    {{- toYaml . | nindent 4 }}
    {{- end }}
  kernel:
    modules:
    - name: openvswitch
    - name: drbd
      parameters:
        - usermode_helper=disabled
    - name: zfs
    - name: spl
    - name: vfio_pci
    - name: vfio_iommu_type1
    {{- with .Values.extraKernelModules }}
    {{- toYaml . | nindent 4 }}
    {{- end }}
  certSANs:
  - 127.0.0.1
  {{- with .Values.certSANs }}
  {{- toYaml . | nindent 2 }}
  {{- end }}
  files:
  - content: |
      [plugins]
        [plugins."io.containerd.grpc.v1.cri"]
          device_ownership_from_security_context = true
        [plugins."io.containerd.cri.v1.runtime"]
          device_ownership_from_security_context = true
    path: /etc/cri/conf.d/20-customization.part
    op: create
  - op: overwrite
    path: /etc/lvm/lvm.conf
    permissions: 0o644
    content: |
      backup {
        backup = 0
        archive = 0
      }
      devices {
         global_filter = [ "r|^/dev/drbd.*|", "r|^/dev/dm-.*|", "r|^/dev/zd.*|", "r|^/dev/loop.*|" ]
      }
  {{- with .Values.extraMachineFiles }}
  {{- toYaml . | nindent 2 }}
  {{- end }}
  install:
    {{- with .Values.image }}
    image: {{ . }}
    {{- end }}
    {{- (include "talm.discovered.disks_info" .) | nindent 4 }}
    disk: {{ include "talm.discovered.system_disk_name" . | quote }}
  {{- with .Values.timeServers }}
  time:
    servers:
      {{- toYaml . | nindent 6 }}
  {{- end }}
  {{- /* carry the running node's machine.network.interfaces
         verbatim when network.preserveExisting is set, ONLY on the v1.12+
         multi-doc schema (where the typed per-link rebuild is skipped). On
         the legacy schema talos.config.network.legacy already emits a
         machine.network block natively, so a second one here would be a
         duplicate key yaml.v3 rejects — and the legacy renderer already
         preserves the running interfaces via its own short-circuit. */}}
  {{- $multidoc := and .TalosVersion (not (semverCompare "<1.12.0-0" .TalosVersion)) }}
  {{- if and $multidoc .Values.network .Values.network.preserveExisting }}
  {{- $existing := include "talm.discovered.existing_interfaces_configuration" . }}
  {{- if $existing }}
  network:
    interfaces:
      {{- $existing | nindent 6 }}
  {{- end }}
  {{- end }}
  {{- /* extraLinks renders typed documents only the multi-doc
         schema supports. On the legacy schema it would silently no-op,
         so fail fast instead of dropping the operator's declared links. */}}
  {{- if and (not $multidoc) .Values.network .Values.network.extraLinks }}
  {{- fail "talm: network.extraLinks is only supported on the v1.12+ multi-doc schema. Pin templateOptions.talosVersion to v1.12 or later, or declare the extra links via a per-node body overlay for legacy renders." }}
  {{- end }}
{{- end }}

{{- /* Shared cluster section */ -}}
{{- define "talos.config.cluster" }}
cluster:
  network:
    cni:
      name: none
    dnsDomain: {{ include "talm.validate.dns1123subdomain" (dict "value" .Values.clusterDomain "field" "clusterDomain") | quote }}
    podSubnets:
      {{- toYaml .Values.podSubnets | nindent 6 }}
    serviceSubnets:
      {{- toYaml .Values.serviceSubnets | nindent 6 }}
  clusterName: {{ include "talm.validate.dns1123subdomain" (dict "value" (.Values.clusterName | default .Chart.Name) "field" "clusterName") | quote }}
  controlPlane:
    endpoint: {{ required "values.yaml: `endpoint` must be set to the cluster control-plane URL (e.g. https://<vip>:6443). This field is cluster-wide: every node's kubelet and kube-proxy dials it, so it cannot be auto-derived from the current node's IP -- `talm template` runs once per node and has no way to reconcile per-node IPs into a single shared endpoint. For multi-node setups use a VIP (cozystack floatingIP) or an external load balancer; for single-node clusters the node's routable IP works." .Values.endpoint | quote }}
  {{- if eq .MachineType "controlplane" }}
  allowSchedulingOnControlPlanes: true
  controllerManager:
    extraArgs:
      bind-address: 0.0.0.0
      {{- if .Values.allocateNodeCIDRs }}
      allocate-node-cidrs: true
      cluster-cidr: "{{ join "," .Values.podSubnets }}"
      {{- else }}
      allocate-node-cidrs: false
      {{- end }}
      {{- range $k, $_ := .Values.extraControllerManagerArgs }}
      {{- /* cluster-cidr is preset-owned only when allocateNodeCIDRs is on
             (that is the only branch that emits it); with it off the
             operator may set cluster-cidr freely. */}}
      {{- if or (eq $k "bind-address") (eq $k "allocate-node-cidrs") (and (eq $k "cluster-cidr") $.Values.allocateNodeCIDRs) }}
      {{- fail (printf "values.yaml: extraControllerManagerArgs.%s collides with the cozystack preset's built-in controllerManager.extraArgs; drop it from extraControllerManagerArgs" $k) }}
      {{- end }}
      {{- end }}
      {{- /* Talos component extraArgs is map[string]string: coerce every
             value to a quoted string so an unquoted numeric (e.g.
             concurrent-gc-syncs: 30) is not emitted as a YAML int Talos
             rejects. range sorts keys, so output stays deterministic. */}}
      {{- range $k, $v := .Values.extraControllerManagerArgs }}
      {{- if kindIs "invalid" $v }}
      {{- fail (printf "values.yaml: extraControllerManagerArgs.%s has no value. A bare `key:` renders as the literal string \"<nil>\" onto the component command line; give it a value or drop the key." $k) }}
      {{- end }}
      {{ $k }}: {{ $v | toString | quote }}
      {{- end }}
  scheduler:
    extraArgs:
      bind-address: 0.0.0.0
      {{- range $k, $_ := .Values.extraSchedulerArgs }}
      {{- if eq $k "bind-address" }}
      {{- fail (printf "values.yaml: extraSchedulerArgs.%s collides with the cozystack preset's built-in scheduler.extraArgs; drop it from extraSchedulerArgs" $k) }}
      {{- end }}
      {{- end }}
      {{- /* Coerce values to quoted strings (Talos extraArgs is
             map[string]string); range sorts keys for determinism. */}}
      {{- range $k, $v := .Values.extraSchedulerArgs }}
      {{- if kindIs "invalid" $v }}
      {{- fail (printf "values.yaml: extraSchedulerArgs.%s has no value. A bare `key:` renders as the literal string \"<nil>\" onto the component command line; give it a value or drop the key." $k) }}
      {{- end }}
      {{ $k }}: {{ $v | toString | quote }}
      {{- end }}
  apiServer:
    {{- $oidcSet := and .Values.oidcIssuerUrl (ne .Values.oidcIssuerUrl "") }}
    {{- if or $oidcSet .Values.extraApiServerArgs }}
    extraArgs:
      {{- if $oidcSet }}
      oidc-issuer-url: "{{ .Values.oidcIssuerUrl }}"
      oidc-client-id: "kubernetes"
      oidc-username-claim: "preferred_username"
      oidc-groups-claim: "groups"
      {{- end }}
      {{- range $k, $_ := .Values.extraApiServerArgs }}
      {{- if and $oidcSet (has $k (list "oidc-issuer-url" "oidc-client-id" "oidc-username-claim" "oidc-groups-claim")) }}
      {{- fail (printf "values.yaml: extraApiServerArgs.%s collides with the cozystack preset's built-in apiServer OIDC args (active because oidcIssuerUrl is set); drop it from extraApiServerArgs" $k) }}
      {{- end }}
      {{- end }}
      {{- /* Coerce values to quoted strings (Talos extraArgs is
             map[string]string); range sorts keys for determinism. */}}
      {{- range $k, $v := .Values.extraApiServerArgs }}
      {{- if kindIs "invalid" $v }}
      {{- fail (printf "values.yaml: extraApiServerArgs.%s has no value. A bare `key:` renders as the literal string \"<nil>\" onto the component command line; give it a value or drop the key." $k) }}
      {{- end }}
      {{ $k }}: {{ $v | toString | quote }}
      {{- end }}
    {{- end }}
    certSANs:
    - 127.0.0.1
    {{- with .Values.certSANs }}
    {{- toYaml . | nindent 4 }}
    {{- end }}
  proxy:
    disabled: true
  discovery:
    enabled: false
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
    {{- /* etcd backend quota, tunable via values. Raises etcd's 2GiB
           default backend ceiling so a LINSTOR-heavy control plane —
           thousands of DRBD-resource CRDs in aggregate — does not trip
           etcd's NOSPACE alarm and drop into read-only mode. This is a
           ceiling, not a reservation: a small cluster's DB stays small
           and costs no extra RAM/disk. 8GiB is etcd's documented upper
           bound (it warns above that). Blank the value to fall back to
           etcd's own default. Note: this governs total DB size, not the
           size of any single object — per-object writes are still gated
           by kube-apiserver's fixed 3MiB request-body limit. */ -}}
    {{- with (.Values.etcd | default dict).quotaBackendBytes }}
    extraArgs:
      quota-backend-bytes: {{ . | quote }}
    {{- end }}
  {{- end }}
{{- end }}

{{- /* Shared network document generation for v1.12+ multi-doc format */ -}}
{{- define "talos.config.network.multidoc" }}
{{- include "talm.config.network.multidoc" . }}
{{- end }}

{{- /* Shared legacy network section for machine.network */ -}}
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
           entry that collides with either is caught, matching the multidoc
           path's dedup. */}}
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
    {{- /* On legacy the primary link already has its own interfaces[]
           entry carrying addresses/routes; a second entry for the same
           link would be a duplicate device Talos does not merge. Refuse
           it and point at the right tool for a primary-link VIP. */}}
    {{- fail (printf "talm: vips entry link %q is the discovered primary link, which already has an interfaces[] entry on the legacy schema; a second entry for it produces a duplicate device Talos won't merge. Use floatingIP for a VIP on the primary link, or pin templateOptions.talosVersion to v1.12+ where VIPs are separate Layer2VIPConfig documents." .link) }}
    {{- end }}
    {{- if has (.link | toString) $existingLinkNames }}
    {{- /* The preserved interfaces block (a re-apply of a legacy-applied
           node, or preserveExisting) already emits this link verbatim, so
           a vips entry naming it would double-declare the device. Fail
           fast the same way the primary-link collision does. */}}
    {{- fail (printf "talm: vips entry link %q is already present in the node's running machine.network.interfaces (emitted verbatim on the legacy schema); a second entry for it produces a duplicate device Talos won't merge. Add the vip inline to that interface via a per-node body overlay, or pin templateOptions.talosVersion to v1.12+ where VIPs are separate Layer2VIPConfig documents." .link) }}
    {{- end }}
    {{- if has (.link | toString) $seenVipLinks }}
    {{- /* The legacy interfaces[].vip holds a single IP per interface, so
           two VIPs on one link (including the vipLink override) cannot both
           be expressed. Fail fast rather than emit a duplicate device. */}}
    {{- fail (printf "talm: link %q already carries a VIP (via vipLink or another vips entry) on the legacy schema, which holds at most one inline vip per interface. Pin templateOptions.talosVersion to v1.12+ where each VIP is a separate Layer2VIPConfig document." .link) }}
    {{- end }}
    {{- if has (.ip | toString) $seenVipIPs }}
    {{- /* The same VIP ip pinned to two links loses arbitration on apply
           (both interfaces claim it). Fail fast, matching the multidoc
           path's ip-uniqueness check. */}}
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
