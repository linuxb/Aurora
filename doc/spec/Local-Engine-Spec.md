# Local Engine Spec（Draft）

## 1. Runtime Mode
- Env: `ARQO_RUNTIME_MODE`
- Enum: `cloud` | `local`
- Default: `cloud`

## 2. Adapter Contracts

### 2.1 ITaskStateStore
- `FetchReadyTasks(batchSize int) ([]Task, error)`
- `LeaseTask(taskID, ownerID string, ttl time.Duration) (bool, error)`
- `CompleteTask(taskID string, result TaskResult) error`
- `AtomicDecrementDependency(taskID string) (int, error)`
- `BeginTx() (Transaction, error)`

### 2.2 IContextStore
- `PutContext(taskID string, data []byte) error`
- `GetContext(taskID string) ([]byte, error)`
- `PutSummary(taskID string, summary string) error`

### 2.3 IGraphStore
- `MergeTriplets(triplets []Triplet) error`
- `SearchSubGraph(query string, userID string) (GraphData, error)`

## 3. Local Scheduler Semantics
- Local mode baseline: single-writer scheduler loop.
- Lease model:
  - fields: `owner_id`, `lease_expire_at`
  - CAS update required for lease renewal/steal.
- Recovery:
  - sweeper interval default `10s`
  - expired running task -> `FAILED` + trigger replanning path.

## 4. Aegis RPC Protocol

### 4.1 Execute Request
```json
{
  "version": "v1",
  "request_id": "req_001",
  "task_id": "task_local_001",
  "skill_name": "ReadLocalConfig",
  "source_code": "export default async function run(ctx){ return {summary:'ok', raw_data:{k:1}} }",
  "limits": {
    "memory_mb": 64,
    "timeout_ms": 5000
  },
  "permissions": {
    "network": false,
    "fs_read_paths": ["/workspace/config.json"],
    "fs_write_paths": []
  },
  "input": {
    "parameters": {},
    "context_refs": []
  }
}
```

### 4.2 Execute Response
```json
{
  "version": "v1",
  "request_id": "req_001",
  "ok": true,
  "result": {
    "summary": "done",
    "raw_data": {"config":"..."}
  },
  "usage": {
    "duration_ms": 132,
    "max_rss_mb": 28
  }
}
```

### 4.3 Error Response
```json
{
  "version": "v1",
  "request_id": "req_001",
  "ok": false,
  "error": {
    "code": "SANDBOX_TIMEOUT",
    "message": "script exceeded timeout",
    "retryable": false
  }
}
```

## 5. Node Type Semantics
- `SKILL_SINK`: must map to concrete skill execution.
- `EXPAND_PLANNING`: invokes predefined planner skill; may emit expansion payload.

## 6. Security Baseline
- Default deny network.
- FS access must be explicit allowlist.
- Enforce memory/time quotas at sandbox layer.
- Any policy violation maps to semantic error codes:
  - `SANDBOX_TIMEOUT`
  - `SANDBOX_OOM`
  - `SANDBOX_PERMISSION_DENIED`

## 7. Compatibility Notes
- `source_code` is JS (not TS). TS transpilation happens before Aegis invocation.
- Protocol versioned by `version`, unknown version must hard fail.
