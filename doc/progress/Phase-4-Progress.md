# Phase 4 Progress

## Started At
- `2026-05-08T10:20:00+08:00`

## Scope of Current Increment
- Start Memory/GraphRAG track with a usable memory ingest + retrieval baseline in `polaris`.

## Delivered in This Increment
- Upgraded `polaris` ingest payload requirements:
  - now requires `user_id`, `session_id`, `task_id`, `summary`.
- Added retrieval endpoint:
  - `GET /memory/search?user_id=...&session_id=...&q=...&limit=...`
- Added retrieval semantics:
  - hard user isolation (`user_id` filter required)
  - optional session filter
  - keyword contains match on summary
  - result limit (default 20)
- Added unit tests:
  - ingest payload parsing with `user_id` requirement
  - search filtering for user-scope + limit behavior

## Verification
- `cargo test` in `apps/polaris` should pass with new memory retrieval tests.

## Pending in Phase 4
- Add `arqo -> polaris` retrieval call path for planner context injection.
- Add persistent memory backend abstraction (current in-memory vector is volatile).
- Add graph-structured memory representation (entity/relation extraction path).
