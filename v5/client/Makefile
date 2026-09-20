# Locus Wails Build System
#
# Builds the Locus desktop client as a single native binary using Wails.
# All backend logic (activation, heartbeat, storage, updater) is in internal/.
# The frontend is a Vue 3 + Vite + TypeScript SPA embedded in the binary.
#
# Prerequisites:
#   - Go 1.22+
#   - Node.js 18+ (for the Vue frontend)
#   - Wails CLI: go install github.com/wailsapp/wails/v2/cmd/wails@latest
#   - Linux: sudo apt install libgtk-3-dev libwebkit2gtk-4.0-dev   (Ubuntu 22.04)
#            or libwebkit2gtk-4.1-dev                              (Ubuntu 24.04+)
#   - Windows: WebView2 (included in Windows 10+)
#
# Usage:
#   make dev          # Hot-reload development mode
#   make build        # Build for current platform
#   make build-all    # Cross-compile (requires platform toolchains)
#   make clean        # Remove build artifacts
#   make test         # Run Go tests
#   make vet          # Run Go vet

APP_NAME    := locus
BUILD_DIR   := dist

# ── Single source of version ─────────────────────────────────────────────
# The canonical version for every official build is <repo>/v5/VERSION (one
# line, e.g. "2.0.1"). Bump that ONE file to change the client version.
# The runtime version (what the updater and UI report) is injected exactly
# once via -ldflags "-X main.version=<VERSION>" — never hardcode a number in
# main.go, the frontend, or wails.json for a release.
#
# This Makefile's directory is v5/client, so v5/VERSION is one level up.
CLIENT_DIR     := $(dir $(lastword $(MAKEFILE_LIST)))
VERSION_FILE   := $(realpath $(CLIENT_DIR)../VERSION)
VERSION        := $(firstword $(shell cat $(VERSION_FILE) 2>/dev/null))
ifeq ($(strip $(VERSION)),)
# Reached only when v5/VERSION is missing or unreadable. Fail loudly rather
# than substituting a literal: a hardcoded fallback here is a version that no
# longer exists in the source tree, so the build would report a number that
# contradicts v5/VERSION and the shipped Windows resource — the exact drift
# version_consistency_test.go exists to prevent. That test cannot see this
# file, so the guard has to live here.
#
# Deliberately NOT `VERSION := 2.x.y`. For a scratch build without v5/VERSION,
# pass VERSION=<x.y.z> on the command line.
$(error v5/VERSION is missing or empty (looked at $(VERSION_FILE)). \
v5/VERSION is the single source of truth for the release version; restore it, \
or pass VERSION=<x.y.z> explicitly for a scratch build)
endif
LDFLAGS        := -s -w -X main.version=$(VERSION)

# ── Detect current OS/arch ──
UNAME_OS    := $(shell uname -s | tr '[:upper:]' '[:lower:]')
UNAME_ARCH  := $(shell uname -m)

# ── Help ──
.PHONY: help dev build build-all clean test vet install-deps frontend-install frontend-build

help:
	@echo "Locus Wails Desktop App"
	@echo ""
	@echo "Targets:"
	@echo "  dev              Hot-reload development mode (wails dev)"
	@echo "  build            Build for current platform (wails build)"
	@echo "  build-all        Build for Linux + Windows + macOS"
	@echo "  clean            Remove build artifacts"
	@echo "  test             Run Go unit tests"
	@echo "  vet              Run go vet"
	@echo "  install-deps     Install Wails + Node dependencies"
	@echo ""
	@echo "Prerequisites:"
	@echo "  wails:   go install github.com/wailsapp/wails/v2/cmd/wails@latest"
	@echo "  node:    Install from https://nodejs.org/ (v18+)"
	@echo "  Linux:   sudo apt install libgtk-3-dev libwebkit2gtk-4.0-dev"
	@echo ""

# ── Development (hot-reload) ──
# The Go binary embeds frontend/dist (//go:embed), so the frontend must be
# built before `wails dev` compiles the Go code.
dev: frontend-build
	wails dev

# ── Frontend (Vue 3 + Vite) ──

frontend-install:
	cd frontend && npm install

frontend-build: frontend-install
	cd frontend && npm run build

# ── Build for current platform ──
# NOTE: `wails build` adds the required Wails tags (desktop,production)
# automatically and runs the frontend build itself (per wails.json).
# `-tags frontend` additionally embeds the real UI — without it the stub
# assets are used (empty FS — app compiles but shows no UI).
# For manual `go build`, use: -tags "frontend desktop production"
build:
	mkdir -p $(BUILD_DIR)
	wails build -tags frontend -o $(BUILD_DIR)/$(APP_NAME) \
		-ldflags="$(LDFLAGS)"

# ── Cross-compilation ──
build-linux:
	mkdir -p $(BUILD_DIR)
	GOOS=linux GOARCH=amd64 wails build -tags frontend \
		-ldflags="$(LDFLAGS)" -o $(BUILD_DIR)/$(APP_NAME)-linux-amd64

build-windows:
	mkdir -p $(BUILD_DIR)
	GOOS=windows GOARCH=amd64 wails build -tags frontend \
		-ldflags="$(LDFLAGS)" -o $(BUILD_DIR)/$(APP_NAME)-windows-amd64.exe

build-macos-intel:
	mkdir -p $(BUILD_DIR)
	GOOS=darwin GOARCH=amd64 wails build -tags frontend \
		-ldflags="$(LDFLAGS)" -o $(BUILD_DIR)/$(APP_NAME)-darwin-amd64

build-macos-arm:
	mkdir -p $(BUILD_DIR)
	GOOS=darwin GOARCH=arm64 wails build -tags frontend \
		-ldflags="$(LDFLAGS)" -o $(BUILD_DIR)/$(APP_NAME)-darwin-arm64

build-all: build-linux build-windows build-macos-intel build-macos-arm
	@echo "═══ All platform builds complete ═══"
	@echo "  $(BUILD_DIR)/$(APP_NAME)-linux-amd64"
	@echo "  $(BUILD_DIR)/$(APP_NAME)-windows-amd64.exe"
	@echo "  $(BUILD_DIR)/$(APP_NAME)-darwin-amd64"
	@echo "  $(BUILD_DIR)/$(APP_NAME)-darwin-arm64"

# ── Frontend setup (install NPM deps manually if wails doesn't do it) ──
frontend-setup:
	cd frontend && npm install

# ── Quality ──
vet:
	go vet ./...

test:
	go test ./... -v -count=1

# ── Install system dependencies ──
install-deps:
	go install github.com/wailsapp/wails/v2/cmd/wails@latest
	@echo ""
	@echo "Node.js dependencies:"
	@echo "  cd frontend && npm install"
	@echo ""
	@echo "Linux GTK/WebKit:"
	@echo "  sudo apt install libgtk-3-dev libwebkit2gtk-4.0-dev"

# ── Clean ──
clean:
	rm -rf $(BUILD_DIR)
	rm -rf frontend/dist
	rm -rf frontend/node_modules
	@echo "Cleaned"

# ── Publish an update (tooling) ──
# Prepares update.json + a PocketBase "update_config" snippet from the built
# current-platform binary. Dry-run unless you pass -d <domain> -k <ssh key>.
# See scripts/publish-update.sh (repo root). Full multi-platform publishing is
# done from CI release artifacts, not from a local scanner.
publish: build
	@bash ../../scripts/publish-update.sh -v "$(VERSION)" -o "$(BUILD_DIR)/updates" \
		"$(BUILD_DIR)/$(APP_NAME)"
	@echo "Publish dry-run done. Re-run with  -d <domain> -k <key>  to upload."
