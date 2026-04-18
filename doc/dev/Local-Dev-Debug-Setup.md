# Local Dev Debug Setup (macOS + Docker dependencies)

This setup is optimized for local development on macOS:
- Run Aurora services (`arqo`, `worker-ts`, `polaris`) directly on host for debugger support.
- Run dependency services in Docker (`mysql`, `redis`, `kvrocks`, `memgraph`).

## Compose files
- `docker-compose.dev.yml`: dependency services only (recommended for daily development).
- `docker-compose.yml`: full stack (dependency services + Aurora services) for system-level integration runs.

## One-command dependency startup

```bash
cd /Users/linzhenbin/workspace/my_proj/aurora
make infra-up
```

Equivalent command:

```bash
docker compose -f docker-compose.dev.yml up -d
```

## Start Aurora services on host

Terminal 1:

```bash
cd /Users/linzhenbin/workspace/my_proj/aurora
make run-arqo
```

Terminal 2:

```bash
cd /Users/linzhenbin/workspace/my_proj/aurora
make run-worker
```

Terminal 3:

```bash
cd /Users/linzhenbin/workspace/my_proj/aurora
make run-polaris
```

## Shutdown

Stop dependency containers:

```bash
cd /Users/linzhenbin/workspace/my_proj/aurora
make infra-down
```

## Optional: full stack in Docker

```bash
cd /Users/linzhenbin/workspace/my_proj/aurora
make infra-up-full
```

Stop full stack:

```bash
make infra-down-full
```
