SHELL := /bin/bash

.DEFAULT_GOAL := help

GO ?= go
NPM ?= npm
ROOT_DIR := $(abspath .)

PUID ?= $(shell id -u)
GUID ?= $(shell id -g)

CORAZA_PORT ?= 9090
HOST_CORAZA_PORT ?= 19090
WAF_LISTEN_PORT ?= 9090
WAF_API_KEY_PRIMARY ?= dev-only-change-this-key-please

BENCH_REQUESTS ?= 120
WARMUP_REQUESTS ?= 20

CORAZA_SRC := coraza/src
UI_DIR := web/mamotama-admin
UI_EMBED_DIR := coraza/src/internal/handler/admin_ui_dist
BIN_DIR ?= bin
APP_NAME ?= mamotama-proxy
APP_PKG ?= ./cmd/server

export PUID GUID CORAZA_PORT HOST_CORAZA_PORT WAF_LISTEN_PORT WAF_API_KEY_PRIMARY

.PHONY: \
	help setup env-init crs-install \
	go-test go-build build \
	ui-install ui-test ui-build ui-sync ui-build-sync \
	compose-config compose-config-mysql compose-up compose-down mysql-up mysql-down \
	migrate-proxy-config migrate-proxy-config-check \
	smoke bench gotestwaf \
	check ci-local clean

help:
	@echo "Targets:"
	@echo "  make setup                      Prepare local dev baseline (.env + CRS)"
	@echo "  make env-init                   Create .env from .env.example if missing"
	@echo "  make crs-install                Install OWASP CRS into data/rules/crs"
	@echo ""
	@echo "  make go-test                    Run Go tests (coraza/src)"
	@echo "  make go-build                   Build single binary into ./bin/$(APP_NAME)"
	@echo "  make build                      One-shot build (ui-build-sync + go-build)"
	@echo ""
	@echo "  make ui-install                 Install admin UI dependencies"
	@echo "  make ui-test                    Run admin UI tests"
	@echo "  make ui-build                   Build admin UI static assets"
	@echo "  make ui-sync                    Sync web dist -> go:embed assets"
	@echo "  make ui-build-sync              Build and sync embedded UI assets"
	@echo ""
	@echo "  make compose-config             Validate docker compose config"
	@echo "  make compose-config-mysql       Validate compose config with mysql profile"
	@echo "  make compose-up                 Start coraza service"
	@echo "  make compose-down               Stop stack"
	@echo "  make mysql-up                   Start local mysql profile service"
	@echo "  make mysql-down                 Stop local mysql profile service"
	@echo ""
	@echo "  make migrate-proxy-config       Generate data/conf/proxy.json from legacy env"
	@echo "  make migrate-proxy-config-check Validate proxy config file"
	@echo "  make smoke                      Run embedded UI + proxy-rules smoke checks"
	@echo "  make bench                      Run proxy tuning benchmark presets"
	@echo "  make gotestwaf                  Run GoTestWAF regression"
	@echo ""
	@echo "  make check                      Run go-test + ui-test + compose config checks"
	@echo "  make ci-local                   Run local CI baseline (check + smoke)"
	@echo "  make clean                      Remove local build/test artifacts"
	@echo ""
	@echo "Key variables (override as needed):"
	@echo "  CORAZA_PORT=$(CORAZA_PORT) HOST_CORAZA_PORT=$(HOST_CORAZA_PORT) WAF_LISTEN_PORT=$(WAF_LISTEN_PORT)"
	@echo "  WAF_API_KEY_PRIMARY=<api-key> BENCH_REQUESTS=$(BENCH_REQUESTS) WARMUP_REQUESTS=$(WARMUP_REQUESTS)"

env-init:
	@if [[ ! -f .env ]]; then \
		cp .env.example .env; \
		echo "[env-init] created .env from .env.example"; \
	else \
		echo "[env-init] .env already exists (skip)"; \
	fi

crs-install:
	./scripts/install_crs.sh

setup: env-init crs-install

