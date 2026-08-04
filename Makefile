GO ?= go
BINARY := fluxa-builder
BUILD_DIR := bin
WRAPPER_DIR := internal/wrapper/bin

.PHONY: all build wrapper test test-race vet lint check clean

all: check build

build: wrapper
	$(GO) build -trimpath -o $(BUILD_DIR)/$(BINARY) ./cmd/fluxa-builder

# Regenerates the committed "adapted runtime" relay binaries that
# internal/wrapper embeds (see docs/adr/0025-linux-adapted-runtime-wrapper.md).
# CI builds and tests directly with `go`, not `make`, so these committed
# binaries must stay current; internal/wrapper's own test rebuilds each from
# source and fails if the committed copy has drifted.
wrapper:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 $(GO) build -trimpath -o $(WRAPPER_DIR)/fluxa-runtime-wrapper-linux-amd64 ./cmd/fluxa-runtime-wrapper
	GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 $(GO) build -trimpath -o $(WRAPPER_DIR)/fluxa-runtime-wrapper-darwin-amd64 ./cmd/fluxa-runtime-wrapper
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 $(GO) build -trimpath -o $(WRAPPER_DIR)/fluxa-runtime-wrapper-darwin-arm64 ./cmd/fluxa-runtime-wrapper

test:
	$(GO) test ./...

test-race:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

lint:
	golangci-lint run

check: wrapper test test-race vet

clean:
	$(GO) clean -testcache
	rm -f $(BUILD_DIR)/$(BINARY) $(BUILD_DIR)/$(BINARY).exe
