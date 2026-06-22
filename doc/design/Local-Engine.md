# **Aurora 本地化与沙盒适配架构设计白皮书**

随着 Local AI 生态的崛起与隐私计算需求的爆发，Aurora 系统不仅需要支撑云端亿级高并发，也需要能够平滑降维，作为 Local-First Agent 运行在用户的 Unix/macOS 本机环境中。

本文档详细说明了 Aurora 的跨环境基础设施适配层（Infra Adapter Layer）设计，以及基于 Zig 语言构建的本地 TypeScript 极速安全沙盒（Sandbox Service）架构。

## **1\. 基础设施适配器层设计 (Infra Adapter Layer)**

为了实现“一套业务代码，两栖平滑运行”，Aurora 在底层的 Arqo（调度大脑）和 Mem3（记忆引擎）中引入了标准的 Adapter 模式。通过依赖注入（Dependency Injection），在启动时根据环境标量（ENV=cloud 或 ENV=local）加载不同的存储驱动。

### **1.1 核心接口定义 (Core Interfaces)**

在 Go 语言的调度网关中，剥离具体数据库实现，抽象出三大核心数据平面接口：
```golang
// 1\. 状态流转引擎接口 (State Engine)  
type ITaskStateStore interface {  
    FetchReadyTasks(batchSize int) (\[\]Task, error) // 对应 SKIP LOCKED 逻辑  
    UpdateTaskStatus(taskID, status string) error  
    AtomicDecrementDependency(taskID string) (int, error) // 对应依赖减法  
    BeginTx() (Transaction, error) // JIT 扩图所需的事务  
}

// 2\. 短期上下文存储接口 (Context/Memory Store)  
type IContextStore interface {  
    PutContext(taskID string, data \[\]byte) error  
    GetContext(taskID string) (\[\]byte, error)  
}

// 3\. 图谱记忆引擎接口 (Graph Engine)  
type IGraphStore interface {  
    MergeTriplets(triplets \[\]Triplet) error  
    SearchSubGraph(query string, userID string) (GraphData, error)  
}
```

### **1.2 云端与本地驱动映射矩阵**

| 接口职责                       | Cloud-Native 驱动 (云端高并发) | Local-First 驱动 (本机跨平台)               | 本地化设计考量与 OpenClaw 思想                                                                                                                                                                                                        |
| :----------------------------- | :----------------------------- | :------------------------------------------ | :------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| **状态控制** (ITaskStateStore) | **TiDB** (分布式 SQL)          | **SQLite** (嵌入式 SQL)                     | SQLite 天生支持跨平台，且支持单机极速的 ACID 事务。利用 SQLite 的 BEGIN EXCLUSIVE 或 WAL 模式，在本地完美模拟 Arqo 并发调度的状态流转，且无需用户安装任何数据库进程。                                                                 |
| **生肉上下文** (IContextStore) | **Apache KvRocks**             | **Local File System (Markdown) / BadgerDB** | 参考 OpenClaw，本地模式下将任务 raw\_data 序列化为标准的 **Markdown 文件** (如 workspace/memory/task\_123.md) 存入本地文件系统。优势：**人类极度可读**，用户可以直接用文本编辑器查看 Agent 的原始抓取记录和思考过程，增强系统透明度。 |
| **图谱记忆** (IGraphStore)     | **NebulaGraph / Neo4j**        | **KùzuDB / DuckDB**                         | 抛弃沉重的图数据库服务进程，引入 KùzuDB（专门为嵌入式图计算设计的 C++ 库，提供 Go 绑定）。它运行在单进程内，直接读写本地磁盘文件，提供极速的跨会话时序图游走查询。                                                                    |

### **1.3 跨平台兼容性保障 (Unix & macOS)**

* **纯 Go 编译**: Arqo 调度器本身使用 Go 编写，通过交叉编译可直接生成 macOS (Apple Silicon / Intel) 和 Linux 系统的单一可执行文件（Static Binary）。  
* **CGO 限制**: 为了保证用户分发的极简性，对于 SQLite 和 KùzuDB，优先采用 Pure Go 实现的版本（如 modernc.org/sqlite），彻底告别 CGO 带来的跨环境编译依赖噩梦。

## **2\. Zig 驱动的 TS 安全沙盒设计 (Aegis Sandbox)**

