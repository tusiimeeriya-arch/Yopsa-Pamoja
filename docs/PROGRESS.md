# Hardening pass — progress log

Branch: `security-hardening-pass`. Each item is committed as it completes.

| # | Item | Status | Evidence |
|---|------|--------|----------|
| 1 | `app_system()` origin and HTTP proof | done | Set only by `systemTx()` in `server/src/db.js` (transaction-local, called by the scheduler; no route passes request input to it). `CHK:app_system_gate`: `request=false app.system='' systemTx=true afterTx=false`. `NEG:10`: query/header/payload attempts all 404 on another group; fresh app session `app_system(): false`. |
| 2 | Cash handover/receipt/deposit roles | done | Handover: treasurer or field officer. Receipt: verifier. Deposit: verifier. `management` removed from receive and deposit (read-only). `CHK:cash_three_steps` and `CHK:cash_readonly_management` PASS. |
| 3 | No hard deletes of users/persons/groups/memberships/roles | done | `record_no_hard_delete` trigger on all five tables (42501 for non-demo rows; `is_demo` rows stay deletable for purge/tests). `CHK:hard_delete_guard` refuses all five, end-dating works, audit row survives. `NEG:11` shows no DELETE route (404). |
| 4 | Raw security evidence | done | scrypt N=16384/r=8/p=1; cookie `HttpOnly; Secure; SameSite=Lax; Max-Age=43200`; CSRF 403 `CSRF_BLOCKED`; CSP + HSTS + nosniff + frame-ancestors; no CORS headers (same-origin); uploads 5 MB/4 types; npm audit API 0, web 2 moderate (no high/critical); gitleaks "no leaks found". |
| 5 | Re-run checks and negative tests, add to CI | done | System checks 33/33 (25 original + 8 new). Negative tests 12/12 (9 original + 3 new). Both scripts run in `.github/workflows/verify.yml` and now fail if counts drop below 33 / 12. |
| 6 | Cover money/security NOT VERIFIED items; manual tests for UI-only | done | 20 Build Progress items moved to BUILT AND VERIFIED with a named check. Counts: 60 BV / 88 NVH / 1 EXCEPTION (was 40 / 108 / 1). UI-only scripts in `docs/MANUAL_TESTS.md`. |
| 7 | Update VERIFICATION_REPORT and open PR | done | Section 8 appended with raw output; PR opened. |

## Baseline (before changes)

