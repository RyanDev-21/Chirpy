# Deploying Chirpy

Two stages: free hosting while testing, then a small VPS once it is stable.
Everything runs in **Singapore** so the app, database and Redis are close
to each other (every request talks to all three).

## Stage 1: free tier (Render + Neon + Upstash)

| Piece    | Service | Free limits to keep in mind |
|----------|---------|-----------------------------|
| API      | Render web service (Docker) | sleeps after 15 min without traffic, ~1 min to wake; 750 instance-hours/month per workspace |
| Postgres | Neon | 0.5 GB storage, 100 compute-hours/month, scales to zero after 5 min idle |
| Redis    | Upstash | 256 MB, 500K commands/month |

Limits change; check each provider's pricing page.

### 1. Postgres on Neon
1. Create a project, region **AWS Asia Pacific (Singapore)**.
2. Copy the **direct** connection string (not the `-pooler` one: the server
   keeps its own small pool) and change `sslmode=require` to
   **`sslmode=verify-full`**. `require` only encrypts; `verify-full` also checks
   the certificate really belongs to Neon. The server logs an
   "insecure configuration" warning on start if this is missing.

### 2. Redis on Upstash
1. Create a Redis database, region **ap-southeast-1 (Singapore)**, TLS on.
2. Copy the `rediss://default:…@….upstash.io:6379` URL. It must start with
   `rediss://` (two s: TLS); Upstash closes plain `redis://` connections.

### 3. API on Render
1. Render dashboard → **New → Blueprint** → select `RyanDev-21/Chirpy`.
   `render.yaml` creates the service; paste `DB_URL` and `REDIS_URL` when
   asked. `SECRET` is generated for you.
2. The first deploy applies all migrations automatically
   (`MIGRATE_ON_START=true`). Check the logs for `database schema up to date`.
3. Verify: `curl https://chirpy-api.onrender.com/readyz` →
   `{"postgres":"ok","redis":"ok"}`.

Deploys happen on every push to `main` once CI passes
(`autoDeployTrigger: checksPass`). If your Render account does not offer
that option, set *Auto-Deploy* to "After CI checks pass" in the service
settings.

### 4. Domain: api.ponypigeon.com
1. Render → service → **Settings → Custom Domains** → add `api.ponypigeon.com`.
2. Namecheap → Domain List → `ponypigeon.com` → **Advanced DNS** → add
   `CNAME  api  →  chirpy-api.onrender.com.`
3. Render issues the HTTPS certificate once DNS resolves (minutes to an hour).
   No `TLS_CERT_FILE` is needed; Render terminates TLS.

### 5. Keep it awake
Render's free service sleeps after 15 idle minutes, which makes the first
app open hang for about a minute. Ping it every 10 minutes:

1. Create a free job at <https://cron-job.org>.
2. URL `https://api.ponypigeon.com/livez`, every **10 minutes**.

Use `/livez`, never `/readyz`: `/livez` does not touch Postgres or Redis,
so Neon can still scale to zero between real requests. One always-on
service uses ~730 of the 750 free instance-hours, so keep only one free
Render service.

### Settings used on the free tier

| Variable | Value | Why |
|----------|-------|-----|
| `QUEUE_BACKEND` | `memory` | Upstash bills per command; idle stream polling alone would exceed 500K/month. Only seen receipts and UI config are queued, so losing them on a restart is harmless. |
| `DB_MAX_CONNS` | `5` | Small pool for a small database. |
| `DB_MAX_IDLE_TIME` | `2m` | Close idle connections so Neon can scale to zero. |
| `TRUST_PROXY_HEADERS` | unset | Leave off unless you confirm Render overwrites `X-Forwarded-For`; otherwise rate limits key on Render's proxy IP. |

## Stage 2: VPS (~$7/month)

When the app is in real use, move to one always-on server (1-2 GB RAM)
running the app, Postgres and Redis with Docker Compose:

- set `QUEUE_BACKEND=redis` (durable queue; no per-command billing)
- run Postgres with a daily `pg_dump` backup copied off the machine
- put Caddy in front for automatic HTTPS on `api.ponypigeon.com`, and set
  `TRUST_PROXY_HEADERS=true` since Caddy sets `X-Forwarded-For`
- keep monitoring on Grafana Cloud's free tier rather than self-hosting
  Prometheus/Loki/Grafana on a small server

A `docker-compose.prod.yml` for this stage comes with the move.

## Environment reference

See `.env.example` for every variable and its default.
