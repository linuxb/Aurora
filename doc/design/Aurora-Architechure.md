# **Aurora Large-Scale Agentic Service System: Detailed Architecture and Solution**

This whitepaper provides the architecture blueprint for Aurora. It covers user request intake, high-concurrency DAG scheduling, multimodal and multi-dimensional memory management, and self-healing behavior for an industrial-grade Agent service infrastructure.

## **1. Global Topology and Core Components**

Aurora uses a multi-language microservice architecture to fully decouple control flow, data flow, and compute flow.

### **1.1 Component Choices and Responsibilities**

- **API Gateway and Scheduler**
  - **Language**: Go.
  - **Responsibilities**: receive HTTP/SSE user requests, run intent recognition and DAG generation, manage the DAG state machine through a distributed database, and stream real-time execution traces to the frontend.
- **Skill Workers**
  - **Language**: TypeScript in Node.js or a serverless runtime.
  - **Responsibilities**: execute business logic such as API calls, code execution, and crawling; condense complex results; classify errors at the source as semantic errors; report fine-grained progress to the gateway.
- **Memory and Knowledge Graph Engine**
  - **Language**: Rust.
  - **Responsibilities**: asynchronously consume execution results, interact with Graph and KV stores, extract knowledge with LLM-assisted triples, and provide dual-track GraphRAG retrieval.
- **Infrastructure Layer**
  - **Distributed scheduling state store**: TiDB in production and MySQL 8.0 for local development.
  - **Intermediate result store**: RocksDB/KvRocks for large JSON/text context.
  - **Event bus**: Redis Pub/Sub for real-time status push.
  - **Graph database**: NebulaGraph in production and Memgraph locally for entity relations and temporal knowledge graphs.

## **2. Scheduling Engine: Billion-Scale Concurrent Flow**

### **2.1 Problems**

- **Polling storm**: frequent Worker scans for READY tasks can saturate database CPU and IO.
- **Thundering herd**: many Workers may discover the same READY task and create write conflicts when trying to claim it.
- **Dependency deadlock**: if an upstream task completes but fails to wake downstream tasks because of phantoms or races, downstream tasks remain stuck in PENDING.
- **Intent-routing accuracy**: LLM output is unstable. Letting an LLM generate a DAG without guardrails can make the DAG unusable, so the system must balance intent quality and cost.

### **2.2 Solutions and Core Mechanisms**

#### **2.2.1 Physical State Separation (PENDING vs READY)**

The scheduler strictly separates PENDING tasks, which are blocked on dependencies, from READY tasks, which can execute immediately. Workers never scan PENDING tasks and only watch the READY queue, reducing query complexity.

#### **2.2.2 Push Wake-Up with Atomic Counters**

- **Principle**: instead of downstream nodes polling upstream state, upstream completion actively decrements downstream dependency counters.
- **Implementation**: each node initializes `pending_dependencies_count` to the number of prerequisites. When node A succeeds, it sends an atomic SQL decrement to downstream node B.
- **Example**:

```sql
-- After node A completes, atomically update downstream node B.
UPDATE tasks
SET pending_dependencies_count = pending_dependencies_count - 1
WHERE task_id = 'node_B'
RETURNING pending_dependencies_count;

-- If Go receives 0, immediately set node_B to READY.
```

#### **2.2.3 Graceful Concurrent Dispatch (SKIP LOCKED)**

- **Principle**: use the relational database lock manager to resolve claim conflicts.
- **Implementation**: Workers use `SELECT ... FOR UPDATE SKIP LOCKED`. When a row is locked, other Workers skip it and lock another row.
- **TiDB optimization**: because distributed locks have network cost, use batch fetch, such as `LIMIT 10 FOR UPDATE SKIP LOCKED`, and use `AUTO_RANDOM` keys to spread write hotspots.

#### **2.2.4 Lease and Zombie Recovery (Visibility Timeout)**

- **Problem**: if a Worker is killed by OOM or loses network while executing, the Task may remain RUNNING forever.
- **Solution**: when a Worker claims a task, it writes `owner_id` and `expire_at`, for example now plus 60 seconds. A singleton Reaper periodically scans expired RUNNING tasks and resets them to FAILED or triggers replanning.

## **3. Fault Tolerance and Self-Healing: Replanning**

### **3.1 Problem**

