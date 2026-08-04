BINARY_NAME ?= vohive
GO_TAGS ?= with_utls nomsgpack
GOOS ?= linux
CGO_ENABLED ?= 0
VERSION ?= $(shell git describe --tags --always 2>/dev/null || echo "unknown")
VERSION_TAG = $(if $(filter v%,$(VERSION)),$(VERSION),v$(VERSION))
BUILD_TIME ?= $(shell date "+%Y-%m-%d %H:%M:%S")
DIST_DIR ?= dist
MAIN_PACKAGE ?= ./cmd/vohive

LDFLAGS = -s -w -X 'github.com/swordstudiox/vohive-plus/internal/global.Version=$(VERSION)' -X 'github.com/swordstudiox/vohive-plus/internal/global.BuildTime=$(BUILD_TIME)'
GO_BUILD = go build -trimpath -buildvcs=false -tags "$(GO_TAGS)" -ldflags "$(LDFLAGS)"

AMD64_OUT = $(DIST_DIR)/$(BINARY_NAME)_$(VERSION_TAG)_linux_amd64
ARM64_OUT = $(DIST_DIR)/$(BINARY_NAME)_$(VERSION_TAG)_linux_arm64
ARMV7_OUT = $(DIST_DIR)/$(BINARY_NAME)_$(VERSION_TAG)_linux_armv7
UPX ?= $(shell command -v upx || command -v upx-ucl)
UPX_FLAGS ?= --best --lzma

.PHONY: all build build-amd64 build-arm64 build-armv7 build-all frontend-dist clean

all: build-all

build: build-amd64

build-all: build-amd64 build-arm64 build-armv7

frontend-dist:
	npm ci --prefix web
	npm run build --prefix web
	rm -rf internal/web/dist
	mkdir -p internal/web
	cp -R web/dist internal/web/dist

build-amd64: frontend-dist
	@test -n "$(UPX)" || { echo "错误: 需要安装 upx"; exit 1; }
	mkdir -p $(DIST_DIR)
	CGO_ENABLED=$(CGO_ENABLED) GOOS=$(GOOS) GOARCH=amd64 $(GO_BUILD) -o $(AMD64_OUT) $(MAIN_PACKAGE)
	$(UPX) $(UPX_FLAGS) $(AMD64_OUT)

build-arm64: frontend-dist
	@test -n "$(UPX)" || { echo "错误: 需要安装 upx"; exit 1; }
	mkdir -p $(DIST_DIR)
	CGO_ENABLED=$(CGO_ENABLED) GOOS=$(GOOS) GOARCH=arm64 $(GO_BUILD) -o $(ARM64_OUT) $(MAIN_PACKAGE)
	$(UPX) $(UPX_FLAGS) $(ARM64_OUT)

build-armv7: frontend-dist
	@test -n "$(UPX)" || { echo "错误: 需要安装 upx"; exit 1; }
	mkdir -p $(DIST_DIR)
	CGO_ENABLED=$(CGO_ENABLED) GOOS=$(GOOS) GOARCH=arm GOARM=7 $(GO_BUILD) -o $(ARMV7_OUT) $(MAIN_PACKAGE)
	$(UPX) $(UPX_FLAGS) $(ARMV7_OUT)

clean:
	go clean
	rm -rf $(DIST_DIR)
