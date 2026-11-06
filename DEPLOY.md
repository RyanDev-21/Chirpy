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
| `TRUSTED_PROXIES` | `2` | Requests pass Cloudflare's edge (Render's CDN) and then Render: two public proxies. Render's internal `10.x` hops are skipped automatically. With `1` the server would see Cloudflare's address for everyone, and all anonymous users would share one rate-limit budget. To check: send a request with a unique `X-Request-Id` and compare its `client_ip` in Render's logs with `curl -4 ifconfig.me`. |

## Email (Resend) and Google sign-in

### Resend: sending from mail.ponypigeon.com
1. Resend → **Domains → Add Domain** → `mail.ponypigeon.com`, region closest
   to you (e.g. Tokyo/`ap-northeast-1`).
2. Resend shows DNS records (an MX and TXT for SPF on a `send.mail` name, a
   TXT `resend._domainkey.mail` for DKIM, optionally `_dmarc`). Add each one
   in **Cloudflare → ponypigeon.com → DNS** exactly as shown, all as
   **DNS only** (grey cloud). The domain is on Cloudflare's nameservers;
   records added at Namecheap are ignored.
3. Back in Resend click **Verify**; wait for every record to turn green.
4. Resend → **API Keys → Create** with **Sending access** for that domain
   only. Paste it into Render as `RESEND_API_KEY` (never into git or chat).

Without `RESEND_API_KEY` the server logs an "insecure configuration" warning
on start and sign-up answers `503` because codes can't be sent.

### Google sign-in
In **Google Cloud Console → APIs & Services**:
1. **OAuth consent screen**: External, app name Chirpy, your support email,
   scopes `openid`, `email`, `profile`. Add yourself as a test user while
   the app is in Testing.
2. **Credentials → Create credentials → OAuth client ID → Web application**,
   name `Chirpy server`. No redirect URIs needed. Its **client ID** is what
   the server checks tokens against: put it in Render as `GOOGLE_CLIENT_IDS`,
   and give it to the Android app as `serverClientId`.
3. **Create credentials → OAuth client ID → Android**, package name of the
   app and the SHA-1 of its signing key (`./gradlew signingReport`). The
   Android client ID itself is not used by the server.

## Stage 2: VPS (~$7/month)

When the app is in real use, move to one always-on server (1-2 GB RAM)
running the app, Postgres and Redis with Docker Compose:

- set `QUEUE_BACKEND=redis` (durable queue; no per-command billing)
- run Postgres with a daily `pg_dump` backup copied off the machine
- put Caddy in front for automatic HTTPS on `api.ponypigeon.com`, and keep
  `TRUSTED_PROXIES=1` (Caddy is then the only public proxy; it appends the
  client IP to `X-Forwarded-For`)
- keep monitoring on Grafana Cloud's free tier rather than self-hosting
  Prometheus/Loki/Grafana on a small server

A `docker-compose.prod.yml` for this stage comes with the move.

## Environment reference

See `.env.example` for every variable and its default.

## Website (chirpy.ponypigeon.com, Cloudflare Pages)

The download page, privacy policy and terms are static files in `website/`.

1. Cloudflare dashboard → **Workers & Pages → Create → Pages → Connect to Git** → `RyanDev-21/Chirpy`.
2. **Production branch:** `main`. **Build command:** empty. **Build output directory:** `website`.
3. After the first deploy: the project → **Custom domains → Set up a custom domain** → `chirpy.ponypigeon.com`.
4. Render → chirpy-api → Environment: add `https://chirpy.ponypigeon.com` to
   `CORS_ALLOWED_ORIGINS` (the download button reads `/api/app/version`).
5. Releasing an APK: upload it (e.g. a GitHub Release asset), then set
   `APP_LATEST_VERSION`, `APP_DOWNLOAD_URL`, `APP_SHA256` (`shasum -a 256 app.apk`),
   optionally `APP_MIN_VERSION` (forces older apps to update) and
   `APP_RELEASE_NOTES` on Render. No website change needed.
6. Google sign-in for everyone: Google Cloud → Google Auth Platform → **Branding**:
   homepage `https://chirpy.ponypigeon.com`, privacy policy
   `https://chirpy.ponypigeon.com/privacy`, terms
   `https://chirpy.ponypigeon.com/terms` (Pages serves them without `.html`;
   the `.html` links redirect there); authorized domain `ponypigeon.com`;
   then **Audience → Publish app**.

## Voice calls: Cloudflare TURN

