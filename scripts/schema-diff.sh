#!/bin/sh
# Prove that an old-schema database upgrades in place to exactly the schema a
# fresh install gets, and that triggers, policies and privileges survive.
#
#   ADMIN_DATABASE_URL=postgres://owner:...@host:5432/pamoja scripts/schema-diff.sh
#
# Needs psql, pg_dump and node with server/node_modules installed. CI runs this
# on every push; the two temporary databases are dropped on exit.
set -eu
: "${ADMIN_DATABASE_URL:?ADMIN_DATABASE_URL is required}"
root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
stamp="$$_$(date +%s)"
fresh="pamoja_fresh_${stamp}"
old="pamoja_old_${stamp}"
base="${ADMIN_DATABASE_URL%%\?*}"
prefix="${base%/*}"
fresh_url="${prefix}/${fresh}"
old_url="${prefix}/${old}"

cleanup() {
  psql "$ADMIN_DATABASE_URL" -q -c "DROP DATABASE IF EXISTS ${fresh}" >/dev/null 2>&1 || true
  psql "$ADMIN_DATABASE_URL" -q -c "DROP DATABASE IF EXISTS ${old}" >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

psql "$ADMIN_DATABASE_URL" -q -v ON_ERROR_STOP=1 -c "CREATE DATABASE ${fresh}"
psql "$ADMIN_DATABASE_URL" -q -v ON_ERROR_STOP=1 -c "CREATE DATABASE ${old}"

# Fresh: every migration, in order, through the same runner production uses.
(cd "$root/server" && ADMIN_DATABASE_URL="$fresh_url" node src/migrate.js >/tmp/schema-diff-fresh.log)
echo "fresh: $(tail -1 /tmp/schema-diff-fresh.log)"

# Old: only the first migration, then the upgrade.
psql "$old_url" -q -v ON_ERROR_STOP=1 -f "$root/server/migrations/0001_core.sql" >/dev/null
(cd "$root/server" && ADMIN_DATABASE_URL="$old_url" node src/migrate.js >/tmp/schema-diff-old.log)
echo "old->head: $(tail -1 /tmp/schema-diff-old.log)"

normalize() {
  pg_dump "$1" --schema-only --no-owner --no-privileges \
    | grep -v '^--' | grep -v '^$' | grep -v '^\\restrict' | grep -v '^\\unrestrict'
}
normalize "$fresh_url" >/tmp/schema-diff-fresh.sql
normalize "$old_url" >/tmp/schema-diff-old.sql

if ! diff -u /tmp/schema-diff-fresh.sql /tmp/schema-diff-old.sql >/tmp/schema-diff.patch; then
  echo "SCHEMA DIFF FAILED: an upgraded database differs from a fresh one" >&2
  head -80 /tmp/schema-diff.patch >&2
  exit 1
fi
echo "schema diff: identical ($(wc -l </tmp/schema-diff-fresh.sql) normalized lines)"

check() {
  got="$(psql "$old_url" -tA -v ON_ERROR_STOP=1 -c "$2")"
  if [ "$got" != "$3" ]; then
    echo "FAIL  $1: expected '$3', got '$got'" >&2
    exit 1
  fi
  echo "PASS  $1: $got"
}

denied() { # label, privilege, table
  got="$(psql "$old_url" -tA -v ON_ERROR_STOP=1 -c "SELECT has_table_privilege('pamoja_app','$3','$2')")"
  if [ "$got" != "f" ]; then
    echo "FAIL  pamoja_app $2 on $3: expected f, got $got" >&2
    exit 1
  fi
  echo "PASS  pamoja_app has no $2 on $3 (has_table_privilege = f)"
}

refused() { # label, statement
  set +e
  out="$(psql "$old_url" -v ON_ERROR_STOP=1 -c "SET ROLE pamoja_app; $2" 2>&1)"
  rc=$?
  set -e
  if [ "$rc" -eq 0 ]; then
    echo "FAIL  $1: statement succeeded as pamoja_app" >&2
    exit 1
  fi
  case "$out" in
    *"permission denied for table"*)
      echo "PASS  $1 refused: $(printf '%s' "$out" | grep -o 'ERROR:.*' | head -1)" ;;
    *)
      echo "FAIL  $1: not refused for the expected reason: $out" >&2
      exit 1 ;;
  esac
}

check "triggers" "SELECT count(*) FROM pg_trigger WHERE NOT tgisinternal" "15"
check "policies" "SELECT count(*) FROM pg_policies" "27"
denied "audit_logs UPDATE" UPDATE audit_logs
denied "audit_logs DELETE" DELETE audit_logs
denied "audit_logs TRUNCATE" TRUNCATE audit_logs
denied "users DELETE" DELETE users
refused "raw UPDATE audit_logs as pamoja_app" "UPDATE audit_logs SET action = action"
refused "raw DELETE audit_logs as pamoja_app" "DELETE FROM audit_logs"
check "audit append-only trigger" "SELECT count(*) FROM pg_trigger WHERE tgname='trg_audit_no_update'" "1"
check "tenant policy on payments" "SELECT count(*) FROM pg_policies WHERE tablename='payment_entries' AND policyname='tenant_scope'" "1"
echo "schema diff: PASS"
