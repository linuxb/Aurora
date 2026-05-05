# Intent Router & JIT 扩图架构审计（2026-05-05）

## 审计目标
围绕最新架构目标审计并修正：
- DAG Node 必须显式区分两类：
  - `SKILL_SINK`（直接映射具体 Skill）
  - `EXPANDING`（JIT 扩图 Step）
- `SUCCESS_AND_EXPAND` 只能由 `EXPANDING` 节点触发。

## 审计结论
在修正前，代码存在“类型语义隐式化”的偏差：系统主要通过 `skill_name=ReActPlanner` 约定来推断扩图节点，缺少一等类型约束。

本次已完成修正并通过测试，当前实现已与目标架构一致：
1. Node/Step 显式类型化。
2. 规划校验器强校验类型与技能映射关系。
3. 调度执行面强约束扩图权限。
4. MySQL/Memory 两个 backend 行为一致。

## 已修正项
- `planner.Node` 新增 `NodeType`；支持常量：`SKILL_SINK`、`EXPANDING`。
- `planner.ValidateDAG` 新增规则：
  - `node_type` 必填且必须为上述两类之一。
  - `EXPANDING` 节点必须使用 `ReActPlanner`。
  - `ReActPlanner` 不允许标注为 `SKILL_SINK`。
- `SessionTaskSpec`、`model.Task` 新增 `NodeType`，实现类型语义端到端传递。
- 扩图权限收敛：
  - `CompleteTask` 收到 `ExpansionPayload` 时，若当前任务不是 `EXPANDING`，返回 `ErrExpansionNotAllowed`。
- MySQL 持久化模型升级：
  - `tasks` 表新增 `node_type`（并在 schema ensure 中兼容补列）。
  - 所有任务查询/扫描/插入路径补齐 `node_type` 字段。
- Mock Router 输出改为显式 `NodeType`。

## 风险与后续建议
- 目前 `NodeType` 以字符串常量实现，可进一步抽象为共享枚举类型包，减少跨模块硬编码。
- `ExpansionPayload.NewNodes` 当前默认落成 `SKILL_SINK`；若未来要支持“扩图产生新的 EXPANDING 节点”，需扩展 payload schema（加入 node_type）并更新校验。

## 验证
- 执行：`cd apps/arqo && GOCACHE=/Users/linzhenbin/workspace/my_proj/aurora/.cache/go-build go test ./...`
- 结果：`internal/api`、`internal/planner`、`internal/scheduler` 全部通过。
