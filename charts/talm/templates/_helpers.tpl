{{- define "talm.discovered.system_disk_name" }}
{{- $systemDisk := (lookup "systemdisk" "" "system-disk") }}
{{- if $systemDisk }}
{{- $systemDisk.spec.devPath }}
{{- else }}
{{- $disk := "/dev/sda" }}
{{- range (lookup "disks" "" "").items }}
{{- if or .spec.wwid .spec.model }}
{{- $disk = .spec.dev_path }}
{{- break }}
{{- end }}
{{- end }}
{{- $disk }}
{{- end }}
{{- end }}

{{- define "talm.discovered.machinetype" }}
{{- (lookup "machinetype" "" "machine-type").spec }}
{{- end }}

{{- define "talm.discovered.hostname" }}
{{- $hostname := lookup "hostname" "" "hostname" }}
{{- if $hostname }}
{{- $hostname.spec.hostname }}
{{- else }}
{{- printf "talos-%s" (include "talm.discovered.default_addresses_by_gateway" . | sha256sum | trunc 5) }}
{{- end }}
{{- end }}

{{- define "talm.discovered.disks_info" }}
# -- Discovered disks:
{{- range (lookup "disks" "" "").items }}
{{- if or .spec.wwid .spec.model }}
# {{ .spec.dev_path }}:
#    model: {{ .spec.model }}
#    serial: {{ .spec.serial }}
#    wwid: {{ .spec.wwid }}
#    size: {{ .spec.pretty_size }}
{{- end }}
{{- end }}
{{- end }}

{{- define "talm.discovered.system_disk_nvme_id" }}
{{- $diskName := (include "talm.discovered.system_disk_name" .) }}
{{- $diskStablePath := "" }}
{{- range (lookup "disks" "" "").items }}
{{- if and (eq .spec.dev_path $diskName) (eq .spec.transport "nvme") .spec.wwid }}
{{- $diskStablePath = (printf "/dev/disk/by-id/nvme-%s" .spec.wwid) }}
{{- break }}
{{- end }}
{{- end }}
{{- if $diskStablePath }}
{{- $diskStablePath }}
{{- else }}
{{- $diskName }} # Unable to determine a stable NVMe disk path, falling back to the disk name
{{- end }}
{{- end }}

{{- define "talm.discovered.default_addresses" }}
{{- with (lookup "nodeaddress" "" "default") }}
{{- toJson .spec.addresses }}
{{- end }}
{{- end }}

{{- define "talm.discovered.default_addresses_by_gateway" }}
{{- $linkName := "" }}
{{- $family := "" }}
{{- range (lookup "routes" "" "").items }}
{{- if and (eq .spec.dst "") (not (eq .spec.gateway "")) (eq .spec.table "main") }}
{{- $linkName = .spec.outLinkName }}
{{- $family = .spec.family }}
{{- end }}
{{- end }}
{{- $addresses := list }}
{{- range (lookup "addresses" "" "").items }}
{{- if and (eq .spec.linkName $linkName) (eq .spec.family $family) (not (eq .spec.scope "host")) }}
{{- if not (hasPrefix (printf "%s/" $.Values.floatingIP) .spec.address) }}
{{- $addresses = append $addresses .spec.address }}
{{- end }}
{{- end }}
{{- end }}
{{- toJson $addresses }}
{{- end }}

{{- define "talm.discovered.physical_links_info" }}
# -- Discovered interfaces:
{{- range (lookup "links" "" "").items }}
{{- if and .spec.busPath (regexMatch "^(eno|eth|enp|enx|ens)" .metadata.id) }}
# {{ .metadata.id }}:
#   hardwareAddr:{{ .spec.hardwareAddr }}
#   busPath: {{ .spec.busPath }}
#   driver: {{ .spec.driver }}
#   vendor: {{ .spec.vendor }}
#   product: {{ .spec.product }})
{{- end }}
{{- end }}
{{- end }}

{{- define "talm.discovered.default_link_name" }}
{{- range (lookup "addresses" "" "").items }}
{{- if has .spec.address (fromJsonArray (include "talm.discovered.default_addresses" .)) }}
{{- .spec.linkName }}
{{- end }}
{{- end }}
{{- end }}

{{- define "talm.discovered.default_link_name_by_gateway" }}
{{- range (lookup "routes" "" "").items }}
{{- if and (eq .spec.dst "") (not (eq .spec.gateway "")) }}
{{- .spec.outLinkName }}
{{- end }}
{{- end }}
{{- end }}

{{- define "talm.discovered.default_link_address_by_gateway" }}
{{- range (lookup "routes" "" "").items }}
{{- if and (eq .spec.dst "") (not (eq .spec.gateway "")) }}
{{- (lookup "links" "" .spec.outLinkName).spec.hardwareAddr }}
{{- end }}
{{- end }}
{{- end }}

