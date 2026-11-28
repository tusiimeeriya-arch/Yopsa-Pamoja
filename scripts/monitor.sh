#!/bin/bash
# Hourly operational checks with one webhook alert channel.
#
#   BASE_URL=https://pamoja-staging.duckdns.org sh scripts/monitor.sh
#
# Checks: disk above 80%, TLS certificate under 14 days, failed scheduled jobs
# in the last 24 hours, audit hash-chain integrity, and backup freshness.
# Exit non-zero when anything alerts. Uptime is covered by an external checker
# (see docs/DEPLOYMENT.md); this script cannot detect that the server is down.
set -uo pipefail
PROJECT_NAME="${COMPOSE_PROJECT_NAME:-pamoja}"
APP_DIR="${APP_DIR:-/opt/pamoja/app}"
ENV_FILE="${ENV_FILE:-/opt/pamoja/.env.production}"
BASE_URL="${BASE_URL:?set BASE_URL}"
ALERT_WEBHOOK_URL="${ALERT_WEBHOOK_URL:-$(sed -n 's/^ALERT_WEBHOOK_URL=//p' "$ENV_FILE" | tail -1)}"
DISK_LIMIT="${DISK_LIMIT:-80}"
CERT_DAYS="${CERT_DAYS:-14}"
FAILED=0

compose() {
  docker compose --project-name "$PROJECT_NAME" --env-file "$ENV_FILE" \
    -f "$APP_DIR/deploy/docker-compose.prod.yml" "$@"
}
alert() {
  printf 'ALERT %s\n' "$*" >&2
  FAILED=1
  if [ -n "$ALERT_WEBHOOK_URL" ]; then
    curl -fsS --max-time 10 -X POST -H 'content-type: application/json' \
      -d "{\"text\":\"Pamoja $(hostname): $*\"}" "$ALERT_WEBHOOK_URL" >/dev/null 2>&1 || true
  fi
}
ok() { printf 'ok    %s\n' "$*"; }

# 1. Disk.
used="$(df -P / | awk 'NR==2 {gsub("%","",$5); print $5}')"
if [ "${used:-100}" -ge "$DISK_LIMIT" ]; then alert "disk ${used}% used (limit ${DISK_LIMIT}%)"; else ok "disk ${used}%"; fi

# 2. Certificate expiry (only for an HTTPS origin).
host="$(printf '%s' "$BASE_URL" | sed -e 's#^https\?://##' -e 's#/.*##' -e 's#:.*##')"
if printf '%s' "$BASE_URL" | grep -q '^https://'; then
  end="$(openssl s_client -servername "$host" -connect "${host}:443" </dev/null 2>/dev/null \
    | openssl x509 -noout -enddate 2>/dev/null | sed 's/^notAfter=//')"
  if [ -z "$end" ]; then
    alert "certificate for ${host} could not be read"
  else
    end_epoch="$(date -d "$end" +%s 2>/dev/null || echo 0)"
    now_epoch="$(date +%s)"
    days=$(( (end_epoch - now_epoch) / 86400 ))
    if [ "$days" -lt "$CERT_DAYS" ]; then alert "certificate for ${host} expires in ${days} days"; else ok "certificate ${days} days"; fi
  fi
else
  ok "certificate check skipped (plain HTTP origin)"
fi

# 3. Failed scheduled jobs in the last 24 hours.
db_name="$(sed -n 's/^POSTGRES_DB=//p' "$ENV_FILE" | tail -1)"
db_user="$(sed -n 's/^POSTGRES_USER=//p' "$ENV_FILE" | tail -1)"
failed_jobs="$(compose exec -T postgres psql -U "$db_user" -d "$db_name" -tAc \
  "SELECT count(*) FROM job_runs WHERE status='failed' AND finished_at > now() - interval '24 hours'" 2>/dev/null || echo unknown)"
if [ "$failed_jobs" = "0" ]; then ok "scheduled jobs"; else alert "scheduled jobs failed in 24h: ${failed_jobs}"; fi

# 4. Audit hash chain.
chain="$(compose exec -T api node scripts/audit-chain.mjs 2>/dev/null | tail -1)"
if printf '%s' "$chain" | grep -q '"ok":true'; then
  ok "audit chain"
else
  alert "audit chain broken: ${chain:-no output}"
fi

# 5. Backup freshness: newest daily object must be under 26 hours old.
bucket="$(sed -n 's/^BACKUP_S3_BUCKET=//p' "$ENV_FILE" | tail -1)"
if [ -n "$bucket" ]; then
  last="$(compose run --rm -T backup bash -c \
    "aws --endpoint-url \"\$BACKUP_S3_ENDPOINT\" s3api list-objects-v2 --bucket \"\$BACKUP_S3_BUCKET\" --prefix daily/ --query 'sort_by(Contents,&LastModified)[-1].LastModified' --output text" 2>/dev/null | tail -1)"
  if [ -z "$last" ] || [ "$last" = "None" ]; then
    alert "no backup object found in s3://${bucket}/daily/"
  else
    last_epoch="$(date -d "$last" +%s 2>/dev/null || echo 0)"
    age_h=$(( ( $(date +%s) - last_epoch ) / 3600 ))
    if [ "$age_h" -gt 26 ]; then alert "newest backup is ${age_h}h old"; else ok "backup ${age_h}h old"; fi
  fi
fi

[ "$FAILED" -eq 0 ] && echo "monitor: all checks passed" || { echo "monitor: ALERTS RAISED"; exit 1; }
