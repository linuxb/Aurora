# **Aurora 大规模 Agentic 服务系统 \- 详细架构设计与解决方案**

本白皮书旨在提供 Aurora 系统的深度架构设计蓝图。从用户请求接入，到亿级任务的高并发调度，再到多模态/多维度的长短期记忆管理，本设计致力于构建一个高可用、低延迟、具备自我修复能力的工业级智能体（Agent）服务基础设施。

## **1\. 系统全局拓扑与核心组件**

Aurora 采用多语言微服务架构，以实现“控制流”、“数据流”和“计算流”的彻底解耦。

### **1.1 组件选型与职责划分**

* **API 网关与调度中心 (Gateway & Scheduler)**  
  * **语言**: Go (Golang)  
  * **核心职责**: 承接 HTTP/SSE 用户请求，意图识别（通过云端 LLM）并生成执行图（DAG），利用分布式数据库管理 DAG 状态机，监听并推送实时执行轨迹至前端。  
* **动作执行沙盒 (Skill Workers)**  
  * **语言**: TypeScript (Node.js) / Serverless 环境  
  * **核心职责**: 执行具体业务逻辑（如调用 API、执行代码、爬虫），将复杂结果浓缩并在源头进行异常分类（Semantic Error），向网关汇报细粒度执行进度。  
* **记忆与知识图谱引擎 (Memory Engine)**  
  * **语言**: Rust  
  * **核心职责**: 异步消费执行结果，与图数据库、KV 存储交互。负责知识榨取（调用 LLM 提取三元组），实现 GraphRAG 双轨检索。  
* **基础设施层 (Infrastructure)**  
  * **分布式调度状态库**: TiDB (本地开发使用 MySQL 8.0)，利用其分布式事务和锁机制。  
  * **中间结果存储**: Apache KvRocks，低成本存储海量大体积上下文 (JSON/文本)。  
  * **事件总线**: Redis，用于发布/订阅 (Pub/Sub) 模式的实时状态推送。  
  * **图数据库**: NebulaGraph (生产) / Memgraph (本地)，用于存储实体关系与时序知识图谱。

## **2\. 调度引擎设计：亿级并发流转**

### **2.1 面临的问题**

* **轮询风暴 (Polling Storm)**：Worker 频繁轮询数据库扫描“已就绪”任务，会导致 CPU 和 IO 瞬间打满，引发数据库熔断。  
* **并发踩踏 (Thundering Herd)**：多个 Worker 同时扫描到相同的 READY 任务并尝试使用 CAS 抢占时，会产生海量的写冲突和重试，导致吞吐量断崖式下跌。  
* **状态依赖死锁**：上游任务完成后，如果因为幻读或竞态条件没有唤醒下游任务，下游将永远卡在 PENDING 状态（丢失唤醒）。  
* **意图路由准确性：**LLM输出不稳定，如果任由LLM生成DAG，很可能导致DAG不可用，如何权衡意图识别的准确度和成本（调用LLM还是Bert等轻量级模型）是一个重要挑战。

### **2.2 解决方案与核心机制**

#### **2.2.1 状态物理分离 (PENDING vs READY)**

调度器将任务状态严格分为 PENDING（阻塞中，等待前置依赖完成）和 READY（就绪，可立即执行）。Worker 绝不扫描 PENDING，只紧盯 READY 队列，极大地降低了查询复杂度。

#### **2.2.2 基于原子减法的 Push 唤醒机制 (Atomic Counter)**

* **原理**: 抛弃“下游轮询上游状态”的做法。采用“上游执行完，主动递减下游依赖计数器”的 Push 模式。  
* **实现**: 每个节点初始化时设置一个 pending\_dependencies\_count（前置依赖数量）。当节点 A 执行成功后，直接向数据库发送原子 SQL 递减下游节点 B 的计数器。  
* **示例**:  
  \-- 节点A完成后，原子更新下游节点B  
  UPDATE tasks   
  SET pending\_dependencies\_count \= pending\_dependencies\_count \- 1   
  WHERE task\_id \= 'node\_B'   
  RETURNING pending\_dependencies\_count;  
  \-- 如果 Go 收到返回值 0，立即触发 SQL 将 node\_B 状态置为 READY

