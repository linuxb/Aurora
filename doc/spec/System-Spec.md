# System Spec

## **0. Design Philosophy and Vibe Coding Rules**

- **Defensive programming**: never trust unstructured LLM output and never assume TS Workers are stable.
- **State isolation**: scheduling control flow in TiDB/MySQL and data flow in KV/Graph stores must remain separate.
- **Interface-first**: components written in different languages must communicate through strongly typed definitions such as Protobuf or JSON Schema.

## **1. Core Backbone: Go Gateway and DAG Scheduling Engine (TiDB)**

### **1.1 Core Data Structures (DB Schema)**

The scheduling system uses a relational database, MySQL 8.0 locally and TiDB in production. It relies on `SKIP LOCKED` and atomic counters for lock-free concurrent scheduling.

```sql
-- DAG macro control table.
CREATE TABLE dags (
    dag_id VARCHAR(64) PRIMARY KEY,
    status VARCHAR(32) NOT NULL,
    original_intent TEXT NOT NULL, -- North-star intent for replanning.
    replan_count INT DEFAULT 0,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Task micro execution table.
CREATE TABLE tasks (
    task_id VARCHAR(64) PRIMARY KEY,
    dag_id VARCHAR(64) NOT NULL,
    node_type VARCHAR(16) NOT NULL,
    skill_name VARCHAR(64), -- Nullable for planner nodes.
    status VARCHAR(32) NOT NULL,
    pending_dependencies_count INT DEFAULT 0, -- Atomic dependency counter.
    owner_id VARCHAR(64), -- Worker lease holder.
    expire_at TIMESTAMP, -- Lease expiration time for OOM capture.
    sequence BIGINT NOT NULL,
    mem_hint_json JSON,
    INDEX idx_ready_tasks (status)
);
```

### **1.2 Core Concurrency Primitives (Go SQL Constraints)**

- **Pull / claim task**: must use `FOR UPDATE SKIP LOCKED`.
- **Push / wake downstream**: must use atomic decrement, such as `UPDATE ... RETURNING` where supported.

```go
// Example: claim tasks.
rows, err := tx.QueryContext(ctx, `
  SELECT task_id FROM tasks
  WHERE status = 'READY'
  ORDER BY sequence
  LIMIT ?
  FOR UPDATE SKIP LOCKED`, batchSize)
```

```go
// Example: wake downstream after a Task succeeds.
count, err := decrementDependency(ctx, childTaskID)
if count == 0 {
    markReady(ctx, childTaskID)
}
```

## **2. Fault Tolerance and Healing: Replanning**

### **2.1 Death Capture (Reaper)**

A Go background daemon, the Sweeper, runs every 10 seconds and captures zombie tasks caused by sandbox OOM or process death:

```sql
UPDATE tasks
SET status = 'FAILED'
WHERE status = 'RUNNING' AND expire_at < NOW();

-- Trigger the DAG to enter REPLANNING.
```

### **2.2 LLM Replanning Interface and Constrained Decoding**

When Replanning is triggered, the cloud LLM must output PatchDAG in `json_schema` format for local graph repair.

```json
{
  "type": "object",
  "properties": {
    "reasoning": { "type": "string", "description": "Brief explanation of the replanning reason" },
    "new_nodes": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "node_id": { "type": "string" },
          "node_type": { "type": "string", "enum": ["skill", "planner"] },
          "skill_name": { "type": "string" },
          "goal": { "type": "string" },
          "dependencies": { "type": "array", "items": { "type": "string" } },
          "mem_hint": { "$ref": "https://aurora/spec/mem-hint.schema.json#/properties/mem_hint" }
        },
        "required": ["node_id", "node_type", "dependencies", "mem_hint"]
      }
    },
    "downstream_wiring": {
      "type": "object",
      "description": "Mapping that reconnects original suspended nodes to new nodes"
    }
  },
  "required": ["reasoning", "new_nodes", "downstream_wiring"]
}
```

## **3. TS Worker Contract (Tool Sandbox)**

### **3.1 Dual-Track Return Contract**

After execution, a TS Worker returns raw output and may provide a local Skill summary. Flory must write the complete result through Mem3 `Ingest(kind=TASK_OUTPUT)`. The local summary can only assist asynchronous reduce and must not replace the Mem3 rolling summary.

```ts
// Core TS SDK interface.
export interface SkillResult {
  // Raw packet saved by Flory through Mem3 Ingest.
  raw_data: unknown;
  // Optional local Skill hint, not the cross-Task rolling summary.
  summary?: string;
}
```

### **3.2 Task Memory Lifecycle**

1. After parent Tasks complete, Flory calls the planning LLM with parent Task outputs and the child Task goal to generate or refresh the child's final `mem_hint`. Multi-parent nodes must merge all parent outputs and generate the final hint once.
2. Before a Task transitions from `READY` to `RUNNING`, Flory calls Mem3 Search with trusted scope, current Task, `recent_limit`, and the final `mem_hint`.
3. Search always returns last-N outputs and the latest committed rolling summary, plus directed retrieval results.
4. After every `skill` or `planner` Task succeeds, Flory calls Mem3 Task Ingest.
5. Mem3 asynchronously computes `new_summary = lightweight_llm(output, last_summary)` and commits summary versions serially by DAG `sequence`.

Complete JSON schemas are in `doc/spec/Mem3-API-Spec.md`.

### **3.3 Semantic Error Funnel**

Native stack traces must never be sent to the LLM. Use a unified exception class.

```ts
export class SemanticError extends Error {
  constructor(
    public code: string,
    public human_readable_msg: string, // Distilled cause sent to the LLM.
    public raw_stack: string // Raw stack stored only for DB/debugging.
  ) {
    super(human_readable_msg);
  }
}
```

### **3.4 Live Telemetry Probe (Telemetry Pub/Sub)**

During execution, Workers must report fine-grained progress to the Go gateway through Redis for SSE rendering.

```json
{
  "task_id": "task_1",
  "event": "progress",
  "percent": 30,
  "message": "Parsing PDF data..."
}
```

## **4. Memory Engine (Rust) and GraphRAG Mechanism**

### **4.1 Asynchronous Side-Path Extraction Pipeline**

During every DAG build, Flory sends `DAG_CONTEXT` Ingest. After every successful Task, it sends `TASK_OUTPUT` Ingest. After durable acceptance, Mem3 uses an internal queue to asynchronously perform summary reduce, fact extraction, and graph writes.

- **Rolling summary**: read current Task output and the previous committed summary to generate a new rolling summary.
- **Fact and relation extraction**: may read policy-permitted output, local Skill summary, and DAG intent slot. It must not depend on or persist private chain-of-thought.

### **4.2 Temporal Knowledge Graph Write Contract**

In Memgraph/Neo4j, every node must contain tenant/user isolation fields, and every edge must include a timestamp.

```cypher
// Normalized MERGE generated by Rust.
MERGE (a:Entity {tenant_id: $tenant_id, entity_id: $src})
MERGE (b:Entity {tenant_id: $tenant_id, entity_id: $dst})
MERGE (a)-[r:REL {type: $rel_type, observed_at: $observed_at}]->(b)
SET r.source_task_id = $task_id, r.confidence = $confidence
```

## **5. Local Development Topology (Recommended Docker Compose Components)**

- **Scheduling store**: `mysql:8.0-arm64`
- **Large-capacity cache**: `apache/kvrocks:latest`
- **Event bus**: `redis:7-alpine`
- **Graph database**: `memgraph/memgraph:latest`
- **LLM inference**: use a unified cloud endpoint such as OpenAI or Gemini API to avoid exhausting local M2 memory.
