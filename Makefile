VERSION=$(shell git describe --tags)
TALOS_VERSION=$(shell  go list -m github.com/siderolabs/talos | awk '{sub(/^v/, "", $$NF); print $$NF}')

build:
	go build -ldflags="-X 'main.Version=$(VERSION)'"
