# SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
#
# SPDX-License-Identifier: AGPL-3.0-only

# Makefile for configd

# Variables
BINARY_NAME=configd
MAIN_PATH=./cmd/configd
BUILD_DIR=./bin
INTERNAL_PACKAGES=./internal/...
CMD_PACKAGES=./cmd/...

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
GOMOD=$(GOCMD) mod
GOFMT=gofmt
GOLINT=golangci-lint
# gotestsum, govulncheck, deadcode and dupl are pinned in tools/go.mod.
GOTOOL=$(GOCMD) tool -modfile=tools/go.mod

# Build flags
LDFLAGS=-ldflags="-s -w"
BUILD_FLAGS=-trimpath $(LDFLAGS)
BUILD_FLAGS_PROFILING=-trimpath -tags profiling $(LDFLAGS)
BUILD_FLAGS_TRACING=-trimpath -tags tracing $(LDFLAGS)
BUILD_FLAGS_FULL=-trimpath -tags "profiling tracing" $(LDFLAGS)

.PHONY: all build clean test test-race test-tags test-integration test-integration-ldap deps fmt lint help install deadcode dup dup-baseline fuzz fuzz-smoke check vet-tags reuse thirdparties generate lc-sync generate-check security

# REUSE compliance (same gate as the Jenkins 'REUSE Lint' stage)
reuse:
	reuse lint

# Regenerate THIRDPARTIES exactly as the pre-commit hook / Jenkins verify do
thirdparties:
	bash .ci/thirdparties/thirdparties-go.sh THIRDPARTIES

# Regenerate internal/localconfig/lcdocs.go and lcdefaults.go from the carbonio-mailbox
# sources vendored in scripts/lc-source (pinned by scripts/lc-mailbox-tag).
generate:
	python3 scripts/gen-lc.py

# Refresh scripts/lc-source from a local carbonio-mailbox clone at the tag pinned in
# scripts/lc-mailbox-tag (run after bumping the tag, then `make generate`).
MAILBOX_REPO ?= $(HOME)/zextras/carbonio-mailbox

lc-sync:
	python3 scripts/gen-lc.py --sync-from $(MAILBOX_REPO)

# Regenerate into a temp dir and diff against the committed files. Hermetic (python3
# only, no network): this is how Jenkins runs it.
generate-check:
	scripts/gen-lc-check.sh

# Default target
all: clean deps fmt lint test build

# Build the application (without profiling by default)
build:
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 $(GOBUILD) $(BUILD_FLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) $(MAIN_PATH)
	CGO_ENABLED=0 $(GOBUILD) $(BUILD_FLAGS) -o $(BUILD_DIR)/carbonio-postfix-conf ./cmd/carbonio-postfix-conf
	@echo "Build complete: $(BUILD_DIR)/$(BINARY_NAME) $(BUILD_DIR)/carbonio-postfix-conf"

# Build with profiling support enabled
build-profiling:
	@echo "Building $(BINARY_NAME) with profiling support..."
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 $(GOBUILD) $(BUILD_FLAGS_PROFILING) -o $(BUILD_DIR)/$(BINARY_NAME) $(MAIN_PATH)
	@echo "Build complete: $(BUILD_DIR)/$(BINARY_NAME) (profiling enabled)"

# Build with tracing support enabled
build-tracing:
	@echo "Building $(BINARY_NAME) with tracing support..."
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 $(GOBUILD) $(BUILD_FLAGS_TRACING) -o $(BUILD_DIR)/$(BINARY_NAME) $(MAIN_PATH)
	@echo "Build complete: $(BUILD_DIR)/$(BINARY_NAME) (tracing enabled)"

# Build with both profiling and tracing enabled
build-full:
	@echo "Building $(BINARY_NAME) with profiling and tracing support..."
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 $(GOBUILD) $(BUILD_FLAGS_FULL) -o $(BUILD_DIR)/$(BINARY_NAME) $(MAIN_PATH)
	@echo "Build complete: $(BUILD_DIR)/$(BINARY_NAME) (profiling + tracing enabled)"

# Clean build artifacts
clean:
	@echo "Cleaning..."
	$(GOCLEAN)
	@rm -rf $(BUILD_DIR)
	@echo "Clean complete"

# Run tests
test:
	@echo "Running tests..."
	$(GOTEST) -v ./...

# Run tests with race detector
test-race:
	@echo "Running tests with race detector..."
	$(GOTEST) -race -v ./...

# vet every build-tag combination: profiling and tracing select alternative
# implementations; integration is vetted (compiled, including its tests) but
# never run here.
vet-tags:
	@for tags in "" integration tracing profiling "tracing profiling"; do \
		echo "go vet -tags '$$tags' ./..."; \
		$(GOCMD) vet -tags "$$tags" ./... || exit 1; \
	done

