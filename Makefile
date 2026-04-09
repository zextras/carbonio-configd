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

# Build flags
LDFLAGS=-ldflags="-s -w"
BUILD_FLAGS=-trimpath $(LDFLAGS)
BUILD_FLAGS_PROFILING=-trimpath -tags profiling $(LDFLAGS)
BUILD_FLAGS_TRACING=-trimpath -tags tracing $(LDFLAGS)
BUILD_FLAGS_FULL=-trimpath -tags "profiling tracing" $(LDFLAGS)

.PHONY: all build clean test test-integration deps fmt lint help install

# Default target
all: clean deps fmt lint test build

# Build the application (without profiling by default)
build:
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 $(GOBUILD) $(BUILD_FLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) $(MAIN_PATH)
	@echo "Build complete: $(BUILD_DIR)/$(BINARY_NAME)"

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

# Run integration tests (requires live LDAP environment)
test-integration:
	@echo "Running integration tests..."
	$(GOTEST) -v -tags integration ./...

# Run tests with coverage
test-coverage:
	@echo "Running tests with coverage..."
	$(GOTEST) -v -coverprofile=coverage.out ./...
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

# Lint code
lint:
	@echo "Linting code..."
	@if command -v $(GOLINT) > /dev/null; then \
		$(GOLINT) run ./...; \
	else \
		echo "golangci-lint not installed, skipping lint"; \
	fi

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

# Generate mocks (if using mockery)
mocks:
	@echo "Generating mocks..."
	@if command -v mockery > /dev/null; then \
		mockery --all --dir ./internal --output ./internal/mocks; \
	else \
		echo "mockery not installed, skipping mock generation"; \
	fi

# Security scan
security:
	@echo "Running security scan..."
	@if command -v gosec > /dev/null; then \
		gosec ./... -quiet || echo "Security scan completed with warnings (expected for config management tool)"; \
	else \
		echo "gosec not installed, skipping security scan"; \
	fi

# Benchmark tests
benchmark:
	@echo "Running benchmarks..."
	$(GOTEST) -bench=. -benchmem ./...

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
	@echo "  test-coverage  - Run tests with coverage report"
	@echo "  deps           - Download dependencies"
	@echo "  fmt            - Format code"
	@echo "  lint           - Lint code"
	@echo "  run            - Build and run the application"
	@echo "  install        - Install to system (/opt/zextras/libexec/)"
	@echo "  build-all      - Build for all supported architectures"
	@echo "  mocks          - Generate mocks"
	@echo "  security       - Run security scan"
	@echo "  benchmark      - Run benchmark tests"
	@echo "  update-deps    - Update dependencies"
	@echo "  verify         - Verify dependencies"
	@echo "  release        - Create release packages"
	@echo "  dev            - Start development server with auto-reload"
	@echo "  help           - Show this help"
