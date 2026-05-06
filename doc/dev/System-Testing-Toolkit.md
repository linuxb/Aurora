# System Testing Toolkit (Ruby first, Jepsen-ready later)

## Why now
- Current project phase already includes MySQL/TiDB scheduler semantics, Redis eventing, and JIT expansion.
- This is the right time to add lightweight system-level regression checks for smoke and failure semantics.
- Full Jepsen-style consistency testing is valuable later, but too heavy as the first step for current iteration speed.

## Recommendation
- Introduce Ruby tools now for:
  - Smoke E2E checks
  - Abnormal/fault-path checks
  - Local and CI-friendly quick diagnostics
- Defer Clojure/Jepsen until:
  - multi-worker concurrency against persistent scheduler is stable
  - TiDB compatibility checklist is completed
  - we need formal linearizability/consistency claims under partitions and process crashes

## Tools added
- `tools/testing/arqo_smoke.rb`
  - Creates a session and waits until DAG reaches terminal state.
  - Exits non-zero if DAG becomes `FAILED/REPLANNING` or timeout.
- `tools/testing/arqo_fault_injector.rb`
  - Scenario 1: force task failure and verify DAG moves to `REPLANNING`.
  - Scenario 2: simulate owner conflict and verify API returns `409`.
- `tools/testing/arqo_missing_skill_regression.rb`
  - Scenario: consecutive `mapping_status=unmapped` expansion should be blocked.
  - Verify API returns `422` with `code=missing_skill`.
  - Verify DAG state transitions to `REPLANNING`.
- `tools/testing/arqo_regression_suite.rb`
  - Serial suite entrypoint: `smoke -> fault -> missing_skill`.
  - Auto-starts/stops `worker-ts` only for smoke phase to avoid lease ownership races in fault/missing-skill scenarios.

## Run
```bash
make test-smoke-ruby
make test-fault-ruby
ruby tools/testing/arqo_missing_skill_regression.rb
make test-regression-ruby
```

Optional env:
- `ARQO_URL` (default `http://127.0.0.1:8080`)
- `SMOKE_TIMEOUT_SECONDS` (default `30`)
- `SMOKE_POLL_INTERVAL_SECONDS` (default `0.5`)

## Next suggested expansions
- Add a chaos loop runner in Ruby:
  - random pull/complete delay
  - random task failure injection rate
  - summary report for replan rate and terminal distribution
- Add a Jepsen prep checklist doc:
  - invariants
  - nemesis matrix
  - consistency expectations by API
