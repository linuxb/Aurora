# **Polaris 记忆系统详细设计与实现参考**

本文件是 Aurora 大规模 Agentic 架构中“Polaris (北极星) 记忆系统”的详细落地规范。供 AI 辅助编程引擎 (Vibe Coding) 在实现 Rust 存储核心与 Go (Arqo) 调度接口时参考。

## **1. 系统定位与核心哲学**

Polaris 并非一个简单的外部数据库，而是 Agent 的**原生堆内存与海量持久化硬盘**。它采用读写分离与冷热分离策略，核心职责包括：

1. 提供O(1)复杂度的极速短期情景检索。  
2. 异步构建时序知识图谱，赋予 Agent 长线逻辑推理能力。  
3. 作为语义查询优化器 (Semantic CBO)，解析大模型的 mem_hint 并路由到最优底层存储。

## **2. 双轨记忆架构设计 (Dual-Track Storage)**

### **2.1 情景窗口记忆 (Episodic Window Memory) - 热数据**

* **物理引擎**: RocksDB (生产环境/本地化部署)
* **存储结构 (KV)**:  
  * **Key**: SessionID:DAGID:StepID (复合键，支持范围扫描前缀)  
  * **Value**:  
```json
    {  
      "step_id": "step_1",  
      "raw_output": "...庞大的原始结果...",  
      "summary": "简短的叙事性总结",  
      "hard_facts": ["IP: 192.168.1.1", "Error: Timeout"],
      "rels": [{"FuncA", "FuncB", "Call"}]
    }
```
  * **rels**约束LLM抽取出一些值得长期记忆的三元组，用于构建GraphRAG。
  * **summary**在skill节点中直接来源于skill spec的输出，在planner节点中通过约束LLM进行异步reduce获取（压缩信息），一般为LLM的Thought等。

### **2.2 长期时序图谱 (Long-Term Semantic Memory) - 冷数据**

* **物理引擎**: NebulaGraph / KùzuDB (本地化)  
* **构建机制**: 绝对旁路异步。在记忆Reduce过程中，从上一次summary，hard_facts，raw outputs等提取包含 user_id 和 observed_at 时间戳的三元组 (Triplets)。

## **3. 核心 API 接口契约 (Interface Spec)**

Polaris 对外暴露三个极简核心接口供 Arqo 调度器调用。

### **3.1 Ingest(req: IngestRequest)**

* **触发时机**: Arqo 执行完当前 Step 变为 SUCCESS 时同步调用。  
* **参数**: session_id, dag_id, step_id, raw_output, step_summary。  
* **执行逻辑**:  
  1. 异步(Rust/Tokio-Thread)进行记忆reduce，将新数据同步写入底层的 KV 引擎 (RocksDB)。  
  2. 触发窗口评估，决定是否进行 Rolling 压缩。乳如需压缩，本次数据更新为rolling reduce后结果。
  3. 该记录推入 GraphRAG。  

### **3.2 List(req: ListRequest) -> []MemoryBlock**

* **触发时机**: Agent 执行下一个节点前，需要加载紧邻的前序上下文时。  
* **参数**: session_id, dag_id, limit=N。 普通reduce N=1，需要rolling时，N>1。 
* **执行逻辑**: 利用底层 KV 引擎的前缀扫描 (Prefix Iterator)，按时间逆序快速返回最近 N 个 Task 的 {output, summary, hard_facts}。

### **3.3 Search(mem_hint: MemHint) -> SearchResult**

* **触发时机**: 大模型在生成 DAG 或 ReAct 扩图时，主动指明需要调取的特定记忆。  
* **执行逻辑**: (见第 5 节 mem_hint 路由机制)。

## **4. 记忆折叠算法：Rolling Summary 与 hard_facts**

为了解决无限累加导致的 Token 暴涨，同时防止**灾难性遗忘 (Catastrophic Forgetting)**，Polaris 采用“叙事压缩 + 事实保留”双轨合并算法。

### **4.1 滚动阈值触发 (Window Rolling)**

维护一个当前 DAG 的 Context Token 计数器。

* 常规情况（普通记忆reduce）：
```rust
new_summary = reduce(current_output, previous_summary)  
```
* 触发 Rolling reduce(如 Token > 2000)：
```rust
new_summary = reduce(outputs[n:], previous_summary)
```

### **4.2 提取器 Prompt 规范 (防遗忘机制)**

在调用廉价 LLM (如 Llama-8B) 执行 reduce 时，强制约束输出结构：

// Reduce 产物 Schema  
```json
{  
  "narrative_summary": "过去 5 步主要进行了网络排查和配置扫描...",  
  "hard_facts": [  
    // 之前积累的，加上最新提取出的确定性参数，永远不被丢弃，每次reduce仅追加，不丢弃  
    "User_ID = 10086",   
    "Failed_DB_Port = 3306"  
  ],
  "rels": [{"FuncA", "FuncB", "Call"}]
}
```

**说明**: narrative_summary 可以高度抽象以节省 Token，但系统级的关键参数 (IP、路径、报错码) 必须沉淀入 hard_facts 数组中永久伴随主干流转。

## **5. 记忆查询优化器：mem_hint 设计**

mem_hint 是由大模型生成的“语义 CBO (Cost-Based Optimizer)”，用于指导 Polaris 走哪条物理路径。

### **5.1 数据结构定义 (JSON Schema)**
```json
"mem_hint": {  
  "type": "object",  
  "properties": {  
    "strategy": {   
      "type": "string",   
      "enum": ["KV_POINT_GET", "GRAPH_TRAVERSAL", "NONE"],  
      "description": "如果明确知道前序步骤，用 KV；如果是找跨任务/跨时间的模糊关系，用 GRAPH"  
    },  
    "target_step_id": { "type": "string", "description": "KV_POINT_GET 必填" },  
    "semantic_query": { "type": "string", "description": "GRAPH_TRAVERSAL 必填，自然语言描述" }  
  }  
}
```

上述schema仅供说明设计原理，具体的schema参考doc/spec中文档定义。

### **5.2 Polaris 内部执行路由 (RBO + CBO 结合)**

当 Polaris 接收到 Search(mem_hint) 请求时：

// 伪代码：Polaris Search 执行器  
```golang
func (p *Polaris) Search(hint MemHint) Result {  
    // 1. 强迫短路规则 (RBO 安全网)  
    // 如果系统预判该实体就在最近 3 步内，强制无视 LLM 建议，走本地内存 List()  
    if isRecentContext(hint.SemanticQuery) {  
        return p.ListRecentFacts()  
    }

    // 2. 语义路由 (CBO)  
    switch hint.Strategy {  
    case "KV_POINT_GET":  
        // O(1) 精确点查，零幻觉  
        return p.KVStore.Get(hint.TargetStepID)   
          
    case "GRAPH_TRAVERSAL":  
        // 昂贵的图游走  
        result := p.GraphDB.CypherQuery(generateSafeCypher(hint.SemanticQuery))  
          
        // 3. Fallback 兜底机制  
        // 如果图库查不到（可能是异步抽取延迟），回退到 KV 进行本地全文/正则检索  
        if result.IsEmpty() {  
            return p.KVStore.ScanFallback(hint.SemanticQuery)  
        }  
        return result  
    }  
}  
```
