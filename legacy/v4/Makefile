# MyVPN Build System
#
# Builds the MyVPN desktop client application for Windows, macOS, and Linux.
#
# Prerequisites:
#   - Go 1.22+
#   - Fyne v2 dependencies (CGO required)
#   - For cross-compilation: MinGW (Windows), osxcross (macOS)
#
# Usage:
#   make build         # Build for current platform
#   make build-all     # Build for all platforms
#   make bundle-all    # Build + bundle with sing-box engine
#   make clean         # Remove build artifacts

APP_NAME    := myvpn
VERSION     := 1.0.0
LDFLAGS     := -s -w -X main.version=$(VERSION)
BUILD_DIR   := dist

# ── Detect current OS ──
UNAME_OS    := $(shell uname -s | tr '[:upper:]' '[:lower:]')
UNAME_ARCH  := $(shell uname -m)

# ── Help ──
.PHONY: help build build-all bundle bundle-all clean

help:
	@echo "MyVPN Build System"
	@echo ""
	@echo "Targets:"
	@echo "  build              Build for current platform"
	@echo "  build-linux        Build for Linux amd64"
	@echo "  build-windows      Build for Windows amd64 (requires MinGW)"
	@echo "  build-macos-intel  Build for macOS Intel (amd64)"
	@echo "  build-macos-arm    Build for macOS Apple Silicon (arm64)"
	@echo "  build-all          Build for all platforms"
	@echo "  build-helper       Build TUN helper for current platform"
	@echo "  bundle-all         Build all + bundle with sing-box"
	@echo "  clean              Remove build artifacts"
	@echo ""
	@echo "Variables:"
	@echo "  VERSION=$(VERSION)    Version string for builds"
	@echo "  BINARY=myvpn          Output binary name"

# ── Build for current platform ──
build:
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=1 go build -ldflags="$(LDFLAGS)" \
		-o $(BUILD_DIR)/$(APP_NAME)$(shell go env GOEXE) \
		./cmd/$(APP_NAME)/

# ── Cross-compilation targets ──
# Linux (native or cross)
build-linux:
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=1 GOOS=linux GOARCH=amd64 \
		go build -ldflags="$(LDFLAGS)" \
		-o $(BUILD_DIR)/$(APP_NAME)-linux-amd64 \
		./cmd/$(APP_NAME)/

# Windows (requires MinGW: x86_64-w64-mingw32-gcc)
build-windows:
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=1 GOOS=windows GOARCH=amd64 \
		CC=x86_64-w64-mingw32-gcc \
		go build -ldflags="$(LDFLAGS) -H windowsgui" \
		-o $(BUILD_DIR)/$(APP_NAME).exe \
		./cmd/$(APP_NAME)/

# macOS Intel (requires osxcross or macOS builder)
build-macos-intel:
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=1 GOOS=darwin GOARCH=amd64 \
		go build -ldflags="$(LDFLAGS)" \
		-o $(BUILD_DIR)/$(APP_NAME)-darwin-amd64 \
		./cmd/$(APP_NAME)/

# macOS ARM (Apple Silicon)
build-macos-arm:
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 \
		go build -ldflags="$(LDFLAGS)" \
		-o $(BUILD_DIR)/$(APP_NAME)-darwin-arm64 \
		./cmd/$(APP_NAME)/

# ── Build all platforms ──
build-all: build-linux build-windows build-macos-intel build-macos-arm
	@echo "✅ All platform builds complete"

# ── TUN Helper (no CGO needed) ──
build-helper:
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" \
		-o $(BUILD_DIR)/myvpn-helper \
		./internal/helper/

build-helper-windows:
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 \
		go build -ldflags="$(LDFLAGS)" \
		-o $(BUILD_DIR)/myvpn-helper.exe \
		./internal/helper/

build-helper-macos-intel:
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 \
		go build -ldflags="$(LDFLAGS)" \
		-o $(BUILD_DIR)/myvpn-helper-darwin-amd64 \
		./internal/helper/

