# **Plato GraphRAG 系统架构设计**

## **1. 系统定位与核心哲学**

Plato 是 Aurora 架构中 Mem3 记忆系统的高阶抽象层（GraphRAG 子系统）。

如果说底层的 KV 存储记录了 Agent 的“情景记忆（Episodic Memory）”，那么 Plato 负责构建 Agent 的“语义网络与宏观认知（Semantic & Macro Memory）”。

Plato 的核心哲学是“微观实时记录，宏观异步折叠”。它通过社区聚类算法，解决传统 RAG 无法回答全局总结性问题的痛点（即“见树不见林”问题）。

## **2. 核心双轨聚类架构 (Adapter 模式)**

为了兼顾云端在线服务（依托高性能独立图数据库）和极简本地化部署（Single Binary 零依赖），Plato 核心的图聚类引擎采用 Rust Trait 接口隔离设计。

### **2.1 聚类引擎接口定义 (Rust Trait)**

在 Plato 核心层，定义统一的图分析适配器抽象：

```rust
pub trait GraphAnalyticsAdapter {
    // 触发图聚类算法，返回 节点ID -> 社区ID 的映射
    fn detect_communities(&self) -> Result<HashMap<String, String>, PlatoError>;
}
```

### **2.2 云端实现 (Memgraph MAGE Adapter)**

* **适用场景**：云原生环境，独立部署 Memgraph 集群。
* **实现原理**：计算下推（In-Database Analytics）。Plato (Rust) 不拉取任何图数据到内存，直接通过 Bolt 协议向 Memgraph 发送 Cypher 指令，调用其底层的 MAGE 算法库（C++ 实现的 Leiden 算法）。
* **执行指令**：CALL community.leiden() YIELD node, community_id;

### **2.3 本地化实现 (Pure Rust Louvain Adapter)**

* **适用场景**：桌面级 Local-First Agent，环境受限，追求零依赖。
* **实现原理**：Plato 启动时拉起内嵌的纯 Rust 图算法引擎。利用 petgraph 库在内存中维护轻量级图拓扑。当需要聚类时，运行手工编写的无外部依赖的 Louvain 算法。
* **极致优化**：算法内部使用连续数组 (Vec<usize>) 映射节点索引，最大化 CPU L1 Cache 命中率，在 10 万级节点规模下，聚类耗时控制在数十毫秒级别。

## **4. 异步宏观摘要生成流水线 (Threshold-Triggered)**

宏观摘要的生成是昂贵的（需要调 LLM），因此绝对不能在 Arqo 任务执行的关键路径上同步进行。

### **4.1 脏数据计数器 (Dirty Edge Counter)**

Plato 维护一个轻量级的状态机，记录自上一次聚类以来，图谱中新增/修改的边（Edge）的数量。

### **4.2 阈值触发机制**

当满足以下任一条件时，触发后台慢流（Slow-path）重构任务：

1. **体积阈值**：dirty_edges_count >= 500（图谱发生实质性拓扑改变）。
2. **时间阈值**：距离上次聚类超过 2 小时，且存在未处理的脏数据。

### **4.3 异步生成流水线 (The Pipeline)**

当阈值被触发，Rust 后台守护线程启动以下工作流：

1. **聚类运算**：调用当前的 GraphAnalyticsAdapter 获取最新的社区划分。
2. **子图切片**：针对每一个包含脏数据的社区，提取其核心节点（如 PageRank 前 10 的 Entity）和 hard_facts。
3. **LLM 摘要归纳**：将子图信息发送给大模型（背景任务），Prompt：“请总结以下系统中关联紧密的组件/事件，提取其核心宏观主题和当前状态”。
4. **回写图库**：在图数据库中创建/更新特殊的 Community 节点，将 LLM 的输出持久化到其 macro_summary 属性上，并建立底层 Entity 到该 Community 的 BELONGS_TO 边。

## **5. 基于 mem_hint 的智能查询路由 (CBO)**

父 Task 完成后，Arqo 使用父 Task output 与子 Task goal 调用规划 LLM，生成子 Task 的最终 `mem_hint`。子 Task 开始前由 Mem3 Search 解释该提示；需要图检索时再路由到 Plato。

### **5.1 mem_hint Schema 契约**

```json
{
  "mem_hint": {
    "version": "1.0",
    "target_system": "PLATO_GRAPH",
    "strategy": "GRAPH_LOCAL_TRAVERSAL",
    "query": {
      "keywords": ["支付模块", "鉴权拦截"],
      "text": "定位支付失败与鉴权链路关系"
    }
  }
}
```

具体 Schema 参考 `doc/spec/Mem-Hint-Schema.md`。Tenant、Agent、Session 与 DAG 安全作用域不允许由 LLM 写入 `mem_hint`，必须由 Arqo 通过 Mem3 Search 请求的可信 scope 注入。

### **5.2 LOCAL (微观游走) 路由逻辑**

* **触发场景**：针对具体实体、具体报错、特定参数的追溯。
* **Plato 执行**：
  1. 利用 keywords 作为起始锚点（Anchor），在底层图谱中定位具体的 Entity 节点。
  2. 沿 OBSERVED 边进行 1~2 Hop（跳）的图游走。
  3. 将游走路径上收集到的实体 hard_facts 和关系直接返回给 Arqo。
* **特性**：极低延迟，实时性强（能查到刚写入前一秒的数据）。

### **5.3 GLOBAL (宏观摘要) 路由逻辑**

* **触发场景**：关于系统演进、整体架构评估、跨度极大的历史总结。
* **Plato 执行 (Map-Reduce)**：
  1. **Map**：跳过底层成千上万的繁杂微观节点，直接在图库中检索 Community 节点。通过向量相似度或关键词，找出与 `query.text` 最相关的 Top-3 社区的 macro_summary。
  2. **Reduce**：将这 3 段高度浓缩的社区摘要拼装在一起，无需二次调用 LLM，直接作为 High-density Context（高浓度上下文）返回给 Arqo。
* **特性**：能够回答极高维度的架构级问题，Token 消耗极低，彻底克服传统图检索的“迷失在细节中”的问题。
