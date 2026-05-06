# Phase 3 Progress

## Started At
- `2026-05-06T11:10:00+08:00`

## Scope of Current Increment
- Start self-healing track with explicit lease-expiry sweep control for integration testing and fault drills.

## Delivered in This Increment
- Added admin endpoint: `POST /v1/admin/sweep-expired`.
- Endpoint triggers `ExpireRunningTasks(now)` and returns:
  - `expired_task_ids`
  - `count`
- Added API test to verify:
  - expired running task is swept
  - task status transitions to `FAILED` with replanning signal path
  - endpoint response count is consistent
- Added configurable lease-expiry policy:
  - `ARQO_LEASE_EXPIRE_POLICY=failed_replan|retry_ready`
  - `failed_replan` (default): expired task -> `FAILED`, DAG -> `REPLANNING`
  - `retry_ready`: expired task -> `READY`, DAG remains running
- Added persistent-store parity tests (MySQL/TiDB SQL path via sqlmock):
  - verify `failed_replan` SQL update behavior
  - verify `retry_ready` SQL update behavior
- Added memory scheduler policy test:
  - verify `retry_ready` transitions and DAG status preservation
- Added sweep observability event:
  - `POST /v1/admin/sweep-expired` now publishes `TASK_SWEEP_EXPIRED` per recovered task
  - event includes `session_id`, `task_id`, and timeline-safe recovery message
- Added API-level event test:
  - verifies sweep endpoint emits `TASK_SWEEP_EXPIRED` to session event stream
- Added explicit replanning patch mechanism:
  - new endpoint: `POST /v1/sessions/{sessionID}/replan`
  - accepts patch task specs and applies them only when DAG is in `REPLANNING`
  - scheduler injects patch tasks, restores DAG to `RUNNING`, and resumes execution path
  - publishes `DAG_REPLAN_APPLIED` event on success

## Verification
- `go test ./...` in `apps/arqo` passes including new sweep endpoint test.

## Pending in Phase 3
- Add fault drill script that combines worker crash + manual sweep + resume.
