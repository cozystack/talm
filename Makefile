# Full describe provenance for display: a clean exact tag yields the release
# version; any other state carries the describe suffix and/or -dirty marker,
# which releaseVersion in main.go classifies as a non-release build — so
# release-only behavior (the chart-drift checks) stays off for WIP builds,
# whose embedded charts are a moving target, while `talm --version` still
# identifies the build in bug reports.
VERSION=$(shell git describe --tags --dirty 2>/dev/null || echo dev)
TALOS_VERSION=$(shell  go list -m github.com/siderolabs/talos | awk '{sub(/^v/, "", $$NF); print $$NF}')

build:
	go build -ldflags="-X 'main.Version=$(VERSION)'"