build-helper-macos-arm:
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 \
		go build -ldflags="$(LDFLAGS)" \
		-o $(BUILD_DIR)/myvpn-helper-darwin-arm64 \
		./internal/helper/

# ── Bundle with sing-box engine ──
# Download the appropriate sing-box binary for each platform
SING_BOX_VERSION := 1.10.0

bundle-windows: build-windows build-helper-windows
	cd $(BUILD_DIR) && \
	curl -sLO "https://github.com/SagerNet/sing-box/releases/download/v$(SING_BOX_VERSION)/sing-box-$(SING_BOX_VERSION)-windows-amd64.zip" && \
	unzip -qo "sing-box-$(SING_BOX_VERSION)-windows-amd64.zip" && \
	mv "sing-box-$(SING_BOX_VERSION)-windows-amd64/sing-box.exe" . && \
	rm -rf "sing-box-$(SING_BOX_VERSION)-windows-amd64" "sing-box-$(SING_BOX_VERSION)-windows-amd64.zip"
	cd $(BUILD_DIR) && zip -j myvpn-windows-$(VERSION).zip myvpn.exe sing-box.exe myvpn-helper.exe
	@echo "✅ Windows bundle: dist/myvpn-windows-$(VERSION).zip"

bundle-macos-intel: build-macos-intel build-helper-macos-intel
	cd $(BUILD_DIR) && \
	curl -sLO "https://github.com/SagerNet/sing-box/releases/download/v$(SING_BOX_VERSION)/sing-box-$(SING_BOX_VERSION)-darwin-amd64.tar.gz" && \
	tar -xzf "sing-box-$(SING_BOX_VERSION)-darwin-amd64.tar.gz" && \
	mv "sing-box-$(SING_BOX_VERSION)-darwin-amd64/sing-box" . && \
	rm -rf "sing-box-$(SING_BOX_VERSION)-darwin-amd64" "sing-box-$(SING_BOX_VERSION)-darwin-amd64.tar.gz"
	cd $(BUILD_DIR) && zip -j myvpn-macos-intel-$(VERSION).zip myvpn-darwin-amd64 sing-box myvpn-helper-darwin-amd64
	@echo "✅ macOS Intel bundle: dist/myvpn-macos-intel-$(VERSION).zip"

bundle-macos-arm: build-macos-arm build-helper-macos-arm
	cd $(BUILD_DIR) && \
	curl -sLO "https://github.com/SagerNet/sing-box/releases/download/v$(SING_BOX_VERSION)/sing-box-$(SING_BOX_VERSION)-darwin-arm64.tar.gz" && \
	tar -xzf "sing-box-$(SING_BOX_VERSION)-darwin-arm64.tar.gz" && \
	mv "sing-box-$(SING_BOX_VERSION)-darwin-arm64/sing-box" . && \
	rm -rf "sing-box-$(SING_BOX_VERSION)-darwin-arm64" "sing-box-$(SING_BOX_VERSION)-darwin-arm64.tar.gz"
	cd $(BUILD_DIR) && zip -j myvpn-macos-arm-$(VERSION).zip myvpn-darwin-arm64 sing-box myvpn-helper-darwin-arm64
	@echo "✅ macOS ARM bundle: dist/myvpn-macos-arm-$(VERSION).zip"

bundle-all: bundle-windows bundle-macos-intel bundle-macos-arm
	@echo "═══════════════════════════════════════════"
	@echo "✅ All bundles complete!"
	@echo "   dist/myvpn-windows-$(VERSION).zip"
	@echo "   dist/myvpn-macos-intel-$(VERSION).zip"
	@echo "   dist/myvpn-macos-arm-$(VERSION).zip"
	@echo "═══════════════════════════════════════════"

# ── Clean ──
clean:
	rm -rf $(BUILD_DIR)
	rm -f $(APP_NAME) $(APP_NAME).exe
	@echo "✅ Cleaned build artifacts"
