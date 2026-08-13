# Hify Makefile
#
# 环境切换（ENV 入参）：
#   ENV=dev（默认）：本地开发 —— start/stop 走 start.sh/stop.sh（go build + vite dev 热重载）
#   ENV=prod        ：生产部署 —— start/stop 走 docker compose（镜像构建 + 容器编排）
# 用法：make start ENV=prod、make build ENV=prod、make stop ENV=prod …
#
# 常用：make（帮助） / start / stop / restart / build / clean / package / certs

GO       ?= go
BINARY   := bin/hify
WEB_DIR  := web
DIST_DIR := dist
VERSION  := $(shell git rev-parse --short HEAD 2>/dev/null || echo dev)
PKG_NAME := hify-$(VERSION)

ENV ?= dev

COMPOSE := docker compose -f deploy/docker-compose.yml --env-file deploy/.env

.DEFAULT_GOAL := help

.PHONY: help start stop restart build build-backend build-frontend clean package \
        certs compose-up compose-down migrate-up migrate-down migrate-status

help:
	@echo "Hify Makefile（当前 ENV=$(ENV)）"
	@echo "  make start [ENV=dev|prod]   启动（dev=本地脚本 / prod=docker compose）"
	@echo "  make stop  [ENV=dev|prod]   停止"
	@echo "  make restart [ENV=dev|prod] 重启"
	@echo "  make build [ENV=dev|prod]   构建后端二进制 + 前端 dist（prod 后端加 -s -w 瘦身）"
	@echo "  make clean                  清理构建产物（bin/ web/dist/ dist/；不动 node_modules、logs）"
	@echo "  make package                打包可分发 tar.gz（依赖 build）"
	@echo "  make certs                  生成自签 TLS 证书（开发用，输出 deploy/certs/）"
	@echo "  make migrate-up             应用全部未执行迁移（goose，./hify migrate up）"
	@echo "  make migrate-down           回滚最近一个迁移"
	@echo "  make migrate-status         查看迁移状态"

# ---- 启动 / 停止 / 重启：按 ENV 分派 ----
ifeq ($(ENV),prod)
start:
	@$(MAKE) compose-up
stop:
	@$(MAKE) compose-down
restart:
	@$(COMPOSE) restart
else
start:
	@./start.sh
stop:
	@./stop.sh
restart: stop start
endif

# ---- 构建 ----
# prod 后端加 -ldflags="-s -w"（CLAUDE.md §镜像）；dev 不加，利调试
ifeq ($(ENV),prod)
LDFLAGS := -s -w
endif

build: build-backend build-frontend

build-backend:
	$(GO) build $(if $(LDFLAGS),-ldflags="$(LDFLAGS)") -o $(BINARY) ./cmd/hify

build-frontend: $(WEB_DIR)/node_modules
	cd $(WEB_DIR) && npm run build

$(WEB_DIR)/node_modules:
	cd $(WEB_DIR) && npm install

# ---- 清理 ----
clean:
	rm -rf bin web/dist $(DIST_DIR)

# ---- 打包 ----
package: build
	@rm -rf $(DIST_DIR)
	@mkdir -p $(DIST_DIR)/$(PKG_NAME)
	@cp bin/hify       $(DIST_DIR)/$(PKG_NAME)/hify
	@cp -R web/dist    $(DIST_DIR)/$(PKG_NAME)/dist
	@cp -R migrations  $(DIST_DIR)/$(PKG_NAME)/migrations
	@cp -R deploy      $(DIST_DIR)/$(PKG_NAME)/deploy
	@rm -rf $(DIST_DIR)/$(PKG_NAME)/deploy/certs $(DIST_DIR)/$(PKG_NAME)/deploy/.env
	@cp .env.example   $(DIST_DIR)/$(PKG_NAME)/.env.example
	@echo "$(VERSION)" > $(DIST_DIR)/$(PKG_NAME)/VERSION
	@tar -C $(DIST_DIR) -czf $(DIST_DIR)/$(PKG_NAME).tar.gz $(PKG_NAME)
	@rm -rf $(DIST_DIR)/$(PKG_NAME)
	@echo "==> 打包完成: $(DIST_DIR)/$(PKG_NAME).tar.gz"

# ---- Docker 部署 ----
# 前置：deploy/.env（配置）+ deploy/certs/fullchain.pem（TLS 证书）
deploy/.env:
	@echo "==> 缺少 deploy/.env：请先 cp deploy/.env.example deploy/.env 并填好密码与密钥"
	@exit 1

deploy/certs/fullchain.pem:
	@echo "==> 缺少 TLS 证书：开发环境可 make certs 生成自签；生产请放正式证书到 deploy/certs/"
	@exit 1

compose-up: deploy/.env deploy/certs/fullchain.pem
	@command -v docker >/dev/null 2>&1 || { echo "==> 未找到 docker，请先安装 Docker Desktop"; exit 1; }
	$(COMPOSE) up -d --build

compose-down: deploy/.env
	@command -v docker >/dev/null 2>&1 || { echo "==> 未找到 docker，请先安装 Docker Desktop"; exit 1; }
	$(COMPOSE) down

# 自签证书（仅开发；生产请用正式证书替换）
certs:
	@mkdir -p deploy/certs
	@openssl req -x509 -nodes -days 365 -newkey rsa:2048 \
	  -keyout deploy/certs/privkey.pem -out deploy/certs/fullchain.pem \
	  -subj "/CN=localhost" >/dev/null 2>&1
	@echo "==> 已生成自签证书 deploy/certs/（开发用）；生产环境请替换为正式证书"

# ---- 数据库迁移（goose，经 ./hify migrate 子命令；需本地 PG 就绪 + .env 配置）----
migrate-up:
	@$(GO) run ./cmd/hify migrate up

migrate-down:
	@$(GO) run ./cmd/hify migrate down

migrate-status:
	@$(GO) run ./cmd/hify migrate status
