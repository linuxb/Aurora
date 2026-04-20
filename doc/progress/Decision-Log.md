# Aurora Decision Log

This file records architecture and product decisions with traceability.

## Record Format
- `recorded_at`: RFC3339 timestamp with timezone
- `phase`: phase identifier (for example, `Phase 0`, `Phase 1`)
- `topic`: short decision topic
- `decision`: final decision
- `status`: `decided` | `pending` | `superseded`
- `context`: why this was discussed
- `impact`: expected technical impact
- `owner`: who confirmed it
- `source`: where the discussion/reply is recorded
- `follow_up`: next action (if any)

## Decision Entries

### 2026-04-18T18:17:00+08:00 | Phase 0 | Intent Slotting model path
- `status`: decided
- `decision`: Start with local lightweight model path (for example Llama 8B), but allow mocked model responses in early development while finishing call flow and result post-processing.
- `context`: Real local model runtime may not be ready at the beginning.
- `impact`: Keep model integration interface stable and unblock development.
- `owner`: user
- `source`: `doc/progress/Phase-0-Progress.md`
- `follow_up`: Implement mockable intent-router model client in Phase 2.

### 2026-04-18T18:17:00+08:00 | Phase 0 | Graph database choice
- `status`: decided
- `decision`: Use Memgraph for the first implementation.
- `context`: Need fast local iteration and early integration.
- `impact`: Simplifies local setup and aligns with current docker-compose.
- `owner`: user
- `source`: `doc/progress/Phase-0-Progress.md`
- `follow_up`: Add Memgraph integration in `polaris` Phase 4.

### 2026-04-18T18:17:00+08:00 | Phase 0 | Replanning capability scope
- `status`: decided
- `decision`: Support subgraph replacement and rollback from the first replanning implementation.
- `context`: Compensation-only approach is insufficient for target reliability.
- `impact`: Requires transaction-safe patch application and rollback strategy.
- `owner`: user
- `source`: `doc/progress/Phase-0-Progress.md`
- `follow_up`: Design rollback mechanism in Phase 3 design doc.

### 2026-04-18T18:17:00+08:00 | Phase 0 | Short-term memory compression policy
- `status`: decided
- `decision`: Start with fixed threshold rolling compression, then migrate to dynamic threshold later.
- `context`: Fixed threshold is easier for MVP validation.
- `impact`: Faster implementation in early phases and clear baseline metrics.
- `owner`: user
- `source`: `doc/progress/Phase-0-Progress.md`
- `follow_up`: Introduce dynamic strategy experiments in Phase 4.

### 2026-04-18T18:30:00+08:00 | Phase 0 | Phase closure record
- `status`: decided
- `decision`: Phase 0 accepted as completed with runnable scaffold and passing baseline tests.
- `context`: MVP skeleton, tests, and local debug setup are available.
- `impact`: Team can proceed to Phase 1 infra-backed scheduler and event streaming.
- `owner`: assistant + user
- `source`: `doc/progress/Phase-0-Progress.md`
- `follow_up`: Continue with Phase 1 increment and track new decision points.

### 2026-04-18T18:40:00+08:00 | Phase 1 | Event stream first increment
- `status`: decided
- `decision`: Build in-process event broker + SSE + telemetry ingest first, then replace broker backend with Redis Pub/Sub.
- `context`: Deliver testable event flow quickly before infra coupling.
- `impact`: Enables frontend/CLI live updates now and preserves migration path.
- `owner`: user + assistant
- `source`: `doc/progress/Phase-1-Progress.md`
- `follow_up`: Continue Phase 1 with persistent scheduler store (MySQL/TiDB) and concurrency tests.

### 2026-04-18T18:55:00+08:00 | Phase 1 | Scheduler persistence rollout strategy
- `status`: decided
- `decision`: Implement MySQL/TiDB scheduler logic first with mock-based tests, then validate against local docker-compose MySQL runtime.
- `context`: Local MySQL/TiDB environment is not ready yet.
- `impact`: Keeps Phase 1 moving while preserving a concrete integration path.
- `owner`: user + assistant
- `source`: conversation + `doc/progress/Phase-1-Progress.md`
- `follow_up`: Provide docker-compose setup and local connection guide for integration verification.

### 2026-04-18T19:10:00+08:00 | Phase 1 | Docker compose split for dev vs system tests
- `status`: decided
- `decision`: Use a dedicated `docker-compose.dev.yml` for dependency-only local development, while keeping `docker-compose.yml` for full-stack system testing.
- `context`: Daily debugging should run Aurora services on macOS host with IDE breakpoints.
- `impact`: Faster local iteration and cleaner separation of dev/debug vs end-to-end stack runs.
- `owner`: user + assistant
- `source`: conversation + `doc/dev/Local-Dev-Debug-Setup.md`
- `follow_up`: Keep `Makefile` commands aligned with both compose modes.

### 2026-04-20T10:00:00+08:00 | Phase 1 | TiDB compatibility start
- `status`: decided
- `decision`: Start TiDB compatibility implementation now; do not keep scheduler backend fixed to MySQL only.
- `context`: Phase 1 pending item response from user.
- `impact`: Scheduler backend now supports `memory` / `mysql` / `tidb` entry paths with shared mysql-compatible SQL logic.
- `owner`: user + assistant
- `source`: conversation + `doc/progress/Phase-1-Progress.md`
- `follow_up`: Run real TiDB integration verification and maintain SQL compatibility checklist.
