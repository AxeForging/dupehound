.PHONY: all build build-all build-local clean test lint tidy version tag release-check install

GOOS_ARCH := linux/amd64 linux/arm64 linux/386 linux/arm darwin/amd64 darwin/arm64 windows/amd64 windows/arm64 windows/386
DIST_DIR := dist
BINARY_NAME := dupehound

ifeq ($(origin VERSION), environment)
else
  VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
endif

BUILD_TIME := $(shell date -u '+%Y-%m-%d_%H:%M:%S')
GIT_COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")

LDFLAGS := -ldflags="-s -w -X main.Version=$(VERSION) -X main.BuildTime=$(BUILD_TIME) -X main.GitCommit=$(GIT_COMMIT)"

all: build-local

build-local:
	CGO_ENABLED=0 go build $(LDFLAGS) -o $(BINARY_NAME) .

build-all:
	@echo "Building binaries... Version: $(VERSION)"
	@mkdir -p $(DIST_DIR)
	@for t in $(GOOS_ARCH); do \
		os=$${t%/*}; arch=$${t#*/}; \
		bin_name=$(BINARY_NAME)-$${os}-$${arch}; \
		if [ "$$os" = "windows" ]; then bin_name="$${bin_name}.exe"; fi; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build $(LDFLAGS) -o $(DIST_DIR)/$$bin_name .; \
	done

install: build-local
	sudo mv $(BINARY_NAME) /usr/local/bin/

test:
	go test ./... -v

lint:
	golangci-lint run --timeout=5m

tidy:
	go mod tidy && go mod download

clean:
	rm -rf $(DIST_DIR)
	rm -f $(BINARY_NAME)

version:
	@echo "Version: $(VERSION) | Build: $(BUILD_TIME) | Commit: $(GIT_COMMIT)"

tag:
	@if [ "$(VERSION)" = "dev" ]; then echo "Set VERSION env var first"; exit 1; fi
	git tag -a $(VERSION) -m "Release $(VERSION)"

release-check: build-all
	go test ./... -v
	@echo "Ready for release $(VERSION)"
