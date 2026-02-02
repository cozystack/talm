{{- define "talos.config" }}
machine:
  type: {{ .MachineType }}
  kubelet:
    nodeIP:
      validSubnets:
        {{- toYaml .Values.advertisedSubnets | nindent 8 }}
  {{- with .Values.certSANs }}
  certSANs:
  {{- toYaml . | nindent 2 }}
  {{- end }}
  install:
    {{- (include "talm.discovered.disks_info" .) | nindent 4 }}
    disk: {{ include "talm.discovered.system_disk_name" . | quote }}
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

cluster:
  network:
    podSubnets:
      {{- toYaml .Values.podSubnets | nindent 6 }}
    serviceSubnets:
      {{- toYaml .Values.serviceSubnets | nindent 6 }}
  clusterName: "{{ .Chart.Name }}"
  controlPlane:
    endpoint: "{{ .Values.endpoint }}"
  {{- if eq .MachineType "controlplane" }}
  apiServer:
    {{- with .Values.certSANs }}
    certSANs:
    {{- toYaml . | nindent 4 }}
    {{- end }}
  etcd:
    advertisedSubnets:
      {{- toYaml .Values.advertisedSubnets | nindent 6 }}
  {{- end }}
{{- end }}
