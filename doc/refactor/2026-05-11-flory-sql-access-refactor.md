# Flory SQL Access Refactor

## Date
- `2026-05-11`

## Scope
- Flory scheduler MySQL/TiDB-compatible persistence path.
- Unit tests covering MySQL store SQL behavior.

## Motivation
- Existing MySQL store code mixed scheduler transaction logic with repeated raw SQL strings.
- SQL text duplication made changes noisy, especially for common insert/update paths.
- sqlmock tests repeated column fixtures and tightly coupled each case to long SQL fragments.

## Changes
- Added a lightweight Squirrel query-builder layer while keeping `database/sql` transactions and MySQL-compatible protocol.
- Moved repeated session/DAG/task insert, ready-task lease, lease-expiry, task completion, raw-data upsert, and DAG status update SQL into shared builders.
- Kept DDL/schema bootstrap SQL as raw statements because it is migration-oriented and clearer as full SQL.
- Simplified MySQL store unit-test setup with shared sqlmock helpers so tests assert behavior without repeating column fixtures in every case.

## Verification
- `cd apps/flory && GOCACHE=/Users/linzhenbin/workspace/my_proj/aurora/.cache/go-build go test ./...`

## Follow-Up
- Continue migrating complex JIT expansion SQL in smaller steps.
- Keep tests behavior-focused; only add sqlmock cases when they cover a distinct transaction branch or error path.
