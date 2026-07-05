# **Aurora Intent Router**

Intent Router is the central brain of Aurora's large-scale Agentic system. Its purpose is to turn open-ended and ambiguous natural-language requests into executable, valid DAGs with stable behavior, low latency, and controlled compute cost.

## **1. Architecture Overview and Core Workflow**

The module avoids the broad "one black-box model generates the whole graph" pattern. Instead, it uses a three-stage funnel: **dimension reduction -> restricted generation -> static validation**.

1. **Step 1: Intent Slotting**: a lightweight fine-tuned model such as Llama-3-8B reduces natural language into structured intent features and entities.
2. **Step 2: DAG Skeleton Generation (Restricted Generation)**: based on extracted features and registered Skill schemas, a planner model performs constrained decoding and produces an initial DAG JSON.
3. **Step 3: DAG Context Ingest**: the raw query and structured intent slot are sent to Mem3; after durable acceptance, Mem3 asynchronously extracts and stores Goals, Profile items, Facts, and Relations.
4. **Step 4: Compiler-Level Static Validation (DAG Validator)**: pure code checks the generated JSON with strict graph constraints to guarantee physical validity.

Every DAG Node, also called a Step, belongs to exactly one of two types: a Skill Node that maps directly to a concrete Skill, or a Planner Node that requires JIT dynamic graph expansion.

### **1.1 Query-to-DAG Sequence**

```mermaid
sequenceDiagram
    autonumber
    actor User
    participant Flory
    participant Router as Intent Router
    participant Mem3
    participant Planner as DAG Planner
    participant Validator
    participant DB as Scheduler DB

    User->>Flory: Submit query
    Flory->>Router: Extract intent slot
    Router-->>Flory: Structured intent slot
    Flory->>Mem3: Ingest DAG_CONTEXT
    Mem3-->>Flory: 202 Accepted
    Note right of Mem3: Async extract goals, profile,<br/>facts and relations

    Flory->>Planner: Query + intent slot + Skill schemas
    Planner-->>Flory: DAG nodes + initial mem_hint
    Flory->>Validator: Validate schema and topology

    alt Validation succeeds
        Validator-->>Flory: Valid DAG
        Flory->>DB: Persist DAG and Tasks
    else Validation fails
        Validator-->>Flory: Structured validation error
        Flory->>Planner: Repair prompt
    end
```

## **2. Module 1: Intent and Entity Slot Extraction with a Lightweight Llama Model**

### **2.1 Core Challenge**

User requests are unbounded and often suffer from semantic drift. Asking a large model to directly output a DAG with dozens of nodes is highly likely to produce hallucinations and broken logic.

### **2.2 Solution: Intent-Slot Generation**

A lightweight open-source model, recommended as Llama-3-8B-Instruct, is placed at the gateway layer for reading comprehension and feature extraction. With `response_format: { type: "json_schema" }` and constrained decoding, it is forced to emit highly structured slot data.

**Input (User Query)**:

"Check the database deadlocks that the online payment service kept reporting yesterday, summarize the root cause, and email the backend lead."

**JSON Schema constraint for Llama-3-8B**:

```json
{
  "type": "object",
  "properties": {
    "macro_intent": {
      "type": "string",
      "enum": ["DATA_RETRIEVAL", "TROUBLESHOOTING", "REPORT_GENERATION", "ACTION_EXECUTION", "UNKNOWN"],
      "description": "High-level category used to select the rough execution path"
    },
    "entities": {
      "type": "array",
      "items": { "type": "string" },
      "description": "Core nouns or entities extracted from the request"
    },
    "temporal_context": { "type": "string", "description": "Time-range constraint" },
    "action_verbs": {
      "type": "array",
      "items": { "type": "string" },
      "description": "Actions the user expects the system to perform"
    }
  },
  "required": ["macro_intent", "entities", "action_verbs"]
}
```

**Output (Extraction Result)**:

```json
{
  "macro_intent": "TROUBLESHOOTING",
  "entities": ["payment service", "database deadlock", "backend lead"],
  "temporal_context": "yesterday",
  "action_verbs": ["inspect/query", "summarize", "send by email"]
}
```

*Benefit*: this step significantly reduces the difficulty of DAG generation by turning black-box natural language into white-box feature variables.

### **2.3 Mem3 DAG Context Ingest**

After intent-slot extraction succeeds, Flory must first call Mem3 `Ingest(kind=DAG_CONTEXT)`. The request carries trusted execution boundaries `tenant_id/agent_id/session_id/dag_id`, the raw query, and the full intent slot. Intent Router does not create long-term memory entries by itself. After Mem3 returns `202 Accepted`, it asynchronously extracts Goals, Profile items, Facts, and Relations, then writes them to KV/Graph.

