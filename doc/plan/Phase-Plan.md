# Aurora Phased R&D Plan (Runnable and Testable)

## Goal Statement

This plan is based on the existing design documents and follows a "demoable + testable + regression-friendly at every phase" delivery rhythm. Each phase must produce a verifiable milestone.

## Decision Traceability Rules

- All open decisions and resolved decisions are recorded in `doc/progress/Decision-Log.md`.
- Each record must include `recorded_at` in RFC3339 with timezone, `phase`, `topic`, `status`, `decision`, and `owner`.
- When a decision changes, do not overwrite the original record. Add a `status=superseded` record or a new version that points to the replaced item.
- At the end of each phase, add a phase-closure record with unresolved items and risk notes.

## Deferred Hardening Cadence

- Deferred hardening items do not block the main phase track, but they must be revisited at a fixed cadence.
- The cadence rules are in `doc/progress/Hardening-Cadence.md`.
- Every phase closure must record which hardening items are complete, which are deferred, and whether the deferral risk is acceptable.

## Phase Document Ownership

- `doc/plan/Phase-Plan.md`: global phase goals, acceptance criteria, and cadence.
- `doc/progress/Phase-0-Progress.md`, `doc/progress/Phase-1-Progress.md`, and so on: phase execution progress, phase-local decision points, and discussion conclusions.
- `doc/progress/Decision-Log.md`: cross-phase decision traceability index.

## Phase 0: MVP Framework Landing (Completed)

### R&D Goals

- Build a runnable three-language minimal framework:
  - `flory` in Go: gateway and DAG state-machine core.
  - `worker-ts` in TypeScript: Skill execution and semantic errors.
  - `mem3` in Rust: minimal memory controller service.
- Implement minimal protocols:
  - Task state flow: `PENDING -> READY -> RUNNING -> SUCCESS/FAILED`.
  - Skill dual-track return: `raw_data` + `summary`.
  - Task failure triggers DAG `REPLANNING`.
- Provide local development and debugging configuration for VSCode, Makefile, and Docker Compose.

### Available Capabilities

- Create a session through `POST /v1/sessions` and automatically generate a demo DAG.
- `worker-ts` can pull, execute, and complete Tasks automatically.
- `mem3` provides minimal `healthz` and `ingest/memory` endpoints.

### Testability

- `go test ./...` validates core DAG flow and failure-to-replanning state transitions.
- `cargo test` validates Mem3 ingest parsing.
- Manual integration: create a session with curl and observe Tasks from READY to SUCCESS.

### Acceptance Criteria

- A local M2 machine can run the demo within 10 minutes.
- The DAG completes the happy path.
- A failed Task can set the DAG to `REPLANNING`.

## Phase 1: Persistent Core Scheduler + Basic Event Stream (In Progress)

### R&D Goals

- Replace Flory's in-memory store with MySQL/TiDB.
- Introduce `SKIP LOCKED` claiming and atomic dependency-counter updates.
- Introduce Redis Pub/Sub for live execution-log events.

### Available Capabilities

- Multiple Workers can claim Tasks concurrently without duplicate execution.
- Downstream nodes automatically become READY when dependencies reach zero.
- Frontend or CLI can subscribe to Task execution events.

### Testability

- Concurrency test: no duplicate claim of the same Task under N concurrent Workers.
- Data-consistency test: dependency counters never go negative and wake-ups are not lost.
- Regression script: 100 batch DAG executions pass.

### Acceptance Criteria

- Key API P95 latency and throughput meet target values to be defined.
- No deadlocks or duplicate consumption under concurrency.

## Phase 2: Intent Router and Structured DAG Generation

### R&D Goals

- Implement the pipeline: intent slot extraction -> restricted DAG generation -> static validation.
- Implement DAG compiler validation:
  - cycle detection;
  - dangling dependency detection;
  - node type and Skill mapping checks.

### Available Capabilities

- Arbitrary natural-language requests can generate a valid DAG or a clear failure reason.
- Validation failures can trigger bounded automatic repair retries.

### Testability

- Mock model-output tests cover valid and invalid graphs.
- Property tests validate graph-checker robustness on random graphs.
- E2E covers intent input through DAG persistence.

