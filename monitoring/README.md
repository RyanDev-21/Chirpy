# Monitoring (Grafana Cloud, free)

The server pushes metrics with OpenTelemetry once a minute when these are
set (Render → chirpy-api → Environment); without them metrics are off:

| Variable | Value |
|---|---|
| `OTEL_EXPORTER_OTLP_ENDPOINT` | the OTLP endpoint, e.g. `https://otlp-gateway-prod-ap-southeast-1.grafana.net/otlp` |
| `OTEL_EXPORTER_OTLP_HEADERS` | `Authorization=Basic <base64 of instanceID:token>` (Grafana shows it ready to copy) |

Get both from Grafana Cloud: **Connections → Add new connection →
OpenTelemetry (OTLP)** → create a token → copy the two environment variables.
After the next deploy the logs say `exporting metrics`.

No message content and no user ids are ever sent: labels are route patterns,
kinds and results only.

## Dashboard

**Dashboards → New → Import** → upload `dashboard.json` → choose the
`grafanacloud-…-prom` data source.

## Alerts (email)

1. **Alerts & IRM → Contact points**: the default email contact point should
   use your address.
2. Load the rules (needs Docker; token with `rules:write`, from **Access
   policies**):
   ```
   GRAFANA_PROM_URL=https://prometheus-prod-XX-….grafana.net \
   GRAFANA_PROM_USER=123456 \
   monitoring/load-alerts.sh
   ```
   (`URL` and `User` are on the stack's **Prometheus → Details** page.)

| Alert | When |
|---|---|
| ChirpyNotReporting | no metrics for 10 minutes (server down) |
| ChirpyHighErrorRate | > 2 % of requests fail with 5xx for 5 minutes |
| ChirpySlow | p95 latency > 1 s for 10 minutes (websocket excluded) |
| ChirpyNeonStorageHigh | Neon > 400 MB (free 0.5 GB) |
| ChirpyMediaStorageHigh | R2 > 8.5 GB (LRU cleanup at 9 GB, uploads stop at 9.5 GB) |
| ChirpyLoginAttack | > 50 failed logins in 5 minutes |
| ChirpyPushFailing | > 20 % of pushes failing for 15 minutes |
| ChirpyJobsFailing | > 10 failed background jobs in 15 minutes |

Also turn on **"notify on failure"** for the keep-alive job in cron-job.org:
it notices downtime even if Grafana can't.

Storage sizes (Neon, R2) are measured by the hourly cleanup, so they update
once an hour; measuring more often would keep the free database awake.
