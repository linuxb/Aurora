# Phase 0 Progress

## 时间范围
- `started_at`: `2026-04-18T16:00:00+08:00` (estimated)
- `completed_at`: `2026-04-18T18:30:00+08:00`

## 目标
- 搭建可运行的最小多语言框架：`arqo` + `worker-ts` + `polaris`
- 完成核心状态流转与最小可测单元
- 完成本地开发工具链与 IDE 调试配置

## 已完成内容
- Go `arqo`:
  - session 创建、task pull、task complete、healthz
  - DAG/task 状态机基础实现
  - sweeper 过期任务回收（最小实现）
- TS `worker-ts`:
  - demo skills
  - dual-track skill response (`raw_data` + `summary`)
  - semantic error model (`AuroraSkillError`)
- Rust `polaris`:
  - `GET /healthz`
  - `POST /ingest`
  - `GET /memory`
- 工程化:
  - `Makefile`, `docker-compose.yml`, `.env.example`
  - VSCode launch/tasks/settings
  - 基础 lint/format 配置

## 验证结果
- `make test` passed
- `npx tsc -p tsconfig.json --noEmit` passed
- E2E smoke flow passed: session created -> 3 tasks success -> DAG `SUCCESS`

## Phase 0 决策点与回复

### `2026-04-18T18:17:00+08:00` | 模型路线（Intent Slotting）
- `question`: 首期直接云端 LLM，还是本地轻量模型（Llama 8B）？
- `reply`: 先尝试本地轻量模型（如 Llama 8B）；若本地环境暂不具备，先 mock 模型调用数据，但要完成模型调用流程与结果处理逻辑。
- `decision_status`: decided

### `2026-04-18T18:17:00+08:00` | 图数据库首期选型
- `question`: 开发阶段优先 Memgraph，还是提前对齐 NebulaGraph？
- `reply`: 选择 Memgraph。
- `decision_status`: decided

### `2026-04-18T18:17:00+08:00` | Replanning 范围
- `question`: 首版只做失败补偿节点，还是支持子图替换 + 回滚？
- `reply`: 需要支持子图替换与回滚（可考虑补偿方案）。
- `decision_status`: decided

### `2026-04-18T18:17:00+08:00` | 短期记忆压缩阈值策略
- `question`: 固定阈值还是动态阈值？
- `reply`: 先固定阈值滚动压缩，后期过渡到动态阈值。
- `decision_status`: decided

## 追溯链接
- Project plan: `doc/progress/Phase-Plan.md`
- Decision log: `doc/progress/Decision-Log.md`
- Next phase progress: `doc/progress/Phase-1-Progress.md`
