VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
# Release tags start with v; the version inside the binary does not.
BUILD_VERSION := $(patsubst v%,%,$(VERSION))
LDFLAGS := -s -w -X github.com/vincentsch/llmention/internal/version.Version=$(BUILD_VERSION) -X github.com/vincentsch/llmention/internal/version.Commit=$(COMMIT)
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64
NATIVE_SUFFIX := $(if $(filter windows,$(shell go env GOOS)),.exe)
STATICCHECK := GOTOOLCHAIN=$(shell go env GOVERSION) go run honnef.co/go/tools/cmd/staticcheck@2026.2.2

.PHONY: build build-all test race lint staticcheck docs docs-check clean

build:
	mkdir -p bin
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/llmention$(NATIVE_SUFFIX) ./cmd/llmention

build-all:
	mkdir -p dist
	rm -f dist/llmention-* dist/checksums.txt
	@set -e; for platform in $(PLATFORMS); do \
		os=$${platform%/*}; arch=$${platform#*/}; \
		asset=llmention-$$os-$$arch; \
		if [ "$$os" = windows ]; then asset=$$asset.exe; fi; \
		echo "Building $$asset"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" -o "dist/$$asset" ./cmd/llmention; \
	done
	@cd dist && if command -v sha256sum >/dev/null 2>&1; then \
		sha256sum llmention-* > checksums.txt; \
	else shasum -a 256 llmention-* > checksums.txt; fi

test:
	go test ./...

race:
	go test -race ./...

lint:
	@test -z "$$(gofmt -l cmd internal scripts)" || { gofmt -l cmd internal scripts; exit 1; }
	go vet ./...

staticcheck:
	$(STATICCHECK) ./...

docs:
	go test ./cmd/llmention -run 'TestGeneratedDocsInSync|TestHelpGoldensInSync' -update

docs-check:
	go test ./cmd/llmention -run 'TestGeneratedDocsInSync|TestHelpGoldensInSync|TestHelpDocsManifestCatalogConsistent'

clean:
	rm -f bin/llmention bin/llmention.exe dist/llmention-* dist/checksums.txt
