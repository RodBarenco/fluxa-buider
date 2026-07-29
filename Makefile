GO ?= go
BINARY := fluxa-builder
BUILD_DIR := bin

.PHONY: all build test test-race vet lint check clean

all: check build

build:
	$(GO) build -trimpath -o $(BUILD_DIR)/$(BINARY) ./cmd/fluxa-builder

test:
	$(GO) test ./...

test-race:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

lint:
	golangci-lint run

check: test test-race vet

clean:
	$(GO) clean -testcache
	rm -f $(BUILD_DIR)/$(BINARY) $(BUILD_DIR)/$(BINARY).exe