The complete request and asynchronous extraction schemas are in `doc/spec/Mem3-API-Spec.md`. Intent Router must not promote information to Agent or Tenant scope by itself.

## **3. Module 2: Structured DAG Generation Engine**

### **3.1 Core Mechanism**

Based on the intent slots and entities extracted in the first step, the Go gateway assembles a complete prompt and submits it to the planner model, such as a fine-tuned Llama or a cloud LLM.

### **3.2 Strongly Typed JSON Schema Constraints**

To guarantee that LLM output can be parsed by Go `json.Unmarshal`, the model must operate under strict constrained decoding.

**Schema Contract**

DAG generation supports two Node types: Skill Node and Planner Node.

- A Skill Node is judged by the LLM to map to a concrete registered Skill and will not trigger another Expand Plan LLM call.
- A Planner Node cannot be directly mapped to a Skill. It requires JIT Planning and generates a child DAG.

```json
{
  "type": "object",
  "properties": {
    "dag_id": { "type": "string" },
    "nodes": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "node_id": { "type": "string", "pattern": "^[a-zA-Z0-9_]+$" },
          "skill_name": {
             "type": "string",
             "enum": ["QueryLog", "LLMSummarize", "SendEmail", "SearchGraph"]
          },
          "goal": { "type": "string", "minLength": 1 },
          "node_type": {
            "type": "string",
            "enum": ["skill", "planner"]
          },
          "mem_hint": {
            "$ref": "https://aurora/spec/mem-hint.schema.json#/properties/mem_hint"
          },
          "dependencies": {
            "type": "array",
            "items": { "type": "string" }
          },
          "input_parameters": { "type": "object" }
        },
        "required": ["node_id", "node_type", "mem_hint", "dependencies"],
        "allOf": [
          {
            "if": { "properties": { "node_type": { "const": "skill" } } },
            "then": { "required": ["skill_name"] }
          },
          {
            "if": { "properties": { "node_type": { "const": "planner" } } },
            "then": { "required": ["goal"] }
          }
        ]
      }
    }
  },
  "required": ["dag_id", "nodes"]
}
```

**DAG Build Flow**

```go
package flory

import "context"

// ==================== 1. Core data structures ====================

// NodeType defines the two fundamental node categories.
type NodeType string

const (
    NodeTypeSkill   NodeType = "skill"   // Execution node that maps directly to a known Skill.
    NodeTypePlanner NodeType = "planner" // Planning node that must be decomposed further.
)

// Node is one execution unit in the DAG.
type Node struct {
    ID           string
    DAGID        string
    Type         NodeType
    SkillName    string                 // Valid only when Type == NodeTypeSkill.
    Goal         string                 // Valid only when Type == NodeTypePlanner.
    Parameters   map[string]interface{}
    MemHint      MemHint                // Passed unchanged by Flory to Mem3 Search before Task start.
    Dependencies []string
}

// IntentContext comes from Intent Router.
type IntentContext struct {
    MacroIntent string
    IntentScore float64
    Slots       map[string]interface{}
}

// ==================== 2. Orchestrator: initial DAG generation ====================

type Orchestrator struct {
    LLMClient *LLMProxy
    Registry  *SkillRegistry // Global Skill registry.
}

// GenerateInitialDAG generates the initial static backbone DAG from intent-router output.
func (o *Orchestrator) GenerateInitialDAG(ctx context.Context, intent IntentContext) ([]*Node, error) {
    availableSkills := o.Registry.GetAvailableSkillSchemas()
    prompt := buildOrchestratorPrompt(intent, availableSkills)
    dagJSON := o.LLMClient.GenerateStructured(ctx, prompt, DAGSchema)
    return parseToNodes(dagJSON), nil
}

// ==================== 3. Flory Dispatcher: node execution and JIT expansion ====================

type Dispatcher struct {
    LLMClient *LLMProxy
    Registry  *SkillRegistry
    DB        *TiDBClient // Operates TiDB transactions for graph expansion.
}

// ExecuteNode runs when a node's dependencies reach zero and it becomes READY.
func (d *Dispatcher) ExecuteNode(ctx context.Context, node *Node, memoryCtx MemoryContext) error {
    if node.Type == NodeTypePlanner {
        if matchedSkill := d.Registry.FindExactMatch(node.Goal); matchedSkill != "" {
            node.Type = NodeTypeSkill
            node.SkillName = matchedSkill
        }
    }

    switch node.Type {
    case NodeTypeSkill:
        return d.dispatchToTSWorker(node, memoryCtx)
    case NodeTypePlanner:
        return d.spawnSubDAG(ctx, node, memoryCtx)
    }
    return nil
}

// spawnSubDAG decomposes and hot-plugs a child DAG.
func (d *Dispatcher) spawnSubDAG(ctx context.Context, plannerNode *Node, memoryCtx MemoryContext) error {
    availableSkills := d.Registry.GetAvailableSkillSchemas()
    prompt := buildIncubatePrompt(plannerNode.Goal, memoryCtx, availableSkills)
    subDAGJSON := d.LLMClient.GenerateStructured(ctx, prompt, SubDAGSchema)
    newNodes := parseToNodes(subDAGJSON)

    tx := d.DB.BeginTx()
    defer tx.Rollback()

    for _, n := range newNodes {
        tx.InsertNode(plannerNode.DAGID, n)
    }

    tailNodes := findTailNodes(newNodes)
    tx.RedirectDependencies(plannerNode.ID, tailNodes)
    tx.MarkNodeSuccess(plannerNode.ID)
    return tx.Commit()
}
```

