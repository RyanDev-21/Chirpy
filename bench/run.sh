#!/usr/bin/env bash
# Load-test a server sized like Render free (default) or a VPS:
#   bench/run.sh              # 0.1 CPU, 512 MB
#   BENCH_CPUS=1 bench/run.sh # 1 CPU
# Starts its own Postgres, Redis and server in Docker, then stops them and
# deletes their data (the compose project "chirpy-bench" only).
set -euo pipefail
cd "$(dirname "$0")/.."
cpus=${BENCH_CPUS:-0.1}
export BENCH_CPUS=$cpus
BENCH_SECRET=$(openssl rand -hex 32)
export BENCH_SECRET
compose=(docker compose -f bench/docker-compose.yml)
trap '"${compose[@]}" down --volumes >/dev/null 2>&1' EXIT
"${compose[@]}" up -d --build --wait
label=${BENCH_LABEL:-"Server with $cpus CPU, 512 MB"}
go run ./cmd/bench -label "$label" -out "bench/results-${cpus}cpu.md" "$@"
