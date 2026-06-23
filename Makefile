SHELL := /bin/zsh
GOCACHE ?= $(CURDIR)/.cache/go-build

.PHONY: help run-arqo run-worker run-mem3 test-arqo test-mem3 test check-env infra-up infra-down infra-up-dev infra-down-dev infra-up-full infra-down-full test-smoke-ruby test-fault-ruby test-missing-skill-ruby test-self-heal-ruby test-self-heal-persistent-ruby test-regression-ruby hardening-regression hardening-tidb

help:
	@echo "Targets:"
	@echo "  run-arqo      - run Go gateway arqo"
	@echo "  run-worker    - run TypeScript worker"
	@echo "  run-mem3      - run Rust memory controller"
	@echo "  test          - run arqo/mem3 tests"
	@echo "  check-env     - check go/rust/node/docker toolchain"
	@echo "  infra-up      - start dev infra dependencies only (mysql/redis/kvrocks/memgraph)"
	@echo "  infra-down    - stop dev infra dependencies"
	@echo "  infra-up-full - start full stack from docker-compose.yml"
	@echo "  infra-down-full - stop full stack from docker-compose.yml"
	@echo "  test-smoke-ruby - run Ruby smoke E2E for arqo session DAG"
	@echo "  test-fault-ruby - run Ruby fault-injection checks"
	@echo "  test-missing-skill-ruby - run Ruby regression for missing_skill guardrail"
	@echo "  test-self-heal-ruby - run Ruby drill for crash -> sweep -> replan -> resume"
	@echo "  test-self-heal-persistent-ruby - run persistent-backend self-heal loop drill with metrics"
	@echo "  test-regression-ruby - run full Ruby API regression suite (smoke -> fault -> missing_skill)"
	@echo "  hardening-regression - run fixed deferred hardening checks"
	@echo "  hardening-tidb - run TiDB smoke check (requires TiDB env)"

run-arqo:
	mkdir -p $(GOCACHE)
	cd apps/arqo && GOCACHE=$(GOCACHE) go run .

run-worker:
	cd apps/worker-ts && npm run dev

run-mem3:
	cd apps/mem3 && cargo run

test-arqo:
	mkdir -p $(GOCACHE)
	cd apps/arqo && GOCACHE=$(GOCACHE) go test ./...

test-mem3:
	cd apps/mem3 && cargo test

test: test-arqo test-mem3

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

test-smoke-ruby:
	ruby tools/testing/arqo_smoke.rb

test-fault-ruby:
	ruby tools/testing/arqo_fault_injector.rb

test-missing-skill-ruby:
	ruby tools/testing/arqo_missing_skill_regression.rb

test-self-heal-ruby:
	ruby tools/testing/arqo_self_heal_drill.rb

test-self-heal-persistent-ruby:
	ruby tools/testing/arqo_self_heal_persistent_drill.rb

test-regression-ruby:
	ruby tools/testing/arqo_regression_suite.rb

hardening-regression: test-arqo test-smoke-ruby test-fault-ruby
	@echo "[hardening] baseline regression checks passed"
	@echo "[hardening] run 'make hardening-tidb' when TiDB is available"

hardening-tidb:
	@if [ -z "$$ARQO_TIDB_DSN" ]; then \
		echo "[hardening] skip TiDB check: ARQO_TIDB_DSN is not set"; \
		echo "[hardening] example: export ARQO_TIDB_DSN='aurora:aurora@tcp(127.0.0.1:4000)/aurora?parseTime=true&multiStatements=true'"; \
		exit 0; \
	fi
	@echo "[hardening] TiDB DSN detected. Start TiDB compatibility smoke in next increment."
