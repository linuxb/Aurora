# Intent Router & JIT Graph Expansion Architecture Audit (2026-05-05)

## Audit Goal

Audit and correct the latest architecture goals:

- DAG Nodes must explicitly distinguish two categories:
  - `skill`: directly maps to a concrete Skill.
  - `planner`: a JIT graph-expansion Step.
- `SUCCESS_AND_EXPAND` can only be triggered by `planner` nodes.

## Audit Conclusion

Before the fix, the code had implicit type semantics. The system mainly inferred expansion nodes through the convention `skill_name=ReActPlanner` and lacked first-class type constraints.

The fix has been completed and tests passed. The implementation now matches the target architecture:

1. Node/Step types are explicit.
2. The planner validator strongly checks type and Skill mapping relations.
3. The scheduling execution plane strictly controls graph-expansion permissions.
4. MySQL and Memory backends behave consistently.

## Fixed Items

- Added `NodeType` to `planner.Node`; supported constants are `skill` and `planner`.
- Added rules to `planner.ValidateDAG`:
  - `node_type` is required and must be one of the two allowed values.
  - `planner` nodes must use `ReActPlanner`.
  - `ReActPlanner` must not be labeled as `skill`.
- Added `NodeType` to `SessionTaskSpec` and `model.Task`, enabling end-to-end type propagation.
- Narrowed expansion permission:
  - When `CompleteTask` receives an `ExpansionPayload`, it returns `ErrExpansionNotAllowed` if the current Task is not a `planner`.
- Upgraded MySQL persistence model:
  - Added `node_type` to the `tasks` table and made schema ensure compatible with column addition.
  - Filled `node_type` in all task query, scan, and insert paths.
- Mock Router output now uses explicit `NodeType`.

## Risks and Follow-Up Suggestions

- `NodeType` is currently implemented as string constants. It could be further abstracted into a shared enum package to reduce cross-module hardcoding.
- `ExpansionPayload.NewNodes` currently defaults to `skill`. If future expansion needs to create new `planner` nodes, extend the payload schema with `node_type` and update validation.

## Verification

- Command: `cd apps/arqo && GOCACHE=/Users/linzhenbin/workspace/my_proj/aurora/.cache/go-build go test ./...`
- Result: `internal/api`, `internal/planner`, and `internal/scheduler` all passed.