- The initial DAG Planner must generate an initial `mem_hint` for every node and the final value directly for root nodes.
- After all parents of a non-root Task complete, Flory calls the planning LLM with parent Task outputs, the child Task goal, and the initial hint to generate the child's final `mem_hint`.
- Multi-parent Tasks must generate the final value once from all parent outputs. The last finishing parent must not overwrite other sources.
- When a Planner Node dynamically expands the graph, it must also generate an initial `mem_hint` for every direct child node.
- Before every Task transitions from `READY` to `RUNNING`, Flory must call Mem3 Search and retrieve last-N outputs, the latest rolling summary, and directed results based on `mem_hint`.
- LLM calls are also modeled as predefined system Skills.
- If a Node expands the DAG multiple times but still cannot map to a concrete Skill, the system must surface this to the UI by raising an error that indicates a new Skill is required or that the requested operation cannot be performed.

**Notes**

- `skill_name` may be empty for non-skill nodes.

### **3.3 Dynamic RAG Prompt Injection (Few-Shot Enhancement)**

For new intents outside the predefined range, the Go gateway uses the extracted `macro_intent` to retrieve 2-3 similar successful historical DAG cases from the vector store. These few-shot samples are appended to the prompt, allowing intent understanding to evolve without code changes.

## **4. Module 3: Compiler-Level DAG Post-Validation Engine**

### **4.1 Why Post-Validation Is Required**

Constrained decoding guarantees JSON shape and field types, but it does not guarantee graph-theoretic validity. The LLM can still produce cycles such as A depending on B and B depending on A. Persisting such a graph would deadlock execution.

### **4.2 Core Validation Flow (Go Layer)**

After the Go gateway receives the DAG JSON, it must pass three gates before writing to TiDB.

#### **Gate 1: Topological Cycle Detection**

- **Algorithm**: implement standard topological sorting, either Kahn's algorithm or DFS.
- **Logic**: sort all nodes. If the number of sorted nodes is smaller than the total node count, a cycle exists.
- **Action**: block the DAG and return `DAG_VALIDATION_FAILED: Cycle detected`.

#### **Gate 2: Dangling Dependency Detection**

- **Problem**: the LLM emits `node_C` depending on `node_X`, but `node_X` does not exist in the node list.
- **Logic**: iterate over every `dependencies` array and verify each referenced `node_id` exists in the graph.
- **Action**: block and return `DAG_VALIDATION_FAILED: Unknown dependency 'node_X'`.

#### **Gate 3: Isolated Node Warning**

- **Problem**: a node neither depends on anything nor is depended on by anything, except allowed entry or exit points.
- **Logic**: inspect in-degree and out-degree.
- **Action**: usually a warning rather than a fatal error, but product policy may choose to block it if it implies invalid work.

### **4.3 Handling Validation Failures (Retry Loop)**

If validation fails, the system does not immediately discard the request.

1. The Go gateway extracts the structured validation error, for example "the graph contains an A->B->A cycle".
2. It builds a repair prompt for the model: "The generated DAG contains a cycle. Fix dependencies and output the DAG again."
3. It sets a maximum retry count such as 3. If all attempts fail, intent parsing is marked as failed and escalated for human intervention.

## **5. Module Summary**

Aurora's brain relies on a triple safety mechanism: lightweight model dimension reduction, strongly constrained generation, and code-level validation. This avoids the instability of traditional Agent graph generation and provides industrial-grade determinism.