Calls connect phone to phone; the TURN relay carries the audio when that
fails (strict NATs, some mobile networks). Free up to 1,000 GB a month
(about 28,000 hours of relayed voice).

1. Cloudflare dashboard → **Realtime** → **TURN Server** → **Create**, name
   `chirpy`.
2. Copy the **Turn Token ID** and the **API Token** it shows (the token is
   shown once).
3. Render → chirpy → **Environment**: `CF_TURN_KEY_ID` = the token ID,
   `CF_TURN_API_TOKEN` = the API token. Save and deploy.
4. Check: `GET /api/calls/ice` (signed in) answers `"relay":true`.

Without them calls still work where STUN suffices (`"relay":false`).

## Web app (React Native Web)

1. Render → `CORS_ALLOWED_ORIGINS`: add the web app's origin, e.g.
   `https://chirpy.ponypigeon.com` (comma-separated with the others).
2. Cloudflare → R2 → `chirpy-media` → **Settings → CORS policy**, so
   browsers can upload and download directly:
   ```json
   [{"AllowedOrigins":["https://chirpy.ponypigeon.com"],
     "AllowedMethods":["GET","PUT"],
     "AllowedHeaders":["Content-Type","If-None-Match"],
     "ExposeHeaders":["ETag"],"MaxAgeSeconds":3600}]
   ```
3. Google sign-in on the web uses the same **Web** client ID
   (`GOOGLE_CLIENT_IDS`); add the web origin under **Authorized JavaScript
   origins** of that client in Google Cloud Console.
4. Web push: Firebase → Project settings → Cloud Messaging → **Web Push
   certificates** → generate a key pair; the web app registers its FCM token
   with `POST /api/devices` and `"platform":"web"`.

## Database backups

`.github/workflows/backup.yml` dumps the Neon database every night
(02:30 Bangkok time), encrypts it with a passphrase and uploads it to a
private R2 bucket, keeping 7 days. Neon's own restore window on the free
plan is short; this survives mistakes, a lost account or a bad migration.
GitHub emails you if a run fails.

**Setup (once)**
1. Cloudflare → R2 → **Create bucket** `chirpy-backups` (location APAC).
   Backups live apart from media, so media cleanup never touches them.
2. R2 → **Manage API tokens** → **Create API token**: permission **Object
   Read & Write**, applied to **`chirpy-backups` only**. Note the Access
   Key ID and Secret Access Key (shown once).
3. Neon → your project → **Connect**: copy the connection string with
   **Connection pooling off** (pg_dump needs a direct connection).
4. Make a long random passphrase and save it in your password manager.
   **Without it the backups can't be opened.**
5. GitHub → repo → **Settings → Secrets and variables → Actions → New
   repository secret**:

   | Name | Value |
   |---|---|
   | `BACKUP_DB_URL` | the direct Neon connection string |
   | `BACKUP_PASSPHRASE` | the passphrase |
   | `BACKUP_R2_ACCOUNT_ID` | your Cloudflare account ID |
   | `BACKUP_R2_ACCESS_KEY_ID` | from step 2 |
   | `BACKUP_R2_SECRET_ACCESS_KEY` | from step 2 |

6. **Actions → Database backup → Run workflow** once, and check that a file
   appears in the bucket under `daily/`.

**Restore** (into an empty database, e.g. a new Neon branch):
```sh
# download daily/chirpy-YYYY-MM-DD.dump.gpg from the bucket, then
gpg --decrypt chirpy-YYYY-MM-DD.dump.gpg > chirpy.dump   # asks for the passphrase
pg_restore --no-owner --no-privileges --dbname "$TARGET_DB_URL" chirpy.dump
```
Point `DB_URL` on Render at the restored database. Photos and videos are not
in the backup (they expire within days anyway).

## Keeping Redis inside the free plan

Upstash's free plan allows 500,000 commands a month, and every command
counts. On a single server, set `RATE_LIMIT_BACKEND=memory` (with
`QUEUE_BACKEND=memory`): rate limits then cost no Redis commands. Switch
both back to `redis` before running more than one server. The dashboard
panel "Redis commands, projected per month" and the `ChirpyRedisBudget`
alert show the pace.

## Call limits

`CALL_MINUTES_PER_DAY` (default 180) caps each user's answered call time
per 24 hours, so the free TURN allowance (1,000 GB a month) can't be used
up; 0 turns the cap off. The `ChirpyCallHoursHigh` alert fires above 300
call hours a day.