go-test:
	cd $(CORAZA_SRC) && $(GO) test ./...

go-build:
	mkdir -p $(abspath $(BIN_DIR))
	cd $(CORAZA_SRC) && $(GO) build -o "$(abspath $(BIN_DIR))/$(APP_NAME)" $(APP_PKG)
	@echo "[go-build] built $(abspath $(BIN_DIR))/$(APP_NAME)"

build: ui-build-sync go-build

ui-install:
	cd $(UI_DIR) && $(NPM) ci

ui-test: ui-install
	cd $(UI_DIR) && $(NPM) test -- --runInBand

ui-build: ui-install
	cd $(UI_DIR) && $(NPM) run build

ui-sync:
	@if [[ ! -d "$(UI_DIR)/dist" ]]; then \
		echo "[ui-sync] missing $(UI_DIR)/dist (run: make ui-build)"; \
		exit 1; \
	fi
	rm -rf $(UI_EMBED_DIR)
	mkdir -p $(UI_EMBED_DIR)
	cp -a $(UI_DIR)/dist/. $(UI_EMBED_DIR)/
	@echo "[ui-sync] synced $(UI_DIR)/dist -> $(UI_EMBED_DIR)"

ui-build-sync: ui-build ui-sync

compose-config:
	docker compose config >/dev/null
	@echo "[compose-config] ok"

compose-config-mysql:
	docker compose --profile mysql config >/dev/null
	@echo "[compose-config-mysql] ok"

compose-up:
	PUID="$(PUID)" GUID="$(GUID)" CORAZA_PORT="$(CORAZA_PORT)" WAF_LISTEN_PORT="$(WAF_LISTEN_PORT)" docker compose up -d --build coraza

compose-down:
	PUID="$(PUID)" GUID="$(GUID)" CORAZA_PORT="$(CORAZA_PORT)" docker compose down --remove-orphans

mysql-up:
	PUID="$(PUID)" GUID="$(GUID)" docker compose --profile mysql up -d mysql

mysql-down:
	PUID="$(PUID)" GUID="$(GUID)" docker compose --profile mysql down -v

migrate-proxy-config:
	./scripts/migrate_proxy_config.sh

migrate-proxy-config-check:
	./scripts/migrate_proxy_config.sh --check

smoke:
	@set -euo pipefail; \
	trap 'PUID="$(PUID)" GUID="$(GUID)" CORAZA_PORT="$(HOST_CORAZA_PORT)" docker compose down --remove-orphans >/dev/null 2>&1 || true' EXIT; \
	PUID="$(PUID)" GUID="$(GUID)" CORAZA_PORT="$(HOST_CORAZA_PORT)" WAF_LISTEN_PORT="$(WAF_LISTEN_PORT)" docker compose up -d --build coraza >/dev/null; \
	HOST_CORAZA_PORT="$(HOST_CORAZA_PORT)" WAF_LISTEN_PORT="$(WAF_LISTEN_PORT)" WAF_API_KEY_PRIMARY="$(WAF_API_KEY_PRIMARY)" ./scripts/ci_proxy_admin_smoke.sh

bench:
	HOST_CORAZA_PORT="$(HOST_CORAZA_PORT)" WAF_LISTEN_PORT="$(WAF_LISTEN_PORT)" WAF_API_KEY_PRIMARY="$(WAF_API_KEY_PRIMARY)" BENCH_REQUESTS="$(BENCH_REQUESTS)" WARMUP_REQUESTS="$(WARMUP_REQUESTS)" ./scripts/benchmark_proxy_tuning.sh

gotestwaf:
	HOST_CORAZA_PORT="$(HOST_CORAZA_PORT)" WAF_LISTEN_PORT="$(WAF_LISTEN_PORT)" ./scripts/run_gotestwaf.sh

check: go-test ui-test compose-config compose-config-mysql

ci-local: check smoke

clean:
	rm -rf $(BIN_DIR) $(UI_DIR)/.tmp-test
	@echo "[clean] done"
