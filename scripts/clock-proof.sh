#!/bin/sh
# Clock proof, in two directions. They are not the same experiment:
#
#   anchor (default)  Seed the demo data as if it had been created N days ago
#                     (SEED_ANCHOR_DAYS_AGO=N), then run the checks today. The
#                     data is backdated; no clock is moved. Every seed-derived
#                     date keeps the same age relative to "today", so this
#                     covers relative-date logic but cannot age anything that
#                     already existed before the jump (sessions, tokens,
#                     scheduled work).
#   forward           Seed at today's date, then move the whole run forward N
#                     days (CLOCK_FORWARD_DAYS=N) with libfaketime, including
#                     PostgreSQL. This is the literal clock jump, and it also
#                     exercises expiry of things created before it.
#
# CI runs both at 1, 31 and 100 days. In forward mode the database must run
# under the same offset; the script refuses to start otherwise, because an
# application-only jump is not the scenario either.
#
#   ADMIN_DATABASE_URL=... DATABASE_URL=... APP_DB_PASSWORD=... \
#     SEED_ANCHOR_DAYS_AGO=31 sh scripts/clock-proof.sh
#   ADMIN_DATABASE_URL=... DATABASE_URL=... APP_DB_PASSWORD=... \
#     CLOCK_FORWARD_DAYS=31 sh scripts/clock-proof.sh
#
# Local use needs psql, node and, for the forward direction, faketime.
set -eu
: "${ADMIN_DATABASE_URL:?ADMIN_DATABASE_URL is required}"
: "${DATABASE_URL:?DATABASE_URL is required}"
: "${APP_DB_PASSWORD:?APP_DB_PASSWORD is required}"
root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
offset="${SEED_ANCHOR_DAYS_AGO:-0}"
forward="${CLOCK_FORWARD_DAYS:-0}"
port="${CLOCK_PROOF_PORT:-18080}"

at() {
  if [ "$forward" -gt 0 ]; then
    faketime "+${forward} days" "$@"
  else
    "$@"
  fi
}

if [ "$forward" -gt 0 ]; then
  command -v faketime >/dev/null 2>&1 || {
    echo "clock proof: CLOCK_FORWARD_DAYS needs libfaketime (faketime) on PATH" >&2
    exit 1
  }
  export FAKETIME_DONT_FAKE_MONOTONIC=1
  # faketime forks its child, so a long-running process started through it
  # cannot be killed by the pid we remember. Preload the library directly for
  # the API below; foreground commands keep using the wrapper.
  fakelib="$(faketime "+0 days" sh -c 'printf %s "$LD_PRELOAD"')"
  [ -n "$fakelib" ] || {
    echo "clock proof: could not resolve libfaketime's LD_PRELOAD path" >&2
    exit 1
  }
  node_now="$(at node -e 'console.log(Math.floor(Date.now()/1000))')"
  db_now="$(psql "$ADMIN_DATABASE_URL" -tA -v ON_ERROR_STOP=1 -c 'SELECT extract(epoch FROM now())::bigint')"
  skew=$((node_now - db_now))
  if [ "$skew" -lt 0 ]; then skew=$((-skew)); fi
  if [ "$skew" -gt 3600 ]; then
    echo "clock proof: database clock is not moved with the application (skew ${skew}s)" >&2
    echo "clock proof: start PostgreSQL under 'faketime +${forward} days' too, or use the anchor direction" >&2
    exit 1
  fi
  echo "clock proof: forward +${forward} days (application and database)"
else
  echo "clock proof: anchor -${offset} days (data backdated, clock not moved)"
fi

(cd "$root/server" && at node src/migrate.js)
psql "$ADMIN_DATABASE_URL" -q -v ON_ERROR_STOP=1 -c "ALTER ROLE pamoja_app PASSWORD '${APP_DB_PASSWORD}'"
(cd "$root/server" && export SEED_ANCHOR_DAYS_AGO="$offset" && at node src/seed.js)

export NODE_ENV=development APP_ENV=development PORT="$port" \
  DATABASE_URL="$DATABASE_URL" PAMOJA_MASTER_KEY=clock-proof-only \
  OTP_ON_SCREEN=1 RUN_SCHEDULER=0
if curl -fsS "http://127.0.0.1:${port}/api/health" >/dev/null 2>&1; then
  echo "clock proof: port ${port} already answers; refusing to run against another API" >&2
  exit 1
fi
if [ "$forward" -gt 0 ]; then
  (cd "$root/server" && export LD_PRELOAD="$fakelib" FAKETIME="+${forward} days" \
    && exec node src/index.js >/tmp/clock-proof-api.log 2>&1) &
else
  (cd "$root/server" && exec node src/index.js >/tmp/clock-proof-api.log 2>&1) &
fi
api_pid=$!
trap 'kill "$api_pid" 2>/dev/null || true; wait "$api_pid" 2>/dev/null || true' EXIT INT TERM
for _ in $(seq 1 60); do
  if curl -fsS "http://127.0.0.1:${port}/api/health" >/dev/null 2>&1; then break; fi
  sleep 1
done
curl -fsS "http://127.0.0.1:${port}/api/health" >/dev/null || {
  echo "API did not become healthy" >&2
  cat /tmp/clock-proof-api.log >&2
  exit 1
}

export API_BASE="http://127.0.0.1:${port}/api" MIN_SYSTEM_CHECKS=35 MIN_NEGATIVE_TESTS=13
(cd "$root/server" && at node scripts/system-checks.mjs)
(cd "$root/server" && at node scripts/negative-tests.mjs)
if [ "$forward" -gt 0 ]; then
  echo "clock proof: PASS (forward +${forward} days)"
else
  echo "clock proof: PASS (anchor -${offset} days)"
fi
