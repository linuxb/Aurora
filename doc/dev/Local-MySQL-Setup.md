# Local MySQL Setup (Docker Compose)

This guide helps you run MySQL locally on macOS and connect from host tools.

## 1) Start infra containers

```bash
cd /Users/linzhenbin/workspace/my_proj/aurora
docker compose -f docker-compose.dev.yml up -d mysql redis kvrocks memgraph
```

## 2) Check MySQL container health

```bash
docker compose -f docker-compose.dev.yml ps mysql
docker logs aurora-mysql --tail 50
```

## 3) Connect from macOS host

Connection details:
- host: `127.0.0.1`
- port: `3306`
- user: `aurora`
- password: `aurora`
- database: `aurora`

Optional CLI check:

```bash
mysql -h 127.0.0.1 -P 3306 -u aurora -paurora -D aurora -e "SHOW TABLES;"
```

If `mysql` CLI is missing on macOS:

```bash
brew install mysql-client
```

## 4) Run arqo with mysql scheduler backend

Enable mysql driver once:

```bash
cd /Users/linzhenbin/workspace/my_proj/aurora/apps/arqo
go get github.com/go-sql-driver/mysql@latest
```

```bash
cd /Users/linzhenbin/workspace/my_proj/aurora/apps/arqo
ARQO_SCHEDULER_BACKEND=mysql \
ARQO_MYSQL_DSN='aurora:aurora@tcp(127.0.0.1:3306)/aurora?parseTime=true&multiStatements=true' \
ARQO_EVENT_BACKEND=memory \
go run -tags mysql_driver .
```

## 5) Verify session flow

```bash
curl -sS -X POST http://127.0.0.1:8080/v1/sessions \
  -H 'content-type: application/json' \
  -d '{"user_id":"u_demo","intent":"summarize logs and send email"}'
```

## Notes
- Current repository includes mysql scheduler logic with optional driver tag import.
- If startup reports mysql driver is not registered, run the `go get` step and start `arqo` with `-tags mysql_driver`.
