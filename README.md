# Aurora Agentic Service (MVP Scaffold)

This repository contains a runnable multi-language MVP scaffold for the Aurora Agentic Service:
- `arqo` (Go): gateway + DAG scheduler core
- `worker-ts` (TypeScript): skill runner with semantic error contract
- `polaris` (Rust): memory controller (MVP in-memory store)

## Quick Start

### 1) Check local toolchain
```bash
make check-env
```

### 2) Run services in 3 terminals
```bash
make run-arqo
make run-worker
make run-polaris
```

### 3) Create a session and trigger DAG
```bash
curl -sS http://127.0.0.1:8080/v1/sessions \
  -H 'content-type: application/json' \
  -d '{"user_id":"u_demo","intent":"summarize logs and email report"}' | jq
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

## Tests

```bash
make test
```

## Engineering Notes

- The current scheduler store is in-memory for fast local iteration.
- The interface contracts already follow the design docs (`READY/PENDING`, semantic errors, dual-track skill output).
- Next phases can swap in MySQL/TiDB, Redis, and graph DB behind stable interfaces.

See roadmap: `doc/Phase-Plan.md`.
TypeScript setup guide: `doc/Dev-Environment.md`.
Decision traceability: `doc/Decision-Log.md`.
Phase 0 progress: `doc/Phase-0-Progress.md`.
Phase 1 progress: `doc/Phase-1-Progress.md`.
