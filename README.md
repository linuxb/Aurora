# <img src="img/icon.png" height="64" style="vertical-align: middle;" /> Aurora Agentic Garden (MVP Scaffold)

This repository contains a runnable multi-language MVP scaffold for the Aurora Agentic Garden:
- `arqo` (Go): gateway + DAG scheduler core
- `worker-ts` (TypeScript): skill runner with semantic error contract
- `mem3` (Rust): versioned memory data plane with KV/Graph retrieval

## Quick Start

### 1) Check local toolchain
```bash
make check-env
```

### 2) Run services in 3 terminals
```bash
make run-arqo
make run-worker
make run-mem3
```

### 3) Create a session and trigger DAG
```bash
curl -sS http://127.0.0.1:8080/v1/sessions \
  -H 'content-type: application/json' \
  -d '{"tenant_id":"tenant_demo","agent_id":"agent_demo","user_id":"u_demo","intent":"summarize logs and email report"}' | jq
```

Then check the session status:
```bash
curl -sS http://127.0.0.1:8080/v1/sessions/sess_000001 | jq
```

## Infra (Optional)

Start infra dependencies via Docker Compose:
```bash
make infra-up
```

Stop everything:
```bash
make infra-down
```

Run full stack in Docker:
```bash
make infra-up-full
```

Stop full stack:
```bash
make infra-down-full
```

## Tests

```bash
make test
```

System smoke/fault toolkit:
```bash
make test-smoke-ruby
make test-fault-ruby
```

## Engineering Notes

- Arqo supports in-memory and MySQL/TiDB-compatible scheduler stores.
- Arqo writes `DAG_CONTEXT` and successful `TASK_OUTPUT` events to Mem3.
- Every leased task receives Mem3 working memory plus directed retrieval based on its canonical `mem_hint`.

See roadmap: `doc/plan/Phase-Plan.md`.
TypeScript setup guide: `doc/dev/Dev-Environment.md`.
Decision traceability: `doc/progress/Decision-Log.md`.
Phase 0 progress: `doc/progress/Phase-0-Progress.md`.
Phase 1 progress: `doc/progress/Phase-1-Progress.md`.
Local MySQL setup: `doc/dev/Local-MySQL-Setup.md`.
Local dev debug setup: `doc/dev/Local-Dev-Debug-Setup.md`.
System testing toolkit: `doc/dev/System-Testing-Toolkit.md`.