### **2.1 本地化 Skill 运行面临的致命挑战**

在云端，TS Skill 跑在一次性 Docker 容器中。但在本地环境：

1. **环境依赖深**: 不能假设用户的 macOS 上装了 Node.js 或 Bun。  
2. **安全风险极高**: Agent 自动下载的恶意的 TS Skill 一旦在宿主机执行，可以直接执行 fs.rmdirSync('/') 摧毁系统。  
3. **冷启动敏感**: 本地硬件资源有限，不能频繁启动重型虚拟机。

### **2.2 解决方案：引入 Zig 构建 Aegis Embedded Engine**

利用 Zig 极强的 C 互操作性、无隐藏控制流以及微秒级的极速启动特性，我们使用 Zig 编写一个名为 **Aegis** 的独立沙盒守护进程，内嵌 **QuickJS** 引擎。

#### **2.2.1 架构工作流**

1. **下发**: Arqo (Go) 将预编译好的纯 JS/TS 代码字符串和执行参数，通过 Unix Domain Socket (UDS) 或标准输入 (STDIN) 发送给 Aegis (Zig) 进程。  
2. **初始化**: Zig 在内存中瞬间O(1)级耗时，\< 1ms）拉起一个新的 QuickJS 运行时实例 (JSRuntime & JSContext)。  
3. **特权注入**: Zig 按照 Arqo 声明的权限标量，向 JS 环境中按需注入原生绑定函数（Native Bindings）。  
   * *例如：如果该 Skill 只被允许访问天气 API，Zig 只会向 JS 暴露一个受限的 fetch 函数，屏蔽掉一切涉及文件系统 fs 的 API。*  
4. **执行与监控**: Zig 触发 JS 代码执行，并开启硬件级中断监控。  
5. **返回**: 执行完毕后，返回符合双轨制规范的 raw\_data 和 summary。

#### **2.2.2 Zig 引擎的核心安全与隔离机制**

* **内存硬约束 (Memory Quota Allocation)**  
  QuickJS 支持自定义内存分配器。我们利用 Zig 优秀的自定义 Allocator（如 FixedBufferAllocator），强制给这个 JS 实例分配例如 128MB 的内存上限。  
  如果 TS 代码写了死循环导致内存溢出，Zig 的 Allocator 会立刻拦截并抛出 OutOfMemory 错误，终止 JS 执行，**宿主机操作系统绝对安全**。  
* **时钟与指令计数熔断 (Instruction Limit)**  
  在 Zig 层设置 QuickJS 的 JS\_SetInterruptHandler。每当 JS 引擎执行了一定数量的字节码指令，Zig 函数就会被回调。如果发现超时（如执行超过 10 秒），Zig 直接返回 \-1 硬中断脚本执行，防止 CPU 算力被榨干。  
* **零系统调用泄漏 (Zero Syscall Leak)**  
  在 macOS (通过 sandbox-exec) 和 Linux (通过 seccomp-bpf) 下，Zig Launcher 自身启动时就锁死自己的系统调用权限。即使 QuickJS 引擎存在 C 语言级别的 0day 漏洞被击穿，黑客也无法逃逸出 Zig 预设的操作系统级监狱。

### **2.3 Arqo 与 Aegis 的通信契约 (Go \<-\> Zig)**

为了保证高效和跨平台，Arqo 调度器与 Aegis 沙盒之间通过标准 I/O 及 JSON-RPC 进行通信。

```json
// Arqo (Go) 发送给 Aegis (Zig) 的执行指令  
{  
  "task\_id": "task\_local\_001",  
  "skill\_name": "ReadLocalConfig",  
  "source\_code": "export default async function run(ctx) { return { summary: 'Done', raw\_data: ctx.env.CONFIG }; }",  
  "limits": {  
    "memory\_mb": 64,  
    "timeout\_ms": 5000  
  },  
  "permissions": {  
    "network": false,  
    "fs\_read\_paths": \["/Users/Aurora/workspace/config.json"\]  
  }  
}
```

通过这套适配架构，Aurora 不仅保持了 TS 开发者生态的繁荣，更在没有任何外部容器依赖的情况下，在本地电脑上构建起了一座坚不可摧的“单文件”堡垒引擎。
