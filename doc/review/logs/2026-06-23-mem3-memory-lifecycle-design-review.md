# Mem3 Memory Lifecycle Design Review (2026-06-23)

## Review Scope

- Intent-slot extraction and long-term memory writes during DAG construction.
- Working-memory and directed-memory retrieval before Task execution.
- Output ingest and rolling-summary reduce after Task completion.
- JSON Schemas for Mem3 Ingest, List, Search, and `mem_hint`.

## Findings Before Revision

The original design only partially satisfied the target lifecycle and had the following gaps:

1. DAG intent slot did not have a complete Ingest Schema and lacked explicit Tenant/Agent long-term-memory scope.
2. Search was described only for DAG/JIT planning, not as a mandatory step before every Task execution.
3. List and Search did not guarantee that last-N outputs and the latest rolling summary are returned together.
4. Earlier docs said Planner Nodes do not write to Mem3, which conflicts with "write and reduce after every Task".
5. Skill-provided `summary` was treated as working-memory summary, conflicting with the unified formula `summary = LLM(output, last_summary)`.
6. Some specs defined `mem_hint` as a string, and the old GraphRAG schema differed from the unified schema.
7. Parallel Task asynchronous reduce had no deterministic commit order, which could overwrite or reorder rolling summaries.
8. LLM-generated retrieval hints carried security scope, creating cross-Tenant authorization risk.

## Applied Revision

- Every DAG build calls `Ingest(kind=DAG_CONTEXT)` and asynchronously extracts Goals, Profile items, Facts, and Relations.
- Every Task calls Search before start; Search always assembles last-N outputs and the latest committed rolling summary.
- After parent Tasks complete, Flory uses parent output and child goal to generate or refresh the child's final `mem_hint`.
- Every Skill/Planner Task calls `Ingest(kind=TASK_OUTPUT)` after completion.
- Mem3 serially executes by DAG `sequence`:

```text
new_summary = lightweight_llm(output, last_summary)
```

- LLM-generated `mem_hint` does not carry security boundaries. `tenant_id/agent_id/session_id/dag_id` are injected by Flory from trusted metadata.
- Ingest uses idempotency keys and quickly returns `202 Accepted`; raw output, summary reduce, and graph writes are decoupled.

## Diagram Additions

To reduce implementation ambiguity, `doc/design/Mem3.md` now contains two Mermaid sequence diagrams for end-to-end memory lifecycle and multi-parent Task/parallel reduce. `doc/design/Intent-Router.md` contains a Query-to-DAG persistence sequence diagram.

## Conclusion

The revised design satisfies the target lifecycle. Implementation must still verify sequence assignment for parallel Tasks, serial summary-reduce commits, Ingest idempotency, and Tenant isolation in Search.
