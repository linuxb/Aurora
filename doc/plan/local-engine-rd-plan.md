# Local Engine 架构评审与研发计划（2026-05-07）

## 评审结论
`doc/design/Local-Engine.md` 的主干方向正确，且与 Aurora 的既有设计哲学一致：
- 通过 Adapter Layer 做云端/本地双态运行，保持上层业务逻辑与执行流统一。
- 本地化采用嵌入式组件（SQLite / 文件或嵌入式 KV / 嵌入式图存储）降低部署门槛。
- 将本地 Skill 执行隔离提升为独立沙盒进程（Aegis）是合理的安全边界。

结论：**方案可推进**，建议以“先最小可用，再安全加固”的节奏实施。

## 与系统目标一致的部分（确认项）
1. 控制流/数据流接口化分离，与 Aurora 既有接口抽象一致。
2. 本地优先部署目标明确，符合“可云可本地”的产品定位。
3. 沙盒执行面独立进程化，符合最小权限和故障隔离原则。
4. 通过 JSON-RPC 协议解耦调度器与执行引擎，便于多语言实现替换。

## 需要评估并拍板的关键不确定点（建议讨论）
1. SQLite 并发语义与 `SKIP LOCKED` 的等价策略
- 问题：SQLite 不支持 MySQL/TiDB 的 `FOR UPDATE SKIP LOCKED` 语义。
- 建议：本地模式采用“单写者调度循环 + lease 字段 CAS 更新 + WAL”方案，不追求多进程抢占一致性。

2. 本地 raw_data 存储选型（Markdown vs BadgerDB）
- 问题：Markdown 可读性高，但大体积/高频写入下性能与碎片管理较差。
- 建议：双层方案：默认 `Markdown + manifest`，超过阈值自动落 BadgerDB，并保留可读摘要索引。

3. macOS 沙箱机制选择
- 问题：`sandbox-exec` 在新版本 macOS 上长期稳定性与可维护性存疑。
- 建议：将 OS 级隔离定义为“可插拔策略”，首版以进程级限制 + 权限白名单 + 资源配额为主，OS 沙箱作为增强项。

4. TS 运行链路边界
- 问题：QuickJS 不原生支持 TS，需要明确“编译责任归属”。
- 建议：协议层明确 `source_code` 为 JS；TS -> JS 编译在 arqo 或打包器侧完成，Aegis 仅执行 JS。

5. 本地记忆图引擎选型
- 问题：KùzuDB 生态与绑定稳定性需验证，DuckDB 图查询能力需额外建模。
- 建议：先抽象 `IGraphStore`，Phase A 用 DuckDB + 边表验证流程，Phase B 再切 Kùzu 进行性能对比。

## 研发计划（建议）

### Phase LE-0：接口固化与本地模式骨架（1-2 周）
- 目标：把 Local-Engine 变成可编排的运行模式，不改主业务语义。
- 交付：
  - 增加运行模式开关：`ARQO_RUNTIME_MODE=cloud|local`。
  - 固化三大接口：`ITaskStateStore`、`IContextStore`、`IGraphStore`。
  - 本地调度最小实现：SQLite + 单写者调度循环。
- 验收：在 macOS 本机 10 分钟内完成一次 session 全链路。

### Phase LE-1：Aegis MVP（2-3 周）
- 目标：可执行 JS skill，具备基本资源限制。
- 交付：
  - Aegis 进程（Zig + QuickJS）最小可运行。
  - JSON-RPC 通信打通（stdio/uds 二选一先落地）。
  - 限流能力：`timeout_ms`、`memory_mb`、permission 白名单。
- 验收：恶意脚本（死循环/内存膨胀/越权 fs）可被阻断。

### Phase LE-2：本地记忆与观测（1-2 周）
- 目标：补齐最小本地 memory 回路与可观测性。
- 交付：
  - raw_data 本地存储策略（Markdown + 可选 Badger）
  - summary 索引与查询 API
  - 本地 telemetry 与执行日志回放
- 验收：跨 session 能检索近历史上下文，调试路径清晰。

### Phase LE-3：安全与稳定性加固（持续）
- 目标：把本地模式从“可用”提升到“可长期运行”。
- 交付：
  - 权限策略模板（network/fs 白名单）
  - 故障注入与恢复测试
  - 性能基线（启动时延、任务吞吐、内存上界）
- 验收：48h soak test 稳定，关键指标达标。

## 里程碑门禁
1. LE-0 完成前，不引入复杂图数据库依赖。
2. LE-1 完成前，不开放“执行任意远程下载 skill”的默认能力。
3. LE-2 完成前，本地模式对外标注为 Beta。

## 建议下一步
- 先按 LE-0 启动，并在开始前拍板以下三项：
  1) SQLite 调度并发模型（单写者 vs 多 worker）
  2) raw_data 默认落地格式（纯 Markdown vs 混合策略）
  3) Aegis 通信通道优先级（stdio vs uds）
