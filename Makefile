# Makefile for cpictl
# Cross-platform build system for the cpictl CLI

# Variables
BINARY_NAME := cpictl
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TIME := $(shell date -u +"%Y-%m-%d_%H:%M:%S" 2>/dev/null || powershell -Command "Get-Date -Format 'yyyy-MM-dd_HH:mm:ss'")
LDFLAGS := -ldflags "-X main.Version=$(VERSION) -X main.BuildTime=$(BUILD_TIME)"

# Directories
DIST_DIR := bin
CMD_DIR := cmd/cpictl

# Detect OS
ifeq ($(OS),Windows_NT)
	DETECTED_OS := Windows
	RM := rm -f
	RMDIR := rm -rf
	EXE_EXT := .exe
	PATH_SEP := /
else
	DETECTED_OS := $(shell uname -s)
	RM := rm -f
	RMDIR := rm -rf
	EXE_EXT :=
	PATH_SEP := /
endif

.PHONY: help
help: ## Show this help message
	@echo "cpictl Build System"
	@echo ""
	@echo "Available targets:"
	@echo "  all            - Clean, test, and build for current platform"
	@echo "  build          - Build for current platform"
	@echo "  build-windows  - Build for Windows amd64"
	@echo "  build-linux    - Build for Linux amd64"
	@echo "  build-darwin   - Build for macOS (Intel and Apple Silicon)"
	@echo "  build-all      - Build for all platforms"
	@echo "  dist           - Release archives + SHA256SUMS in dist/ (VERSION=v1.2.3)"
	@echo "  test           - Run offline tests (no tenant access)"
	@echo "  cover          - Offline tests with coverage report"
	@echo "  test-integration - Run tenant integration tests (WRITES TO A REAL TENANT)"
	@echo "  test-coverage  - Run tests with coverage report"
	@echo "  clean          - Remove build artifacts"
	@echo "  install        - Install to GOPATH/bin"
	@echo "  version        - Show version information"
	@echo "  info           - Show build environment"
	@echo "  fmt            - Format code"
	@echo "  vet            - Run go vet"
	@echo "  verify         - Run fmt, vet, and test"

.PHONY: all
all: clean test build

.PHONY: build
build:
	@echo "Building for current platform..."
	@mkdir -p $(DIST_DIR)
	go build $(LDFLAGS) -o $(DIST_DIR)$(PATH_SEP)$(BINARY_NAME)$(EXE_EXT) ./$(CMD_DIR)
	@echo "Build complete: $(DIST_DIR)$(PATH_SEP)$(BINARY_NAME)$(EXE_EXT)"

.PHONY: build-windows
build-windows:
	@echo "Building for Windows amd64..."
	@mkdir -p $(DIST_DIR)
	GOOS=windows GOARCH=amd64 go build $(LDFLAGS) -o $(DIST_DIR)$(PATH_SEP)$(BINARY_NAME)-windows-amd64.exe ./$(CMD_DIR)
	@echo "Windows build complete: $(DIST_DIR)$(PATH_SEP)$(BINARY_NAME)-windows-amd64.exe"

.PHONY: build-linux
build-linux:
	@echo "Building for Linux amd64..."
	@mkdir -p $(DIST_DIR)
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $(DIST_DIR)$(PATH_SEP)$(BINARY_NAME)-linux-amd64 ./$(CMD_DIR)
	@echo "Linux build complete: $(DIST_DIR)$(PATH_SEP)$(BINARY_NAME)-linux-amd64"

.PHONY: build-linux-arm64
build-linux-arm64:
	@echo "Building for Linux arm64..."
	@mkdir -p $(DIST_DIR)
	GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o $(DIST_DIR)$(PATH_SEP)$(BINARY_NAME)-linux-arm64 ./$(CMD_DIR)
	@echo "Linux ARM64 build complete: $(DIST_DIR)$(PATH_SEP)$(BINARY_NAME)-linux-arm64"

.PHONY: build-darwin
build-darwin:
	@echo "Building for macOS amd64..."
	@mkdir -p $(DIST_DIR)
	GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o $(DIST_DIR)$(PATH_SEP)$(BINARY_NAME)-darwin-amd64 ./$(CMD_DIR)
	@echo "macOS Intel build complete"
	@echo "Building for macOS arm64..."
	GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o $(DIST_DIR)$(PATH_SEP)$(BINARY_NAME)-darwin-arm64 ./$(CMD_DIR)
	@echo "macOS Apple Silicon build complete"

