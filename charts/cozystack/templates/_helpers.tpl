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
        {{- toYaml .Values.advertisedSubnets | nindent 8 }}
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
    # TCP orphan handling
    net.ipv4.tcp_orphan_retries: "3"
    net.ipv4.tcp_fin_timeout: "30"
    # Network backlog
    net.core.netdev_max_backlog: "5000"
    net.core.netdev_budget: "600"
    net.core.netdev_budget_usecs: "8000"
    # TCP keepalive (early detection of dead connections)
    net.ipv4.tcp_keepalive_time: "600"
    net.ipv4.tcp_keepalive_intvl: "10"
    net.ipv4.tcp_keepalive_probes: "6"
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
    endpoint: "{{ .Values.endpoint }}"
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
      {{- toYaml .Values.advertisedSubnets | nindent 6 }}
    extraArgs:
      quota-backend-bytes: "8589934592"  # 8GiB - prevent etcd running out of space with large LINSTOR CRD datasets
      max-request-bytes: "10485760"      # 10MiB - allow larger CRD objects to be stored
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
{{- $defaultLinkName := include "talm.discovered.default_link_name_by_gateway" . }}
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
{{- $vipLinkName := $interfaceName }}
{{- if $isVlan }}
{{- $vipLinkName = $defaultLinkName }}
{{- end }}
{{- if and .Values.floatingIP (eq .MachineType "controlplane") }}
---
apiVersion: v1alpha1
kind: Layer2VIPConfig
name: {{ .Values.floatingIP | quote }}
link: {{ $vipLinkName }}
{{- end }}
{{- end }}

{{- /* Shared legacy network section for machine.network */ -}}
{{- define "talos.config.network.legacy" }}
  network:
    hostname: {{ include "talm.discovered.hostname" . | quote }}
    nameservers: {{ include "talm.discovered.default_resolvers" . }}
    {{- (include "talm.discovered.physical_links_info" .) | nindent 4 }}
    interfaces:
    {{- $existingInterfacesConfiguration := include "talm.discovered.existing_interfaces_configuration" . }}
    {{- if $existingInterfacesConfiguration }}
    {{- $existingInterfacesConfiguration | nindent 4 }}
    {{- else }}
    {{- $defaultLinkName := include "talm.discovered.default_link_name_by_gateway" . }}
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
