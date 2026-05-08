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
- Added `file_md` backend quality gates:
  - retention/rotation by session (`POLARIS_MEMORY_FS_MAX_FILES_PER_SESSION`)
  - concurrent-write protection with internal write mutex
  - corruption-tolerant markdown reads (broken files are skipped)
  - added tests for rotation and corruption tolerance
- Added `SearchMemoryGraph` style retrieval baseline:
  - new endpoint: `GET /memory/graph/search?user_id=...&session_id=...&q=...&limit=...`
  - enforces `user_id` isolation via existing scoped memory search
  - builds lightweight co-occurrence graph (`nodes` + `edges`) from memory summaries
  - added graph-construction unit test

## Verification
- `cargo test` in `apps/polaris` should pass with new memory retrieval tests.
- `go test ./...` in `apps/arqo` should pass with memory injection API tests.

## Pending in Phase 4
- Add graph-structured memory representation (entity/relation extraction path).
- Define and implement `arqo -> polaris` memory-query strategy controls:
  - query rewrite policy
  - hit-ranking/limit policy
  - timeout/fallback policy