# Race-test the tracing/profiling variants (the default tags are test-race)
test-tags:
	@for tags in tracing profiling "tracing profiling"; do \
		echo "go test -race -tags '$$tags' ./..."; \
		$(GOTEST) -race -tags "$$tags" ./... || exit 1; \
	done

# Run integration tests (requires live LDAP environment)
test-integration:
	@echo "Running integration tests..."
	$(GOTEST) -v -tags integration ./...

# Self-contained LDAP integration suite: configd loaders, plan, Execute and `configd ldap` against
# an OpenLDAP container (needs docker or podman, plus network for the alpine image
# and its slapd packages; skips when no container runtime is reachable). With podman
# and no DOCKER_HOST, the rootless podman socket is used when present.
test-integration-ldap:
	@echo "Running LDAP container integration tests..."
	@sock="$${XDG_RUNTIME_DIR:-/run/user/$$(id -u)}/podman/podman.sock"; \
	if [ -z "$$DOCKER_HOST" ] && [ -S "$$sock" ]; then export DOCKER_HOST="unix://$$sock"; fi; \
	$(GOTEST) -v -count=1 -tags integration -run '^Test(OpenLDAP|Container)_' ./internal/configmgr ./internal/cli/ldapcmd ./internal/ldapwatch

# Run tests with coverage (all build tag combinations merged)
test-coverage:
	@echo "Running tests with coverage (default tags)..."
	$(GOTEST) -v -coverprofile=coverage.default.out ./...
	@echo "Running tests with coverage (tracing tag)..."
	$(GOTEST) -v -tags tracing -coverprofile=coverage.tracing.out ./...
	@echo "Running tests with coverage (profiling tag)..."
	$(GOTEST) -v -tags profiling -coverprofile=coverage.profiling.out ./...
	@echo "Running tests with coverage (tracing+profiling tags)..."
	$(GOTEST) -v -tags "tracing profiling" -coverprofile=coverage.full.out ./...
	@echo "Merging coverage profiles..."
	@head -1 coverage.default.out > coverage.out
	@tail -n +2 coverage.default.out >> coverage.out
	@tail -n +2 coverage.tracing.out >> coverage.out
	@tail -n +2 coverage.profiling.out >> coverage.out
	@tail -n +2 coverage.full.out >> coverage.out
	@rm -f coverage.default.out coverage.tracing.out coverage.profiling.out coverage.full.out
	$(GOCMD) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"

# Download dependencies
deps:
	@echo "Downloading dependencies..."
	$(GOMOD) download
	$(GOMOD) tidy

# Format code
fmt:
	@echo "Formatting code..."
	$(GOFMT) -s -w .

# Lint code (fails when golangci-lint is not installed: a skipped lint is a false green)
lint:
	@echo "Linting code..."
	@command -v $(GOLINT) > /dev/null || { echo "golangci-lint not installed; install it (see .golangci.yml, version pinned in the Jenkins golang-v1 pod)" >&2; exit 1; }
	$(GOLINT) run ./...

# Run the application
run:
	@echo "Running $(BINARY_NAME)..."
	$(GOBUILD) -o $(BUILD_DIR)/$(BINARY_NAME) $(MAIN_PATH)
	$(BUILD_DIR)/$(BINARY_NAME)

# Install to system
install: build
	@echo "Installing $(BINARY_NAME) to /opt/zextras/libexec/..."
	@sudo cp $(BUILD_DIR)/$(BINARY_NAME) /opt/zextras/libexec/
	@sudo chown zextras:zextras /opt/zextras/libexec/$(BINARY_NAME)
	@sudo chmod 755 /opt/zextras/libexec/$(BINARY_NAME)
	@echo "Installing carbonio-postfix-conf (root-owned: the only writer of common/conf)..."
	@sudo install -m 755 -o root -g root $(BUILD_DIR)/carbonio-postfix-conf /opt/zextras/libexec/carbonio-postfix-conf
	@echo "Installation complete"

