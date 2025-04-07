{{- define "talos.config" }}
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
      maxPods: 512
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
  files:
  - content: |
      [plugins]
        [plugins."io.containerd.grpc.v1.cri"]
          device_ownership_from_security_context = true
        [plugins."io.containerd.cri.v1.runtime"]
          device_ownership_from_security_context = true
    path: /etc/cri/conf.d/20-customization.part
    op: create
  install:
    {{- with .Values.image }}
    image: {{ . }}
    {{- end }}
    {{- (include "talm.discovered.disks_info" .) | nindent 4 }}
    disk: {{ include "talm.discovered.system_disk_name" . | quote }}
  network:
    hostname: {{ include "talm.discovered.hostname" . | quote }}
    nameservers: {{ include "talm.discovered.default_resolvers" . }}
    {{- (include "talm.discovered.physical_links_info" .) | nindent 4 }}
    interfaces:
    - deviceSelector:
        {{- include "talm.discovered.default_link_selector_by_gateway" . | nindent 8 }}
      addresses: {{ include "talm.discovered.default_addresses_by_gateway" . }}
      routes:
        - network: 0.0.0.0/0
          gateway: {{ include "talm.discovered.default_gateway" . }}
      {{- if eq .MachineType "controlplane" }}{{ with .Values.floatingIP }}
      vip:
        ip: {{ . }}
      {{- end }}{{ end }}

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
  proxy:
    disabled: true
  discovery:
    enabled: false
  etcd:
    advertisedSubnets:
      {{- toYaml .Values.advertisedSubnets | nindent 6 }}
  {{- end }}
{{- end }}
