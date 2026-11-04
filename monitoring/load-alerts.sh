#!/usr/bin/env bash
# Upload monitoring/alerts.yaml to Grafana Cloud's alert ruler.
#   GRAFANA_PROM_URL  e.g. https://prometheus-prod-XX-prod-ap-southeast-1.grafana.net
#   GRAFANA_PROM_USER the Prometheus "Username / Instance ID" (a number)
# The token (rules:write) is read without echoing it.
set -euo pipefail
cd "$(dirname "$0")"
: "${GRAFANA_PROM_URL:?set GRAFANA_PROM_URL}" "${GRAFANA_PROM_USER:?set GRAFANA_PROM_USER}"
read -rsp "Grafana Cloud token with rules:write: " TOKEN; echo
docker run --rm -v "$PWD:/w" grafana/mimirtool:2.14.3 rules load /w/alerts.yaml \
  --address="$GRAFANA_PROM_URL" --id="$GRAFANA_PROM_USER" --key="$TOKEN"
echo "loaded; see Alerts & IRM -> Alert rules (Data source-managed)"
