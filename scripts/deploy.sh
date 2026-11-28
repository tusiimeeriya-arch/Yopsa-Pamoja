#!/bin/sh
# Deploy one commit to a Pamoja server.
#
#   sudo -u pamoja-deploy /opt/pamoja/app/scripts/deploy.sh
#   DRY_RUN=1 scripts/deploy.sh          # print the plan, change nothing
#
# Order: pull -> back up -> build -> migrate -> start -> health check. If the
# health check fails, the previous image tags are restored and started again.
# Migrations are forward-only; a rollback restores the application image, so a
# release whose migration is not backward-compatible needs a forward fix.
set -eu
PROJECT_NAME="${COMPOSE_PROJECT_NAME:-pamoja}"

APP_DIR="${APP_DIR:-/opt/pamoja/app}"
ENV_FILE="${ENV_FILE:-/opt/pamoja/.env.production}"
BRANCH="${DEPLOY_BRANCH:-deploy-staging}"
STATE_FILE="${STATE_FILE:-/opt/pamoja/.deploy-state}"
DRY_RUN="${DRY_RUN:-0}"
FIRST_DEPLOY="${FIRST_DEPLOY:-0}"
HEALTH_TIMEOUT="${HEALTH_TIMEOUT:-120}"
LOCK_FILE="${LOCK_FILE:-/tmp/pamoja-deploy.lock}"

log() { printf '%s %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*"; }
die() { log "ERROR: $*" >&2; exit 1; }
run() {
  if [ "$DRY_RUN" = "1" ]; then
    printf 'DRY-RUN %s\n' "$*"
    return 0
  fi
  "$@"
}

compose() {
  docker compose --project-name "$PROJECT_NAME" --env-file "$ENV_FILE" \
    -f "$APP_DIR/deploy/docker-compose.prod.yml" "$@"
}

[ -d "$APP_DIR/.git" ] || die "APP_DIR is not a git checkout: $APP_DIR"
[ -f "$APP_DIR/deploy/docker-compose.prod.yml" ] || die "compose file missing"
if [ "$DRY_RUN" != "1" ]; then
  [ -f "$ENV_FILE" ] || die "environment file missing: $ENV_FILE"
  [ "$(id -u)" -ne 0 ] || die "run as the unprivileged deploy user, not root"
  command -v docker >/dev/null || die "docker is not installed"
  command -v curl >/dev/null || die "curl is not installed"
fi

# One deploy at a time. flock is in util-linux, present on Ubuntu 24.04.
if [ "$DRY_RUN" != "1" ] && command -v flock >/dev/null; then
  exec 9>"$LOCK_FILE"
  flock -n 9 || die "another deploy is running"
fi

cd "$APP_DIR"

if [ -f "$ENV_FILE" ]; then
  # Only non-secret values are read here; secrets stay in the file and reach
  # the containers through Compose.
  PAMOJA_DOMAIN="$(sed -n 's/^PAMOJA_DOMAIN=//p' "$ENV_FILE" | tail -1)"
fi
[ -n "${PAMOJA_DOMAIN:-}" ] || die "PAMOJA_DOMAIN is not set in $ENV_FILE"
BASE_URL="https://${PAMOJA_DOMAIN}"

PREV_SHA=""
if [ -f "$STATE_FILE" ]; then
  PREV_SHA="$(cat "$STATE_FILE")"
fi

log "deploy start branch=$BRANCH previous=${PREV_SHA:-none} dry_run=$DRY_RUN"

# 1. Pull the commit to deploy.
run git fetch --prune origin
run git checkout "$BRANCH"
run git pull --ff-only origin "$BRANCH"
SHA="$(git rev-parse --short=12 HEAD)"
[ -n "$SHA" ] || die "cannot resolve HEAD"
log "commit $SHA $(git log -1 --pretty=%s)"

# 2. Back up before anything touches the database. The first deploy has no
#    database to back up; pass FIRST_DEPLOY=1 once, then never again.
if [ "$FIRST_DEPLOY" = "1" ]; then
  log "first deploy: skipping the pre-deploy backup"
elif [ -z "$PREV_SHA" ]; then
  die "no previous deploy state; pass FIRST_DEPLOY=1 for the first run"
else
  log "pre-deploy backup"
  run compose run --rm backup || die "backup failed; not migrating"
fi

# 3. Build and tag the release images by commit.
export API_IMAGE="pamoja-api:${SHA}"
export WEB_IMAGE="pamoja-web:${SHA}"
export CADDY_IMAGE="pamoja-caddy:${SHA}"
export BACKUP_IMAGE="pamoja-backup:${SHA}"
log "build images ${SHA}"
run compose build api web caddy backup

# 4. Migrate as the owner, through the one-shot service.
log "migrate"
run compose run --rm migrate || die "migration failed; the old release is still running"

# 5. Start the release.
log "start ${SHA}"
run compose up -d --remove-orphans || die "compose up failed"

wait_healthy() {
  elapsed=0
  while [ "$elapsed" -lt "$HEALTH_TIMEOUT" ]; do
    if curl -fsS --max-time 5 "${BASE_URL}/health" >/dev/null 2>&1 \
      && curl -fsS --max-time 5 "${BASE_URL}/ready" >/dev/null 2>&1; then
      return 0
    fi
    sleep 2
    elapsed=$((elapsed + 2))
  done
  return 1
}

if [ "$DRY_RUN" = "1" ]; then
  log "DRY-RUN would poll ${BASE_URL}/health and /ready for ${HEALTH_TIMEOUT}s"
  log "DRY-RUN would write ${SHA} to ${STATE_FILE}"
  log "deploy plan complete"
  exit 0
fi

# 6. Health check, with rollback to the previous image.
log "health check ${BASE_URL}"
if wait_healthy; then
  printf '%s\n' "$SHA" > "$STATE_FILE"
  log "deploy ok: ${SHA}"
  if [ "${RUN_SMOKE:-1}" = "1" ] && [ -x "$APP_DIR/scripts/smoke-test.sh" ]; then
    log "smoke test"
    BASE_URL="$BASE_URL" "$APP_DIR/scripts/smoke-test.sh" || {
      log "SMOKE TEST FAILED for ${SHA}; investigate before announcing the release" >&2
      exit 1
    }
  fi
  exit 0
fi

log "health check failed for ${SHA}" >&2
if [ -z "$PREV_SHA" ]; then
  die "no previous release to roll back to"
fi
log "rolling back to ${PREV_SHA}" >&2
export API_IMAGE="pamoja-api:${PREV_SHA}"
export WEB_IMAGE="pamoja-web:${PREV_SHA}"
export CADDY_IMAGE="pamoja-caddy:${PREV_SHA}"
export BACKUP_IMAGE="pamoja-backup:${PREV_SHA}"
compose up -d --remove-orphans || die "rollback start failed"
if wait_healthy; then
  log "rolled back to ${PREV_SHA}" >&2
else
  log "ROLLBACK ALSO UNHEALTHY: manual intervention required" >&2
fi
exit 1
