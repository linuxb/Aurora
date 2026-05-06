# Phase 2 Progress

## Started At
- `2026-05-05T12:05:00+08:00`

## Scope of Current Increment
- Start Intent Router + DAG Validator track with a testable static DAG validator core.

## Delivered in This Increment
- Added `arqo` DAG validator module: `apps/arqo/internal/planner/validator.go`.
- Added validation coverage:
  - cycle detection
  - dangling dependency detection
  - isolated node warning
- Added unit tests: `apps/arqo/internal/planner/validator_test.go`.
- Added mock intent-router planner: `apps/arqo/internal/planner/router.go`.
- Wired planner + validator into `POST /v1/sessions`:
  - validate DAG plan before session creation
  - return `422 invalid_dag_plan` when plan is invalid
- Added API tests for create-session validation path:
  - invalid plan rejected
  - valid plan accepted
- Added planner backend factory (`ARQO_PLANNER_BACKEND`, default `mock`):
  - `apps/arqo/internal/planner/factory.go`
  - wired in `apps/arqo/main.go`
  - factory tests added
- Upgraded planner output to a structured plan contract:
  - `planner.Plan` now includes `plan_id`, `source`, `nodes`, `warnings`
  - `POST /v1/sessions` returns `plan` in both success and invalid-plan responses
- Added plan-to-scheduler runtime graph mapping:
  - new scheduler API `CreateSessionFromPlan`
  - memory + MySQL/TiDB backends now build tasks from planner nodes
  - planner node `ref_id` is mapped to unique runtime `task_id` per session
  - `POST /v1/sessions` now creates sessions from planner output instead of fixed demo graph
- Added Intent Router lightweight-model mock extraction path:
  - new `LightweightIntentModel` interface + `MockLightweightIntentModel`
  - planner now emits `intent_context` into `planner.Plan`
- Added JIT expansion recursion guard for missing-skill scenarios:
  - expansion payload now supports `mapping_status` and node `node_type`
  - scheduler now supports dynamic expansion with both `SKILL_SINK` and `EXPAND_PLANNING` nodes
  - added `ErrSkillMappingExhausted` with `MISSING_SKILL` task error propagation
  - after consecutive unmapped expansions reaches threshold (`max_unmapped_streak`), API returns `422 missing_skill`
- Added DAG-level intent context propagation:
  - `intent_context` persisted on DAG (memory + MySQL/TiDB)
  - expanding nodes auto-inject `intent_context` into task `parameters` for every ReActPlanner call
- Added planner backend failover contract:
  - `Router.Plan(...)` now returns `(Plan, error)`
  - added `ModelRouter` stub + `FallbackRouter`
  - `ARQO_PLANNER_BACKEND=model` supports `ARQO_PLANNER_FALLBACK=mock|none`
  - API now returns `plan_generation_failed` when router cannot produce a plan
- Replaced model planner stub with HTTP adapter:
  - `ModelRouter` now calls configurable endpoint (`ARQO_PLANNER_MODEL_URL`)
  - request includes `intent`, `planning_mode`, `model`, `schema`
  - supports both `{ plan: {...} }` and flat plan response forms
  - added tests for unavailable endpoint and successful model plan decode
- Added plan payload compatibility baseline test:
  - API test now validates `plan_id/source/intent_context/nodes` presence and node required fields
- Added deterministic mixed `node_type` fixture test:
  - verifies `EXPAND_PLANNING -> SKILL_SINK` graph creation, intent context injection, and execution ordering

## Verification
- `go test ./...` in `apps/arqo` passes with validator tests included.

## Pending in Phase 2
- Extend compatibility fixtures to cover backward-tolerant decoding for optional fields.
- Add deterministic fixture set for mixed `node_type` with branching and parallel leaves.
