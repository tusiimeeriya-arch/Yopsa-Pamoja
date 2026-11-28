#!/bin/sh
# Regenerate server/src/schema.sql from the versioned migrations.
#
# server/src/schema.sql is a read-only snapshot kept for the local/Base44
# preview stack and for anyone who wants one file to inspect. The migrations in
# server/migrations/ are the source of truth; CI fails when the snapshot and
# the migrations disagree.
set -eu
root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
out="$root/server/src/schema.sql"
tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT

{
  echo "-- GENERATED FILE - do not edit by hand."
  echo "-- Source of truth: server/migrations/*.sql"
  echo "-- Regenerate with: scripts/build-schema.sh"
  echo
  for f in "$root"/server/migrations/*.sql; do
    echo "-- ===== $(basename "$f") ====="
    cat "$f"
    echo
  done
} > "$tmp"

mv "$tmp" "$out"
echo "wrote $out"
