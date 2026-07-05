# Mem3 and Intent Router Alignment Refactor

## Date
- `2026-06-23`

## Scope
- Rename the Rust memory service from Polaris to Mem3.
- Align Flory intent planning, DAG persistence, task dispatch, and task completion with the reviewed Mem3 lifecycle.
- Replace legacy memory query payloads with the versioned Mem3 Ingest/Search contracts.

## Delivered
- Moved the Rust service from `apps/polaris` to `apps/mem3`.
- Renamed runtime configuration from `POLARIS_*` / `FLORY_POLARIS_*` to `MEM3_*` / `FLORY_MEM3_*`.
- Added `POST /v1/memory/ingest` for `DAG_CONTEXT` and `TASK_OUTPUT`.
- Added `POST /v1/memory/search` with trusted scope, current Task metadata, recent window, canonical `mem_hint`, working memory, retrieval, and consistency sections.
- Split the Flory planning lifecycle into intent extraction and context-aware DAG planning when the router supports those capabilities.
- Pre-allocated `session_id` and `dag_id` so `DAG_CONTEXT` uses the same trusted scope later persisted by the scheduler.
- Added `tenant_id`, `agent_id`, Task `sequence`, `goal`, and canonical `mem_hint` to scheduler domain and MySQL persistence.
- Changed external node types to `skill` and `planner`; legacy values remain parse-only compatibility aliases.
- Added Mem3 Search before Task dispatch and TASK_OUTPUT Ingest after successful Task completion.
- Updated TS Worker expansion payloads, Docker Compose, Make targets, VS Code settings, README, and Ruby system fixtures.

## Compatibility
- Flory accepts legacy `SKILL_SINK`, `EXPAND_PLANNING`, `EXPANDING`, and `GRAPH_TRAVERSAL` values while normalizing persisted and emitted values to the reviewed contract.
- Legacy Mem3 endpoints remain available temporarily, but Flory no longer calls them.

## Verification
- `cd apps/flory && GOCACHE=/Users/linzhenbin/workspace/my_proj/aurora/.cache/go-build go test ./...`
- `cargo test --manifest-path apps/mem3/Cargo.toml`
- Ruby syntax checks for the updated system-test fixtures.
- `git diff --check`
- Local API smoke on `127.0.0.1:18082`: DAG_CONTEXT Ingest `202`, TASK_OUTPUT Ingest `202`, Search `200` with working memory/retrieval/consistency.

## Remaining Work
- Move Mem3 rolling-summary reduce and graph enrichment fully behind a durable asynchronous queue. The current API boundary returns `202 Accepted`, while the in-process MVP still performs part of the persistence/enrichment path synchronously.
- Replace deterministic dependency-output mem_hint refinement with the planned structured LLM call.
- Add a persistent MySQL + Mem3 integration test that verifies search occurs before worker dispatch and that TASK_OUTPUT idempotency survives retries.
