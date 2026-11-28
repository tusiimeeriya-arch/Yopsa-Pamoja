#!/bin/sh
# Production preflight: prove that production has no demo data, no demo
# tooling, no seed path and no owner credential in the API.
#
#   BASE_URL=https://pamoja.example.ug sh scripts/production-preflight.sh
#
# Run after the first production deploy, before real member data is loaded.
set -eu
CURL_OPTS=""
[ "${PREFLIGHT_INSECURE:-0}" = "1" ] && CURL_OPTS="-k"
PROJECT_NAME="${COMPOSE_PROJECT_NAME:-pamoja}"
APP_DIR="${APP_DIR:-/opt/pamoja/app}"
ENV_FILE="${ENV_FILE:-/opt/pamoja/.env.production}"
: "${BASE_URL:?set BASE_URL to the production HTTPS origin}"

compose() {
  docker compose --project-name "$PROJECT_NAME" --env-file "$ENV_FILE" \
    -f "$APP_DIR/deploy/docker-compose.prod.yml" "$@"
}
db_name="$(sed -n 's/^POSTGRES_DB=//p' "$ENV_FILE" | tail -1)"
db_user="$(sed -n 's/^POSTGRES_USER=//p' "$ENV_FILE" | tail -1)"
FAILED=0
check() {
  name="$1" expected="$2" actual="$3"
  if [ "$actual" = "$expected" ]; then
    printf 'PASS  %-46s %s\n' "$name" "$actual"
  else
    printf 'FAIL  %-46s expected %s, got %s\n' "$name" "$expected" "$actual"
    FAILED=1
  fi
}

# 1. No demo rows of any kind.
for table in users persons groups memberships role_assignments payment_entries; do
  n="$(compose exec -T postgres psql -U "$db_user" -d "$db_name" -tAc "SELECT count(*) FROM ${table} WHERE is_demo")"
  check "no demo rows in ${table}" "0" "$n"
done

# 2. The seed refuses to run with production semantics.
seed_out="$(compose run --rm -T -e NODE_ENV=production -e APP_ENV=production migrate node src/seed.js 2>&1 || true)"
case "$seed_out" in
  *"Refusing to load demo data in production"*) check "demo seed refuses in production" "refused" "refused" ;;
  *) check "demo seed refuses in production" "refused" "NOT REFUSED: ${seed_out}" ;;
esac

# 3. The API process holds no owner credential.
owner="$(compose exec -T api sh -c 'printenv ADMIN_DATABASE_URL >/dev/null 2>&1 && echo present || echo absent')"
check "API has no owner credential" "absent" "$owner"

# 4. Demo tooling answers 404 at the edge.
for path in /api/admin/purge-demo /api/admin/system-checks /api/admin/demo-guide; do
  code="$(curl $CURL_OPTS -sS -o /dev/null -w '%{http_code}' "${BASE_URL}${path}" || echo 000)"
  check "demo tooling 404 ${path}" "404" "$code"
done

# 5. Health and readiness.
check "/health" "200" "$(curl $CURL_OPTS -sS -o /dev/null -w '%{http_code}' "${BASE_URL}/health" || echo 000)"
check "/ready" "200" "$(curl $CURL_OPTS -sS -o /dev/null -w '%{http_code}' "${BASE_URL}/ready" || echo 000)"

[ "$FAILED" -eq 0 ] && echo "production preflight: PASS" || { echo "production preflight: FAIL"; exit 1; }
