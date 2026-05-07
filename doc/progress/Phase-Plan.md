# Aurora 分阶段研发计划（可运行、可测试）

## 目标说明
该计划基于现有设计文档，采用“每阶段都可演示 + 可测试 + 可回归”的推进方式。每个阶段都必须输出一个可被验证的里程碑。

## 决策追溯规则
- 所有待决策点与已决策结果统一记录到 `doc/progress/Decision-Log.md`。
- 每条记录必须包含：`recorded_at`（RFC3339 含时区）、`phase`、`topic`、`status`、`decision`、`owner`。
- 当决策被修改时，不覆盖原记录；新增一条 `status=superseded` 或新版本记录，并指向被替代项。
- 每个 phase 结束时，补一条“phase closure”记录，包含未决项和风险说明，方便复盘。

## Deferred Hardening 固定节奏
- Deferred hardening 项不阻塞主线 phase 研发推进，但必须固定回归。
- 固定回归规则见：`doc/progress/Hardening-Cadence.md`。
- 每次 phase closure 需要明确记录：哪些 hardening 已完成，哪些延期，延期风险是否可接受。

## Phase 文档分工
- `doc/progress/Phase-Plan.md`: 仅维护全局阶段目标、验收标准与整体节奏。
- `doc/progress/Phase-0-Progress.md`, `doc/progress/Phase-1-Progress.md`, ...: 维护阶段执行进度、阶段内待决策点、讨论结论。
- `doc/progress/Decision-Log.md`: 维护跨阶段的统一决策追溯索引。

## Phase 0: MVP 框架落地（已完成）

### 研发目标
- 搭建可运行的三语言最小框架：
  - `arqo`（Go）: 网关与 DAG 状态机核心
  - `worker-ts`（TS）: Skill 执行与语义化错误
  - `polaris`（Rust）: 记忆控制器最小服务
- 落地最小协议：
  - Task 状态流转：`PENDING -> READY -> RUNNING -> SUCCESS/FAILED`
  - Skill 双轨返回：`raw_data` + `summary`
  - 失败触发 DAG `REPLANNING`
- 提供本地开发与调试配置（VSCode / Makefile / Docker Compose）

### 可用功能
- 通过 `POST /v1/sessions` 创建 session 并自动生成 demo DAG
- `worker-ts` 自动 pull/execute/complete Task
- `polaris` 提供 `healthz` 与 `ingest/memory` 最小接口

### 可测试性
- `go test ./...`：校验核心 DAG 流转与失败重规划状态变迁
- `cargo test`：校验 polaris ingest 解析
- 手工联调：curl 建 session，观察任务从 READY 到 SUCCESS

### 验收标准
- 本地 M2 环境可在 10 分钟内跑起 demo
- DAG 能完成完整 happy-path
- 失败任务能把 DAG 置为 `REPLANNING`

## Phase 1: 核心调度落库 + 基础事件流（进行中）

### 研发目标
- 把 `arqo` 的 in-memory store 替换为 MySQL/TiDB 存储
- 引入 `SKIP LOCKED` 抢占和原子依赖计数更新
- 引入 Redis Pub/Sub，打通执行日志实时事件流

### 可用功能
- 多 worker 并发抢占无重复执行
- 下游节点依赖归零后自动 READY
- 前端或 CLI 可订阅任务执行事件

### 可测试性
- 并发测试：N workers 并发下无重复领取同一 task
- 数据一致性测试：依赖计数无负数，无丢唤醒
- 回归脚本：100 次 DAG 批量执行通过率

### 验收标准
- 关键接口 P95 延迟与吞吐达到预期（需定义目标值）
- 并发场景下无死锁/重复消费

## Phase 2: Intent Router + DAG Validator

### 研发目标
- 实现“意图插槽提取 -> DAG 受限生成 -> 静态校验”流水线
- 实现 DAG 编译器校验：
  - cycle detection
  - dangling dependency
  - isolated node warning

### 可用功能
- 任意用户自然语言请求可生成合法 DAG 或明确失败原因
- 校验失败可触发自动修正重试（有限次数）

### 可测试性
- 模型输出 mock 测试：覆盖合法图/非法图
- 属性测试：随机图校验器健壮性
- E2E：意图输入到 DAG 入库全链路

