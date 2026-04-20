# Phase 1 Progress

## Started At
- `2026-04-18T18:40:00+08:00`

## Scope of Current Increment
- Add a usable real-time event flow in `arqo`.
- Keep implementation testable locally before wiring Redis backend.

## Delivered in This Increment
- `arqo` in-process event broker (`internal/events`).
- `arqo` telemetry ingest endpoint: `POST /v1/telemetry`.
- `arqo` SSE stream endpoint: `GET /v1/sessions/{sessionID}/events`.
- `arqo` system events on session/task lifecycle.
- `worker-ts` telemetry forwarding to `arqo`.
- `arqo` event backend abstraction (`memory` / `redis`).
- Redis Pub/Sub broker implementation (channel per session).
- Startup backend selection by env (`ARQO_EVENT_BACKEND`), with default `memory`.
- Docker Compose `arqo` defaults to Redis event backend for integration runs.
- Scheduler backend abstraction (`memory` / `mysql`) with env selection.
- MySQL scheduler store implementation (schema bootstrap + transactional pull/complete path).
- Mock-based backend selection tests without requiring live MySQL.
- MySQL runtime is gated by driver/environment readiness (safe fallback to `memory`).
- Added split compose strategy:
  - `docker-compose.dev.yml` for dependency-only local debug
  - `docker-compose.yml` for full-stack system runs
- Real docker-compose integration run completed on `2026-04-19`:
  - MySQL scheduler backend (`ARQO_SCHEDULER_BACKEND=mysql`) validated with real task lifecycle persistence.
  - Redis event backend (`ARQO_EVENT_BACKEND=redis`) validated with SSE event stream.
  - End-to-end demo DAG (`QueryLog -> LLMSummarize -> SendEmail`) completed with final `DAG=SUCCESS`.
- Fixed MySQL `PullReadyTask` scan mismatch bug found during integration:
  - Symptom: `POST /v1/tasks/pull` returned `500`, message: `sql: expected 7 destination arguments in Scan, not 12`.
  - Root cause: Ready-task query selected 7 columns but used 12-column scanner.
  - Fix: Added dedicated ready-task scanner path and covered by unit test.
- Added TiDB-compatible scheduler backend entry (`ARQO_SCHEDULER_BACKEND=tidb`) reusing mysql-compatible SQL path.
- Added concurrency safety tests in scheduler memory engine:
  - concurrent pull duplicate-lease prevention
  - concurrent complete idempotency and dependency-counter underflow guard

## Integration Verification (2026-04-19T23:00:00+08:00)
- Environment:
  - Infra from `make infra-up` already running: `mysql`, `redis`, `kvrocks`, `memgraph`.
  - `arqo` launched with MySQL scheduler + Redis broker.
  - `worker-ts` launched against local `arqo`.
- Network diagnostics summary:
  - Sandbox mode could not directly reach local ports (`127.0.0.1:3306/6379/7890`) or `proxy.golang.org`.
  - Elevated mode could reach local proxy `127.0.0.1:7890` and external Go proxy.
  - MySQL driver dependency (`github.com/go-sql-driver/mysql`) was downloaded via host proxy path in elevated mode.
- Lifecycle evidence for session `sess_000001` / DAG `dag_000001`:
  - Created at `2026-04-19T15:08:56.938770Z` with tasks:
    - `task_000001 QueryLog READY`
    - `task_000002 LLMSummarize PENDING(dep=task_000001)`
    - `task_000003 SendEmail PENDING(dep=task_000002)`
  - SSE timeline (UTC):
    - `15:09:18.737343Z` `TASK_LEASED task_000001`
    - `15:09:18.742Z` `NODE_START task_000001`
    - `15:09:18.948Z` `NODE_FINISH task_000001`
    - `15:09:18.964145Z` `TASK_COMPLETED task_000001`
    - `15:09:18.968954Z` `TASK_LEASED task_000002`
    - `15:09:18.970Z` `NODE_START task_000002`
    - `15:09:19.123Z` `NODE_FINISH task_000002`
    - `15:09:19.138063Z` `TASK_COMPLETED task_000002`
    - `15:09:19.144356Z` `TASK_LEASED task_000003`
    - `15:09:19.145Z` `NODE_START task_000003`
    - `15:09:19.267Z` `NODE_FINISH task_000003`
    - `15:09:19.281401Z` `TASK_COMPLETED task_000003`
  - Final persisted result:
    - `dags.status=SUCCESS`, `replan_count=0`
    - all tasks `SUCCESS`, `pending_dependencies_count=0`
    - `task_raw_data` present for all 3 tasks.

## Pending in Phase 1
- Add concurrency tests for duplicate lease prevention with persistent storage.
- Run real TiDB integration verification and collect SQL compatibility checklist.

## New Decision Points for Phase 1
1. `2026-04-18T18:40:00+08:00` | Should we keep SSE event payload versioned (`schema_version`) from this phase?
2. `2026-04-18T18:40:00+08:00` | Should telemetry ingestion be best-effort (current) or strict-fail when broker/publish fails?
