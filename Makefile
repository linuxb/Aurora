SHELL := /bin/zsh
GOCACHE ?= $(CURDIR)/.cache/go-build

.PHONY: help run-arqo run-worker run-polaris test-arqo test-polaris test check-env infra-up infra-down infra-up-dev infra-down-dev infra-up-full infra-down-full

help:
	@echo "Targets:"
	@echo "  run-arqo      - run Go gateway arqo"
	@echo "  run-worker    - run TypeScript worker"
	@echo "  run-polaris   - run Rust memory controller"
	@echo "  test          - run arqo/polaris tests"
	@echo "  check-env     - check go/rust/node/docker toolchain"
	@echo "  infra-up      - start dev infra dependencies only (mysql/redis/kvrocks/memgraph)"
	@echo "  infra-down    - stop dev infra dependencies"
	@echo "  infra-up-full - start full stack from docker-compose.yml"
	@echo "  infra-down-full - stop full stack from docker-compose.yml"

run-arqo:
	mkdir -p $(GOCACHE)
	cd apps/arqo && GOCACHE=$(GOCACHE) go run .

run-worker:
	cd apps/worker-ts && npm run dev

run-polaris:
	cd apps/polaris && cargo run

test-arqo:
	mkdir -p $(GOCACHE)
	cd apps/arqo && GOCACHE=$(GOCACHE) go test ./...

test-polaris:
	cd apps/polaris && cargo test

test: test-arqo test-polaris

check-env:
	@echo "Go:" && go version
	@echo "Rust:" && rustc --version && cargo --version
	@echo "Node:" && node -v && npm -v
	@echo "TypeScript compiler(global optional):" && (tsc -v || echo "tsc not installed globally")
	@echo "Docker:" && docker --version && docker compose version

infra-up:
	docker compose -f docker-compose.dev.yml up -d

infra-up-dev:
	docker compose -f docker-compose.dev.yml up -d

infra-down:
	docker compose -f docker-compose.dev.yml down

infra-down-dev:
	docker compose -f docker-compose.dev.yml down

infra-up-full:
	docker compose -f docker-compose.yml up -d

infra-down-full:
	docker compose -f docker-compose.yml down