#### **2.2.3 优雅的并发分发 (SKIP LOCKED)**

* **原理**: 利用关系型数据库的底层锁管理器，完美解决抢占冲突。  
* **实现**: Worker 通过 SELECT ... FOR UPDATE SKIP LOCKED 获取任务。当行被锁定，其他 Worker 会自动“跳过”该行，立刻锁定下一行，实现真正的零锁等待 ![][image1] 分发。  
* **针对 TiDB 的优化**: 由于 TiDB 分布式锁的网络开销较大，采用**批量抢占 (Batch Fetch)** 策略（例如 LIMIT 10 FOR UPDATE SKIP LOCKED）来摊销 RPC 延迟。同时，强制使用 AUTO\_RANDOM 主键打散写入热点。

#### **2.2.4 租约机制与僵尸回收 (Visibility Timeout)**

* **问题**: Worker 在执行时由于 OOM 被杀或网络断开，导致任务永远处于 RUNNING 状态（僵死）。  
* **方案**: Worker 抢占任务时，一并写入 owner\_id 和 expire\_at（例如当前时间 \+ 60秒）。后台单例 Reaper 进程定期扫描 expire\_at \< NOW() 且处于 RUNNING 的任务，将其重置为 FAILED 或触发重规划。

## **3\. 容错与自愈：自动重路由 (Replanning)**

### **3.1 面临的问题**

外部环境不可控（如目标 API 宕机、网页结构变化），若单点失败导致整个 DAG 崩溃，Agent 的可用性将极低。

### **3.2 解决方案：引入 REPLANNING 状态机与局部热修复**

#### **3.2.1 案发现场快照 (Crime Scene Snapshot)**

当某核心节点失败时，Go 网关不立即报错，而是将 DAG 宏观状态置为 REPLANNING。随后提取以下关键信息打包为 Prompt 语境：

1. **原始意图 (Original Intent)**: 存储于 dags 元数据表中（例如：“帮我总结昨天日志并报警”），作为重规划的北极星指标。  
2. **可用资产 (Successful Nodes)**: 已完成任务的输出。  
3. **失败根因 (Error Distillation)**: 经过提炼的语义化错误原因（见 4.2 节）。

#### **3.2.2 受限解码与边界对齐 (Structured Outputs & Boundary Matching)**

为了防止大模型重规划产生“幻觉”或生成无法衔接的孤岛节点，在调用云端 LLM (Replanner) 时：

* **明确断口**: 在 Prompt 中明确指出新节点必须对接的上游输入类型和下游期望输出类型。  
* **强制格式化**: 利用 response\_format: { type: "json\_schema" } 强制模型输出标准的 PatchDAG JSON 结构，包含 reasoning, new\_nodes, 和 downstream\_wiring。

#### **3.2.3 事务级图修复与静态检查**

* **静态校验**: Go 网关拿到 PatchDAG 后，在内存中与原图拼接，进行循环依赖和类型兼容性检查。  
* **原子热修复**: 校验通过后，使用数据库事务将废弃节点置为 ABORTED，插入新节点，并将 DAG 状态恢复为 RUNNING。底层 Worker 会无缝接管新任务。

## **4\. TS Worker 约束规范：噪音过滤与异常隔离**

### **4.1 面临的问题**

* **内存灾难**: 将未经处理的 MB 级别原生数据直接发给图谱引擎或放入 Prompt，会导致 Token 暴涨并拖慢系统。  
* **报错迷宫**: 将万行堆栈日志交给 LLM 会导致其注意力分散，无法准确进行 Replanning。  
* **单点崩溃**: 多个任务在同进程执行，一人 OOM 全家陪葬。

### **4.2 解决方案：源头清洗与物理隔离**

#### **4.2.1 双轨制返回接口 (Dual-Track Return Protocol)**

强制 TS Skill 开发者返回两种数据结构，实现“计算流”与“状态流”的分离：

* raw\_data: 原始的庞大负载（JSON/文本），由 Go 引擎直接存入 **KvRocks**。  
* summary: 极简的总结描述，拼接到系统的工作记忆中，用于 LLM 后续决策。  
  // TS Skill 返回规范  
  return {  
    raw\_data: "\<html\>...2MB...\</html\>",  
    summary: "爬取成功，获取 2MB 数据，包含 50 条有效评论。"  
  }

