# Intent Router / JIT 扩图递归护栏审计（2026-05-06）

## 目标
对齐本轮约束目标：
- DAG 生成阶段仅允许两类节点：`skill` 与 `planner`。
- 调度阶段禁止无限递归 JIT 扩图：当连续扩展 N 次后仍无法映射到可执行 skill，需向 UI 返回“缺少特定 skill”。

## 结论摘要
- 第一条（两类节点约束）当前已基本落地：`planner.ValidateDAG` 与调度执行面均有硬约束。
- 第二条（连续扩展 N 次未落地时上抛缺 skill）当前未落地：现有仅有 `max_depth` 深度护栏，语义上无法表达“连续未映射到 skill”的业务异常。

## 代码面核查

### 已满足
1. NodeType 枚举化并收敛到 `skill` / `planner`。
2. `SUCCESS_AND_EXPAND` 仅允许在 `planner` 节点触发。
3. MySQL 与内存 Store 在“仅扩图节点可扩图”上行为一致。

### 未满足（关键差距）
1. 缺少“缺 skill”领域错误类型。
   - 当前扩图错误仅有 `ErrExpansionInvalid`、`ErrExpansionDepthExceeded` 等基础错误。
2. 缺少“连续未映射计数器”（建议按 DAG 或按 planner 链路追踪）。
   - 当前 `current_depth/max_depth` 是全局深度上限，不等价于“连续无法映射 skill”。
3. API 返回未区分“深度上限”与“缺 skill”。
   - 当前统一映射为 `task_completion_failed`，HTTP 409，UI 无法精确提示“需要新增 skill”。

## 建议改造（最小可行）
1. 新增领域错误：
   - `ErrSkillMappingExhausted`（建议 message: `skill mapping exhausted, missing required skill`）。
2. 在 DAG 维度新增连续计数：
   - `jit_unmapped_streak`（连续未映射计数）。
   - 成功映射到业务 skill 时清零；再次触发“仅继续规划”时 +1。
3. 扩图 payload 增补结果语义字段（避免仅靠 node 数量推断）：
   - 例如 `mapping_status: mapped|unmapped`。
4. 达到阈值 N 时：
   - planner task 置 `FAILED`，`last_error_code=MISSING_SKILL`；
   - DAG 置 `REPLANNING`（或按产品策略直接终态）；
   - `CompleteTask` 返回 `ErrSkillMappingExhausted`。
5. API 层单独映射：
   - HTTP `422`（推荐）或 `409`；
   - 业务 code 建议 `missing_skill`；
   - payload 带 `required_skill_hint`（若可推断）。

## 验收测试建议
1. `planner` 连续 3 次 `mapping_status=unmapped`，第 3 次触发 `ErrSkillMappingExhausted`。
2. 第 2 次 `unmapped` 后第 3 次 `mapped`，计数清零，后续不应误触发。
3. MySQL/Memory 两套 backend 行为一致。
4. API 返回包含 `code=missing_skill`，UI 可直接消费。

## 备注
本报告仅覆盖 Intent Router + JIT recursion guard 语义，不含预算系统、GraphRAG 检索策略与 PatchDAG 修复策略审计。
