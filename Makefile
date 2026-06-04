APP        := swift-devops
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GO_LDFLAGS := -s -w -X main.Version=$(VERSION)
GOFLAGS    := -trimpath -ldflags "$(GO_LDFLAGS)"

WEB_DIR    := web
WEB_DIST   := $(WEB_DIR)/dist
WIN_DIR    := dist/windows
WIN_SETUP_ASSET_DIR := cmd/swift-devops-setup/assets
WIN_SETUP := dist/release/$(APP)-setup-windows-amd64-$(VERSION).exe

.PHONY: all build web go-build build-windows release release-arm64 release-windows run dev tidy fmt vet clean

all: build

# --- 前端 ---
web:
	@if [ ! -d "$(WEB_DIR)/node_modules" ]; then \
	  echo ">> installing web deps"; \
	  cd $(WEB_DIR) && ( command -v pnpm >/dev/null && pnpm install || npm install ); \
	fi
	@cd $(WEB_DIR) && ( command -v pnpm >/dev/null && pnpm build || npm run build )

# --- 后端（纯 Go，无 CGO，可静态编译） ---
go-build:
	@test -f $(WEB_DIST)/index.html || (echo "!! $(WEB_DIST)/index.html missing; run 'make web' first" && exit 1)
	@mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(GOFLAGS) -o dist/$(APP) ./cmd/swift-devops

# Windows exe（前端静态资源已 embed 到二进制）
build-windows: web
	@mkdir -p $(WIN_DIR)
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build $(GOFLAGS) -o $(WIN_DIR)/$(APP).exe ./cmd/swift-devops

# 完整构建（前端 + 后端）
build: web go-build

# --- 发布包 ---
release: build
	@mkdir -p dist/release
	tar -czf dist/release/$(APP)-linux-amd64-$(VERSION).tar.gz -C dist $(APP)
	cp deploy/install.sh dist/release/
	@echo ">> release artifacts in dist/release/"

release-arm64: web
	@mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build $(GOFLAGS) -o dist/$(APP)-arm64 ./cmd/swift-devops
	@mkdir -p dist/release
	tar -czf dist/release/$(APP)-linux-arm64-$(VERSION).tar.gz -C dist $(APP)-arm64
	cp deploy/install.sh dist/release/

release-windows: build-windows
	@mkdir -p dist/release
	@mkdir -p $(WIN_SETUP_ASSET_DIR)
	cp $(WIN_DIR)/$(APP).exe $(WIN_SETUP_ASSET_DIR)/$(APP).exe
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -tags windows_installer $(GOFLAGS) -o $(WIN_SETUP) ./cmd/swift-devops-setup
	@echo ">> windows installer artifact: $(WIN_SETUP)"

# --- 本地跑 ---
run: build
	./dist/$(APP) serve --config deploy/config.yaml.example

# 开发模式：前端 5173 反代后端 8088
dev:
	@echo ">> backend: go run ./cmd/swift-devops serve --config deploy/config.yaml.example"
	@echo ">> frontend: cd web && pnpm dev"
	cd $(WEB_DIR) && ( command -v pnpm >/dev/null && pnpm dev || npm run dev )

# --- 杂项 ---
tidy:
	go mod tidy

fmt:
	gofmt -s -w .

vet:
	go vet ./...

clean:
	rm -rf dist $(WEB_DIST) $(WEB_DIR)/node_modules
	rm -f $(WIN_SETUP_ASSET_DIR)/$(APP).exe
