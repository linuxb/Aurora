# Plato Implementation Status (Phase 4)

## Snapshot
- Updated at: 2026-05-26
- Scope: `apps/polaris` Plato GraphRAG path

## Design-to-Implementation Matrix

1. Graph analytics adapter abstraction
- Design: required (`GraphAnalyticsAdapter`)
- Status: Implemented
- Notes: `local_scc`, `local_louvain_approx`, `memgraph_mage_stub` selectable by `PLATO_ANALYTICS_BACKEND`.

2. Cloud analytics adapter (Memgraph MAGE / Leiden)
- Design: in-database call-down (`CALL community.leiden()`)
- Status: Partial
- Notes: `memgraph_mage_stub` seam exists; real remote algorithm call not implemented yet.

3. Local-first analytics adapter (Pure Rust path)
- Design: local algorithm path
- Status: Implemented (baseline)
- Notes: `petgraph` SCC baseline + Louvain-style approximation backend.

4. Dirty-edge counter + threshold trigger
- Design: edge-count / time dual trigger
- Status: Implemented
- Notes: `PLATO_DIRTY_EDGE_THRESHOLD`, `PLATO_CLUSTER_INTERVAL_SECONDS`.

5. Async macro summary pipeline
- Design: asynchronous slow-path with LLM map-reduce
- Status: Partial
- Notes: background summary job worker is implemented; current summary backend is template-based, not LLM map-reduce yet.

6. LOCAL query route
- Design: anchor-based local traversal and micro facts return
- Status: Implemented
- Notes: keyword/text anchors + 1-hop neighborhood filter in current baseline.

7. GLOBAL query route
- Design: community macro summary retrieval
- Status: Implemented (baseline)
- Notes: Top-K community summaries returned; currently lexical scoring baseline.

8. Unified mem_hint compatibility
- Design: compatible with Polaris + Plato schema
- Status: Implemented
- Notes: supports legacy `query_type=LOCAL|GLOBAL` and unified `strategy` forms.

## High-Priority Remaining Items
- Implement real Memgraph MAGE/Leiden adapter call-down.
- Replace rule-based macro summary template with async LLM map-reduce generation.
- Add integration tests against real graph service.
