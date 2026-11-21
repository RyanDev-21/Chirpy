#!/usr/bin/env bash
# Restore a backup into this server's Postgres:  ./restore.sh [YYYY-MM-DD]
# (default: the newest). Stops the API while restoring. The database is
# REPLACED, so it asks first.
set -euo pipefail
cd "$(dirname "$0")"
set -a; . ./backup.env; set +a
BUCKET=${BACKUP_BUCKET:-chirpy-backups}
ENDPOINT="https://$BACKUP_R2_ACCOUNT_ID.r2.cloudflarestorage.com"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
# keys go through the environment (-e NAME), never on the command line,
# where any user on the machine could read them with ps
export AWS_ACCESS_KEY_ID="$BACKUP_R2_ACCESS_KEY_ID" AWS_SECRET_ACCESS_KEY="$BACKUP_R2_SECRET_ACCESS_KEY"
aws() {
  docker run --rm -v "$work:/w" -e AWS_ACCESS_KEY_ID -e AWS_SECRET_ACCESS_KEY \
    -e AWS_DEFAULT_REGION=auto -e AWS_REQUEST_CHECKSUM_CALCULATION=WHEN_REQUIRED -e AWS_RESPONSE_CHECKSUM_VALIDATION=WHEN_REQUIRED \
    amazon/aws-cli:2.22.35 --endpoint-url "$ENDPOINT" --only-show-errors "$@"
}
if [ $# -ge 1 ]; then name="chirpy-$1.dump.gpg"; else
  name=$(aws s3 ls "s3://$BUCKET/daily/" | awk '{print $4}' | sort | tail -1)
fi
[ -n "$name" ] || { echo "no backups found"; exit 1; }
read -r -p "Replace this server's database with $name? Type yes: " ok
[ "$ok" = yes ] || exit 1

aws s3 cp "s3://$BUCKET/daily/$name" /w/db.dump.gpg
printf '%s' "$BACKUP_PASSPHRASE" | gpg --batch --yes --pinentry-mode loopback --passphrase-fd 0 \
  --decrypt --output "$work/db.dump" "$work/db.dump.gpg"
docker compose stop api
docker compose exec -T postgres pg_restore -U chirpy -d chirpy --clean --if-exists --no-owner --no-privileges < "$work/db.dump"
docker compose start api
echo "restored $name"
