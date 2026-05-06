# Lease and Self-Healing Mechanism

## Scope
- Component: `arqo` scheduler (`memory` and `mysql/tidb` backends)
- Topic: task lease ownership, expiry handling, and recovery behavior

## Why Lease Exists
- Prevent duplicate execution on the same task under concurrent workers.
- Bound worker ownership by time so stuck/crashed workers do not hold tasks forever.
- Convert runtime failures (worker timeout) into deterministic recovery paths.

## Core Lease Lifecycle
1. Worker pulls a task:
   - task status: `READY -> RUNNING`
   - fields set: `owner_id`, `expire_at`
2. Worker completes in lease window:
   - success path: `RUNNING -> SUCCESS`
   - failure path: `RUNNING -> FAILED` and DAG transitions according to failure logic
3. Lease expires before completion:
   - handled by sweeper (`ExpireRunningTasks(now)`) using configured expiry policy

## Configurable Expiry Policy
Environment variable:
- `ARQO_LEASE_EXPIRE_POLICY`

Supported values:
- `failed_replan` (default)
- `retry_ready`

### Policy A: `failed_replan` (default)
- Expired running task transitions:
  - `RUNNING -> FAILED`
  - `owner_id = NULL/""`
  - `expire_at = NULL`
  - error fields:
    - `last_error_code = WORKER_TIMEOUT`
    - `last_human_readable_error_msg = "worker lease expired"`
- DAG transitions:
  - `status -> REPLANNING`
  - `replan_count += 1`

This is the conservative mode: timeout becomes explicit failure, then replanning/recovery path is triggered.

### Policy B: `retry_ready`
- Expired running task transitions:
  - `RUNNING -> READY`
  - `owner_id = NULL/""`
  - `expire_at = NULL`
  - error fields:
    - `last_error_code = WORKER_TIMEOUT_RETRY`
    - `last_human_readable_error_msg = "worker lease expired, task returned to ready queue"`
- DAG transitions:
  - DAG remains `RUNNING`
  - no `replan_count` increment

This mode optimizes for retry throughput when timeout may be transient.

## Execution Paths

### Automatic sweep
- `main.go` launches background sweeper (`runSweeper`) with periodic ticker.
- It calls `engine.ExpireRunningTasks(now)` every tick.

### Manual sweep (for debugging/fault drills)
- API endpoint: `POST /v1/admin/sweep-expired`
- Behavior:
  - executes `ExpireRunningTasks(time.Now().UTC())`
  - returns:
    - `expired_task_ids`
    - `count`

## Backend Parity

### Memory backend
- Implementation in `internal/scheduler/store.go`.
- Uses in-memory task map scan with `status=RUNNING && expire_at <= now`.
- Applies configured policy (`failed_replan`/`retry_ready`).

### MySQL/TiDB backend
- Implementation in `internal/scheduler/mysql_store.go`.
- Transactional flow:
  1. `SELECT task_id, dag_id ... FOR UPDATE` on expired running tasks.
  2. Update expired tasks according to policy.
  3. For `failed_replan`, update corresponding DAG rows (`REPLANNING`, `replan_count+1`).
  4. Commit transaction.
- Same policy semantics as memory backend.

## Test Coverage

### Memory policy tests
- `TestExpireRunningTasksRetryReadyPolicy`:
  - verifies expired task returns to `READY`
  - verifies DAG remains `RUNNING`

### SQL parity tests (sqlmock)
- `TestMySQLStoreExpireRunningTasksFailedReplanPolicy`
- `TestMySQLStoreExpireRunningTasksRetryReadyPolicy`
- verify correct SQL update behavior under both policies.

### API-level sweep test
- `TestSweepExpiredEndpoint`
- verifies admin sweep endpoint returns expected payload and task transition visibility.

## Operational Guidance
- Recommended default: `failed_replan` for safer correctness and explicit failure traceability.
- Use `retry_ready` when:
  - workloads are idempotent or safe to re-run
  - transient worker/network interruptions are frequent
  - faster auto-retry is preferred over immediate replanning

## Current Limitations
- No per-task policy override yet (policy is global per scheduler instance).
- Sweep observability currently returns API response; timeline-level sweep events are pending.
- Persistent integration drills for real MySQL/TiDB runtime can be extended beyond sqlmock parity tests.
