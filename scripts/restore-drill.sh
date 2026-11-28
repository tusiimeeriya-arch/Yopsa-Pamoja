#!/bin/bash
# Restore the latest encrypted backup into a temporary database. Prints the
# elapsed time and a PASS/FAIL line.
#
# Two ways to run it:
#
#   on the server (staging)   Copy the age PRIVATE key in, run the drill, then
#                             shred the key (docs/DEPLOYMENT.md S9).
#                             LIVE_COMPARE=1 (default) compares the live and
#                             restored row counts.
#   off-server (production)   Run from an operator workstation that holds the
#                             key, against a local PostgreSQL. The key never
#                             touches the server and no live credential is
#                             needed; LIVE_COMPARE=0 proves the dump restores.
#
#   AGE_IDENTITY_FILE=/root/age-key.txt sh scripts/restore-drill.sh
#   LIVE_COMPARE=0 AGE_IDENTITY_FILE="$HOME/pamoja-backup-key.txt" \
#     sh scripts/restore-drill.sh
#
# Required: PGHOST PGPORT PGUSER PGPASSWORD, BACKUP_S3_*, AGE_IDENTITY_FILE.
#           PGDATABASE is required when LIVE_COMPARE=1.
set -euo pipefail
umask 077

: "${PGHOST:?}" "${PGPORT:?}" "${PGUSER:?}" "${PGPASSWORD:?}"
: "${BACKUP_S3_ENDPOINT:?}" "${BACKUP_S3_BUCKET:?}"
: "${BACKUP_S3_ACCESS_KEY:?}" "${BACKUP_S3_SECRET_KEY:?}"
: "${AGE_IDENTITY_FILE:?set AGE_IDENTITY_FILE to the offline age private key}"
LIVE_COMPARE="${LIVE_COMPARE:-1}"
if [ "$LIVE_COMPARE" = "1" ]; then
  : "${PGDATABASE:?set PGDATABASE for the live comparison, or LIVE_COMPARE=0}"
fi

BACKUP_PREFIX="${BACKUP_PREFIX:-pamoja}"
BACKUP_S3_REGION="${BACKUP_S3_REGION:-fra}"
RESTORE_DB="${RESTORE_DB:-pamoja_restore_drill}"
WORK_DIR="${WORK_DIR:-/tmp/restore-drill}"
LOG_FILE="${LOG_FILE:-/var/log/pamoja/restore-drill.log}"

export AWS_ACCESS_KEY_ID="$BACKUP_S3_ACCESS_KEY"
export AWS_SECRET_ACCESS_KEY="$BACKUP_S3_SECRET_KEY"
export AWS_DEFAULT_REGION="$BACKUP_S3_REGION"
export AWS_EC2_METADATA_DISABLED=true

mkdir -p "$WORK_DIR" "$(dirname "$LOG_FILE")"
STARTED=$(date +%s)
log() { printf '%s %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*" | tee -a "$LOG_FILE"; }
aws() { command aws --endpoint-url "$BACKUP_S3_ENDPOINT" "$@"; }

cleanup() {
  dropdb --if-exists "$RESTORE_DB" >/dev/null 2>&1 || true
  rm -rf "$WORK_DIR"
}
trap cleanup EXIT

log "START latest backup from s3://${BACKUP_S3_BUCKET}/daily/ live_compare=${LIVE_COMPARE}"
LATEST="$(aws s3api list-objects-v2 --bucket "$BACKUP_S3_BUCKET" --prefix "daily/" \
  --query 'sort_by(Contents,&Key)[-1].Key' --output text)"
[ -n "$LATEST" ] && [ "$LATEST" != "None" ] || { log "FAIL no backups found"; exit 1; }
log "latest=${LATEST}"

aws s3 cp "s3://${BACKUP_S3_BUCKET}/${LATEST}" "$WORK_DIR/backup.age" --only-show-errors
age --decrypt --identity "$AGE_IDENTITY_FILE" --output "$WORK_DIR/backup.sql.gz" "$WORK_DIR/backup.age"
gzip -dc "$WORK_DIR/backup.sql.gz" >"$WORK_DIR/backup.sql"
[ -s "$WORK_DIR/backup.sql" ] || { log "FAIL decrypted dump is empty"; exit 1; }

dropdb --if-exists "$RESTORE_DB"
createdb "$RESTORE_DB"
psql -q -v ON_ERROR_STOP=1 -d "$RESTORE_DB" -f "$WORK_DIR/backup.sql" >/dev/null

TABLES="users persons groups memberships payment_entries receipts audit_logs"
FAILED=0
if [ "$LIVE_COMPARE" = "1" ]; then
  printf '%-20s %10s %10s\n' table live restored
  for t in $TABLES; do
    live="$(psql -tA -d "$PGDATABASE" -c "SELECT count(*) FROM ${t}")"
    restored="$(psql -tA -d "$RESTORE_DB" -c "SELECT count(*) FROM ${t}")"
    printf '%-20s %10s %10s\n' "$t" "$live" "$restored"
    [ "$live" = "$restored" ] || FAILED=1
  done
else
  # Off-server: there is no live database to compare with. The proof is that
  # the dump decrypts and restores into every expected table, with data.
  TOTAL=0
  printf '%-20s %10s\n' table restored
  for t in $TABLES; do
    restored="$(psql -tA -d "$RESTORE_DB" -c "SELECT count(*) FROM ${t}")"
    printf '%-20s %10s\n' "$t" "$restored"
    TOTAL=$((TOTAL + restored))
  done
  [ "$TOTAL" -gt 0 ] || { log "FAIL restored database is empty"; exit 1; }
fi

ELAPSED=$(( $(date +%s) - STARTED ))
if [ "$FAILED" -eq 0 ]; then
  if [ "$LIVE_COMPARE" = "1" ]; then
    log "PASS restore drill completed in ${ELAPSED}s (database=${PGDATABASE} backup=${LATEST})"
  else
    log "PASS restore drill completed in ${ELAPSED}s (off-server, live comparison skipped, backup=${LATEST})"
  fi
  exit 0
fi
log "FAIL row counts differ after restore (database=${PGDATABASE} backup=${LATEST})"
exit 1
