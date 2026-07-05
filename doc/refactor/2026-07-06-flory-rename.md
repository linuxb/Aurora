# Flory Service Rename Refactor

## Scope

- Rename the Go agent orchestration scheduler service from `arqo` to `flory`.
- Move the Go service module from `apps/arqo` to `apps/flory`.
- Rename runtime environment variables from `ARQO_*` to `FLORY_*`.
- Rename Make targets, Docker Compose service names, Ruby testing scripts, editor launch tasks, and documentation references.

## Compatibility Notes

- The public HTTP API shape is unchanged.
- The default service port remains `8080`.
- The worker now reads `FLORY_URL` instead of `ARQO_URL`.
- Mem3 integration now uses `FLORY_MEM3_URL`, `FLORY_MEM3_TIMEOUT_MS`, and `FLORY_MEMORY_FALLBACK_STRICT`.

## Verification

- `make test`
- `docker compose -f docker-compose.yml config --quiet`
- `git diff --check`
- Ruby syntax checks for the renamed testing scripts.