# Build for different architectures
build-linux-amd64:
	@echo "Building for Linux AMD64..."
	@mkdir -p $(BUILD_DIR)
	GOOS=linux GOARCH=amd64 $(GOBUILD) $(BUILD_FLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64 $(MAIN_PATH)

build-linux-arm64:
	@echo "Building for Linux ARM64..."
	@mkdir -p $(BUILD_DIR)
	GOOS=linux GOARCH=arm64 $(GOBUILD) $(BUILD_FLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-arm64 $(MAIN_PATH)

# Build for all supported architectures
build-all: build-linux-amd64 build-linux-arm64

# Vulnerability scan of the module's dependencies (govulncheck, pinned in
# tools/go.mod; needs network for the vuln DB). Fails on any finding in code
# that is reachable. gosec runs as part of golangci-lint (see .golangci.yml).
security:
	@echo "Running vulnerability scan..."
	$(GOTOOL) govulncheck ./...

# Benchmark tests
benchmark:
	@echo "Running benchmarks..."
	$(GOTEST) -bench=. -benchmem ./...

# Quality checks, all run by Jenkins ('Static checks' and 'Fuzz smoke' stages).
# Tools are pinned in tools/go.mod.
# Unreachable code across all profiling/tracing tag combinations
deadcode:
	scripts/deadcode-check.sh

# Duplication ratchet (dupl -t 100) against scripts/dup-baseline.json
dup:
	scripts/dup-check.sh

dup-baseline:
	scripts/dup-check.sh --update

# Run every fuzz target for FUZZTIME (default 30s) each
fuzz:
	scripts/fuzz.sh

# Short smoke run of every fuzz target (10s each), as in Jenkins
fuzz-smoke:
	FUZZTIME=10s scripts/fuzz.sh

# The Jenkins checks that need no container runtime (fuzz-smoke, security and
# generate-check are separate targets): lint, vet and race tests for every tag combination, deadcode, duplication
check: lint vet-tags test-race test-tags deadcode dup

# Initialize Go module (only run once)
init-module:
	@echo "Initializing Go module..."
	$(GOMOD) init configd

# Update dependencies
update-deps:
	@echo "Updating dependencies..."
	$(GOMOD) get -u ./...
	$(GOMOD) tidy

# Verify dependencies
verify:
	@echo "Verifying dependencies..."
	$(GOMOD) verify

# Check for outdated dependencies
outdated:
	@echo "Checking for outdated dependencies..."
	@$(GOCMD) list -u -m all

# Create a release
release: clean deps fmt lint test build-all
	@echo "Creating release..."
	@mkdir -p releases
	@tar -czf releases/$(BINARY_NAME)-linux-amd64.tar.gz -C $(BUILD_DIR) $(BINARY_NAME)-linux-amd64
	@tar -czf releases/$(BINARY_NAME)-linux-arm64.tar.gz -C $(BUILD_DIR) $(BINARY_NAME)-linux-arm64
	@echo "Release packages created in releases/"

# Development server with auto-reload (requires air)
dev:
	@echo "Starting development server..."
	@if command -v air > /dev/null; then \
		air; \
	else \
		echo "air not installed. Install with: go install github.com/cosmtrek/air@latest"; \
		echo "Falling back to regular run..."; \
		$(MAKE) run; \
	fi

# Help
help:
	@echo "Available targets:"
	@echo "  all            - Clean, deps, fmt, lint, test, and build"
	@echo "  build          - Build the application (no profiling/tracing)"
	@echo "  build-profiling- Build with profiling support (--cpuprofile, --memprofile, --trace)"
	@echo "  build-tracing  - Build with tracing support (execution spans)"
	@echo "  build-full     - Build with both profiling and tracing support"
	@echo "  clean          - Clean build artifacts"
	@echo "  test           - Run unit tests"
	@echo "  test-integration - Run integration tests (requires live environment)"
	@echo "  test-integration-ldap - Run the OpenLDAP container integration suite (docker/podman)"
	@echo "  test-coverage  - Run tests with coverage report"
	@echo "  deps           - Download dependencies"
	@echo "  fmt            - Format code"
	@echo "  lint           - Lint code"
	@echo "  run            - Build and run the application"
	@echo "  install        - Install to system (/opt/zextras/libexec/)"
	@echo "  build-all      - Build for all supported architectures"
	@echo "  security       - Vulnerability scan (govulncheck, pinned in tools/go.mod)"
	@echo "  benchmark      - Run benchmark tests"
	@echo "  deadcode       - Fail on code no main package reaches (all tag combinations)"
	@echo "  dup            - Fail when duplication (dupl -t 100) grows past scripts/dup-baseline.json"
	@echo "  dup-baseline   - Rewrite scripts/dup-baseline.json"
	@echo "  fuzz           - Run every fuzz target for FUZZTIME (default 30s) each"
	@echo "  fuzz-smoke     - Run every fuzz target for 10s each (as in Jenkins)"
	@echo "  vet-tags       - go vet for the default, integration, tracing, profiling and tracing+profiling tags"
	@echo "  test-tags      - Race tests for the tracing, profiling and tracing+profiling tags"
	@echo "  generate-check - Fail when lcdocs.go/lcdefaults.go differ from the vendored carbonio-mailbox inputs"
	@echo "  lc-sync        - Refresh scripts/lc-source from a carbonio-mailbox clone at the pinned tag"
	@echo "  check          - lint, vet-tags, test-race, test-tags, deadcode, dup"
	@echo "  update-deps    - Update dependencies"
	@echo "  verify         - Verify dependencies"
	@echo "  release        - Create release packages"
	@echo "  dev            - Start development server with auto-reload"
	@echo "  help           - Show this help"
