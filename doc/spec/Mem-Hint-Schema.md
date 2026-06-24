# Unified Mem Hint Schema (Mem3 + Plato)

## Goal
This spec unifies `mem_hint` across:
- `doc/design/Mem3.md` (KV_POINT_GET / GRAPH_TRAVERSAL / NONE)
- `doc/design/Plato-GraphRAG.md` (PLATO graph `LOCAL` / `GLOBAL`)

Design principles:
- One schema for planner constrained decoding.
- Backward-compatible with existing Mem3 fields.
- Explicit routing intent for KV vs Graph vs Plato global summaries.

## Key Field Design Intent

### `strategy`
- **Intent**: Explicitly state primary retrieval path to avoid planner/runtime ambiguity.
- **Why needed**: Without it, backend routing becomes heuristic-heavy and unstable across versions.
- **Can be omitted?**: Not recommended. In this unified schema it is required.

### `target_system`
- **Intent**: Constrain execution backend (`MEM3_KV` vs `PLATO_GRAPH`) when business logic requires it.
- **Why needed**: Same `strategy` may map to multiple physical paths in mixed deployments.
- **Can be omitted?**: Yes. `AUTO` works for most cases.

### `query.text` / `query.keywords` / `query.target_task_id`
- **Intent**:
  - `target_task_id`: deterministic exact lookup key.
  - `keywords`: graph anchor hints for local traversal.
  - `text`: natural-language semantic expression for broader retrieval.
- **Why needed**: Separate deterministic key lookup from semantic retrieval signals.
- **Can be omitted?**:
  - depends on `strategy` (`KV_POINT_GET` requires `target_task_id`, graph paths require `text` or `keywords`).

### Security scope
- `mem_hint` does not carry Tenant/Agent/Session/DAG security boundaries.
- Security scope is written by Arqo into `Mem3SearchRequest.scope` from trusted Task metadata, and LLM overrides must not be accepted.
- Time range is retrieval semantics, so it remains in `query.time_range`.

### `planner_intent`
- **Intent**: Encode planner-side optimization preference (latency, cost, freshness), not just retrieval target.
- **Why needed**:
  - stable behavior under different SLO constraints,
  - predictable degrade behavior for online serving,
  - avoid implicit hard-coded tradeoffs.
- **Typical effect**:
  - `latency_budget_ms`: bounds primary + fallback execution budget.
  - `cost_tier`: controls whether expensive global/community paths are allowed.
  - `prefer_freshness`: prioritizes recent episodic context over older macro summaries.
- **Can be omitted?**: Yes. Runtime defaults are defined, but keeping it improves control and observability.

### `fallback`
- **Intent**: Make degrade policy declarative and deterministic instead of hidden in code.
- **Why needed**:
  - graph writes may lag behind KV ingest,
  - different query classes need different fallback chains,
  - production troubleshooting requires explicit failover semantics.
- **Typical effect**:
  - `allow_fallback=false`: strict mode, no degrade.
  - `fallback_order`: defines exact operator sequence.
- **Can be omitted?**: Yes. Defaults apply. Recommended to keep for critical workloads.

## Canonical JSON Shape

```json
{
  "mem_hint": {
    "version": "1.0",
    "target_system": "AUTO | MEM3_KV | PLATO_GRAPH",
    "strategy": "NONE | KV_POINT_GET | GRAPH_LOCAL_TRAVERSAL | GRAPH_GLOBAL_SUMMARY",
    "query": {
      "text": "string",
      "keywords": ["string"],
      "target_task_id": "string",
      "time_range": {
        "from": "RFC3339 datetime",
        "to": "RFC3339 datetime"
      }
    },
    "planner_intent": {
      "question": "string",
      "cost_tier": "LOW | MEDIUM | HIGH",
      "latency_budget_ms": 1500,
      "prefer_freshness": true
    },
    "fallback": {
      "allow_fallback": true,
      "fallback_order": [
        "KV_RECENT_WINDOW",
        "KV_FULLTEXT",
        "GRAPH_LOCAL_TRAVERSAL"
      ]
    }
  }
}
```

