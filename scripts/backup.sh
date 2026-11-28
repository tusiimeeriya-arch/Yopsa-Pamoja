#!/bin/bash
# Encrypted PostgreSQL backup to Vultr Object Storage (S3-compatible).
#
# Runs from the `backup` service in deploy/docker-compose.prod.yml, and from a
# daily systemd timer on the server. The dump is compressed with gzip and
# encrypted with age to a PUBLIC key; the private key is kept off the server.
#
# Required environment:
#   PGHOST PGPORT PGDATABASE PGUSER PGPASSWORD
#   BACKUP_S3_ENDPOINT BACKUP_S3_REGION BACKUP_S3_BUCKET
#   BACKUP_S3_ACCESS_KEY BACKUP_S3_SECRET_KEY
#   BACKUP_AGE_RECIPIENT
# Optional:
#   BACKUP_PREFIX (default pamoja), BACKUP_RETENTION_DAYS (7),
#   BACKUP_RETENTION_WEEKS (4), BACKUP_RETENTION_MONTHS (6),
#   BACKUP_DIR (/tmp), LOG_FILE (/var/log/pamoja/backup.log),
#   ALERT_WEBHOOK_URL
set -euo pipefail
umask 077

: "${PGHOST:?}" "${PGPORT:?}" "${PGDATABASE:?}" "${PGUSER:?}" "${PGPASSWORD:?}"
: "${BACKUP_S3_ENDPOINT:?}" "${BACKUP_S3_BUCKET:?}"
: "${BACKUP_S3_ACCESS_KEY:?}" "${BACKUP_S3_SECRET_KEY:?}"
: "${BACKUP_AGE_RECIPIENT:?}"

BACKUP_PREFIX="${BACKUP_PREFIX:-pamoja}"
BACKUP_S3_REGION="${BACKUP_S3_REGION:-fra}"
BACKUP_RETENTION_DAYS="${BACKUP_RETENTION_DAYS:-7}"
BACKUP_RETENTION_WEEKS="${BACKUP_RETENTION_WEEKS:-4}"
BACKUP_RETENTION_MONTHS="${BACKUP_RETENTION_MONTHS:-6}"
BACKUP_DIR="${BACKUP_DIR:-/tmp}"
LOG_FILE="${LOG_FILE:-/var/log/pamoja/backup.log}"

export AWS_ACCESS_KEY_ID="$BACKUP_S3_ACCESS_KEY"
export AWS_SECRET_ACCESS_KEY="$BACKUP_S3_SECRET_KEY"
export AWS_DEFAULT_REGION="$BACKUP_S3_REGION"
export AWS_EC2_METADATA_DISABLED=true

mkdir -p "$(dirname "$LOG_FILE")"
STARTED=$(date +%s)
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
FILE="${BACKUP_DIR}/${BACKUP_PREFIX}-${STAMP}.sql.gz.age"
KEY="daily/${BACKUP_PREFIX}-${STAMP}.sql.gz.age"

log() { printf '%s %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*" | tee -a "$LOG_FILE"; }
alert() {
  if [ -n "${ALERT_WEBHOOK_URL:-}" ]; then
    curl -fsS --max-time 10 -X POST -H 'content-type: application/json' \
      -d "{\"text\":\"Pamoja backup FAILED on ${PGDATABASE}: $*\"}" \
      "$ALERT_WEBHOOK_URL" >/dev/null 2>&1 || true
  fi
}
on_error() {
  local line="$1"
  log "FAILED line=${line} database=${PGDATABASE} stamp=${STAMP}"
  alert "line ${line}"
  rm -f "$FILE"
  exit 1
}
trap 'on_error $LINENO' ERR

aws() { command aws --endpoint-url "$BACKUP_S3_ENDPOINT" "$@"; }

log "START database=${PGDATABASE} host=${PGHOST} bucket=${BACKUP_S3_BUCKET} prefix=${BACKUP_PREFIX}"

# 1. Dump, compress, encrypt. pg_dump is the same major version as the server.
pg_dump --no-owner --no-privileges --format=plain \
  | gzip -9 \
  | age --encrypt --recipient "$BACKUP_AGE_RECIPIENT" --output "$FILE"

SIZE="$(wc -c <"$FILE" | tr -d ' ')"
[ "$SIZE" -gt 1024 ] || { log "dump is suspiciously small: ${SIZE} bytes"; false; }

# 2. Upload to the private bucket.
aws s3 cp "$FILE" "s3://${BACKUP_S3_BUCKET}/${KEY}" --only-show-errors

# 3. Retention: every run is daily; Sunday is also weekly, the 1st is monthly.
if [ "$(date -u +%u)" = "7" ]; then
  aws s3 cp "s3://${BACKUP_S3_BUCKET}/${KEY}" "s3://${BACKUP_S3_BUCKET}/weekly/${BACKUP_PREFIX}-${STAMP}.sql.gz.age" --only-show-errors
fi
if [ "$(date -u +%d)" = "01" ]; then
  aws s3 cp "s3://${BACKUP_S3_BUCKET}/${KEY}" "s3://${BACKUP_S3_BUCKET}/monthly/${BACKUP_PREFIX}-${STAMP}.sql.gz.age" --only-show-errors
fi

prune() {
  local prefix="$1" keep="$2" keys=() i n
  mapfile -t keys < <(aws s3api list-objects-v2 --bucket "$BACKUP_S3_BUCKET" --prefix "$prefix" \
    --query 'Contents[].Key' --output text 2>/dev/null | tr '\t' '\n' | sed '/^None$/d' | sed '/^$/d' | sort)
  n="${#keys[@]}"
  if [ "$n" -gt "$keep" ]; then
    log "prune ${prefix}: ${n} -> ${keep}"
    for ((i = 0; i < n - keep; i++)); do
      aws s3 rm "s3://${BACKUP_S3_BUCKET}/${keys[$i]}" --only-show-errors
    done
  fi
}
prune "daily/" "$BACKUP_RETENTION_DAYS"
prune "weekly/" "$BACKUP_RETENTION_WEEKS"
prune "monthly/" "$BACKUP_RETENTION_MONTHS"

ELAPSED=$(( $(date +%s) - STARTED ))
log "OK key=s3://${BACKUP_S3_BUCKET}/${KEY} bytes=${SIZE} seconds=${ELAPSED}"
rm -f "$FILE"
