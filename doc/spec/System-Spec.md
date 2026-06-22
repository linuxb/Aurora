#  **Vibe Coding Spec**

## **0\. 设计哲学与 Vibe Coding 军规**

* **防御性编程**：永远不要信任 LLM 的非结构化输出，永远不要信任 TS Worker 的稳定性。
* **状态隔离**：调度控制流（TiDB）与数据流（KvRocks/Graph）绝对分离。
* **面向接口**：各语言组件之间必须通过强类型定义（Protobuf / JSON Schema）交互。

## **1\. 核心骨干：Go 网关与 DAG 调度引擎 (TiDB)**

### **1.1 核心数据结构 (DB Schema)**

调度系统采用关系型数据库（本地 MySQL 8.0 / 生产 TiDB），依赖 SKIP LOCKED 和原子计数器实现无锁并发。

```sql
\-- DAG 宏观控制表
CREATE TABLE dags (
    dag\_id VARCHAR(64) PRIMARY KEY,
    tenant\_id VARCHAR(64) NOT NULL,
    agent\_id VARCHAR(64) NOT NULL,
    session\_id VARCHAR(64) NOT NULL,
    user\_id VARCHAR(64) NOT NULL,
    original\_intent TEXT NOT NULL, \-- 原始意图（重规划的北极星指标）
    status VARCHAR(20) DEFAULT 'RUNNING', \-- RUNNING, REPLANNING, SUCCESS, FAILED
    replan\_count INT DEFAULT 0,
    created\_at TIMESTAMP DEFAULT CURRENT\_TIMESTAMP
);

\-- Task 微观执行表
CREATE TABLE tasks (
    task\_id VARCHAR(64) PRIMARY KEY,
    dag\_id VARCHAR(64) NOT NULL,
    sequence BIGINT NOT NULL,
    node\_type VARCHAR(16) NOT NULL, \-- skill | planner
    skill\_name VARCHAR(64), \-- planner 节点允许为空
    mem\_hint JSON NOT NULL,
    status VARCHAR(20) DEFAULT 'PENDING', \-- PENDING, READY, RUNNING, SUCCESS, FAILED
    pending\_dependencies\_count INT DEFAULT 0, \-- 原子依赖计数器
    owner\_id VARCHAR(64), \-- Worker 租约持有者
    expire\_at TIMESTAMP, \-- 租约过期时间 (OOM 捕获机制)
    INDEX idx\_ready\_tasks (status) \-- 极速扫描 READY 任务
);
```

### **1.2 核心并发原语 (Go SQL 约束)**

* **抢占任务 (Pull)**：必须使用 FOR UPDATE SKIP LOCKED。
* **触发下游 (Push)**：必须使用原子减法 UPDATE ... RETURNING。

// 抢占任务示例 (Go)
```sql
const PullTaskSQL \= \`
SELECT task\_id, skill\_name, dag\_id
FROM tasks
WHERE status \= 'READY'
LIMIT 1
FOR UPDATE SKIP LOCKED;
\`

// 触发下游示例 (Go) \- Task 成功后唤醒依赖它的子节点
const WakeUpChildSQL \= \`
UPDATE tasks
SET pending\_dependencies\_count \= pending\_dependencies\_count \- 1
WHERE task\_id \= ?
RETURNING pending\_dependencies\_count;
// 如果返回 0，则将该 Child 状态置为 READY
\`
```

## **2\. 容错与治愈：Replanning (动态重路由)**

### **2.1 死亡捕获机制 (Reaper)**

Go 后台守护进程（Sweeper）每 10 秒执行一次，捕获物理沙盒 OOM 导致的僵尸任务：

UPDATE tasks
SET status \= 'FAILED'
WHERE status \= 'RUNNING' AND expire\_at \< NOW();
\-- 触发 DAG 进入 REPLANNING 状态

### **2.2 LLM 重规划接口与受限解码**

当触发 Replanning 时，强制云端 LLM 使用 json\_schema 格式输出 PatchDAG，进行局部图修复。

```json
{
  "type": "object",
  "properties": {
    "reasoning": { "type": "string", "description": "简短说明重规划原因" },
    "new_nodes": {
      "type": "array",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["node_id", "node_type", "dependencies", "mem_hint"],
        "properties": {
          "node_id": { "type": "string" },
          "skill_name": { "type": "string" },
          "node_type": { "type": "string", "enum": ["skill", "planner"] },
          "dependencies": { "type": "array", "items": { "type": "string" } },
          "mem_hint": {
            "$ref": "https://aurora/spec/mem-hint.schema.json#/properties/mem_hint"
          }
        }
      }
    },
    "downstream_wiring": {
      "type": "object",
      "description": "原挂起节点如何与新节点连接的映射"
    }
  },
  "required": ["reasoning", "new_nodes", "downstream_wiring"]
}
```

