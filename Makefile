# fan-video-tr 常用操作
#
# 版本号统一取自 internal/version/version.go，编译时通过 ldflags 注入。
# 常用目标：
#   make build          编译当前平台二进制到 bin/
#   make run            本地启动（默认端口 8790）
#   make test           go vet + go test
#   make docker         构建本地 Docker 镜像
#   make release        走 scripts/build-and-push.sh 完整发版流程

SHELL := /bin/bash
GO ?= go
BINARY := fan-video-tr
PKG := github.com/meimolihan/fan-video-tr
VERSION := $(shell sed -n 's/^var Version = "\([0-9.]*\)"/\1/p' internal/version/version.go | head -1)
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X $(PKG)/internal/version.Version=$(VERSION) -X $(PKG)/internal/version.Commit=$(COMMIT) -X $(PKG)/internal/version.BuildTime=$(DATE)

DOCKER_IMAGE ?= mobufan/fan-video-tr
DOCKER_TAG ?= $(VERSION)

.PHONY: help build build-all run test fmt vet tidy clean docker docker-push install uninstall

help:
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-14s\033[0m %s\n", $$1, $$2}'

build: ## 编译当前平台二进制到 bin/
	@mkdir -p bin
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY) .
	@echo "完成: bin/$(BINARY) (v$(VERSION))"

build-all: ## 交叉编译 linux amd64 + arm64（与 release 资产同名）
	@mkdir -p bin
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY)_linux_amd64 .
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY)_linux_arm64 .
	@echo "完成: bin/$(BINARY)_linux_{amd64,arm64}"

run: ## 本地启动服务（可用 PORT/MEDIA/DATA 覆盖）
	@mkdir -p data
	$(GO) run . -port $${PORT:-8790} -media "$${MEDIA:-$$PWD}" -data $${DATA:-$$PWD/data}

test: ## 静态检查 + 单元测试
	$(GO) vet ./...
	$(GO) test ./...

fmt: ## 格式化
	gofmt -w .

tidy: ## 整理依赖
	$(GO) mod tidy

clean: ## 清理构建产物
	rm -rf bin dist

docker: ## 构建 Docker 镜像（不推送）
	docker build -t $(DOCKER_IMAGE):$(DOCKER_TAG) \
		--build-arg FVT_VERSION=$(VERSION) -t $(DOCKER_IMAGE):latest .

docker-push: ## 构建并推送 Docker 镜像
	docker buildx build --platform linux/amd64,linux/arm64 \
		--build-arg FVT_VERSION=$(VERSION) \
		-t $(DOCKER_IMAGE):$(DOCKER_TAG) -t $(DOCKER_IMAGE):latest --push .

install: ## 安装到当前系统（systemd 服务）
	bash scripts/install.sh

uninstall: ## 卸载并清理服务
	bash scripts/uninstall.sh