.PHONY: build-all
build-all: build-windows build-linux build-linux-arm64 build-darwin
	@echo "All platforms built successfully"
	@echo "Output files in $(DIST_DIR):"
	@ls -lh $(DIST_DIR) 2>/dev/null || dir $(DIST_DIR)

# Release packaging (used by .github/workflows/release.yml)
RELEASE_DIR := dist
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64
RELEASE_LDFLAGS := -ldflags "-s -w -X main.Version=$(VERSION) -X main.BuildTime=$(BUILD_TIME)"

.PHONY: dist
dist: ## Build release archives for all platforms into dist/ (VERSION=v1.2.3)
	@rm -rf $(RELEASE_DIR) && mkdir -p $(RELEASE_DIR)
	@for platform in $(PLATFORMS); do \
		os=$${platform%/*}; arch=$${platform#*/}; \
		name=$(BINARY_NAME)_$(VERSION)_$${os}_$${arch}; \
		ext=""; [ "$$os" = "windows" ] && ext=".exe"; \
		echo "Building $$name"; \
		mkdir -p $(RELEASE_DIR)/$$name; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath $(RELEASE_LDFLAGS) \
			-o $(RELEASE_DIR)/$$name/$(BINARY_NAME)$$ext ./$(CMD_DIR) || exit 1; \
		cp LICENSE NOTICE README.md CHANGELOG.md $(RELEASE_DIR)/$$name/; \
		if [ "$$os" = "windows" ]; then \
			(cd $(RELEASE_DIR) && zip -qr $$name.zip $$name) || exit 1; \
		else \
			tar -C $(RELEASE_DIR) -czf $(RELEASE_DIR)/$$name.tar.gz $$name || exit 1; \
		fi; \
		rm -rf $(RELEASE_DIR)/$$name; \
	done
	@tar -czf $(RELEASE_DIR)/$(BINARY_NAME)_$(VERSION)_schemas.tar.gz schemas
	@cd $(RELEASE_DIR) && sha256sum *.tar.gz *.zip > SHA256SUMS
	@echo "Release archives in $(RELEASE_DIR):" && ls -1 $(RELEASE_DIR)

.PHONY: test
test:
	@echo "Running tests..."
	go test -v ./...
	@echo "Tests complete"

.PHONY: cover
cover: ## Offline tests with cross-package coverage (coverage.out, coverage.html)
	go test -coverpkg=./... -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1
	go tool cover -html=coverage.out -o coverage.html

.PHONY: test-integration
test-integration:
	@echo "Running tenant integration tests (requires CPICTL_* tenant env vars)..."
	go test -tags integration -v ./...

.PHONY: test-coverage
test-coverage:
	@echo "Running tests with coverage..."
	go test -v -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"

.PHONY: test-short
test-short:
	@echo "Running short tests..."
	go test -short ./...
	@echo "Short tests complete"

.PHONY: fmt
fmt:
	@echo "Formatting code..."
	go fmt ./...
	@echo "Code formatted"

.PHONY: vet
vet:
	@echo "Running go vet..."
	go vet ./...
	@echo "Vet complete"

.PHONY: tidy
tidy:
	@echo "Tidying go modules..."
	go mod tidy
	@echo "Modules tidied"

.PHONY: deps
deps:
	@echo "Downloading dependencies..."
	go mod download
	@echo "Dependencies downloaded"

.PHONY: verify
verify: fmt vet test
	@echo "Verification complete"

.PHONY: clean
clean:
	@echo "Cleaning build artifacts..."
	@rm -rf $(DIST_DIR) 2>/dev/null || true
	@rm -f $(BINARY_NAME)$(EXE_EXT) 2>/dev/null || true
	@rm -f coverage.out coverage.html 2>/dev/null || true
	@rm -rf $(RELEASE_DIR) 2>/dev/null || true
	@echo "Clean complete"

.PHONY: install
install:
	@echo "Installing to GOPATH/bin..."
	go install $(LDFLAGS) ./$(CMD_DIR)
	@echo "Installed"

.PHONY: version
version:
	@echo "Version Information:"
	@echo "  Version:    $(VERSION)"
	@echo "  Build Time: $(BUILD_TIME)"

.PHONY: info
info:
	@echo "Build Environment:"
	@go version
	@echo "GOPATH: $$(go env GOPATH)"
	@echo "GOOS:   $$(go env GOOS)"
	@echo "GOARCH: $$(go env GOARCH)"
	@echo ""
	@echo "Project:"
	@echo "  Binary:    $(BINARY_NAME)"
	@echo "  Version:   $(VERSION)"
	@echo "  Build Dir: $(DIST_DIR)"
	@echo "  OS:        $(DETECTED_OS)"

.DEFAULT_GOAL := help