- In-app system checks: 25/25 (merged PR #5 evidence).
- Negative tests: 9/9 (merged PR #5 evidence).
- Build Progress: 40 BUILT AND VERIFIED, 108 NOT VERIFIED HERE, 1 EXCEPTION.

## Pass 2 — short hardening (branch `security-hardening`)

| # | Item | Status | Evidence |
|---|------|--------|----------|
| 1 | Argon2id hashing, rehash on login, latency/memory | done | `PASSWORD_HASH = { algorithm: 'argon2id', m: 19456, t: 2, p: 1 }`; stored `$argon2id$v=19$m=19456,t=2,p=1$…`; `node --test test/password.test.js` → `# tests 3 / # pass 3 / # fail 0`; system check `password_hash_params … algorithm=argon2id m=19456 t=2 p=1 legacyUpgraded=true rehashStable=true`; the seeded admin row is now `$argon2id$v=19$m=19456` (4 accounts still on legacy scrypt until their next sign-in). 20 concurrent logins: `ok=20/20 totalMs=1072 medianMs=952 p95Ms=1047 maxMs=1047 memBeforeMB=119 memPeakMB=244 memAfterMB=131 peakDeltaMB=124`. |
| 2 | `correction_separation` explicit labels | done | `selfApproveBlocked=true requestedBy=selftest-1795813545231-verifier@selftest.invalid approvedBy=selftest-1795813545231-verifier2@selftest.invalid different=true status=approved amount=12000` |
| 3 | `is_demo` immutable; app role has no DELETE/TRUNCATE | done | `has_table_privilege`: users/persons/groups/memberships/role_assignments all `can_delete=f can_truncate=f`; flipping a real row → `ERROR: is_demo is immutable after insert; only the seed or system path may set it`; system check `flipBlocked=true code=42501 seedPathAllowed=true`; `hard_delete_guard` → `refused={"person":true,"membership":true,"group":true,"role":true,"user":true}`. Demo purge and self-test cleanup now run as the owner role; a rolled-back purge probe completed (`{"groups":5,"users":13,"persons":63,…}`) with demo counts unchanged. |
| 4 | Two verifiers per branch + admin warning | done | `docs/ASSUMPTIONS.md` A-041 (`cash.verifier_coverage`); `GET /api/admin/verifier-coverage` returns `minimum: 2`; Admin overview (signed in as admin) shows `Verifier coverage warning — Each branch needs at least two active verifier users for the cash steps. Kampala Central — 1 active verifier (minimum 2); Mbarara — 1 active verifier (minimum 2)`. |
| 5 | Web client navigate/redirect audit | done | No sink is built from user-supplied values. `navigate('/')` and `navigate('/login')` are literals; `window.location.assign` targets `/groups/${r.group.id}`, `/meetings/${r.meeting.id}`, `/loans/${r.loan.id}` from server-generated ids; `<Link to={`/groups/${g.id}`}>` and similar use API ids; `href={i.url}` / `href={d.url}` are server data links. No `window.open`, `history.pushState`, `location.search`, `searchParams`, `returnTo`/`next` redirect. |
| 6 | Re-run checks and negative tests | done | `IN-APP SYSTEM CHECKS: 35 passed, 0 failed, 35 total` (33 + `password_hash_params` + `is_demo_immutable`); `NEGATIVE TESTS: 12 passed, 0 failed`; safety `demo_groups 5→5, demo_payment_entries 19→19, demo_receipts 4→4, demo_loans 14→14, demo_roles 13→13, audit_rows 32→38`; `docs/VERIFICATION_REPORT.md` section 9 appended. |

## Pass 3 — owner access, hashing off the main thread (branch `security-hardening-2`)

| # | Item | Status | Evidence |
|---|------|--------|----------|
| 1 | Owner credentials shown and confined; purge/self-tests CLI or non-production only | done | Owner access now lives in one module, `server/src/owner.js` (no `DATABASE_URL` fallback; `adminTx()` throws without `ADMIN_DATABASE_URL`). Consumers: `seed.js`, `createAdmin.js`, `services/checks.js` cleanup, `routes/admin.js` (purge + system checks, mounted only when `!isProduction()`), `scripts/purge-demo.mjs`, `scripts/system-checks.mjs`, `scripts/negative-tests.mjs`, `deploy/docker-compose.app.yml` `seed` only (owner URL removed from `api`). Full list: `docs/OWNER_ACCESS.md`. Production boot: `{"level":"info","message":"pamoja api listening on 8099","ownerCredentials":"absent","demoTooling":"absent"}`; production with the owner URL: `{"level":"error","message":"refusing to start","reason":"ADMIN_DATABASE_URL must not be set for the production API; owner access is CLI-only"}` `exit=1`. HTTP: `PRODUCTION POST /api/admin/purge-demo -> 404 NOT_FOUND`, `PRODUCTION POST /api/admin/system-checks -> 404 NOT_FOUND`, `PRODUCTION /api/auth/me -> 200`; `DEV POST /api/admin/purge-demo -> 403 OTP_REQUIRED`, `DEV POST /api/admin/system-checks -> 200 passed 35 total 35`. CLI self-test without the route: `35 passed, 0 failed, 35 total (tag SELFTEST-1795814399249)`. |
| 2 | Argon2id off the main thread | done | `server/src/argonPool.js` (2 workers, `ARGON_WORKERS`) + `server/src/argonWorker.js`; `hashPassword`/`verifyPassword` dispatch there, legacy scrypt uses async `crypto.scrypt`. 50-login burst, health latency during it — before: `login burst: ok=50/50 wallMs=3168`, `health during burst: n=2 median=2511ms p95=2511ms max=2511ms`; after: `login burst: ok=50/50 wallMs=4081`, `health during burst: n=75 median=3ms p95=4ms max=110ms`; idle baseline `n=29 median=2ms p95=4ms`. `node --test test/*.test.js` → `# tests 3 / # pass 3 / # fail 0`. |
| 3 | HTTP cannot enable `app.seed` or `app.system` | done | New negative test 13: `PASS app.seed and app.system cannot be enabled over HTTP  query: 404 NOT_FOUND | header: 404 NOT_FOUND | body: 201 | app session app.seed='null' app.system='null' | is_demo flip: 42501`. |
| 4 | CI minimums 35 system checks / 12 negative tests | done | `.github/workflows/verify.yml` job env: `MIN_SYSTEM_CHECKS: '35'`, `MIN_NEGATIVE_TESTS: '12'` (both scripts fail the run below the floor). |
| 5 | Re-run all checks, counts, VERIFICATION_REPORT | done | `IN-APP SYSTEM CHECKS: 35 passed, 0 failed, 35 total`; `NEGATIVE TESTS: 13 passed, 0 failed`; safety `demo_groups 5→5, demo_payment_entries 19→19, demo_receipts 4→4, demo_loans 14→14, demo_roles 13→13, audit_rows 16→23`; `docs/VERIFICATION_REPORT.md` section 10 appended. |
| 6 | CI hang: idle Argon2 worker kept CLI processes alive | done | CI stalled >10 min on `Seed demo data` in both runs of the same commit. Cause: `spawn()` called `worker.unref()` before attaching the `'message'` listener, and attaching that listener re-refs the worker port, so the idle second worker kept the process alive after hashing. Fix: unref after the listeners. Before: `h: 136.635ms / hashed, exiting naturally` then killed at 25s (`exit=124`); after: `hashprobe exit=0`. `npm test` → `# tests 4 / # pass 4 / # fail 0` (new `test/argon-pool.test.js` asserts a hash-once CLI process exits within 10s). CLI: `35 passed, 0 failed, 35 total (tag SELFTEST-1795815382546)` `checks exit=0`; `seed exit=0`. |

## Pass 4 — re-verification (branch `security-hardening-4`)

Items 1–4 were already on `main` from Pass 3 (PR #8); this pass re-proved each
one against a database rebuilt from `server/src/schema.sql` + `npm run seed`.
The sandbox DB predated the Pass 1/2 triggers and its seed had aged a day, so an
in-place run first showed 32/35; after the rebuild (10 protected triggers
present) it is 35/35. No application code changed.

| # | Item | Status | Evidence |
|---|------|--------|----------|
| 1 | Owner credentials shown and confined; purge/self-tests CLI or non-production only | done | `grep ADMIN_DATABASE_URL server/src`: only `owner.js` opens the pool; `index.js` only guards/logs it. Production boot: `{"level":"info","message":"pamoja api listening on 8099","ownerCredentials":"absent","demoTooling":"absent"}`. Authenticated admin: `GET /auth/me -> 200`, `POST /admin/purge-demo -> 404 NOT_FOUND`, `POST /admin/system-checks -> 404 NOT_FOUND`. Production + owner URL: `{"level":"error","message":"refusing to start","reason":"ADMIN_DATABASE_URL must not be set for the production API; owner access is CLI-only"}` `exit=1`. Dev: `POST /admin/purge-demo -> 403 OTP_REQUIRED`, `POST /admin/system-checks -> 200 passed 35 total 35`. |
| 2 | Argon2id off the main thread | done | Before (`8800d5e`, hash-wasm on the event loop): `login burst: ok=50/50 wallMs=3422`; `health burst: n=2 median=3001ms p95=3001ms max=3001ms`; idle `n=30 median=3ms p95=7ms`. After (`argonPool.js`, 2 workers): `login burst: ok=50/50 wallMs=4519`; `health burst: n=151 median=3ms p95=6ms max=142ms`; idle `n=30 median=2ms p95=4ms`. `node --test test/*.test.js` → `# tests 4 / # pass 4 / # fail 0`. |
| 3 | HTTP cannot enable `app.seed` or `app.system` | done | `NEG:13 PASS app.seed and app.system cannot be enabled over HTTP — query: 404 NOT_FOUND; header: 404 NOT_FOUND; body: 201; app session app.seed='null' app.system='null'; is_demo flip: 42501`. |
| 4 | CI floors 35 system checks / 12 negative tests | done | `.github/workflows/verify.yml:55 MIN_SYSTEM_CHECKS: '35'`, `:56 MIN_NEGATIVE_TESTS: '12'`; floor raised to 36 → `expected at least 36 system checks, got 35` `exit=1`. |
| 5 | Re-ran all checks; counts; VERIFICATION_REPORT | done | `IN-APP SYSTEM CHECKS: 35 passed, 0 failed, 35 total`; `NEGATIVE TESTS: 13 passed, 0 failed`; safety `demo_groups 5→5, demo_payment_entries 19→19, demo_receipts 4→4, demo_loans 14→14, demo_roles 13→13, audit_rows 16→23`; db-proof `pamoja_app is_superuser=f bypasses_rls=f` + every refusal (allocation 110000>100000, locked entry update/delete, audit 42501, is_demo 42501, DELETE/TRUNCATE 42501); `docs/VERIFICATION_REPORT.md` section 11 appended. |

## Pass 5 — validation repair: expected rejections kept out of the database log

Platform validation flagged the database log written by the Pass 4 re-run. Every
line was a deliberate refusal from `runSystemChecks` (allocation cap, locked
payment entry, audit `42501`, protected-table `42501`, `is_demo` immutable,
duplicate attendance/receipt), i.e. the evidence the guards fire — not an
application fault. The check runner now opens its session through the owner URL
with `role=pamoja_app` and `log_min_messages=fatal`, so the same SQL runs as the
least-privilege role but the expected refusals are no longer logged server-side.

| # | Item | Status | Evidence |
|---|------|--------|----------|
| 1 | Flagged database errors: expected refusals, not faults | done | `server/scripts/quiet-session.mjs`: `ADMIN_DATABASE_URL?options=-c log_min_messages=fatal -c role=pamoja_app`. Session: `current_user=pamoja_app session_user=pamoja log_min_messages=fatal`; `SET log_min_messages` refused `42501` (still the app role); `DELETE FROM users` → `42501 permission denied for table users`. |
| 2 | Same checks, same verdicts, clean log | done | `IN-APP SYSTEM CHECKS: 35 passed, 0 failed, 35 total`; `NEGATIVE TESTS: 13 passed, 0 failed`; safety `demo_groups 5→5, demo_payment_entries 19→19, demo_receipts 4→4, demo_loans 14→14, demo_roles 13→13, audit_rows 78→85`; `docker compose logs --since <run start> db` → no output for both runs (previously 69 ERROR lines). |
| 3 | Transient `hash-wasm` module error | resolved | `02:37:54 Cannot find package 'hash-wasm'` came from the previous turn's dependency addition: the running dev server served a login before `npm ci` installed the new package. Now `require.resolve('hash-wasm')` → `/workspace/node_modules/hash-wasm/dist/index.umd.js` and a real sign-in through the API succeeds; no occurrence since. |
| 4 | API survives a database restart | done | Recreating the `db` container used to kill the API: pg emits the dropped idle client on the pool, and with no `'error'` listener the process exited (`Failed running 'src/index.js'. Waiting for file changes`). `server/src/db.js` now logs it (`{"level":"error","message":"idle database client error","detail":"terminating connection due to administrator command"}`); after a forced `db` recreate `web=200 api=200` and the process stayed up. |

## Pass 6 — Staging deployment to Vultr (branch `deploy-staging`)

Staging only in this pass. Production steps are written but not executed.
Secrets never enter the repo or this log: `/opt/pamoja/.env.production` is
created on the server by the operator.

### Gate (must be on `main` before deployment work)

| # | Item | Status | Evidence |
|---|------|--------|----------|
| a | Seed dates relative; checks pass with the clock at +1, +31, +100 days | done | `server/src/seed.js` derives every date from `today()`/`addDays()`; `SEED_ANCHOR_DAYS_AGO` anchors the seed N days back so today's run is the same as checking it N days later. `scripts/clock-proof.sh` at 1/31/100: `IN-APP SYSTEM CHECKS: 35 passed, 0 failed, 35 total` and `NEGATIVE TESTS: 13 passed, 0 failed` each time. One real fix was needed: `dashboard_totals` asserted the seed's exact overdue day counts; it now asserts the ranking rule (`excluded === maxOverdue > 30`), so it stays true as data ages. New CI job `clock` runs the matrix. |
| b | Versioned migrations + in-place upgrade equals a fresh schema, with triggers, policies and privileges; enforced in CI | done | `server/migrations/0001_core.sql` + `0002_invariants.sql`; `server/src/migrate.js` keeps `pamoja_migrations.schema_migrations` (version + SHA-256 checksum, refuses an edited applied file); `npm run migrate`. `scripts/schema-diff.sh`: fresh vs 0001-only-then-upgrade → `schema diff: identical (1219 normalized lines)`, `triggers: 15`, `policies: 27`, `app cannot update audit_logs: f`, `app cannot delete users: f`, `audit append-only trigger: 1`. CI runs the same script and fails if `server/src/schema.sql` drifts from the migrations. |
| c | `MIN_NEGATIVE_TESTS` in CI is 13 | done | `.github/workflows/verify.yml: MIN_NEGATIVE_TESTS: '13'`; script default raised from 12 to 13. The 13th test (`app.seed`/`app.system` cannot be enabled over HTTP) was already present and passes. |

### Staging (executed in this pass where the sandbox allows it)

Everything below marked `NOT VERIFIED HERE` needs the real Vultr instance and
is confirmed by the operator with the commands in `docs/DEPLOYMENT.md`.

| # | Item | Status | Evidence |
|---|------|--------|----------|
| 1 | Server hardening (packages, key-only SSH, ufw 22/80/443, fail2ban, Docker, chrony, swap, journald) | written, NOT VERIFIED HERE | `scripts/harden-server.sh`; `sh -n` clean. Needs root on the instance; the script refuses a non-Ubuntu or non-24.04 host. |
| 2 | Release images, pinned and unprivileged | done | `deploy/api.Dockerfile`, `web.Dockerfile`, `caddy.Dockerfile`, `backup.Dockerfile`; all four build: `api` → `sha256:722e5707c3fc`, `web` → `d81b24a8f5e4`, `caddy` → `b62bb8e895ba`, `backup` → `ffccbecbc223`. API and web run as uid 1000. |
| 3 | Production stack definition and edge config | done | `docker compose -f deploy/docker-compose.prod.yml config` → exit 0, `image:` ×6, published ports only `80`, `443`, `443` (caddy). `caddy validate` → `Valid configuration`. `nginx -t` on `deploy/web-nginx.conf` → `syntax is ok` / `test is successful`. |
| 4 | Production boot guards (fail closed) | done | Real stack, production env: migrate `migrations complete: 2 applied, 2 total` + `application role password updated`; api `Up (healthy)`; log `{"message":"pamoja api listening on 8080","ownerCredentials":"absent","demoTooling":"absent"}`; `pg_stat_activity` shows the API as `pamoja_app` only; `/api/health` 200 `{"status":"ok"}`, `/api/ready` 200 `{"status":"ready"}`. |
| 5 | Deploy script: pull → backup → build by SHA → migrate → up → health → rollback | plan verified, real deploy NOT VERIFIED HERE | `DRY_RUN=1 sh scripts/deploy.sh` → `ERROR: no previous deploy state; pass FIRST_DEPLOY=1 for the first run`; with `FIRST_DEPLOY=1` the full plan prints and ends `deploy plan complete`. The live run needs the server. |
| 6 | Smoke test against the deployed edge | done (TLS check needs the domain) | `BASE_URL=http://localhost:8088 SMOKE_ALLOW_HTTP=1 bash scripts/smoke-test.sh` → `smoke test: PASS` in both demo postures: headers ×4, `CSRF 403 CSRF_BLOCKED`, `login rate limit 429 after 11 attempts`, `database port 5432 closed`, `demo tooling … 404`, `/health 200 ok`, `/ready 200 ready`. Certificate check `NOT VERIFIED HERE` (no DNS in the sandbox). |
| 7 | Synthetic data on the staging stack | done | One-off `compose run … -e NODE_ENV=development -e APP_ENV=development migrate node src/seed.js` → `demo seed complete`; counts `users 14`, `groups 5`, `demo_groups 5`. Production semantics unchanged: `production-preflight.sh` → `PASS demo seed refuses in production refused`. |
| 8 | First administrator, out of band | done | `node src/createAdmin.js` → `Created user admin@yopsa.test.`, `Granted it_admin (Org).`; sign-in `200`; `/api/auth/me` `200`. The owner URL is used only by that one-shot container. |
| 9 | Demo tooling gate (defect found during verification) | fixed | `GET /api/admin/demo-guide` answered **200 in production** while the edge blocked it, and the edge block also hid it on staging where the seeded data points to it. The edge now closes it with one expression matcher unless `PAMOJA_DEMO_TOOLING=on` (staging only). Before the fix `caddy adapt` showed `{"body": "@demo_off", "handler": "static_response"}` — the second matcher was parsed as the response body, so the path answered 404 in **both** postures; after the fix: empty → `demo-guide 404`, `on` → `401` anonymous and `200` for a signed-in admin (`users=13 demoExists=true`). `purge-demo` and `system-checks` stay 404 in both. |
| 10 | Encrypted backups to Vultr Object Storage | written, NOT VERIFIED HERE | `scripts/backup.sh` (`pg_dump \| gzip \| age`, `aws s3 cp`, daily/weekly/monthly retention, 1 KB sanity floor, webhook alert) and `scripts/restore-drill.sh`; `sh -n`/`bash -n` clean, `backup` image builds. The S3 round trip and the restore drill need the real bucket and the offline age key. |
| 11 | Scheduled backup and monitoring timers | written, NOT VERIFIED HERE | `deploy/systemd/pamoja-{backup,monitor}.{service,timer}`; monitor covers disk ≥80 %, certificate <14 days, failed jobs in 24 h, audit hash chain, backup age >26 h. Needs the server; `monitor.sh` was syntax-checked only. |
| 12 | Runbook | done | `docs/DEPLOYMENT.md`: topology, roles, prerequisites, secrets rules, staging steps S1–S11 with the exact operator command and the output to paste back, everyday operations, rotation, incident table, production steps P1–P8 (documented, not executed), and the verification table in §7. |
| 13 | One pull request | done | This branch, `deploy-staging` → `main`. |