{{- define "talm.discovered.default_link_bus_by_gateway" }}
{{- range (lookup "routes" "" "").items }}
{{- if and (eq .spec.dst "") (not (eq .spec.gateway "")) }}
{{- (lookup "links" "" .spec.outLinkName).spec.hardwareAddr }}
{{- end }}
{{- end }}
{{- end }}

{{- define "talm.discovered.default_link_selector_by_gateway" }}
{{- range (lookup "routes" "" "").items }}
{{- if and (eq .spec.dst "") (not (eq .spec.gateway "")) }}
{{- with (lookup "links" "" .spec.outLinkName) }}
busPath: {{ .spec.busPath }}
{{- break }}
{{- end }}
{{- end }}
{{- end }}
{{- end }}

{{- define "talm.predictable_link_name" -}}
{{ printf "enx%s" (lookup "links" "" . | dig "spec" "hardwareAddr" . | replace ":" "") }}
{{- end }}

{{- define "talm.discovered.default_gateway" }}
{{- range (lookup "routes" "" "").items }}
{{- if and (eq .spec.dst "") (not (eq .spec.gateway "")) (eq .spec.table "main") }}
{{- .spec.gateway }}
{{- break }}
{{- end }}
{{- end }}
{{- end }}

{{- define "talm.discovered.default_resolvers" }}
{{- with (lookup "resolvers" "" "resolvers") }}
{{- toJson .spec.dnsServers }}
{{- end }}
{{- end }}

{{- define "talm.discovered.existing_interfaces_configuration" }}
{{- with (lookup "machineconfig" "" "v1alpha1") }}
{{- $spec := .spec }}
{{- $interfaces := list }}
{{- if kindIs "string" $spec }}
{{- $interfaces = $spec | fromYaml | dig "machine" "network" "interfaces" (list) }}
{{- else }}
{{- $interfaces = $spec | dig "machine" "network" "interfaces" (list) }}
{{- end }}
{{- if $interfaces }}
{{- $interfaces | toYaml }}
{{- end }}
{{- end }}
{{- end }}

{{- /* Get bond slave interfaces for a given bond index */ -}}
{{- define "talm.discovered.bond_slaves" -}}
{{- $bondIndex := . -}}
{{- $slaves := list -}}
{{- range (lookup "links" "" "").items -}}
{{- if and (eq .spec.slaveKind "bond") (eq (int .spec.masterIndex) (int $bondIndex)) -}}
{{- $slaves = append $slaves .metadata.id -}}
{{- end -}}
{{- end -}}
{{- toJson $slaves -}}
{{- end -}}

{{- /* Generate bond configuration from bondMaster spec */ -}}
{{- define "talm.discovered.bond_config" -}}
{{- $linkName := . -}}
{{- $link := lookup "links" "" $linkName -}}
{{- if and $link (eq $link.spec.kind "bond") -}}
{{- $bondMaster := $link.spec.bondMaster -}}
{{- $slaves := fromJsonArray (include "talm.discovered.bond_slaves" $link.spec.index) -}}
bond:
  interfaces:
    {{- range $slaves }}
    - {{ . }}
    {{- end }}
  mode: {{ $bondMaster.mode }}
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
{{- end -}}
{{- end -}}

{{- /* Check if a link is a bond interface */ -}}
{{- define "talm.discovered.is_bond" -}}
{{- $linkName := . -}}
{{- $link := lookup "links" "" $linkName -}}
{{- if and $link (eq $link.spec.kind "bond") -}}
true
{{- end -}}
{{- end -}}

{{- /* Check if a link is a vlan interface */ -}}
{{- define "talm.discovered.is_vlan" -}}
{{- $linkName := . -}}
{{- $link := lookup "links" "" $linkName -}}
{{- if and $link (eq $link.spec.kind "vlan") -}}
true
{{- end -}}
{{- end -}}

{{- /* Get parent link name by linkIndex */ -}}
{{- define "talm.discovered.parent_link_name" -}}
{{- $linkName := . -}}
{{- $link := lookup "links" "" $linkName -}}
{{- if and $link $link.spec.linkIndex -}}
{{- $parentIndex := $link.spec.linkIndex -}}
{{- range (lookup "links" "" "").items -}}
{{- if eq (int .spec.index) (int $parentIndex) -}}
{{- .metadata.id -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- /* Get vlan ID from link */ -}}
{{- define "talm.discovered.vlan_id" -}}
{{- $linkName := . -}}
{{- $link := lookup "links" "" $linkName -}}
{{- if and $link $link.spec.vlan -}}
{{- $link.spec.vlan.vlanID -}}
{{- end -}}
{{- end -}}

{{- /* Generate vlan configuration */ -}}
{{- define "talm.discovered.vlan_config" -}}
{{- $linkName := . -}}
{{- $link := lookup "links" "" $linkName -}}
{{- if and $link (eq $link.spec.kind "vlan") -}}
vlans:
  - vlanId: {{ $link.spec.vlan.vlanID }}
{{- end -}}
{{- end -}}