#### **4.2.2 语义化异常漏斗 (Semantic Error Funnel)**

TS SDK 提供统一错误类，要求开发者在源头对异常进行分类（如 NETWORK\_TIMEOUT, RATE\_LIMIT）。

Go 引擎在组装案发现场时，丢弃底层 raw\_stack，只把 human\_readable\_msg 发给 Replanner LLM，确保 LLM 只阅读高质量的诊断报告。

#### **4.2.3 严格物理隔离**

* 采用 Docker 容器级隔离（后期演进为 MicroVM）。  
* **暖池架构 (Warm Pool)**：预先拉起空闲 Node.js 容器。调度时注入代码，执行完毕**立即销毁 (Disposable)**，从根本上杜绝内存泄漏和状态污染。

## **5\. 记忆引擎设计：双轨长短记忆与 GraphRAG**

### **5.1 面临的问题**

* **短期记忆黑洞**: 复杂任务经过 20 步流转，上下文线性增长，导致 OOM 或超出 Token 限制。  
* **长期记忆断层**: Agent 无法记住跨会话的历史经验，仅依靠文本向量检索（Vector RAG）经常出现找错上下文的“召回幻觉”。

### **5.2 解决方案：Token 阈值折叠与时序知识图谱**

#### **5.2.1 短期工作记忆的“惰性摘要 (Lazy Summary)”**

* 工作记忆作为一段递增的 Buffer。当 TS Worker 返回纯文本 summary 时，直接 Append（零 LLM 成本）。  
* 当 Buffer Token 数量超过阈值（如 2000 Token）时，Go 网关触发“中断”，调用廉价本地小模型将 Buffer 总结压缩为 500 字摘要，清空 Buffer 继续流转。

#### **5.2.2 长期记忆的旁路抽取流水线 (Asynchronous Extraction)**

* **时机**: 绝不在执行主路径上提取图谱。节点完成后，通过 Redis 发送信号，Rust 引擎在后台异步消费。  
* **数据源过滤**: Rust 引擎仅读取 LLM 执行时的 Thought（思维链）或 TS 返回的极简 summary 进行提取，坚决不碰 KvRocks 里的 raw\_data 原数据。如果TS Worker不执行LLM时，通过TS Worker的输出规约要求输出协议带上summary字段供图谱关系抽取。  
* 实体抽取。我们经过数据源过滤噪音后生成summry，然后通过Prompt模版约束一个轻量级LLM进行实体抽取，得到JSON结构化输出的实体关系。

#### **5.2.3 时序多租户知识图谱 (Temporal Multi-Tenant GraphRAG)**

* **严格租户隔离**: 采用逻辑隔离，图数据库中每一个节点（Node）强关联 user\_id 属性。  
* **时序保留**: 使用 MERGE 语句更新实体，但所有关系边（Edge）必须带有时间戳属性（如 observed\_at），保留知识的演进时间线。  
* **防注入查询重写 (Zero-Trust Rewriting)**:  
  当 Agent 调用 SearchMemoryGraph 内部 Skill 时，Go 网关进行拦截。不信任 LLM 生成的 Cypher，而是解析语义意图后，在 Go 后端强制拼接 AND n.user\_id \= ? 约束，确保物理级防穿透。

## **6\. 用户体验与前端交互：白盒化轨迹 (Glass-box UX)**

### **6.1 面临的问题**

长时 Agent 任务会让前端呈现“假死”状态，严重损害用户体验。且高频的数据库状态写入会导致底层数据库 IO 打满。

### **6.2 解决方案：多级探针与实时流推送**

* **数据平面隔离**: 执行探针日志（如“正在下载图片 50%”）**严禁写入 TiDB**。  
* **Pub/Sub 机制**: TS Worker SDK 提供轻量级打点 API（如 ctx.emitLog(...)）。日志直接序列化后 PUBLISH 到 Redis 指定 Session 的 Channel。  
* **SSE 桥接**: Go 网关监听 Redis Channel，将接收到的微观进度、节点流转事件、以及 LLM 的 Stream Token 透传至前端，形成类似打字机加动态时间轴的酷炫视觉效果。