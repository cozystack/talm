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
{{- break }}
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
{{- if and .spec.busPath (regexMatch "^(eno|eth|enp|enx|ens)" (.metadata.id | toString)) }}
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
{{- if and (eq .spec.dst "") (not (eq .spec.gateway "")) (eq .spec.table "main") }}
{{- .spec.outLinkName }}
{{- break }}
{{- end }}
{{- end }}
{{- end }}

{{- define "talm.discovered.default_link_address_by_gateway" }}
{{- range (lookup "routes" "" "").items }}
{{- if and (eq .spec.dst "") (not (eq .spec.gateway "")) (eq .spec.table "main") }}
{{- (lookup "links" "" .spec.outLinkName).spec.hardwareAddr }}
{{- break }}
{{- end }}
{{- end }}
{{- end }}

{{- define "talm.discovered.default_link_bus_by_gateway" }}
{{- range (lookup "routes" "" "").items }}
{{- if and (eq .spec.dst "") (not (eq .spec.gateway "")) (eq .spec.table "main") }}
{{- (lookup "links" "" .spec.outLinkName).spec.hardwareAddr }}
{{- break }}
{{- end }}
{{- end }}
{{- end }}

{{- define "talm.discovered.default_link_selector_by_gateway" }}
{{- range (lookup "routes" "" "").items }}
{{- if and (eq .spec.dst "") (not (eq .spec.gateway "")) (eq .spec.table "main") }}
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

{{- /*
  Multi-NIC discovery helpers (#125).

  The `default_*_by_gateway` family above resolves only the link carrying the
  default route (primary). Templates targeting nodes with secondary NICs
  (storage links on a control-plane, second uplink, etc.) need to enumerate
  every physical link and read its addresses/routes/MAC by name. The helpers
  below are the by-name building blocks; existing default_*_by_gateway helpers
  remain wrappers that resolve the primary link and call into these.
*/ -}}

{{- /* JSON list of physical link names (raw NICs only — not bond/vlan masters). */ -}}
{{- define "talm.discovered.physical_links" -}}
{{- $names := list -}}
{{- range (lookup "links" "" "").items -}}
{{- if and .spec.busPath (regexMatch "^(eno|eth|enp|enx|ens)" (.metadata.id | toString)) -}}
{{- $names = append $names .metadata.id -}}
{{- end -}}
{{- end -}}
{{- toJson $names -}}
{{- end -}}

{{- /* JSON list of every link a user template can configure: physical NICs
       plus bond / vlan / bridge top-level links. */ -}}
{{- define "talm.discovered.configurable_links" -}}
{{- $names := list -}}
{{- range (lookup "links" "" "").items -}}
{{- $isPhysical := and .spec.busPath (regexMatch "^(eno|eth|enp|enx|ens)" (.metadata.id | toString)) -}}
{{- $isVirtual := has (.spec.kind | toString) (list "bond" "vlan" "bridge") -}}
{{- if or $isPhysical $isVirtual -}}
{{- $names = append $names .metadata.id -}}
{{- end -}}
{{- end -}}
{{- toJson $names -}}
{{- end -}}

{{- /* JSON list of CIDR addresses configured on the given link (any family),
       excluding host-scoped addresses. Caller is responsible for filtering
       VIPs or family if needed. */ -}}
{{- define "talm.discovered.addresses_by_link" -}}
{{- $linkName := . -}}
{{- $addresses := list -}}
{{- range (lookup "addresses" "" "").items -}}
{{- if and (eq .spec.linkName $linkName) (not (eq .spec.scope "host")) -}}
{{- $addresses = append $addresses .spec.address -}}
{{- end -}}
{{- end -}}
{{- toJson $addresses -}}
{{- end -}}

{{- /* Scalar gateway IP for the default route (dst="", main table) on the
       given link. Empty if no default route uses this link. */ -}}
{{- define "talm.discovered.gateway_by_link" -}}
{{- $linkName := . -}}
{{- range (lookup "routes" "" "").items -}}
{{- if and (eq .spec.outLinkName $linkName) (eq .spec.dst "") (not (eq .spec.gateway "")) (eq .spec.table "main") -}}
{{- .spec.gateway -}}
{{- break -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- /* JSON list of non-default routes on the given link. Each entry is a flat
       map {dst, gateway, family, table, priority} so consumers can
       fromJsonArray + range + dig. */ -}}
{{- define "talm.discovered.routes_by_link" -}}
{{- $linkName := . -}}
{{- $routes := list -}}
{{- range (lookup "routes" "" "").items -}}
{{- if and (eq .spec.outLinkName $linkName) (not (eq .spec.dst "")) -}}
{{- $entry := dict "dst" .spec.dst "gateway" (.spec.gateway | toString) "family" (.spec.family | toString) "table" (.spec.table | toString) "priority" (.spec.priority | toString) -}}
{{- $routes = append $routes $entry -}}
{{- end -}}
{{- end -}}
{{- toJson $routes -}}
{{- end -}}

{{- /* Scalar MAC address for the given link, or empty. */ -}}
{{- define "talm.discovered.mac_by_link" -}}
{{- $link := lookup "links" "" . -}}
{{- if $link -}}
{{- $link.spec.hardwareAddr | toString -}}
{{- end -}}
{{- end -}}

{{- /* Scalar PCI / bus path for the given link, or empty. */ -}}
{{- define "talm.discovered.bus_by_link" -}}
{{- $link := lookup "links" "" . -}}
{{- if $link -}}
{{- $link.spec.busPath | toString -}}
{{- end -}}
{{- end -}}

{{- /* YAML fragment `busPath: <path>` for use as a Talos deviceSelector by
       link name. Prefer this over emitting `interface:` when you need the
       config to be portable across renames (e.g. predictable network names). */ -}}
{{- define "talm.discovered.link_selector_by_name" -}}
{{- $link := lookup "links" "" . -}}
{{- if and $link $link.spec.busPath -}}
busPath: {{ $link.spec.busPath }}
{{- end -}}
{{- end -}}
