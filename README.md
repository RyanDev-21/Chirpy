# Chirpy

Backend for my TUI chat app: accounts, friends, private and group chat over
REST + WebSocket. Go, Postgres, Redis.

## Running locally

```sh
cp .env.example .env        # set SECRET (openssl rand -hex 32)
make deps                   # postgres + redis via docker compose
make migrate
make run                    # http://localhost:8080 (TLS when TLS_CERT_FILE/TLS_KEY_FILE are set)
```

## Layout

```
cmd/server            entrypoint
internal/app          wiring, chi routes, worker registry, graceful shutdown
internal/config       environment config
internal/{users,friends,groups,chat,auth,ai}
                      one package per domain: handler -> service -> repo (postgres) + store (redis)
internal/ws           websocket hub and client
internal/platform     httpx (middleware, responses), queue (redis streams), ratelimit,
                      redisx, reqctx, secure (passwords, JWT)
internal/database     sqlc output (do not edit; `make sqlc`)
sql/schema            goose migrations
sql/queries           sqlc queries
```

## How data flows

Postgres is the source of truth. Messages, friend requests and group
membership are written to Postgres before the API responds (group joins
lock the group row so capacity cannot be exceeded). Redis then caches
reads: a write invalidates the affected entries, and the next read reloads
them from Postgres. A version counter stops a slow read from caching data
that a concurrent write already replaced. Redis can be flushed at any time.

**Queue**: used only for writes where a short delay is harmless (seen
receipts, UI config) and for future side effects. One Redis stream per topic
(`jobs:{topic}`), one reader per topic feeding a worker pool, on its own
connection pool. Failed jobs retry with backoff through `jobs:delayed`;
permanent failures and exhausted retries land in `jobs:dead`. Jobs left by a
crashed instance are reclaimed.

**Rate limits** – sliding window in Redis per route policy and caller (user id,
or IP when anonymous). See `internal/app/routes.go`.

## Deploying

See [DEPLOY.md](DEPLOY.md): free tier (Render + Neon + Upstash, Singapore) first, a small VPS later.

## Tests

```sh
make test               # unit tests, no services needed (miniredis)
make test-integration   # full server against real postgres + redis
```

Integration tests truncate tables and flush Redis, so they use the separate
`chirpy_test` database. CI runs lint, unit, integration and build on every
pull request to `main`.
