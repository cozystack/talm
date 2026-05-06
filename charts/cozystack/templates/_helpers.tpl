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
    extraConfig:
      cpuManagerPolicy: static
      maxPods: 512
  sysctls:
    {{- with $.Values.nr_hugepages }}
    vm.nr_hugepages: {{ . | quote }}
    {{- end }}
    net.ipv4.neigh.default.gc_thresh1: "4096"
    net.ipv4.neigh.default.gc_thresh2: "8192"
    net.ipv4.neigh.default.gc_thresh3: "16384"
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
         global_filter = [ "r|^/dev/drbd.*|", "r|^/dev/dm-.*|", "r|^/dev/zd.*|" ]
      }
  install:
    {{- with .Values.image }}
    image: {{ . }}
    {{- end }}
    {{- (include "talm.discovered.disks_info" .) | nindent 4 }}
    disk: {{ include "talm.discovered.system_disk_name" . | quote }}
{{- end }}

{{- /* Shared cluster section */ -}}
{{- define "talos.config.cluster" }}
cluster:
  network:
    cni:
      name: none
    dnsDomain: {{ .Values.clusterDomain }}
    podSubnets:
      {{- toYaml .Values.podSubnets | nindent 6 }}
    serviceSubnets:
      {{- toYaml .Values.serviceSubnets | nindent 6 }}
  clusterName: "{{ .Chart.Name }}"
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
  scheduler:
    extraArgs:
      bind-address: 0.0.0.0
  apiServer:
    {{- if and .Values.oidcIssuerUrl (ne .Values.oidcIssuerUrl "") }}
    extraArgs:
      oidc-issuer-url: "{{ .Values.oidcIssuerUrl }}"
      oidc-client-id: "kubernetes"
      oidc-username-claim: "preferred_username"
      oidc-groups-claim: "groups"
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
  {{- end }}
{{- end }}

{{- /* Shared network document generation for v1.12+ multi-doc format */ -}}
{{- define "talos.config.network.multidoc" }}
{{- /* Multi-doc format always reconstructs network config from discovery resources.
       existing_interfaces_configuration is not used here because v1.12 nodes store
       network config in separate documents (LinkConfig, BondConfig, etc.), not in
       the legacy machine.network.interfaces field. */ -}}
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
{{- if $defaultLinkName }}
{{- $isVlan := include "talm.discovered.is_vlan" $defaultLinkName }}
{{- $parentLinkName := "" }}
{{- if $isVlan }}
{{- $parentLinkName = include "talm.discovered.parent_link_name" $defaultLinkName }}
{{- end }}
{{- $interfaceName := $defaultLinkName }}
{{- if and $isVlan $parentLinkName }}
{{- $interfaceName = $parentLinkName }}
{{- end }}
{{- $isBondInterface := include "talm.discovered.is_bond" $interfaceName }}
{{- if $isBondInterface }}
{{- $link := lookup "links" "" $interfaceName }}
{{- if $link }}
{{- $bondMaster := $link.spec.bondMaster }}
{{- $slaves := fromJsonArray (include "talm.discovered.bond_slaves" $link.spec.index) }}
---
apiVersion: v1alpha1
kind: BondConfig
name: {{ $interfaceName }}
links:
{{- range $slaves }}
  - {{ . }}
{{- end }}
bondMode: {{ $bondMaster.mode }}
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
{{- if not $isVlan }}
addresses:
{{- range fromJsonArray (include "talm.discovered.default_addresses_by_gateway" .) }}
  - address: {{ . }}
{{- end }}
routes:
  - gateway: {{ include "talm.discovered.default_gateway" . }}
{{- end }}
{{- end }}
{{- end }}
{{- if $isVlan }}
---
apiVersion: v1alpha1
kind: VLANConfig
name: {{ $defaultLinkName }}
vlanID: {{ include "talm.discovered.vlan_id" $defaultLinkName }}
parent: {{ $interfaceName }}
addresses:
{{- range fromJsonArray (include "talm.discovered.default_addresses_by_gateway" .) }}
  - address: {{ . }}
{{- end }}
routes:
  - gateway: {{ include "talm.discovered.default_gateway" . }}
{{- else if not $isBondInterface }}
---
apiVersion: v1alpha1
kind: LinkConfig
name: {{ $interfaceName }}
addresses:
{{- range fromJsonArray (include "talm.discovered.default_addresses_by_gateway" .) }}
  - address: {{ . }}
{{- end }}
routes:
  - gateway: {{ include "talm.discovered.default_gateway" . }}
{{- end }}
{{- /* Discovery-derived Layer2VIPConfig: skipped when the operator
       has set .Values.vipLink, since the override-path block above
       has already emitted the document with the operator's chosen
       link. */}}
{{- if and .Values.floatingIP (not .Values.vipLink) (eq .MachineType "controlplane") }}
{{- $vipLinkName := $interfaceName }}
{{- if $isVlan }}
{{- $vipLinkName = $defaultLinkName }}
{{- end }}
---
apiVersion: v1alpha1
kind: Layer2VIPConfig
name: {{ .Values.floatingIP | quote }}
link: {{ $vipLinkName }}
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
    {{- if or $existingInterfacesConfiguration $defaultLinkName }}
    interfaces:
    {{- if $existingInterfacesConfiguration }}
    {{- $existingInterfacesConfiguration | nindent 4 }}
    {{- else }}
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
          {{- if and .Values.floatingIP (eq .MachineType "controlplane") }}
          vip:
            ip: {{ .Values.floatingIP }}
          {{- end }}
      {{- else }}
      addresses: {{ include "talm.discovered.default_addresses_by_gateway" . }}
      routes:
        - network: 0.0.0.0/0
          gateway: {{ include "talm.discovered.default_gateway" . }}
      {{- if and .Values.floatingIP (eq .MachineType "controlplane") }}
      vip:
        ip: {{ .Values.floatingIP }}
      {{- end }}
      {{- end }}
    {{- end }}
    {{- end }}
{{- end }}

{{- define "talos.config.legacy" }}
{{- include "talos.config.machine.common" . }}
  registries:
    mirrors:
      docker.io:
        endpoints:
        - https://mirror.gcr.io
{{- include "talos.config.network.legacy" . }}

{{- include "talos.config.cluster" . }}
{{- end }}

{{- define "talos.config.multidoc" }}
{{- include "talos.config.machine.common" . }}

{{- include "talos.config.cluster" . }}
---
apiVersion: v1alpha1
kind: RegistryMirrorConfig
name: docker.io
endpoints:
  - url: https://mirror.gcr.io
{{- include "talos.config.network.multidoc" . }}
{{- end }}
