# Mem3 Memory Lifecycle Design Review

Date: 2026-06-23

## Review Scope

- DAG 构建时的 intent slot 提取与长期记忆写入。
- Task 执行前的工作记忆与定向记忆检索。
- Task 完成后的 output 写入与 rolling summary reduce。
- Mem3 Ingest、List、Search 与 `mem_hint` JSON Schema。

## Review Result

原设计只部分满足目标，存在以下偏差：

1. DAG intent slot 没有完整的 Ingest Schema，也没有明确 Tenant/Agent 长期记忆作用域。
2. Search 仅被描述为 DAG/JIT 规划阶段调用，而不是每个 Task 执行前的强制步骤。
3. List 与 Search 没有保证同时返回 last-N outputs 和最新 rolling summary。
4. 文档曾规定 Planner Node 不写 Mem3，与“每个 Task 完成后写入并 reduce”冲突。
5. Skill 自带 summary 被当作工作记忆摘要，与统一公式 `summary = LLM(output, last_summary)` 冲突。
6. `mem_hint` 在部分 Spec 中被定义为字符串，且旧 GraphRAG Schema 与统一 Schema 不一致。
7. 并行 Task 异步 reduce 没有确定提交顺序，可能造成 rolling summary 覆盖或乱序。
8. LLM 生成的检索提示携带安全 scope 会产生跨 Tenant 越权风险。

## Corrected Design

- 每次 DAG 构建调用 `Ingest(kind=DAG_CONTEXT)`，异步提取 Goal、Profile、Facts、Relations。
- 每个 Task 开始前调用 Search；Search 无条件装配 last-N outputs 与最新 committed rolling summary。
- 父 Task 完成后，使用父 output 与子 goal 生成或刷新子 Task 最终 `mem_hint`。
- 每个 Skill/Planner Task 完成后调用 `Ingest(kind=TASK_OUTPUT)`。
- Mem3 按 DAG `sequence` 串行执行：

```text
new_summary = lightweight_llm(current_output, previous_committed_summary)
```

- LLM 生成的 `mem_hint` 不携带安全边界；`tenant_id/agent_id/session_id/dag_id` 由 Arqo 可信注入。
- Ingest 使用幂等键并快速返回 `202 Accepted`；原始 output、摘要 reduce 和 Graph 写入解耦。

## Updated Documents

- `doc/design/Mem3.md`
- `doc/design/Intent-Router.md`
- `doc/design/Arqo-JIT.md`
- `doc/design/Aurora-Architechure.md`
- `doc/design/Plato-GraphRAG.md`
- `doc/spec/Mem3-API-Spec.md`
- `doc/spec/Mem-Hint-Schema.md`
- `doc/spec/System-Spec.md`

为降低实现歧义，`doc/design/Mem3.md` 已增加端到端记忆生命周期，以及多父 Task/并行 reduce 两张 Mermaid 时序图；`doc/design/Intent-Router.md` 增加 Query 到 DAG 持久化时序图。

## Conclusion

修订后的设计满足目标生命周期。实现阶段仍需重点验证：并行 Task 的 sequence 分配、summary reduce 串行提交、Ingest 幂等，以及 Search 的 Tenant 隔离。
