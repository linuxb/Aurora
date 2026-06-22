# **Arqo 调度器 JIT 动态扩图引擎详细设计**

## **1. 架构理念：静态主干与动态叶子 (Static Backbone & Dynamic Leaves)**

在传统的 AOT（提前编译）模式下，DAG 是一次性生成的死树。在 JIT（即时编译）模式下，arqo 调度器允许 DAG 成为一棵“生长中的树”。

* **静态主干**：初始由用户意图生成的确定性骨架，由 `skill` 与 `planner` 两类节点组成。
* **动态叶子**：在执行过程中，由 Planner Node 根据环境反馈动态生成子图，作为新的 Skill Node 或 Planner Node“热插拔”到主干上。

## **2. 核心通信协议 (The Expansion Spec)**

要实现动态扩图，必须在 Planner Node 与 arqo 调度器之间定义严格的扩图握手协议。当 Planner Node 完成一次规划时，向 arqo 汇报的不再是简单的 SUCCESS，而是特殊的 SUCCESS_AND_EXPAND 状态，并附带扩展负载（Payload）。

### **2.1 动态扩图负载规范 (JSON Schema)**

```json
{
  "status": "SUCCESS_AND_EXPAND",
  "task_id": "node_react_planner_1",
  "dag_id": "dag_888",
  "node_type": "planner",
  "expansion_payload": {
    "reasoning": "发现目标有3个子系统，需要并行搜集数据然后再汇总",
    "new_nodes": [
      {
        "node_id": "node_dyn_1",
        "node_type": "skill",
        "skill_name": "WebSearch",
        "mem_hint": {
          "version": "1.0",
          "strategy": "GRAPH_LOCAL_TRAVERSAL",
          "query": {
            "text": "检索与子系统 A 相关的历史事实",
            "keywords": ["子系统A"]
          }
        },
        "parameters": {"query": "子系统A 文档"},
        "dependencies": ["node_react_planner_1"] // 依赖当前 Planner 节点
      },
      {
        "node_id": "node_dyn_2",
        "node_type": "skill",
        "skill_name": "WebSearch",
        "mem_hint": {
          "version": "1.0",
          "strategy": "NONE"
        },
        "parameters": {"query": "子系统B 文档"},
        "dependencies": ["node_react_planner_1"]
      },
      {
        "node_id": "node_dyn_summary",
        "node_type": "skill",
        "skill_name": "LLMSummarize",
        "mem_hint": {
          "version": "1.0",
          "strategy": "KV_POINT_GET",
          "query": {
            "target_task_id": "node_dyn_2"
          }
        },
        "dependencies": ["node_dyn_1", "node_dyn_2"] // 动态子图内部的依赖
      }
    ],
    "downstream_wiring": {
      // 核心难点：重定向原有的下游依赖！
      // 假设原本有一个 node_final 依赖 node_react_planner_1
      // 扩图后，node_final 必须改为依赖新生成的尾部节点 node_dyn_summary
      "redirect_from": "node_react_planner_1",
      "redirect_to": ["node_dyn_summary"]
    }
  }
}
```

## **3. Planner Node：ReAct Planner 运行机制**

Planner Node 的 `node_type` 固定为 `planner`，由 Arqo 的内置 ReAct Planner 执行，不映射外部 Skill。其核心逻辑是循环调用内部的 LLM Proxy 进行状态评估，并为每个直接子节点生成独立 `mem_hint`。

### **3.1 内部执行流程 (TS 伪代码)**

```typescript
async function executeReActPlanner(ctx: TaskContext) {
    // 1. 获取系统当前所有已收集到的上下文 (Working Memory)
    const currentMemory = await ctx.getWorkingMemory();

    // 2. 组装 ReAct Prompt，请求 arqo 内部的 LLM Proxy
    const prompt = `
    你是一个动态规划引擎。当前任务总目标：${ctx.dag.original_intent}。
    已收集到的信息：${currentMemory}。
    请判断：
    A. 如果目标已达成，请直接输出最终结果，不进行扩图。
    B. 如果目标未达成，请基于当前发现，输出必须执行的【下一步子图】。输出格式严格遵守 ExpansionPayload Schema。
    C. 每个新节点必须包含 mem_hint；不需要额外定向检索时使用 strategy=NONE。
    `;

    const llmResponse = await llmProxy.generate(prompt, { responseFormat: ExpansionSchema });

    // 3. 判定结果
    if (llmResponse.isFinished) {
        return { status: "SUCCESS", result: llmResponse.finalAnswer };
    } else {
        // 触发 JIT 扩图
        return {
            status: "SUCCESS_AND_EXPAND",
            expansion_payload: llmResponse.expansionPayload
        };
    }
}
```

