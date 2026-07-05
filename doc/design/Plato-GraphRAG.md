# **Plato GraphRAG System Architecture**

## **1. System Positioning and Core Philosophy**

Plato is the high-level abstraction layer for Mem3's memory system inside the Aurora architecture. It is the GraphRAG subsystem.

If the underlying KV store records an Agent's episodic memory, Plato builds the Agent's semantic network and macro-level memory.

Plato's core philosophy is "record at the micro level in real time, fold at the macro level asynchronously." Community clustering solves the classic RAG weakness where global summary questions cannot be answered because retrieval sees individual trees but not the forest.

## **2. Dual-Track Clustering Architecture (Adapter Pattern)**

To support both cloud online services with standalone high-performance graph databases and minimal local single-binary deployments, Plato isolates its graph clustering engine behind Rust traits.

### **2.1 Clustering Engine Interface (Rust Trait)**

At the Plato core layer, define one graph analytics adapter abstraction:

```rust
pub trait GraphAnalyticsAdapter {
    // Trigger graph clustering and return a node_id -> community_id mapping.
    async fn run_community_detection(&self) -> Result<HashMap<String, String>>;

    async fn get_dirty_edges_count(&self) -> Result<u64>;
}
```

### **2.2 Cloud Implementation (Memgraph MAGE Adapter)**

- **Scenario**: cloud-native environments with an independently deployed Memgraph cluster.
- **Mechanism**: push computation into the database. Plato (Rust) does not pull graph data into memory; it sends Cypher through Bolt and invokes Memgraph MAGE algorithms, including the C++ Leiden implementation.
- **Command**: `CALL community.leiden() YIELD node, community_id;`

### **2.3 Local Implementation (Pure Rust Louvain Adapter)**

- **Scenario**: desktop local-first Agents with restricted environments and zero-dependency goals.
- **Mechanism**: Plato starts an embedded pure Rust graph algorithm engine. It maintains a lightweight graph topology with `petgraph` and runs a hand-written Louvain algorithm when clustering is needed.
- **Optimization**: use contiguous arrays such as `Vec<usize>` to map node indexes and maximize CPU L1 cache locality. For graphs around 100k nodes, clustering should complete within tens of milliseconds.

## **4. Asynchronous Macro-Summary Generation Pipeline (Threshold Triggered)**

Macro-summary generation is expensive because it calls an LLM, so it must never run synchronously on Flory's Task execution critical path.

### **4.1 Dirty Edge Counter**

Plato maintains a lightweight state machine that records how many graph edges have been added or modified since the last clustering run.

### **4.2 Threshold Triggering**

A background slow-path reconstruction job starts when either condition is met:

1. **Volume threshold**: `dirty_edges_count >= 500`, meaning the graph topology changed materially.
2. **Time threshold**: more than 2 hours have elapsed since the last clustering run and unprocessed dirty data exists.

### **4.3 Asynchronous Pipeline**

When a threshold fires, the Rust background daemon runs this workflow:

1. **Cluster**: call the active `GraphAnalyticsAdapter` to obtain the latest community assignment.
2. **Slice subgraphs**: for each dirty community, extract core nodes such as the top-10 PageRank Entities and their hard facts.
3. **Summarize with LLM**: send the subgraph information to the model as a background task with a prompt asking it to summarize closely related components/events, macro themes, and current state.
4. **Write back**: create or update a special Community node in the graph database, persist the LLM output to its `macro_summary` property, and create `BELONGS_TO` edges from underlying Entity nodes to that Community.

## **5. Smart Query Routing by `mem_hint` (CBO)**

After parent Tasks complete, Flory calls the planning LLM with parent Task output and the child Task goal to generate the child Task's final `mem_hint`. Before the child Task starts, Mem3 Search interprets the hint and routes to Plato only when graph retrieval is required.

### **5.1 `mem_hint` Schema Contract**

```json
{
  "strategy": "LOCAL_GRAPH",
  "query": {
    "keywords": ["payment module", "auth interception"],
    "text": "Trace the relationship between payment failures and the authentication path"
  }
}
```

The complete schema lives in `doc/spec/Mem-Hint-Schema.md`. Tenant, Agent, Session, and DAG security scopes must not be written by the LLM into `mem_hint`; Flory injects them through the trusted scope of the Mem3 Search request.

### **5.2 LOCAL Routing (Micro Walk)**

- **Trigger**: tracing specific entities, concrete errors, or particular parameters.
- **Execution**:
  1. Use `keywords` as anchors to locate Entity nodes in the underlying graph.
  2. Walk 1~2 hops along observed relation edges.
  3. Return hard facts and relations collected on the path to Flory.
- **Property**: very low latency and high freshness, including data written moments earlier.

### **5.3 GLOBAL Routing (Macro Summary)**

- **Trigger**: system evolution, architecture-level assessment, or large-span historical summaries.
- **Execution (Map-Reduce)**:
  1. **Map**: skip thousands of low-level micro nodes and retrieve Community nodes directly. Use vector similarity or keywords to find the top-3 `macro_summary` values most relevant to `query.text`.
  2. **Reduce**: concatenate the three dense community summaries without a second LLM call and return them to Flory as high-density context.
- **Property**: answers high-dimensional architecture questions with very low token cost and avoids getting lost in micro-level graph details.