## **3\. TS Worker 约束规范 (工具沙盒)**

### **3.1 双轨制返回契约**

TS Worker 执行完毕后返回原始 output，并可提供局部 skill summary。Arqo 必须把完整结果通过 Mem3 `Ingest(kind=TASK_OUTPUT)` 写入；局部 summary 只能辅助异步 reduce，不能替代 Mem3 rolling summary。

// TS SDK 核心接口
```typescript
export interface SkillResponse {
  // 由 Arqo 通过 Mem3 Ingest 保存的原始数据包
  raw\_data: string | Record\<string, any\>;
  // 可选的 Skill 局部提示，不是跨 Task rolling summary
  summary?: string;
}
```

### **3.2 Task 记忆生命周期**

1. 父 Task 完成后，Arqo 使用父 Task output 与子 Task goal 调用规划 LLM，生成或刷新子 Task 的最终 `mem_hint`；多父节点必须合并全部父输出后生成一次。
2. Task 从 `READY` 进入 `RUNNING` 前，Arqo 调用 Mem3 Search，传入可信 scope、当前 Task、`recent_limit` 与最终 `mem_hint`。
3. Search 无条件返回 last-N outputs 与最新 committed rolling summary，再附加定向检索结果。
4. 每个 `skill` 或 `planner` Task 成功后，Arqo 调用 Mem3 Task Ingest。
5. Mem3 异步执行 `new_summary = lightweight_llm(output, last_summary)`，按 DAG `sequence` 串行提交摘要版本。

完整 JSON Schema 见 `doc/spec/Mem3-API-Spec.md`。

### **3.3 错误提炼漏斗 (Semantic Error)**

绝对禁止抛出原生 Stack Trace 给 LLM。必须使用统一的异常类。

```typescript
export class AuroraSkillError extends Error {
  constructor(
    public code: 'NETWORK\_TIMEOUT' | 'AUTH\_FAILED' | 'RATE\_LIMIT' | 'API\_DEPRECATED' | 'UNKNOWN',
    public human\_readable\_msg: string, // 提炼后的死因（发给 LLM）
    public raw\_stack: string // 原始堆栈（仅存数据库排查用）
  ) { super(); }
}
```

### **3.4 实况遥测探针 (Telemetry Pub/Sub)**

Worker 在执行中，必须通过 Redis 向 Go 网关汇报细粒度进度，用于 SSE 前端渲染。

```jsonc
// Redis Publish 协议
{
  "session\_id": "sess\_001",
  "event\_type": "NODE\_PROGRESS", // NODE\_START, NODE\_PROGRESS, NODE\_FINISH, TOKEN\_STREAM
  "task\_id": "task\_456",
  "message": "正在解析 PDF 数据..."
}
```

## **4\. 记忆引擎 (Rust) 与 GraphRAG 机制**

### **4.1 异步旁路抽取流水线**

每次 DAG 构建时，Arqo 发送 `DAG_CONTEXT` Ingest；每个 Task 成功后发送 `TASK_OUTPUT` Ingest。Mem3 可靠接收后通过内部队列异步执行摘要 reduce、事实提取与图谱写入。

* **滚动摘要**：读取当前 Task output 与上一个 committed summary，生成新的 rolling summary。
* **事实与关系抽取**：可以读取受策略约束的 output、局部 skill summary 与 DAG intent slot；不得依赖或持久化私有思维链。

### **4.2 时序知识图谱 (Temporal Knowledge Graph) 写入规范**

在 Memgraph/Neo4j 中，所有节点强制要求包含 user\_id（多租户硬隔离），边必须包含时间戳。

```rust
// Rust 生成的标准化 MERGE 写入语句
MERGE (u:User {id: $user\_id})
MERGE (e:Entity {name: $entity\_name, type: $entity\_type, user\_id: $user\_id})
MERGE (u)-\[r:OBSERVED {
    task\_id: $task\_id,
    observed\_at: datetime()
}\]-\>(e)
```

## **5\. 本地开发拓扑 (Docker Compose 推荐组件)**

* **调度存储**: mysql:8.0-arm64
* **大容量缓存**: apache/kvrocks:latest
* **事件总线**: redis:7-alpine
* **图数据库**: memgraph/memgraph:latest
* **大模型推断**: 统一接入云端 (OpenAI / Gemini API)，避免本地 M2 内存枯竭。