### 验收标准
- DAG 校验错误可解释且可重试修复
- 全链路失败可观测可追踪

## Phase 3: Replanning 与故障自愈

### 研发目标
- 实现 Sweeper/Reaper 租约过期回收
- 接入结构化 PatchDAG 重规划
- 支持事务级局部热修复

### 可用功能
- Worker 崩溃后可自动识别僵尸任务
- 失败 DAG 可插入新节点并继续执行

### 可测试性
- 故障注入：kill worker / timeout / network fail
- 事务回滚测试：PatchDAG 校验失败不污染原图

### 验收标准
- 自愈路径可稳定恢复核心业务流程
- 重规划次数和成功率有观测指标

## Phase 4: Memory 与 GraphRAG

### 研发目标
- `polaris` 从最小服务升级为异步 memory pipeline
- 接入 KV（raw_data）和 GraphDB（summary/实体关系）
- 提供内部 `SearchMemoryGraph` 安全查询接口

### 可用功能
- 长短记忆分离
- 跨任务检索记忆能力上线
- 图查询强制 user_id 隔离

### 可测试性
- 多租户隔离测试（防串读）
- memory 抽取质量测试（抽样人工评估 + 自动评估）

### 验收标准
- 跨 session 记忆召回可用
- 无跨租户数据泄漏

## Phase 4.5: Local-Engine 最小原型（在 Polaris 完成后立即启动）

### 研发目标
- 在保持 cloud 主干推进的前提下，提前启动本地化最小原型（Local-First）。
- 基于现有 `arqo + polaris` 能力，验证本地单机可运行闭环。
- 固化本地化关键接口与执行协议，降低后续 Phase 6 风险。

### 可用功能
- `ARQO_RUNTIME_MODE=local` 的最小运行模式。
- 本地单机 session 执行闭环（创建、调度、执行、结果回读）。
- Local 沙盒执行面 MVP 协议打通（先最小可用，不追求最终安全形态）。

### 可测试性
- 本机 E2E：创建 session -> DAG 执行 -> 结果可查询。
- 资源限制测试：超时/内存上限策略生效。
- 回归验证：不影响 cloud 模式原有路径。

### 验收标准
- 在 macOS/Linux 本机，无外部重型依赖可运行最小 demo。
- local/cloud 两种模式切换不改变上层业务语义。

## Phase 5: 工程化与上线准备

### 研发目标
- 完善 CI/CD、质量门禁、压测、观测、告警
- 引入灰度与回滚策略
- 形成运维手册与 SLO

### 可用功能
- PR 自动测试 + lint + 安全扫描
- 线上可观测（日志、指标、链路）

### 可测试性
- 压测（峰值吞吐、尾延迟）
- 混沌演练（组件级故障注入）

### 验收标准
- 具备小流量灰度发布条件
- 满足核心 SLO


## Phase 6: Local-Engine（生产级本地化能力）

### 研发目标
- 在 Phase 4.5 最小原型基础上，升级为生产级本地化能力。
- 完整落地 Infra Adapter Layer，保证调度/上下文/图谱接口可插拔。
- 落地 Aegis 安全加固与长期运行稳定性保障。

### 可用功能
- 本机单机可运行完整 session 生命周期。
- 本地 Skill 通过受限沙盒执行，具备超时/内存/权限控制。
- 本地上下文与执行轨迹可查询回放。

### 可测试性
- 本地 E2E：创建 session -> 执行 DAG -> 结果回读。
- 安全测试：越权文件访问、死循环、内存膨胀被拦截。
- 稳定性测试：48h soak 与任务恢复验证。

### 验收标准
- 无需额外数据库服务即可在 macOS/Linux 本机运行（最小依赖）。
- 沙盒策略默认拒绝越权能力，失败可解释。
- 不影响 cloud 模式既有语义与接口兼容性。

## 阶段追溯入口
- Phase 0 progress: `doc/progress/Phase-0-Progress.md`
- Phase 1 progress: `doc/progress/Phase-1-Progress.md`
- Decision index: `doc/progress/Decision-Log.md`
- Hardening cadence: `doc/progress/Hardening-Cadence.md`
