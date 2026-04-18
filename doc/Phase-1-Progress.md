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

## Pending in Phase 1
- Replace in-process broker with Redis Pub/Sub backend.
- Replace in-memory scheduler store with MySQL/TiDB-backed repository.
- Add concurrency tests for duplicate lease prevention with persistent storage.

## New Decision Points for Phase 1
1. `2026-04-18T18:40:00+08:00` | Should we keep SSE event payload versioned (`schema_version`) from this phase?
2. `2026-04-18T18:40:00+08:00` | Should telemetry ingestion be best-effort (current) or strict-fail when broker/publish fails?
