#!/bin/sh
# Send one test message through the API container's configured provider.
#
#   TEST_DELIVERY_TO=+2567... sh scripts/test-delivery.sh
#
# On the server it uses the same compose project and environment file as the
# deployment. Exits non-zero when no provider is configured or delivery fails.
set -eu
PROJECT_NAME="${COMPOSE_PROJECT_NAME:-pamoja}"
APP_DIR="${APP_DIR:-/opt/pamoja/app}"
ENV_FILE="${ENV_FILE:-/opt/pamoja/.env.production}"
: "${TEST_DELIVERY_TO:?set TEST_DELIVERY_TO to a phone number or address you control}"

docker compose --project-name "$PROJECT_NAME" --env-file "$ENV_FILE" \
  -f "$APP_DIR/deploy/docker-compose.prod.yml" \
  exec -T -e TEST_DELIVERY_TO="$TEST_DELIVERY_TO" api \
  node scripts/test-delivery.mjs