## **4. 热插拔注入 (Hot-Plugging) - arqo 端事务引擎**

当 arqo 收到 SUCCESS_AND_EXPAND 时，必须保证扩图过程是原子性的，不能出现断链。由于 TiDB 支持强大的分布式事务，我们在底层执行以下逻辑。

### **4.1 动态注入事务流程 (Go / SQL)**

```sql
BEGIN;

-- 1. 挂起原有的关联，处理依赖重定向 (Wiring)
-- 找出所有依赖原来 Planner 的下游节点（比如原计划的收尾节点）
-- 将它们的前置依赖数调整，并更新有向边映射
UPDATE tasks
SET pending_dependencies_count = pending_dependencies_count - 1 + {新注入的尾部节点数量}
WHERE dag_id = 'dag_888'
  AND JSON_CONTAINS(dependencies, '"node_react_planner_1"');

-- 2. 批量插入新动态节点 (初始状态均为 PENDING)
INSERT INTO tasks (task_id, dag_id, node_type, skill_name, status, pending_dependencies_count, dependencies)
VALUES
('node_dyn_1', 'dag_888', 'skill', 'WebSearch', 'PENDING', 0, '["node_react_planner_1"]'),
('node_dyn_2', 'dag_888', 'skill', 'WebSearch', 'PENDING', 0, '["node_react_planner_1"]'),
('node_dyn_summary', 'dag_888', 'skill', 'LLMSummarize', 'PENDING', 2, '["node_dyn_1", "node_dyn_2"]');

-- 3. 将 Planner 本身标记为成功
UPDATE tasks SET status = 'SUCCESS' WHERE task_id = 'node_react_planner_1';

-- 4. 触发新节点进入就绪态 (因为 Planner 成功了，它的直属子节点应该被唤醒)
UPDATE tasks
SET status = 'READY'
WHERE task_id IN ('node_dyn_1', 'node_dyn_2');

COMMIT;
```

**设计精髓**：事务提交的瞬间，TiDB 调度底层依旧如同钟表般运转，Go Worker 线程通过 SKIP LOCKED 瞬间捕获到了新诞生的 READY 任务（node_dyn_1 和 node_dyn_2），整个图没有一丝停滞，继续滚滚向前。

## **5. 防失控护栏机制 (Guardrails)**

让 Agent 自动写循环、写子图是危险的。arqo 必须在数据表元数据层设立三道钢铁长城。

### **5.1 数据结构护栏注入**

在 dags 表中增加以下控制字段：

```sql
ALTER TABLE dags ADD COLUMN (
    current_depth INT DEFAULT 1,       -- 当前执行深度
    max_depth INT DEFAULT 10,          -- 最大允许深度（防死循环）
    budget_limit_usd DECIMAL(10,4),    -- 任务总预算
    budget_consumed DECIMAL(10,4),     -- 已消耗成本
    requires_hitl BOOLEAN DEFAULT FALSE -- 是否处于等待人类救场状态
);
```

### **5.2 护栏拦截逻辑 (arqo 拦截器)**

在 arqo 接收到扩图请求并开始 DB 事务前，执行以下断言校验：

1. **深度拦截器 (Depth Breaker)**
   * 每执行一次 SUCCESS_AND_EXPAND，current_depth += 1。
   * 如果 current_depth >= max_depth：拒绝插入新子图，强制将 node_react_planner_1 的状态改为 FAILED_MAX_DEPTH_REACHED，强制触发系统的错误收尾逻辑（抛出当前最佳结果）。
2. **破产熔断器 (Budget Circuit Breaker)**
   * arqo 根据 Proxy 的计费回传，累加 budget_consumed。
   * 当预算触底，拦截任何 LLM 相关的节点执行并挂起 DAG。
3. **人类在环降级 (Human-in-the-Loop Yield)**
   * ReAct_Planner 在 Prompt 中被告知：*“如果你在同一类问题上循环了超过 3 次依然毫无头绪，请输出特殊节点 Ask_Human。”*
   * 扩图负载如果包含 Ask_Human 节点，arqo 将执行一种特殊的挂起（Suspend）。
   * 前端 UI 捕获该事件，弹窗提示用户：“Agent 在配置解析环节卡死，请求人工提供正确的数据库连接字符串。”
   * 用户输入后，该文本作为 Ask_Human 节点的 raw_data 输出，继续激活下游扩图节点。
