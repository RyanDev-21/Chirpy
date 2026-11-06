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

1. **Alerts & IRM → Notification configuration → + New contact point**:
   name `email-me`, integration **Email**, your address, **Test**, save. Then
   **Notification policies → Default policy → Edit**: contact point `email-me`.
2. **Alerts & IRM → Alert rules → More → Import alert rules → Prometheus YAML
   file** → upload `alerts-import.yaml`, target folder `Chirpy`, and turn
   **"Pause imported alerting rules" off**. The 8 rules then appear under
   Grafana-managed → Chirpy and email through `email-me`.

(`alerts.yaml` + `load-alerts.sh` load the same rules into the Prometheus
ruler with mimirtool instead; on Grafana Cloud the built-in Prometheus data
source is locked, so those don't show in the UI. Prefer the import above.)

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
| ChirpyRedisBudget | Redis on pace for > 400k commands this month (Upstash free: 500k) |
| ChirpyCallHoursHigh | > 300 call hours in a day (TURN relay allowance) |

The last two were added later. To add them to an existing setup, import
**`alerts-budget-import.yaml`** the same way (it holds only those two, in
their own group `chirpy-budget`, so nothing is duplicated). Don't re-import
`alerts-import.yaml` on top of an earlier import: Grafana adds a second copy
of every rule.

Also turn on **"notify on failure"** for the keep-alive job in cron-job.org:
it notices downtime even if Grafana can't.

Storage sizes (Neon, R2) are measured by the hourly cleanup, so they update
once an hour; measuring more often would keep the free database awake.
