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
    skill\_name VARCHAR(64) NOT NULL,  
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
// Replanner LLM 必须输出的 JSON Schema (Pydantic 规范)  
{  
  "type": "object",  
  "properties": {  
    "reasoning": { "type": "string", "description": "简短说明重规划原因" },  
    "new\_nodes": {  
      "type": "array",  
      "items": {  
        "node\_id": { "type": "string" },  
        "skill\_name": { "type": "string" },  
        "dependencies": { "type": "array", "items": { "type": "string" } }  
      }  
    },  
    "downstream\_wiring": {  
      "type": "object",  
      "description": "原挂起节点如何与新节点连接的映射"  
    }  
  },  
  "required": \["reasoning", "new\_nodes", "downstream\_wiring"\]  
}
```

## **3\. TS Worker 约束规范 (工具沙盒)**

### **3.1 双轨制返回契约**

TS Worker 执行完毕后，必须返回两种不同维度的数据，分别用于长期记忆存储和短期上下文流转。

// TS SDK 核心接口  
```typescript
export interface SkillResponse {  
  // 供长期记忆 (KvRocks) 存储的原始数据包 (MB级别)  
  raw\_data: string | Record\<string, any\>;   
  // 供图谱提取器和下一个 Agent 快速理解的结构化短文本摘要 (零 LLM 成本)  
  summary: string;   
}
```

### **3.2 错误提炼漏斗 (Semantic Error)**

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

### **3.3 实况遥测探针 (Telemetry Pub/Sub)**

Worker 在执行中，必须通过 Redis 向 Go 网关汇报细粒度进度，用于 SSE 前端渲染。

```json
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

TS Worker 返回 SUCCESS 后，向 Redis 队列发送信号。Rust 引擎异步消费，触发图谱抽取。

* **只提取精华**：提取策略仅读取 LLM 的 Thought (思维链) 或 TS Worker 的 summary，**绝对不读** raw\_data。

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

### **4.3 内部记忆检索 Skill (The "Remember" Tool)**

注入给大模型的内部工具，用于跨 Task 检索长线记忆。Go 网关在执行此 Skill 时，实现**零信任重写**。

```json
// Agent 看到的 Skill 描述  
{  
  "name": "SearchMemoryGraph",  
  "description": "如果你需要回想之前任务中的实体关系，请提供实体名称关键词。",  
  "parameters": { "query": "关于某实体的描述" }  
}
```

// Go 网关底层的安全拦截逻辑 (伪代码)  

```go
func ExecuteGraphSearch(ctx Context, query string) {  
    userID := ctx.GetUserID()  
    // 强制在底层图查询中注入 AND node.user\_id \= userID 约束  
    cypher := generateSafeCypher(query, userID)  
    graphDB.Execute(cypher)  
}
```

## **5\. 本地开发拓扑 (Docker Compose 推荐组件)**

* **调度存储**: mysql:8.0-arm64  
* **大容量缓存**: apache/kvrocks:latest  
* **事件总线**: redis:7-alpine  
* **图数据库**: memgraph/memgraph:latest  
* **大模型推断**: 统一接入云端 (OpenAI / Gemini API)，避免本地 M2 内存枯竭。