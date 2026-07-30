# MyVPN Wails Build System
#
# Builds the MyVPN desktop client as a single native binary using Wails.
# All backend logic (activation, heartbeat, storage, updater) is in internal/.
# The frontend is a Vue 3 + Vite + TypeScript SPA embedded in the binary.
#
# Prerequisites:
#   - Go 1.22+
#   - Node.js 18+ (for the Vue frontend)
#   - Wails CLI: go install github.com/wailsapp/wails/v2/cmd/wails@latest
#   - Linux: sudo apt install libgtk-3-dev libwebkit2gtk-4.0-dev
#   - macOS: Xcode Command Line Tools
#   - Windows: WebView2 (included in Windows 10+)
#
# Usage:
#   make dev          # Hot-reload development mode
#   make build        # Build for current platform
#   make build-all    # Cross-compile (requires platform toolchains)
#   make clean        # Remove build artifacts
#   make test         # Run Go tests
#   make vet          # Run Go vet

APP_NAME    := myvpn
VERSION     := 2.0.0
BUILD_DIR   := dist

# ── Detect current OS/arch ──
UNAME_OS    := $(shell uname -s | tr '[:upper:]' '[:lower:]')
UNAME_ARCH  := $(shell uname -m)

# ── Help ──
.PHONY: help dev build build-all clean test vet install-deps

help:
	@echo "MyVPN Wails Desktop App"
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
dev:
	wails dev

# ── Build for current platform ──
build:
	mkdir -p $(BUILD_DIR)
	wails build -o $(BUILD_DIR)/$(APP_NAME)

# ── Cross-compilation ──
build-linux:
	mkdir -p $(BUILD_DIR)
	GOOS=linux GOARCH=amd64 wails build -o $(BUILD_DIR)/$(APP_NAME)-linux-amd64

build-windows:
	mkdir -p $(BUILD_DIR)
	GOOS=windows GOARCH=amd64 wails build -o $(BUILD_DIR)/$(APP_NAME)-windows-amd64.exe

build-macos-intel:
	mkdir -p $(BUILD_DIR)
	GOOS=darwin GOARCH=amd64 wails build -o $(BUILD_DIR)/$(APP_NAME)-darwin-amd64

build-macos-arm:
	mkdir -p $(BUILD_DIR)
	GOOS=darwin GOARCH=arm64 wails build -o $(BUILD_DIR)/$(APP_NAME)-darwin-arm64

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
