APP_NAME := myvpn
VERSION := 1.0.0
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: all build-windows build-macos-intel build-macos-arm build-native clean test

all: build-windows build-macos-intel build-macos-arm

build-windows:
	GOOS=windows GOARCH=amd64 CGO_ENABLED=1 \
		CC=x86_64-w64-mingw32-gcc \
		go build -trimpath -ldflags="$(LDFLAGS)" -o $(APP_NAME)_windows_amd64.exe .

build-macos-intel:
	GOOS=darwin GOARCH=amd64 CGO_ENABLED=1 \
		go build -trimpath -ldflags="$(LDFLAGS)" -o $(APP_NAME)_darwin_amd64 .

build-macos-arm:
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=1 \
		go build -trimpath -ldflags="$(LDFLAGS)" -o $(APP_NAME)_darwin_arm64 .

build-native:
	CGO_ENABLED=1 go build -trimpath -ldflags="$(LDFLAGS)" -o $(APP_NAME) .

clean:
	rm -f $(APP_NAME) $(APP_NAME).exe $(APP_NAME)_darwin_*

test:
	go test -v -race ./internal/...
