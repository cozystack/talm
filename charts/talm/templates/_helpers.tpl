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
{{ toYaml .spec | fromYaml | dig "machine" "network" "interfaces" (list) | toYaml }}
{{- end }}
{{- end }}
