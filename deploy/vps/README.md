# Chirpy on one server (Azure for Students VM)

One machine runs everything that was on Render, Neon and Upstash: the API,
Postgres, Redis, and Caddy for HTTPS. R2, Cloudflare TURN, FCM, Grafana
Cloud and email stay as they are.

Measured locally at idle: API ~70 MB, Postgres ~50 MB, Redis ~25 MB of RAM.
The limits in `docker-compose.yml` (400 / 350 / 100 / 100 MB) fit in 1 GB
with the 2 GB swap as a safety net.

## 1. Create the VM (Azure portal)

**Virtual machines → Create**:

| Field | Value |
|---|---|
| Subscription | Azure for Students |
| Resource group | new: `chirpy` |
| Name | `chirpy-api` |
| Region | **Southeast Asia** (Singapore) |
| Image | **Ubuntu Server 24.04 LTS - x64 Gen2** |
| Size | **B2ats v2** (2 vCPU, 1 GiB), the one marked free |
| Authentication | **SSH public key**, user `azureuser`, key: generate new (download the `.pem` and keep it safe), or use your own |
| Inbound ports | SSH (22), HTTP (80), HTTPS (443) |
| Disk (next tab) | OS disk **Standard SSD**, 30-64 GB (check what the free offer covers) |
| Networking | public IP: **Static** |

After it's created, write down the **public IP**.

Optional but wise: **Auto-shutdown: off**, and **Microsoft Defender: Free**.
In **Cost Management → Budgets**, set a budget alert at $5 so a mistake
can't eat the credit.

## 2. First login and setup

```bash
chmod 600 ~/Downloads/chirpy-api_key.pem
ssh -i ~/Downloads/chirpy-api_key.pem azureuser@<PUBLIC_IP>

git clone https://github.com/RyanDev-21/Chirpy.git ~/chirpy
cd ~/chirpy/deploy/vps
bash setup.sh          # updates, firewall, SSH keys only, fail2ban, swap, Docker, backup timer
exit                   # log out and back in, so the docker group applies
```

(The repository is private: use a GitHub **fine-grained token** with
read-only access to this repository as the password when cloning, or add a
read-only deploy key.)

## 3. Secrets

```bash
cd ~/chirpy/deploy/vps
cp .env.example .env && chmod 600 .env && nano .env
cp backup.env.example backup.env && chmod 600 backup.env && nano backup.env
```

- `POSTGRES_PASSWORD`, `REDIS_PASSWORD`: the fresh ones `setup.sh` printed.
- `SECRET`: **the same value as on Render**, otherwise every phone is
  signed out.
- Everything else: copy from Render → chirpy-api → Environment. The
  `FIREBASE_SERVICE_ACCOUNT` JSON goes on one line, in single quotes.
- `backup.env`: the same values as the GitHub backup secrets.

Never paste these into chat or commit them (`.env` is git-ignored).

## 4. Start Chirpy and test it (Render keeps serving users)

```bash
cd ~/chirpy/deploy/vps
VERSION=$(git rev-parse HEAD) docker compose up -d --build
docker compose ps                 # all running, postgres and redis healthy
docker compose logs -f api        # ctrl-c to stop following
```

DNS still points at Render, so test the new server by its IP, telling curl
which name to use:

```bash
curl --resolve api.ponypigeon.com:443:<PUBLIC_IP> https://api.ponypigeon.com/livez
```

Caddy can only get the HTTPS certificate once DNS points here (step 6).
Until then, test from the server itself:
`docker run --rm --network chirpy_default curlimages/curl:8.11.1 -s http://api:8080/readyz`.

Test a backup and a restore **before** moving:

```bash
./backup.sh      # uploads daily/chirpy-<today>.dump.gpg to R2
./restore.sh     # pick it, type yes: proves the passphrase and format work
```

## 5. Copy the data from Neon (about 10 minutes of downtime)

1. Tell testers. In Render, **Suspend** the service, so nothing writes to
   Neon any more.
2. On the VM, dump Neon (the connection string with **pooling off**, as for
   backups) and load it:

   ```bash
   cd ~/chirpy/deploy/vps
   docker compose stop api
   read -r -s -p "Neon URL: " NEON_URL; echo
   docker run --rm -e NEON_URL="$NEON_URL" postgres:18 \
     sh -c 'pg_dump --format=custom --no-owner --no-privileges "$NEON_URL"' > /tmp/neon.dump
   docker compose exec -T postgres pg_restore -U chirpy -d chirpy --clean --if-exists --no-owner --no-privileges < /tmp/neon.dump
   rm /tmp/neon.dump; unset NEON_URL
   docker compose start api
   ```

3. Quick check: `docker compose exec postgres psql -U chirpy -c "select count(*) from users;"`
   should match Neon's number.

## 6. Point the domain at the VM

Cloudflare → ponypigeon.com → DNS: change the `api` record from the CNAME
(`chirpy-api.onrender.com`) to an **A** record with the VM's **public IP**,
**DNS only (grey cloud)**, TTL Auto. Within a minute or two Caddy gets the
certificate:

```bash
docker compose logs caddy | grep -i certificate
curl https://api.ponypigeon.com/readyz     # from your laptop: 200
```

Phones reconnect by themselves; messages sent during the switch are
retried by the app (client_msg_id) and nothing is sent twice.

## 7. Afterwards

- Watch Grafana for an hour: requests, errors, the new **memory** and
  **disk** panels.
- In Grafana, import `monitoring/alerts-import.yaml` again (new disk and
  memory alerts), and **delete** `ChirpyRedisBudget` and
  `ChirpyNeonStorageHigh`.
- GitHub → Actions → **Database backup** workflow: disable it (the VM backs
  up now). Keep its secrets until the VM backups have run a few nights.
- Remove the external keep-alive ping to `/livez` (it only kept Render
  awake).
- Keep Render (suspended) and Neon for **one week** as a fallback. To go
  back: un-suspend Render, point the `api` DNS record back to the CNAME.
  After a quiet week, delete them, and the Upstash database.
- Put the VM's **renewal date** (Azure for Students, 12 months) in your
  calendar.

## Everyday commands

```bash
cd ~/chirpy/deploy/vps
git pull && VERSION=$(git rev-parse HEAD) docker compose up -d --build   # deploy
docker compose logs --since 1h api                                       # logs
docker compose restart api                                               # restart
systemctl list-timers chirpy-backup                                      # next backup
journalctl -u chirpy-backup --since today                                # last backup's output
```

Deploys take the API down for a few seconds while the new container
starts; the queue drains first and phones reconnect.
