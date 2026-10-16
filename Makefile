# quai-node builds. A release binary is quai-node plus a payload (go-quai,
# and quai-dash where it exists) for one OS/arch:
#
#   make release GOOS=linux GOARCH=amd64     # needs zig for cgo
#   make release GOOS=darwin GOARCH=arm64    # on a Mac
#
# `make build` builds for this machine without rebuilding the payload; with
# no payload the binary runs but can't set up a node.

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GOOS    ?= $(shell go env GOOS)
GOARCH  ?= $(shell go env GOARCH)
EXT     := $(if $(filter windows,$(GOOS)),.exe,)
OUT     := dist/quai-node-$(VERSION)-$(GOOS)-$(GOARCH)$(EXT)
LDFLAGS := -s -w -X github.com/mpoletiek/quai-node/internal/cli.Version=$(VERSION)

.PHONY: build payload release test clean

build:
	CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) go build -trimpath -ldflags '$(LDFLAGS)' -o $(OUT) ./cmd/quai-node
	@echo "built $(OUT)"

payload:
	scripts/build-payload.sh $(GOOS) $(GOARCH)

release: payload build

test:
	go vet ./...
	go test ./...

clean:
	rm -rf dist build internal/bundle/payload/payload.tar.gz
