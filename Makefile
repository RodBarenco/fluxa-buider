GO ?= go
BINARY := fluxa-builder
BUILD_DIR := bin
WRAPPER_DIR := internal/wrapper/bin
LAUNCHER_DIR := internal/launcherbin/bin

.PHONY: all build wrapper launcher test test-race vet lint check clean

all: check build

build: wrapper launcher
	$(GO) build -trimpath -o $(BUILD_DIR)/$(BINARY) ./cmd/fluxa-builder

# Regenerates the committed "adapted runtime" relay binaries that
# internal/wrapper embeds (see docs/adr/0025-linux-adapted-runtime-wrapper.md).
# CI builds and tests directly with `go`, not `make`, so these committed
# binaries must stay current; internal/wrapper's own test rebuilds each from
# source and fails if the committed copy has drifted.
#
# -buildvcs=false is required, not cosmetic: Go's default VCS stamping
# embeds the current git commit hash/timestamp into the binary, so without
# it, every build after the *next* commit — regardless of whether this
# wrapper's own source changed at all — would embed a different commit
# hash and spuriously "drift" from the committed copy.
wrapper:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 $(GO) build -trimpath -buildvcs=false -o $(WRAPPER_DIR)/fluxa-runtime-wrapper-linux-amd64 ./cmd/fluxa-runtime-wrapper
	GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 $(GO) build -trimpath -buildvcs=false -o $(WRAPPER_DIR)/fluxa-runtime-wrapper-darwin-amd64 ./cmd/fluxa-runtime-wrapper
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 $(GO) build -trimpath -buildvcs=false -o $(WRAPPER_DIR)/fluxa-runtime-wrapper-darwin-arm64 ./cmd/fluxa-runtime-wrapper

# Regenerates the committed application-launcher binaries that
# internal/launcherbin embeds, one per supported target (see
# docs/adr/0029-cross-target-application-launcher.md). Same contract and
# same -buildvcs=false reasoning as `wrapper` above; internal/launcherbin's
# own test rebuilds each from source and fails if a committed copy drifted.
#
# windows/amd64 is the one that made this necessary: assembling a Windows
# application on a Linux machine has no other way to obtain a real PE
# launcher.
#
# -ldflags="-s -w" is the one flag difference from `wrapper` above, and it
# is deliberate: unlike the relay, a copy of this binary ships inside every
# application a user distributes, where symbol tables and DWARF are dead
# weight nobody will ever debug with. It cuts each launcher from ~6.5MB to
# ~4.2MB, in the repository and in every distributed application alike.
# internal/launcherbin's drift test rebuilds with these exact flags.
launcher:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 $(GO) build -trimpath -buildvcs=false -ldflags="-s -w" -o $(LAUNCHER_DIR)/fluxa-launcher-linux-amd64 ./cmd/fluxa-launcher
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 $(GO) build -trimpath -buildvcs=false -ldflags="-s -w" -o $(LAUNCHER_DIR)/fluxa-launcher-windows-amd64.exe ./cmd/fluxa-launcher
	GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 $(GO) build -trimpath -buildvcs=false -ldflags="-s -w" -o $(LAUNCHER_DIR)/fluxa-launcher-darwin-amd64 ./cmd/fluxa-launcher
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 $(GO) build -trimpath -buildvcs=false -ldflags="-s -w" -o $(LAUNCHER_DIR)/fluxa-launcher-darwin-arm64 ./cmd/fluxa-launcher

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
