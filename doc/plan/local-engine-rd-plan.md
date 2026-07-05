# Local Engine Architecture Review and R&D Plan (2026-05-07)

## Review Conclusion

The main direction in `doc/design/Local-Engine.md` is correct and aligns with Aurora's existing design philosophy:

- Use an Adapter Layer to support cloud/local dual-mode runtime while preserving upper-layer business logic and execution flow.
- Use embedded components for local mode, such as SQLite, file or embedded KV storage, and embedded graph storage, to reduce deployment cost.
- Promote local Skill isolation into an independent sandbox process, Aegis, as a reasonable security boundary.

Conclusion: **the design can proceed**. Implement it with a "minimum viable first, then security hardening" rhythm.

## Confirmed Alignment with System Goals

1. Control-flow and data-flow interfaces are separated, matching Aurora's existing abstractions.
2. The local-first deployment goal is clear and matches the product direction of supporting both cloud and local modes.
3. The sandbox execution plane is isolated into a separate process, following least privilege and failure isolation.
4. JSON-RPC decouples scheduler and execution engine, making multi-language implementation replacement easier.

## Key Uncertainties to Decide

1. SQLite concurrency semantics and an equivalent strategy for `SKIP LOCKED`.
   - Issue: SQLite does not support MySQL/TiDB `FOR UPDATE SKIP LOCKED` semantics.
   - Recommendation: use a single-writer scheduling loop plus lease-field CAS updates and WAL in local mode, rather than pursuing multi-process claim consistency.
2. Local `raw_data` storage choice: Markdown versus BadgerDB.
   - Issue: Markdown is readable, but large or frequent writes have worse performance and fragmentation behavior.
   - Recommendation: use a two-layer strategy: default to `Markdown + manifest`, automatically fall back to BadgerDB above a threshold, and keep a readable summary index.
3. macOS sandbox mechanism.
   - Issue: long-term stability and maintainability of `sandbox-exec` are uncertain on newer macOS versions.
   - Recommendation: define OS-level isolation as a pluggable strategy. The first version focuses on process-level limits, permission allowlists, and resource quotas. OS sandboxing is an enhancement.
4. TS runtime boundary.
   - Issue: QuickJS does not natively support TypeScript, so compilation ownership must be explicit.
   - Recommendation: define `source_code` as JavaScript at the protocol layer. TS-to-JS compilation happens in Flory or the bundler, while Aegis only executes JS.
5. Local memory graph engine selection.
   - Issue: KuzuDB ecosystem and bindings require validation; DuckDB graph queries require additional modeling.
   - Recommendation: abstract `IGraphStore` first. Phase A validates the flow with DuckDB plus edge tables; Phase B compares Kuzu performance.

## R&D Plan

### Phase LE-0: Interface Freeze and Local-Mode Skeleton (1-2 weeks)

- Goal: turn Local Engine into an orchestratable runtime mode without changing main business semantics.
- Deliverables:
  - Add runtime mode switch: `FLORY_RUNTIME_MODE=cloud|local`.
  - Freeze three interfaces: `ITaskStateStore`, `IContextStore`, and `IGraphStore`.
  - Minimal local scheduler: SQLite plus single-writer scheduling loop.
- Acceptance: complete one full session path on macOS within 10 minutes.

### Phase LE-1: Aegis MVP (2-3 weeks)

- Goal: execute JS Skills with basic resource limits.
- Deliverables:
  - Minimal runnable Aegis process using Zig + QuickJS.
  - JSON-RPC communication through either stdio or UDS.
  - Limits: `timeout_ms`, `memory_mb`, and permission allowlists.
- Acceptance: malicious scripts, including infinite loops, memory growth, and unauthorized filesystem access, are blocked.

### Phase LE-2: Local Memory and Observability (1-2 weeks)

- Goal: complete the minimal local memory loop and observability.
- Deliverables:
  - Local `raw_data` storage strategy: Markdown with optional BadgerDB.
  - Summary index and query API.
  - Local telemetry and execution-log replay.
- Acceptance: recent cross-session context is retrievable and the debug path is clear.

### Phase LE-3: Security and Stability Hardening (continuous)

- Goal: raise local mode from usable to long-running.
- Deliverables:
  - Permission policy templates for network and filesystem allowlists.
  - Fault injection and recovery tests.
  - Performance baselines for startup latency, task throughput, and memory ceiling.
- Acceptance: 48-hour soak test is stable and key metrics meet targets.

## Milestone Gates

1. Do not introduce complex graph database dependencies before LE-0 is complete.
2. Do not enable arbitrary remote-downloaded Skill execution by default before LE-1 is complete.
3. Mark local mode as Beta until LE-2 is complete.

## Recommended Next Step

Start with LE-0 and decide the following before implementation begins:

1. SQLite scheduling concurrency model: single writer or multiple Workers.
2. Default `raw_data` storage format: pure Markdown or hybrid strategy.
3. Aegis communication channel priority: stdio or UDS.
