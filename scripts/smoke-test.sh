#!/bin/bash
# Post-deploy smoke test. Run against staging after every deploy:
#
#   BASE_URL=https://pamoja-staging.duckdns.org sh scripts/smoke-test.sh
#
# For a local plain-HTTP stack (no certificate) set SMOKE_ALLOW_HTTP=1.
# Optional: SMOKE_LOCKOUT_EMAIL=<dedicated test account> to prove account
# lockout; without it the script proves the per-IP login rate limit instead.
set -uo pipefail
BASE_URL="${BASE_URL:?set BASE_URL}"
ALLOW_HTTP="${SMOKE_ALLOW_HTTP:-0}"
CURL_INSECURE="${SMOKE_INSECURE:-0}"
CERT_MIN_DAYS="${SMOKE_MIN_CERT_DAYS:-14}"
CURL_OPTS=""
[ "$CURL_INSECURE" = "1" ] && CURL_OPTS="-k"
FAILED=0
HOST="$(printf '%s' "$BASE_URL" | sed -e 's#^https\?://##' -e 's#/.*##' -e 's#:.*##')"

pass() { printf 'PASS  %-42s %s\n' "$1" "$2"; }
fail() { printf 'FAIL  %-42s %s\n' "$1" "$2"; FAILED=1; }
code() { curl $CURL_OPTS -sS -o /dev/null -w '%{http_code}' --max-time 10 "$@" 2>/dev/null || echo 000; }
body() { curl $CURL_OPTS -sS --max-time 10 "$@" 2>/dev/null || true; }

# 1. HTTPS and certificate validity.
if printf '%s' "$BASE_URL" | grep -q '^https://'; then
  if curl $CURL_OPTS -sS -o /dev/null --max-time 15 "$BASE_URL/health"; then
    end="$(openssl s_client -servername "$HOST" -connect "${HOST}:443" </dev/null 2>/dev/null | openssl x509 -noout -enddate 2>/dev/null | sed 's/^notAfter=//')"
    days=$(( ( $(date -d "$end" +%s 2>/dev/null || echo 0) - $(date +%s) ) / 86400 ))
    if [ "$days" -ge "$CERT_MIN_DAYS" ]; then pass "HTTPS certificate" "${days} days remaining"; else fail "HTTPS certificate" "${days} days remaining"; fi
  else
    fail "HTTPS certificate" "TLS request failed"
  fi
elif [ "$ALLOW_HTTP" = "1" ]; then
  pass "HTTPS certificate" "skipped (SMOKE_ALLOW_HTTP=1)"
else
  fail "HTTPS certificate" "BASE_URL is not https"
fi

# 2. Security headers.
headers="$(curl $CURL_OPTS -sSI --max-time 10 "$BASE_URL/" 2>/dev/null || true)"
for h in "x-content-type-options: nosniff" "referrer-policy: strict-origin-when-cross-origin" "content-security-policy:" "x-frame-options:"; do
  if printf '%s' "$headers" | tr 'A-Z' 'a-z' | grep -q "^${h}"; then pass "header ${h%%:*}" "present"; else fail "header ${h%%:*}" "missing"; fi
done
if printf '%s' "$BASE_URL" | grep -q '^https://'; then
  if printf '%s' "$headers" | tr 'A-Z' 'a-z' | grep -q '^strict-transport-security:'; then pass "header strict-transport-security" "present"; else fail "header strict-transport-security" "missing"; fi
fi

# 3. CSRF: a state-changing request without the client header is refused.
csrf_body="$(body -X POST -H 'content-type: application/json' -d '{"email":"smoke@invalid","password":"x"}' "$BASE_URL/api/auth/login")"
csrf_code="$(code -X POST -H 'content-type: application/json' -d '{"email":"smoke@invalid","password":"x"}' "$BASE_URL/api/auth/login")"
if [ "$csrf_code" = "403" ] && printf '%s' "$csrf_body" | grep -q 'CSRF_BLOCKED'; then
  pass "CSRF" "403 CSRF_BLOCKED"
else
  fail "CSRF" "${csrf_code} ${csrf_body:0:80}"
fi

# 4. Login lockout (dedicated account) or the per-IP rate limit.
if [ -n "${SMOKE_LOCKOUT_EMAIL:-}" ]; then
  last=""
  for _ in 1 2 3 4 5 6; do
    last="$(code -X POST -H 'content-type: application/json' -H 'x-pamoja-client: web' \
      -d "{\"email\":\"${SMOKE_LOCKOUT_EMAIL}\",\"password\":\"wrong-password\"}" "$BASE_URL/api/auth/login")"
  done
  if [ "$last" = "423" ] || [ "$last" = "429" ]; then pass "login lockout" "final status ${last}"; else fail "login lockout" "final status ${last}"; fi
else
  last=""
  # The login limiter is keyed by address + account, so repeat one account.
  for _ in $(seq 1 11); do
    last="$(code -X POST -H 'content-type: application/json' -H 'x-pamoja-client: web' \
      -d '{"email":"smoke-rate@invalid","password":"wrong-password"}' "$BASE_URL/api/auth/login")"
  done
  if [ "$last" = "429" ]; then pass "login rate limit" "429 after 11 attempts"; else fail "login rate limit" "final status ${last}"; fi
fi

# 5. PostgreSQL is not reachable from the internet.
if timeout 5 bash -c "cat < /dev/null > /dev/tcp/${HOST}/5432" 2>/dev/null; then
  fail "database port 5432" "OPEN from the internet"
else
  pass "database port 5432" "closed"
fi

# 6. Demo tooling is absent.
for path in /api/admin/purge-demo /api/admin/system-checks /api/admin/demo-guide; do
  c="$(code "$BASE_URL$path")"
  if [ "$c" = "404" ]; then pass "demo tooling ${path}" "404"; else fail "demo tooling ${path}" "$c"; fi
done

# 7. Health and readiness.
h="$(code "$BASE_URL/health")"
hb="$(body "$BASE_URL/health")"
r="$(code "$BASE_URL/ready")"
rb="$(body "$BASE_URL/ready")"
if [ "$h" = "200" ] && printf '%s' "$hb" | grep -q '"status":"ok"'; then pass "/health" "200 ok"; else fail "/health" "${h} ${hb:0:80}"; fi
if [ "$r" = "200" ] && printf '%s' "$rb" | grep -q '"status":"ready"'; then pass "/ready" "200 ready"; else fail "/ready" "${r} ${rb:0:80}"; fi

[ "$FAILED" -eq 0 ] && echo "smoke test: PASS" || { echo "smoke test: FAIL"; exit 1; }