External environments are unreliable. Target APIs may fail and web pages may change. If one node failure collapses the whole DAG, Agent availability is poor.

### **3.2 Solution: REPLANNING State Machine and Local Hot Repair**

#### **3.2.1 Crime Scene Snapshot**

When a core node fails, the Go gateway does not immediately fail the whole request. It marks the DAG as REPLANNING and packages the following context for the prompt:

1. **Original Intent**: stored in the `dags` metadata table and used as the north star for replanning.
2. **Successful Nodes**: outputs from already completed Tasks.
3. **Root Cause**: distilled semantic error information.

#### **3.2.2 Structured Outputs and Boundary Matching**

To prevent hallucinated or disconnected repair graphs, the Replanner prompt must:

- **Declare boundaries**: specify required upstream input types and downstream output expectations.
- **Force structure**: use `response_format: { type: "json_schema" }` to output PatchDAG JSON containing `reasoning`, `new_nodes`, and `downstream_wiring`.

#### **3.2.3 Transactional Graph Repair and Static Checks**

- **Static validation**: after Go receives PatchDAG, it merges it with the original graph in memory and checks cycles and type compatibility.
- **Atomic hot repair**: after validation succeeds, a database transaction marks obsolete nodes ABORTED, inserts new nodes, and restores the DAG to RUNNING. Workers then pick up the new tasks normally.

## **4. TS Worker Constraints: Noise Filtering and Fault Isolation**

### **4.1 Problems**

- **Memory disaster**: passing raw MB-scale data directly to the graph engine or prompts inflates tokens and slows the system.
- **Error maze**: giving an LLM huge stack traces hurts replanning accuracy.
- **Single-process crash**: if many tasks run in one process, one OOM can kill all of them.

### **4.2 Solution: Source Cleaning and Physical Isolation**

#### **4.2.1 Dual-Track Return Protocol**

TS Skills must return two data tracks to separate compute flow from state flow:

- `raw_data`: raw payload, JSON or text, saved by Arqo through Mem3 Task Ingest.
- `summary`: optional local hint from the Skill. It can help Mem3 reduce but does not become the cross-Task rolling summary.

```ts
export interface SkillResult {
  raw_data: unknown;
  summary?: string;
}

const result: SkillResult = {
  raw_data: { comments: [] },
  summary: "Crawl succeeded, collected 2 MB of data and 50 valid comments."
};
```

#### **4.2.2 Semantic Error Funnel**

The TS SDK provides a unified error class. Developers classify errors at the source, such as `NETWORK_TIMEOUT` or `RATE_LIMIT`. When building the crime-scene snapshot, Go drops low-level `raw_stack` and sends only `human_readable_msg` to the Replanner LLM.

#### **4.2.3 Strict Physical Isolation**

- Use Docker container isolation initially, with a future path to MicroVMs.
- **Warm Pool**: pre-start idle Node.js containers. Inject code at scheduling time and destroy the container immediately after execution to avoid state pollution and memory leaks.

## **5. Memory Engine: Dual-Track Short/Long Memory and GraphRAG**

### **5.1 Problems**

- **Short-term memory black hole**: complex tasks can span 20 steps and grow context linearly until OOM or token limits.
- **Long-term memory gap**: Agents cannot remember cross-session history. Plain vector RAG often retrieves the wrong context.

### **5.2 Solution: Rolling Summaries and Temporal Knowledge Graphs**

See `doc/design/Mem3.md` for the detailed design.

The fixed lifecycle is:

1. During every DAG build, write the query intent slot through `DAG_CONTEXT` Ingest.
2. Before every Task, call Search to read last-N outputs, the latest rolling summary, and directed memory selected by `mem_hint`.
3. After every Task, write output through `TASK_OUTPUT` Ingest and asynchronously reduce `new_summary = LLM(output, last_summary)`.
4. GraphRAG relations are extracted asynchronously and stored with tenant isolation.

## **6. Local-First Evolution**

Aurora also supports a local-first runtime through the Local Engine design. The cloud path keeps TiDB/KvRocks/GraphDB. The local path can use SQLite, local files or BadgerDB, and embedded graph storage. See `doc/design/Local-Engine.md`.

## **7. Summary**

Aurora separates scheduling, execution, and memory so that each plane can scale and fail independently. The key design principles are deterministic DAG control, strict schema boundaries, asynchronous memory extraction, and transactional repair.
