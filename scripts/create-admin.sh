#!/bin/sh
# Create the first organisation administrator, once, out of band.
#
#   ADMIN_EMAIL=you@yopsa.co.ug ADMIN_NAME="Your Name" sh scripts/create-admin.sh
#
# A strong one-time password is generated and printed ONCE if ADMIN_PASSWORD is
# not supplied. Sign in immediately and change it; the current schema has no
# forced-change flag, so the change is an operational step in the go-live
# checklist, not something this script can enforce in the app. Sensitive admin
# actions already require a one-time code (guardOtp) once a provider is set.
#
# The owner role is used only by this one-shot container; the API never sees it.
set -eu
PROJECT_NAME="${COMPOSE_PROJECT_NAME:-pamoja}"
APP_DIR="${APP_DIR:-/opt/pamoja/app}"
ENV_FILE="${ENV_FILE:-/opt/pamoja/.env.production}"
: "${ADMIN_EMAIL:?set ADMIN_EMAIL}"
: "${ADMIN_NAME:?set ADMIN_NAME}"

if [ -z "${ADMIN_PASSWORD:-}" ]; then
  # 24 random bytes -> 32 base64url characters; add a symbol and a digit so the
  # existing strength check (12+, upper, lower, digit, symbol) always passes.
  ADMIN_PASSWORD="$(openssl rand -base64 24 | tr -d '\n=' | tr '+/' '-_')Aa1!"
  GENERATED=1
fi

docker compose --project-name "$PROJECT_NAME" --env-file "$ENV_FILE" \
  -f "$APP_DIR/deploy/docker-compose.prod.yml" \
  run --rm --no-deps -T \
  -e ADMIN_EMAIL="$ADMIN_EMAIL" -e ADMIN_NAME="$ADMIN_NAME" -e ADMIN_PASSWORD="$ADMIN_PASSWORD" \
  migrate node src/createAdmin.js

if [ "${GENERATED:-0}" = "1" ]; then
  cat <<EOF

One-time password for ${ADMIN_EMAIL} (shown once, not stored by this script):

  ${ADMIN_PASSWORD}

Sign in now and change it. Do not paste it into chat, tickets or logs.
EOF
fi