## JSON Schema (Draft 2020-12)

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://aurora/spec/mem-hint.schema.json",
  "title": "Aurora Unified MemHint",
  "type": "object",
  "additionalProperties": false,
  "required": ["mem_hint"],
  "properties": {
    "mem_hint": {
      "type": "object",
      "additionalProperties": false,
      "required": ["version", "strategy"],
      "properties": {
        "version": { "type": "string", "const": "1.0" },
        "target_system": {
          "type": "string",
          "enum": ["AUTO", "MEM3_KV", "PLATO_GRAPH"],
          "default": "AUTO"
        },
        "strategy": {
          "type": "string",
          "enum": [
            "NONE",
            "KV_POINT_GET",
            "GRAPH_LOCAL_TRAVERSAL",
            "GRAPH_GLOBAL_SUMMARY"
          ]
        },
        "query": {
          "type": "object",
          "additionalProperties": false,
          "properties": {
            "text": { "type": "string", "minLength": 1 },
            "keywords": {
              "type": "array",
              "items": { "type": "string", "minLength": 1 },
              "maxItems": 16
            },
            "target_task_id": { "type": "string", "minLength": 1 },
            "time_range": {
              "type": "object",
              "additionalProperties": false,
              "required": ["from", "to"],
              "properties": {
                "from": { "type": "string", "format": "date-time" },
                "to": { "type": "string", "format": "date-time" }
              }
            }
          }
        },
        "planner_intent": {
          "type": "object",
          "additionalProperties": false,
          "properties": {
            "question": { "type": "string" },
            "cost_tier": { "type": "string", "enum": ["LOW", "MEDIUM", "HIGH"] },
            "latency_budget_ms": { "type": "integer", "minimum": 1, "maximum": 30000 },
            "prefer_freshness": { "type": "boolean" }
          }
        },
        "fallback": {
          "type": "object",
          "additionalProperties": false,
          "properties": {
            "allow_fallback": { "type": "boolean", "default": true },
            "fallback_order": {
              "type": "array",
              "items": {
                "type": "string",
                "enum": [
                  "KV_RECENT_WINDOW",
                  "KV_FULLTEXT",
                  "GRAPH_LOCAL_TRAVERSAL",
                  "GRAPH_GLOBAL_SUMMARY"
                ]
              },
              "maxItems": 4
            }
          }
        }
      },
      "allOf": [
        {
          "if": { "properties": { "strategy": { "const": "KV_POINT_GET" } } },
          "then": {
            "required": ["query"],
            "properties": {
              "query": { "required": ["target_task_id"] },
              "target_system": { "enum": ["AUTO", "MEM3_KV"] }
            }
          }
        },
        {
          "if": { "properties": { "strategy": { "const": "GRAPH_LOCAL_TRAVERSAL" } } },
          "then": {
            "required": ["query"],
            "properties": {
              "query": {
                "anyOf": [
                  { "required": ["text"] },
                  { "required": ["keywords"] }
                ]
              },
              "target_system": { "enum": ["AUTO", "PLATO_GRAPH"] }
            }
          }
        },
        {
          "if": { "properties": { "strategy": { "const": "GRAPH_GLOBAL_SUMMARY" } } },
          "then": {
            "required": ["query"],
            "properties": {
              "query": { "required": ["text"] },
              "target_system": { "enum": ["AUTO", "PLATO_GRAPH"] }
            }
          }
        }
      ]
    }
  }
}
```

## Routing Mapping

- `NONE`
  - No directed retrieval; Search still returns mandatory last-N outputs and latest rolling summary.
- `KV_POINT_GET`
  - Mem3 KV exact fetch by `target_task_id`.
- `GRAPH_LOCAL_TRAVERSAL`
  - Plato local graph walk (1~2 hop by keyword/text anchors).
- `GRAPH_GLOBAL_SUMMARY`
  - Plato community-level summary retrieval (global/map-reduce style).

## Mem3 Interpretation and Execution Rules

This section defines how Mem3 should interpret `mem_hint` fields and make routing decisions deterministically.

### 1. Field Priority (from high to low)

1. `strategy` (hard routing intent)
2. `target_system` (backend constraint)
3. trusted `Mem3SearchRequest.scope` (injected by Arqo, not generated by LLM)
4. `planner_intent` (latency/cost/freshness preference)
5. `fallback` (degrade path)
6. `query` (`text/keywords/target_task_id/time_range`)

If fields conflict, higher-priority fields win.

### 2. Defaulting Rules

If missing:
- `target_system` => `AUTO`
- `fallback.allow_fallback` => `true`
- `fallback.fallback_order` => `["KV_RECENT_WINDOW","KV_FULLTEXT","GRAPH_LOCAL_TRAVERSAL"]`
- `planner_intent.latency_budget_ms` => `1500`
- `planner_intent.cost_tier` => `MEDIUM`
- `planner_intent.prefer_freshness` => `true`

### 3. Strategy-Level Behavior

#### `NONE`
- Do not execute additional directed retrieval.
- Mem3 Search still returns the mandatory working memory assembled from last-N outputs and the latest rolling summary.
- `fallback` is ignored because there is no directed primary retrieval to degrade.

#### `KV_POINT_GET`
- Required: `query.target_task_id`.
- Route to Mem3 KV exact lookup.
- If `target_system=PLATO_GRAPH`, ignore and treat as `AUTO` + `KV_POINT_GET` (KV still wins for exact id).
- If miss and fallback allowed, execute fallback chain.

#### `GRAPH_LOCAL_TRAVERSAL`
- Required: `query.text` or `query.keywords`.
- Preferred backend: `PLATO_GRAPH` (or `AUTO` that resolves to graph availability).
- Use `keywords` as anchors; `text` as semantic supplement.
- Apply trusted `Mem3SearchRequest.scope.tenant_id` hard isolation and optional Agent/Session/DAG filters.
- If graph result empty and fallback allowed, run `KV_FULLTEXT` first.

#### `GRAPH_GLOBAL_SUMMARY`
- Required: `query.text`.
- Preferred backend: `PLATO_GRAPH`.
- Query community/macro summaries (Top-K community nodes).
- If latency budget too low (e.g., `<700ms`) and `cost_tier=LOW`, degrade directly to `GRAPH_LOCAL_TRAVERSAL` before hitting global path.
- If empty and fallback allowed, run configured fallback chain.

### 4. `target_system` Resolution

- `MEM3_KV`: disallow graph primary route; graph strategies degrade to KV fallback route.
- `PLATO_GRAPH`: graph strategies remain primary; `KV_POINT_GET` is allowed only as fallback.
- `AUTO`: choose backend by strategy:
  - `KV_POINT_GET` => KV
  - `GRAPH_LOCAL_TRAVERSAL` / `GRAPH_GLOBAL_SUMMARY` => Graph first, then fallback
  - `NONE` => RBO recent window then fallback

### 5. Trusted Scope Enforcement

Mem3 must enforce:
- `tenant_id` is always required at the execution boundary.
- `agent_id`, `session_id` and `dag_id` from `Mem3SearchRequest.scope` must be applied to KV and Graph filters where relevant.
- LLM-generated `mem_hint` cannot add, remove or override those boundaries.
- `query.time_range` (if provided) should be translated:
  - KV: filter by `observed_at`
  - Graph: filter edge/node observation timestamp.

### 6. Planner Intent Usage

- `latency_budget_ms`:
  - sets end-to-end deadline for primary query + fallback budget partition.
- `cost_tier`:
  - `LOW`: avoid expensive global/community scans unless explicitly `GRAPH_GLOBAL_SUMMARY`.
  - `HIGH`: permit wider graph expansion and larger Top-K.
- `prefer_freshness=true`:
  - prioritize recent episodic results before historical/global summaries.

### 7. Fallback Semantics

- If `allow_fallback=false`: return primary route result (or empty/error) without degrade.
- If `allow_fallback=true`: execute `fallback_order` sequentially until non-empty result.
- Recommended fallback operator mapping:
  - `KV_RECENT_WINDOW`: list recent N records in scoped KV.
  - `KV_FULLTEXT`: scoped KV contains/regex search.
  - `GRAPH_LOCAL_TRAVERSAL`: graph anchor walk.
  - `GRAPH_GLOBAL_SUMMARY`: community summary retrieval.

### 8. Deterministic Pseudocode

```python
validate(search_request)
normalize_defaults(mem_hint)
apply_scope_guard(search_request.scope.tenant_id required)

