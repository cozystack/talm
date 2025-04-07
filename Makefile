VERSION=$(shell git describe --tags)
TALOS_VERSION=$(shell  go list -m github.com/siderolabs/talos | awk '{sub(/^v/, "", $$NF); print $$NF}')

generate:
	go generate

build:
	go build -ldflags="-X 'main.Version=$(VERSION)'"

import: import-internal import-commands

import-commands:
	go run tools/import_commands.go --talos-version v$(TALOS_VERSION) \
    bootstrap \
    containers \
    dashboard \
    disks \
    dmesg \
    events \
    get \
    health \
    image \
    kubeconfig \
    list \
    logs \
    memory \
    mounts \
    netstat \
    pcap \
    processes \
    read \
    reboot \
    reset \
    restart \
    rollback \
    service \
    shutdown \
    stats \
    time \
    copy \
    meta \
    edit \
    rollback \
    rotate-ca \
    support \
    wipe \
    diskusage \
    version

import-internal:
	rm -rf internal/pkg internal/app
	wget -O- https://github.com/siderolabs/talos/archive/refs/tags/v$(TALOS_VERSION).tar.gz | tar --strip=1 -xzf- \
		talos-$(TALOS_VERSION)/internal/app \
		talos-$(TALOS_VERSION)/internal/pkg
	rm -rf internal/app/init/ internal/pkg/rng/ internal/pkg/tui/
	sed -i 's|github.com/siderolabs/talos/internal|github.com/cozystack/talm/internal|g' `grep -rl 'github.com/siderolabs/talos/internal' internal`
