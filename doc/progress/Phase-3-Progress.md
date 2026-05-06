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

## Verification
- `go test ./...` in `apps/arqo` passes including new sweep endpoint test.

## Pending in Phase 3
- Add persistent-store parity tests for expiry sweep behavior (MySQL/TiDB path).
- Add fault drill script that combines worker crash + manual sweep + resume.
- Add observable sweep events for UI/CLI timeline.
