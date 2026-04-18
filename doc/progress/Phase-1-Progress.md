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

## Pending in Phase 1
- Validate MySQL scheduler flow with real docker-compose MySQL runtime.
- Decide TiDB migration timing and SQL compatibility checklist.
- Add concurrency tests for duplicate lease prevention with persistent storage.

## New Decision Points for Phase 1
1. `2026-04-18T18:40:00+08:00` | Should we keep SSE event payload versioned (`schema_version`) from this phase?
2. `2026-04-18T18:40:00+08:00` | Should telemetry ingestion be best-effort (current) or strict-fail when broker/publish fails?
