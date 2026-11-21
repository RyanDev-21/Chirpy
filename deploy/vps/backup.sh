#!/usr/bin/env bash
# Nightly backup: pg_dump inside the Postgres container, encrypted with the
# passphrase, uploaded to R2 (daily/chirpy-YYYY-MM-DD.dump.gpg); keeps the
# last KEEP_DAYS days. Same format as the old GitHub workflow, so restore.sh
# reads both. Run by the chirpy-backup timer, or by hand: ./backup.sh
set -euo pipefail
cd "$(dirname "$0")"
set -a; . ./backup.env; set +a
: "${BACKUP_PASSPHRASE:?}" "${BACKUP_R2_ACCOUNT_ID:?}" "${BACKUP_R2_ACCESS_KEY_ID:?}" "${BACKUP_R2_SECRET_ACCESS_KEY:?}"
BUCKET=${BACKUP_BUCKET:-chirpy-backups}
KEEP_DAYS=${KEEP_DAYS:-7}
ENDPOINT="https://$BACKUP_R2_ACCOUNT_ID.r2.cloudflarestorage.com"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

docker compose exec -T postgres pg_dump -U chirpy --format=custom --compress=9 --no-owner --no-privileges chirpy > "$work/db.dump"
test -s "$work/db.dump"
printf '%s' "$BACKUP_PASSPHRASE" | gpg --batch --yes --pinentry-mode loopback --passphrase-fd 0 \
  --symmetric --cipher-algo AES256 --output "$work/db.dump.gpg" "$work/db.dump"
rm -f "$work/db.dump"

# keys go through the environment (-e NAME), never on the command line,
# where any user on the machine could read them with ps
export AWS_ACCESS_KEY_ID="$BACKUP_R2_ACCESS_KEY_ID" AWS_SECRET_ACCESS_KEY="$BACKUP_R2_SECRET_ACCESS_KEY"
aws() {
  docker run --rm -v "$work:/w" -e AWS_ACCESS_KEY_ID -e AWS_SECRET_ACCESS_KEY \
    -e AWS_DEFAULT_REGION=auto -e AWS_REQUEST_CHECKSUM_CALCULATION=WHEN_REQUIRED -e AWS_RESPONSE_CHECKSUM_VALIDATION=WHEN_REQUIRED \
    amazon/aws-cli:2.22.35 --endpoint-url "$ENDPOINT" --only-show-errors "$@"
}
key="daily/chirpy-$(date -u +%F).dump.gpg"
aws s3 cp /w/db.dump.gpg "s3://$BUCKET/$key"
echo "uploaded $key ($(stat -c %s "$work/db.dump.gpg") bytes)"

cutoff=$(date -u -d "$KEEP_DAYS days ago" +%F)
aws s3 ls "s3://$BUCKET/daily/" | awk '{print $4}' | while read -r name; do
  day=${name#chirpy-}; day=${day%.dump.gpg}
  if [[ "$day" < "$cutoff" ]]; then aws s3 rm "s3://$BUCKET/daily/$name"; echo "removed $name"; fi
done
