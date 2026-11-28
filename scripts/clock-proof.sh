#!/bin/sh
# Clock proof: seed the demo data as if it had been created N days ago, then run
# the in-app system checks and the negative tests against it today. This is the
# same thing as moving the clock forward N days: every seeded date is relative,
# so nothing may depend on today's exact day count.
#
#   ADMIN_DATABASE_URL=... DATABASE_URL=... APP_DB_PASSWORD=... \
#     SEED_ANCHOR_DAYS_AGO=31 sh scripts/clock-proof.sh
#
# CI runs it for 1, 31 and 100 days. Local use needs psql and node.
set -eu
: "${ADMIN_DATABASE_URL:?ADMIN_DATABASE_URL is required}"
: "${DATABASE_URL:?DATABASE_URL is required}"
: "${APP_DB_PASSWORD:?APP_DB_PASSWORD is required}"
root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
offset="${SEED_ANCHOR_DAYS_AGO:-0}"
port="${CLOCK_PROOF_PORT:-18080}"

echo "clock proof: anchor -${offset} days"
(cd "$root/server" && node src/migrate.js)
psql "$ADMIN_DATABASE_URL" -q -v ON_ERROR_STOP=1 -c "ALTER ROLE pamoja_app PASSWORD '${APP_DB_PASSWORD}'"
(cd "$root/server" && SEED_ANCHOR_DAYS_AGO="$offset" node src/seed.js)

cd "$root/server"
NODE_ENV=development APP_ENV=development PORT="$port" \
  DATABASE_URL="$DATABASE_URL" PAMOJA_MASTER_KEY=clock-proof-only \
  OTP_ON_SCREEN=1 RUN_SCHEDULER=0 node src/index.js >/tmp/clock-proof-api.log 2>&1 &
api_pid=$!
trap 'kill "$api_pid" 2>/dev/null || true' EXIT INT TERM
for _ in $(seq 1 60); do
  if curl -fsS "http://127.0.0.1:${port}/api/health" >/dev/null 2>&1; then break; fi
  sleep 1
done
curl -fsS "http://127.0.0.1:${port}/api/health" >/dev/null || {
  echo "API did not become healthy" >&2
  cat /tmp/clock-proof-api.log >&2
  exit 1
}

API_BASE="http://127.0.0.1:${port}/api" MIN_SYSTEM_CHECKS=35 node scripts/system-checks.mjs
API_BASE="http://127.0.0.1:${port}/api" MIN_NEGATIVE_TESTS=13 node scripts/negative-tests.mjs
echo "clock proof: PASS (anchor -${offset} days)"
