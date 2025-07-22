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

render-template: build
	@echo "Rendering templates..."; \
	TALM_PATH="`pwd`/talm"; \
	TMPDIR="`mktemp -d -t talm-tmp.XXXXXX`"; \
	echo "Initializing template workspace in $$TMPDIR..."; \
	cd "$$TMPDIR"; \
	if [ ! -x "$$TALM_PATH" ]; then echo "Error: '$$TALM_PATH' not found or not executable."; exit 127; fi; \
	"$$TALM_PATH" init --preset cozystack; \
	echo; echo "--------- Rendering controlplane.yaml template ---------"; \
	"$$TALM_PATH" template -t templates/controlplane.yaml --offline $(SET_ARGS); \
	echo; echo "--------- Rendering worker.yaml template ---------"; \
	"$$TALM_PATH" template -t templates/worker.yaml --offline $(SET_ARGS); \
	echo "--------- controlplane and worker template rendering complete ---------"; \
	rm -rf "$$TMPDIR"