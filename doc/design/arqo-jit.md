# **Arqo JIT Dynamic Graph Expansion Design**

Arqo JIT lets Aurora continue planning when an initial DAG contains a Planner Node that cannot map directly to an existing Skill. The runtime expands that node into a concrete child DAG, hot-plugs the child graph into the scheduler, and continues execution without rebuilding the whole session.

## **1. Core Motivation**

Initial DAG generation should be conservative. If a step can be mapped to a known Skill, it becomes a `skill` node. If not, it becomes a `planner` node. A `planner` node does not execute business logic directly; it evaluates current context and generates the next executable subgraph.

## **2. Node Semantics**

- `skill`: executes a registered Skill through the Worker path.
- `planner`: invokes the internal planning flow and may return `SUCCESS_AND_EXPAND` with a child DAG.

Only `planner` nodes may trigger dynamic graph expansion. This is a hard scheduler rule, not a prompt convention.

## **3. ReAct Planner Skill**

The planner capability is represented as a predefined internal Skill. Its core behavior is to call the internal LLM Proxy in a loop-like reasoning step and generate an independent `mem_hint` for every direct child node.

### **3.1 Internal Execution Flow (TS Pseudocode)**

```ts
export async function runPlanner(ctx: PlannerContext): Promise<PlannerResult> {
  // 1. Fetch all currently collected working memory.
  const currentMemory = await ctx.memory.search({
    task_id: ctx.task_id,
    mem_hint: ctx.mem_hint,
  });

  // 2. Build a ReAct prompt and call Arqo's internal LLM Proxy.
  const prompt = `
You are a dynamic planning engine. Current global task goal: ${ctx.dag.original_intent}.
Collected information: ${currentMemory}.
Decide:
A. If the goal is already achieved, output the final result and do not expand the graph.
B. If the goal is not achieved, output the next required subgraph based on current findings. The output must strictly follow ExpansionPayload Schema.
C. Every new node must include mem_hint; use strategy=NONE when no additional directed retrieval is needed.
`;

  const decision = await ctx.llm.generateStructured(prompt, ExpansionPayloadSchema);

  // 3. Interpret the result.
  if (decision.status === "SUCCESS") {
    return { status: "SUCCESS", raw_data: decision.final_output };
  }

  if (decision.status === "SUCCESS_AND_EXPAND") {
    return { status: "SUCCESS_AND_EXPAND", expansion: decision.expansion };
  }

  return { status: "FAILED", error: decision.error };
}
```

## **4. Hot-Plugging: Arqo Transaction Engine**

When Arqo receives `SUCCESS_AND_EXPAND`, graph expansion must be atomic and must not leave broken edges. Because TiDB supports distributed transactions, Arqo executes the mutation as one transaction.

### **4.1 Dynamic Injection Transaction Flow (Go / SQL)**

```sql
-- 1. Suspend original downstream relationships and rewire dependencies.
-- Find all downstream nodes that depended on the original Planner node.
-- Adjust their dependency counters and redirect edges to the tail nodes of the injected subgraph.
UPDATE tasks
SET pending_dependencies_count = pending_dependencies_count - 1 + {new_tail_node_count}
WHERE task_id IN ({downstream_task_ids});

-- 2. Insert new dynamic nodes in bulk. Initial status is PENDING.
INSERT INTO tasks (task_id, dag_id, node_type, skill_name, status, pending_dependencies_count)
VALUES
  ('node_dyn_1', 'dag_1', 'skill', 'QueryLog', 'PENDING', 0),
  ('node_dyn_2', 'dag_1', 'skill', 'LLMSummarize', 'PENDING', 1);

-- 3. Mark the Planner itself as successful.
UPDATE tasks SET status = 'SUCCESS' WHERE task_id = {planner_task_id};

-- 4. Wake direct children whose dependencies are satisfied.
UPDATE tasks SET status = 'READY'
WHERE dag_id = {dag_id}
  AND status = 'PENDING'
  AND pending_dependencies_count = 0;
```

**Design essence**: at the instant the transaction commits, TiDB scheduling continues like clockwork. Go Worker threads using `SKIP LOCKED` can immediately claim newly born READY Tasks, and the graph proceeds without a pause.

## **5. Runaway Guardrails**

Letting an Agent write loops and subgraphs automatically is dangerous. Arqo must add three layers of guardrails at the metadata layer.

### **5.1 Guardrail Fields**

Add the following control fields to the `dags` table:

```sql
ALTER TABLE dags ADD COLUMN current_depth INT DEFAULT 1;
ALTER TABLE dags ADD COLUMN max_depth INT DEFAULT 10;
ALTER TABLE dags ADD COLUMN budget_limit_usd DECIMAL(10,4);
ALTER TABLE dags ADD COLUMN budget_consumed DECIMAL(10,4);
ALTER TABLE dags ADD COLUMN requires_hitl BOOLEAN DEFAULT FALSE;
```

### **5.2 Guardrail Interceptor Logic**

Before Arqo starts the DB transaction for an expansion request, it validates:

1. **Depth breaker**
   - Each `SUCCESS_AND_EXPAND` increments `current_depth`.
   - If `current_depth >= max_depth`, reject the child graph insertion, mark the planner node as `FAILED_MAX_DEPTH_REACHED`, and trigger failure handling with the best current result.
2. **Budget circuit breaker**
   - Arqo accumulates `budget_consumed` from proxy billing callbacks.
   - When budget is exhausted, block any LLM-related node execution and suspend the DAG.
3. **Human-in-the-loop yield**
   - The planner prompt may instruct the model to output a special `Ask_Human` node if it loops over the same problem more than three times.
   - If the expansion payload contains `Ask_Human`, Arqo performs a controlled suspend.
   - The UI asks the user for missing information, and the response becomes the `raw_data` output of the `Ask_Human` node before downstream execution resumes.

## **6. Missing-Skill Recursion Guard**

Depth alone does not express repeated inability to map work to a concrete Skill. Arqo should track `jit_unmapped_streak` at the DAG or planner-chain level.

- Reset the streak when a generated child graph maps to at least one business Skill.
- Increment the streak when expansion only produces more planning without concrete Skill execution.
- When the streak reaches the configured threshold, fail the planner task with `last_error_code=MISSING_SKILL` and return a user-facing error that a required Skill is missing.

This guard prevents infinite planning loops while making the product failure mode understandable.
