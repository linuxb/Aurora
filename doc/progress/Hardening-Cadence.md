# Deferred Hardening Cadence

## Purpose
- Keep roadmap velocity for core feature delivery.
- Run deferred hardening items on a fixed cadence to prevent risk accumulation.

## Scope (from Phase 1)
- Persistent-store concurrency regression checks.
- Real TiDB integration compatibility checks.

## Fixed Cadence
- Every Wednesday:
  - Run `make hardening-regression`.
  - Record result summary in phase progress document.
- At each phase closure:
  - Run `make hardening-regression`.
  - Run `make hardening-tidb` when TiDB environment is available.
  - Update unresolved risk section before declaring phase closure.
- Before release milestone:
  - Hardening items must be fully executed and signed off.

## Execution Commands
```bash
make hardening-regression
make hardening-tidb
```

## Reporting Template
- `recorded_at`:
- `phase`:
- `command`:
- `result`: `pass` | `fail` | `skipped`
- `notes`:
- `owner`:
