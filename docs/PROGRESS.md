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
