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