route = decide_primary_route(strategy, target_system, planner_intent)
working_memory = list_recent_outputs_and_latest_summary(
  search_request.scope,
  search_request.recent_limit
)

if strategy == NONE:
  return working_memory + empty_retrieval

result = execute(route, query, search_request.scope, planner_intent)

if result not empty:
  return working_memory + result

if fallback.allow_fallback != true:
  return empty_or_error

for op in fallback.fallback_order:
  candidate = execute(op, query, search_request.scope, planner_intent)
  if candidate not empty:
    return working_memory + candidate

return working_memory + empty_retrieval
```

## Backward Compatibility Mapping

### Legacy Mem3 Schema -> Unified
- `strategy=KV_POINT_GET` -> same
- `strategy=GRAPH_TRAVERSAL` -> `GRAPH_LOCAL_TRAVERSAL`
- `target_step_id` -> `query.target_task_id`
- `semantic_query` -> `query.text`

### Legacy Plato-GraphRAG -> Unified
- `target_system=PLATO_GRAPH` -> same
- `query_type=LOCAL` -> `strategy=GRAPH_LOCAL_TRAVERSAL`
- `query_type=GLOBAL` -> `strategy=GRAPH_GLOBAL_SUMMARY`
- `keywords` -> `query.keywords`
- `intent_question` -> `query.text` and optional `planner_intent.question`

## Minimal Examples

### KV precise lookup
```json
{
  "mem_hint": {
    "version": "1.0",
    "target_system": "MEM3_KV",
    "strategy": "KV_POINT_GET",
    "query": {
      "target_task_id": "task_123"
    }
  }
}
```

### Plato local traversal
```json
{
  "mem_hint": {
    "version": "1.0",
    "target_system": "PLATO_GRAPH",
    "strategy": "GRAPH_LOCAL_TRAVERSAL",
    "query": {
      "keywords": ["payment module", "auth interception"],
      "text": "Trace the relationship between payment failures and the authentication path"
    }
  }
}
```

### Plato global summary
```json
{
  "mem_hint": {
    "version": "1.0",
    "target_system": "PLATO_GRAPH",
    "strategy": "GRAPH_GLOBAL_SUMMARY",
    "query": {
      "text": "Payment-system stability evolution and main risks over the past week"
    },
    "planner_intent": {
      "cost_tier": "HIGH",
      "latency_budget_ms": 2500
    }
  }
}
```