### Acceptance Criteria

- DAG validation errors are explainable and repairable by retry.
- End-to-end failures are observable and traceable.

## Phase 3: Replanning and Fault Self-Healing

### R&D Goals

- Implement Sweeper/Reaper lease-expiration recovery.
- Integrate structured PatchDAG replanning.
- Support transactional local hot repair.

### Available Capabilities

- Zombie Tasks can be detected after Worker crashes.
- Failed DAGs can insert new nodes and continue execution.

### Testability

- Fault injection: kill Worker, timeout, and network failure.
- Transaction rollback test: invalid PatchDAG does not contaminate the original graph.

### Acceptance Criteria

- Self-healing paths can reliably restore core business flows.
- Replanning attempts and success rate have metrics.

## Phase 4: Memory and GraphRAG

### R&D Goals

- Upgrade `mem3` from a minimal service into an asynchronous memory pipeline.
- Connect KV for `raw_data` and GraphDB for summaries and entity relations.
- Provide a secure internal `SearchMemoryGraph` query interface.

### Available Capabilities

- Short-term and long-term memory are separated.
- Cross-Task memory retrieval is online.
- Graph queries enforce user/tenant isolation.

### Testability

- Multi-tenant isolation tests prevent cross-tenant reads.
- Memory extraction quality tests use sampled human review plus automated evaluation.

### Acceptance Criteria

- Cross-session memory recall is usable.
- No cross-tenant data leakage.

## Phase 4.5: Local-Engine Minimal Prototype (Start Immediately After Mem3)

### R&D Goals

- Start a minimal local-first prototype while preserving the cloud-track mainline.
- Use existing `flory + mem3` capabilities to validate a single-machine local execution loop.
- Freeze key local interfaces and execution protocols to reduce Phase 6 risk.

### Available Capabilities

- Minimal `FLORY_RUNTIME_MODE=local` runtime mode.
- Local single-machine session execution loop: create, schedule, execute, and read results.
- Local sandbox execution-plane MVP protocol; prioritize usability before final security hardening.

### Testability

- Local E2E: create session -> execute DAG -> query result.
- Resource-limit tests: timeout and memory-limit policies take effect.
- Regression validation: cloud-mode paths remain unchanged.

### Acceptance Criteria

- A minimal demo runs on local macOS/Linux without heavy external dependencies.
- Switching between local and cloud modes does not change upper-layer business semantics.

## Phase 5: Engineering and Launch Readiness

### R&D Goals

- Improve CI/CD, quality gates, load testing, observability, and alerting.
- Introduce canary release and rollback strategies.
- Produce operation manuals and SLOs.

### Available Capabilities

- PR automation for tests, lint, and security scanning.
- Production observability across logs, metrics, and traces.

### Testability

- Load testing for peak throughput and tail latency.
- Chaos drills with component-level fault injection.

### Acceptance Criteria

- The system is ready for low-traffic canary releases.
- Core SLOs are satisfied.

## Phase 6: Local-Engine Production-Grade Local Capability

### R&D Goals

- Upgrade the Phase 4.5 prototype into production-grade local capability.
- Fully implement the Infra Adapter Layer so scheduling, context, and graph interfaces are pluggable.
- Implement Aegis security hardening and long-running stability guarantees.

### Available Capabilities

- A full session lifecycle runs on a single local machine.
- Local Skills execute in a restricted sandbox with timeout, memory, and permission controls.
- Local context and execution traces can be queried and replayed.

### Testability

- Local E2E: create session -> execute DAG -> read result.
- Security tests: unauthorized file access, infinite loops, and memory growth are blocked.
- Stability tests: 48-hour soak and task recovery validation.

### Acceptance Criteria

- The system runs on macOS/Linux without additional database services in the minimal setup.
- Sandbox policy denies unauthorized capabilities by default and failures are explainable.
- Cloud-mode semantics and interface compatibility are preserved.

## Phase Traceability Entry Points

- `doc/progress/Decision-Log.md`
- `doc/progress/Phase-0-Progress.md`
- `doc/progress/Hardening-Cadence.md`
