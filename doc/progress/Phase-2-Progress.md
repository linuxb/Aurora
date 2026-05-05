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

## Verification
- `go test ./...` in `apps/arqo` passes with validator tests included.

## Pending in Phase 2
- Define model-backed planner contract and fallback behavior (`mock` -> `model` failover policy).
- Add plan-to-scheduler mapping layer to support non-demo DAG creation from planner output.
- Extend API/schema tests for plan metadata compatibility and versioning.
