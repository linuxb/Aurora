SHELL := /bin/zsh
GOCACHE ?= $(CURDIR)/.cache/go-build

.PHONY: help run-arqo run-worker run-polaris test-arqo test-polaris test check-env infra-up infra-down

help:
	@echo "Targets:"
	@echo "  run-arqo      - run Go gateway arqo"
	@echo "  run-worker    - run TypeScript worker"
	@echo "  run-polaris   - run Rust memory controller"
	@echo "  test          - run arqo/polaris tests"
	@echo "  check-env     - check go/rust/node/docker toolchain"
	@echo "  infra-up      - start mysql/redis/kvrocks/memgraph"
	@echo "  infra-down    - stop local infra"

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
	docker compose up -d mysql redis kvrocks memgraph

infra-down:
	docker compose down
