# Intent Router / JIT Graph Expansion Recursion Guard Audit (2026-05-06)

## Goal

Align with the constraints for this round:

- DAG generation only allows two node types: `skill` and `planner`.
- Scheduling must prevent unbounded recursive JIT graph expansion. If N consecutive expansions still cannot map to an executable Skill, the system should return a "missing required Skill" message to the UI.

## Summary Conclusion

- The first requirement, two node types, is mostly implemented: `planner.ValidateDAG` and the scheduling execution plane both enforce hard constraints.
- The second requirement, surfacing missing Skill after N consecutive unmapped expansions, is not implemented yet. The current `max_depth` guard only limits depth and cannot express the business error "consecutive inability to map to a Skill".

## Code-Level Check

### Satisfied

1. NodeType is enumerated and narrowed to `skill` / `planner`.
2. `SUCCESS_AND_EXPAND` can only be triggered on `planner` nodes.
3. MySQL and Memory stores behave consistently for "only expansion nodes may expand".

### Not Satisfied (Key Gaps)

1. Missing domain error type for "missing Skill".
   - Current expansion errors only include basic errors such as `ErrExpansionInvalid` and `ErrExpansionDepthExceeded`.
2. Missing consecutive-unmapped counter, recommended at DAG level or planner-chain level.
   - Current `current_depth/max_depth` is a global depth cap and is not equivalent to "consecutive inability to map a Skill".
3. API responses do not distinguish "depth cap" from "missing Skill".
   - Current mapping is `task_completion_failed` with HTTP 409, so the UI cannot accurately tell the user that a new Skill is needed.

## Recommended Minimal Change

1. Add a domain error:
   - `ErrSkillMappingExhausted`, recommended message: `skill mapping exhausted, missing required skill`.
2. Add a DAG-level consecutive counter:
   - `jit_unmapped_streak`.
   - Reset it when a business Skill is mapped successfully; increment it when the planner only continues planning.
3. Add result semantics to the expansion payload to avoid inferring only from node count:
   - Example: `mapping_status: mapped|unmapped`.
4. When threshold N is reached:
   - Set the planner Task to `FAILED` with `last_error_code=MISSING_SKILL`.
   - Set the DAG to `REPLANNING` or a terminal state according to product policy.
   - Make `CompleteTask` return `ErrSkillMappingExhausted`.
5. Map at the API layer separately:
   - HTTP `422` is recommended, or `409` if the current API convention requires it.
   - Business code should be `missing_skill`.
   - Payload may include `required_skill_hint` if it can be inferred.

## Acceptance Test Suggestions

1. A `planner` emits `mapping_status=unmapped` for 3 consecutive attempts; the third attempt triggers `ErrSkillMappingExhausted`.
2. The second attempt is `unmapped`, then the third is `mapped`; the counter resets and must not falsely trigger.
3. MySQL and Memory backends behave consistently.
4. API response contains `code=missing_skill` and can be consumed directly by the UI.

## Notes

This report covers only Intent Router plus JIT recursion-guard semantics. It does not audit the budget system, GraphRAG retrieval strategy, or PatchDAG repair strategy.
