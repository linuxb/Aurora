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
- Added `arqo -> polaris` retrieval integration on session planning:
  - `arqo` optionally calls `polaris /memory/search` when `ARQO_POLARIS_URL` is configured
  - memory hits are injected into planner `intent_context` as:
    - `memory_hits`
    - `memory_hit_count`
  - integration is best-effort and non-blocking for session creation
- Added API test coverage for memory context injection in create-session response
- Refactored `polaris` memory backend into explicit store abstraction:
  - introduced `MemoryStore` trait (`ingest/list_all/search`)
  - introduced `InMemoryStore` implementation behind `Arc<dyn MemoryStore>`
  - HTTP handlers now consume store interface instead of direct shared vector access
  - keeps current behavior stable while preparing for KV/graph-backed store implementations
- Added multi-backend memory preparation in `polaris`:
  - backend factory from env (`POLARIS_MEMORY_BACKEND`)
  - `memory` backend (default in-memory store)
  - `file_md` backend with markdown persistence (`POLARIS_MEMORY_FS_DIR`)
  - markdown entry format includes `user_id/session_id/task_id` header and summary body
  - added persistence+search test for `FileMarkdownStore`

## Verification
- `cargo test` in `apps/polaris` should pass with new memory retrieval tests.
- `go test ./...` in `apps/arqo` should pass with memory injection API tests.

## Pending in Phase 4
- Add `arqo -> polaris` retrieval call path for planner context injection.
- Add persistent memory backend abstraction (current in-memory vector is volatile).
- Add graph-structured memory representation (entity/relation extraction path).
