# Verification report

Branch `system-integrity-audit`. Every block below is output copied from a run
against the compose stack on this branch; nothing here is asserted from reading
the code alone.

Stack: `db` (PostgreSQL 16), `migrate` (schema.sql), `api` (Node 22 + Express),
`web` (Vite). All checks were run inside the running containers.

---

## 1. Test integrity

This section lists every change made to a check or acceptance script after it
first failed, so a reviewer can confirm none of them made a failing assertion
pass.

| Check | Before | After | Loosened? |
| --- | --- | --- | --- |
| `idempotency` in `server/src/services/checks.js` | `e2.replayed === true && e1.entry.id === e2.entry.id` | `e1.entry.id === e2.entry.id && JSON.stringify(e1) === JSON.stringify(e2)` | **No — tightened.** The replay must now return a byte-identical payload, not just set a `replayed` flag. |
| `Replayed request` in `server/scripts/negative-tests.mjs` | `replay.ok && replay.body.replayed === true && same id` | `replay.status === first.status && replay.text === first.text` | **No — tightened.** Raw response text and status must match the first call. |
| `Member reading another member's payments` | one group asserted invisible | the same test now also asserts a member of the *other* group cannot read Group A | **No — added assertion.** |
| Test-data safety snapshot | none | before/after counts of demo groups, payments, receipts, loans, roles, plus audit rows that may only grow | **Added check** (new, not a replacement). |
| Cleanup loop | deleted with whatever ids the run had | skips a cleanup statement whose id is missing and reports it | Not an assertion: it prevents a delete from falling back to a broad filter, which would be a data-safety hazard. The safety snapshot above is what proves no demo row moved. |

### What "backfilled attendance" changed

The seed used to skip absent register entries:

```js
if (status === 'absent') continue;          // removed
```

so no `attendance_records` row ever said `absent`. The demo dispute was then
built from a fallback that took *any* attendance row for the group, which meant
the dispute claimed "the register shows me absent" against a record that said
`present` — the seed was producing a false fixture.

The fix inserts every status (absent included) and selects a record that really
is absent, for that record's own person:

```sql
SELECT a.*, p.linked_user_id FROM attendance_records a
  JOIN meetings m ON m.id = a.meeting_id
  JOIN persons p ON p.id = a.person_id
 WHERE m.group_id = $1 AND a.status = 'absent'
 ORDER BY (p.linked_user_id IS NULL), a.person_id LIMIT 1
```

If no absent record exists the seed now throws instead of inventing one. The
seed is not tuned to any check: the attendance formula check computes its own
in-memory fixture, and the dispute fixture only has to be truthful.

---

## 2. Database proofs as the application role

Run as `pamoja_app` — the role the API connects with, which is **not** the table
owner and **not** a superuser:

```
docker exec -i -e PGPASSWORD=... <db> psql -U pamoja_app -d pamoja < scripts/db-proof.sql
```

```
== role and privileges ==
    role    | is_superuser | bypasses_rls
------------+--------------+--------------
 pamoja_app | f            | f

== 1. allocation total cannot exceed the receipt ==
-- 80,000 allocated of 100,000. Now try to allocate 30,000 more (must be refused):
ERROR:  allocation of 30000 would make the total 110000 exceed the receipt of 100000
CONTEXT:  PL/pgSQL function enforce_allocation_cap() line 13 at RAISE

== 2. locked / verified payment rows cannot be updated or deleted ==
-- update the amount (must be refused):
ERROR:  payment entry 2222... is locked: amount, member, category and date cannot be edited; request a correction
-- delete the row (must be refused):
ERROR:  payment entry 2222... is locked and cannot be deleted; record a correction instead
-- move it to an unknown status (must be refused):
ERROR:  payment entry 2222... cannot move from locked to cancelled

== 3. AuditLog rows cannot be updated, deleted or truncated (app role) ==
-- update an audit row (must be refused):
ERROR:  permission denied for table audit_logs
-- delete an audit row (must be refused):
ERROR:  permission denied for table audit_logs
-- truncate the audit log (must be refused):
ERROR:  permission denied for table audit_logs

== 4. cross-group reads return nothing ==
-- payment entries in the member's own group (Tukolere Wamu VSLA):
 own_group_entries = 10
-- payment entries in a group the member does not belong to (Nkumba Investment Club):
 other_group_entries = 0
-- every group the member can see:
 Tukolere Wamu VSLA
-- persons in a group the member does not belong to (must be 0):
 other_group_persons = 0
```

All four guarantees are enforced in the database (triggers
`enforce_allocation_cap`, `payment_entry_guard`, `audit_logs_append_only`, plus
RLS policies on every tenant table), not only in application code. The fixtures
above are created inside a transaction and rolled back.

### Owner-level append-only proof

The app role is stopped by privileges; the owner holds the grant and is stopped
by the trigger:

```
== append-only trigger as the table owner (grant present) ==
  role  | can_update
--------+------------
 pamoja | t
ERROR:  audit_logs is append-only: TRUNCATE is not permitted
```

`UPDATE`/`DELETE` on the owner return `UPDATE 0` / `DELETE 0` because the
append-only trigger rejects any row operation; `TRUNCATE` raises outright.

### Demo purge with a non-demo group on a demo branch

A group created through the UI is not demo-tagged, so it can sit on a demo
branch. Purging demo branches unconditionally aborted the whole purge on
`groups_branch_id_fkey`; the purge now skips any demo branch a group still
references. Proof, run inside a transaction that is rolled back:

```
non-demo marker group on a demo branch: c6834237-...
OLD unconditional delete rejected: update or delete on table "org_branches"
  violates foreign key constraint "groups_branch_id_fkey" on table "groups"
NEW purge succeeded; branches removed = 1 ; groups removed = 5
demo branches still present (referenced by non-demo group) = 1
ROLLED BACK — no data changed
```

After the rollback the database was unchanged (`groups=5`, `branches=2`,
`markers=0`).

---

## 3. In-app system checks

```
docker exec app-api-1 node scripts/system-checks.mjs
```

`25 passed, 0 failed, 25 total`. Highlights:

```
PASS  allocation_cap         Allocation can never exceed the receipt — rejected: CONFLICT
PASS  self_verification      Nobody verifies their own submission — FORBIDDEN
PASS  verifier_recorded      A verifier who recorded an entry cannot verify — FORBIDDEN
PASS  idempotency            repeat returns the identical response, changed payload rejected
PASS  cross_group            A member cannot read an unrelated group — FORBIDDEN
PASS  it_admin_limits        IT Admin cannot verify or approve — FORBIDDEN
PASS  disburser_separation   The approver cannot also disburse — FORBIDDEN
PASS  allocation_cap_db      Database trigger rejects an allocation above the receipt — 23514
PASS  locked_entry_db        Database trigger blocks editing and deleting a locked entry — 23514
PASS  audit_append_only_db   AuditLog rows cannot be updated or deleted — 42501
PASS  cash_three_steps       Cash: handover, branch receipt and deposit are three steps by three people
                             — final reconciled; same-person steps rejected
PASS  member_loans_separate  Internal member loans are never counted in Yopsa loans — advanced stayed 0
PASS  audit_chain            Audit hash chain integrity — 9 entries verified
PASS  cleanup                Self-test data cleaned up — all self-test rows removed; audit entries retained
```

`cash_three_steps` drives the real API: a treasurer hands the cash over, a
different user records the branch receipt, a third user confirms the deposit
with a bank reference, and each step attempted by the same person as the
previous one is refused.

---

## 4. Negative tests

```
docker exec -e API_BASE=http://127.0.0.1:8080/api app-api-1 node scripts/negative-tests.mjs
```

`9 passed, 0 failed`.

```
PASS  Member reading another member's payments   own group: 1 row, all own; other group: 404; member of another group: 404
PASS  Treasurer of Group A recording in Group B  404 NOT_FOUND
PASS  Kampala officer opening an Mbarara group   404 NOT_FOUND
PASS  IT Admin verifying and approving           verify 403, approve 403
PASS  Forged ids and amounts                     over-allocate 409, negative amount 400, forged group 404
PASS  Replayed request                           first 201, replay 201, identical body: true, changed payload 409
PASS  Treasurer verifying their own submission   403 FORBIDDEN
PASS  Approver approving their own application   403 FORBIDDEN
PASS  Disbursing without Operations review       403 FORBIDDEN

TEST DATA SAFETY (demo rows before -> after; audit log may only grow)
PASS  demo_groups            5 -> 5
PASS  demo_payment_entries   19 -> 19
PASS  demo_receipts          4 -> 4
PASS  demo_loans             14 -> 14
PASS  demo_roles             13 -> 13
PASS  audit_rows             9 -> 12
```

---

## 5. Production guards

Scheduler intervals must be at least 60 s in production:

```
$ NODE_ENV=production ESCALATION_JOB_SECONDS=15 node -e "assertSchedulerConfig()"
REFUSED: ESCALATION_JOB_SECONDS=15s is below the 60s production minimum.
```

The demo seed refuses to run in production:

```
$ NODE_ENV=production node src/seed.js
Refusing to load demo data in production. Use `npm run create-admin` instead.
```

Demo login is refused in production, and no one-time code is written for a
production admin (checked in an earlier run of this branch: 0 messages, 0 OTP
rows).

---

## 6. Screens

Checked in the running app (signed in as the demo verifier):

| Screen | Result |
| --- | --- |
| Payments → Cash | "Three-step reconciliation" card renders with the handover → branch receipt → third-person deposit wording; empty state shown for the verifier's group, which holds no unreconciled cash. 0 console errors. |
| Loans → Internal member-to-member loans | Card renders with its own group selector, empty state and the "Record internal loan" form; explicitly states these loans never create a Yopsa loan or ledger posting. 0 console errors. |

The populated cash table (rows with Receive / Confirm deposit buttons) was not
captured in the browser: the preview session belongs to a verifier scoped to a
group with no unreconciled cash. The same three-step flow is covered end to end
by the `cash_three_steps` system check above.

---

## 7. Not verified here

- GitHub Actions pipeline — written, runs on GitHub, not executed in this sandbox.
- SMS/WhatsApp delivery — provider-neutral stub only.
- Production deployment — out of scope for this branch.

---

## 8. Hardening pass (`security-hardening-pass`)

Every block below is output copied from a run against the compose stack on this
branch. Counts are from the final run of each suite.

### 8.1 `app_system()`

`server/src/db.js` is the only place that sets it:

```
await client.query("SELECT set_config('app.system', 'on', true)");   // systemTx only
```

`systemTx()` is called by the scheduler (`server/src/scheduler.js`). No route
passes request input to `set_config`; the only other caller is the self-test,
which flips the switch inside a transaction it then rolls back. The `true`
makes the setting transaction-local, so it cannot outlive that transaction.
In-app check and negative test:

```
PASS  app_system_gate    request=false app.system='' systemTx=true afterTx=false
PASS  app_system cannot be enabled over HTTP
      plain: 404 NOT_FOUND | query: 404 NOT_FOUND | header: 404 NOT_FOUND |
      after payload: 404 NOT_FOUND | fresh app session app_system(): false
```

### 8.2 Cash steps and roles

| Step | Allowed roles after this pass |
| --- | --- |
| Handover | group treasurer, or an assigned field officer (group/branch scope) |
| Branch receipt | verifier only (**management removed — read-only**) |
| Deposit confirmation | verifier only (**management removed — read-only**) |

The database trigger `cash_reconciliation_guard` still requires three distinct
users in order.

```
PASS  cash_three_steps          final reconciled; same-person steps rejected
PASS  cash_readonly_management  Management receiving cash rejected: FORBIDDEN;
                                Management confirming a deposit rejected: FORBIDDEN
```

### 8.3 Hard deletes

`record_no_hard_delete` (BEFORE DELETE on `users`, `persons`, `groups`,
`memberships`, `role_assignments`) raises `42501` unless the row is tagged
`is_demo`, so the demo purge and the self-tests still clean up.

```
PASS  hard_delete_guard  refused={"person":true,"membership":true,"group":true,
                         "role":true,"user":true} endDated=ended auditRowsForRecord=1
PASS  No hard-delete route for protected records
      group: 404 | person: 404 | role: 404 | user: 404 | group still present: 1
```

The audit row written for the person is still readable after the refused delete
(`auditRowsForRecord=1`), and the allowed alternative (end-dating the membership)
succeeds in the same transaction.

### 8.4 Raw security evidence

Password hashing (`server/src/auth.js`): scrypt, N=16384, r=8, p=1, 16-byte
salt, 64-byte derived key. A stored value:

```
scrypt$16384$8$1|130        (prefix | total length; salt and hash not printed)
```

Session cookie (login with `X-Forwarded-Proto: https`):

```
Set-Cookie: pamoja_sid=…; Max-Age=43200; Path=/; Expires=…; HttpOnly; Secure; SameSite=Lax
```

12-hour session; the database row also expires (`sessions.expires_at`).

CSRF (POST without the client header):

```
{"error":{"code":"CSRF_BLOCKED","message":"Request rejected."}} [403]
```

Response headers:

```
X-Content-Type-Options: nosniff
Strict-Transport-Security: max-age=31536000; includeSubDomains
Referrer-Policy: strict-origin-when-cross-origin
Permissions-Policy: geolocation=(), camera=(), microphone=()
Content-Security-Policy: default-src 'self'; img-src 'self' data:; style-src 'self'
  'unsafe-inline'; script-src 'self'; connect-src 'self';
  frame-ancestors 'self' https://*.base44.com https://*.base44.app
```

HSTS is sent only over TLS (or in production); on a plain-HTTP sandbox it would
pin the browser to https for localhost.

CORS: the API is same-origin (the web app proxies `/api`), so no
`Access-Control-Allow-*` header is returned for a foreign `Origin`:

```
no Access-Control-Allow-* header returned
```

Uploads: `ALLOWED = ['image/png','image/jpeg','image/webp','application/pdf']`,
`MAX = 5 * 1024 * 1024`, JSON body `10mb`; magic-byte check `CHK:upload_content`.

`npm audit --audit-level=high`:

```
server: found 0 vulnerabilities
web:    2 moderate severity vulnerabilities (react-router-dom 6.0.0-alpha.0 - 7.17.0)
```

No high or critical findings; the two moderate findings need a breaking router
upgrade and are left for a separate change.

gitleaks (`zricethezav/gitleaks:latest detect --source=/repo --no-git --redact`):

```
no leaks found
```

### 8.5 Counts

```
IN-APP SYSTEM CHECKS: 33 passed, 0 failed, 33 total   (25 original + 8 added)
NEGATIVE TESTS:       12 passed, 0 failed             (9 original + 3 added)
TEST DATA SAFETY:     demo_groups 5 -> 5, demo_payment_entries 19 -> 19,
                      demo_receipts 4 -> 4, demo_loans 14 -> 14,
                      demo_roles 13 -> 13, audit_rows 50 -> 56
Build Progress:       60 BUILT AND VERIFIED, 88 NOT VERIFIED HERE, 1 EXCEPTION
                      (before this pass: 40 / 108 / 1)
```

New automated checks: `app_system_gate`, `hard_delete_guard`,
`cash_readonly_management`, `attendance_integrity`, `payment_controls`,
`correction_separation`, `loan_controls`, `dashboard_totals`. New negative
tests: `app_system cannot be enabled over HTTP`, `No hard-delete route for
protected records`, `Registration review separation`. They run inside the same
two scripts that `.github/workflows/verify.yml` already executes, and each script
now fails if its count drops below 33 / 12.

### 8.6 Still not verified here

- UI-only items (registration, attendance, payment, loan and dashboard screens)
  have exact scripts in `docs/MANUAL_TESTS.md`; they are not browser-tested.
- GitHub Actions has not been executed in this sandbox.
- Two moderate `react-router-dom` advisories remain in the web client.

---

## 9. Short hardening pass

Branch `security-hardening`. Output copied from the compose stack on this branch.

### 9.1 Password hashing

New parameters (`server/src/auth.js`):

```
PASSWORD_HASH = { algorithm: 'argon2id', m: 19456, t: 2, p: 1, hashLength: 32 }
stored: $argon2id$v=19$m=19456,t=2,p=1$<salt>$<hash>
```

Unit test (`node --test test/password.test.js`):

```
# tests 3
# pass 3
# fail 0
```

In-app check:

```
PASS  password_hash_params  Passwords use Argon2id (m=19456, t=2, p=1); legacy hashes upgrade on login
      — algorithm=argon2id m=19456 t=2 p=1 legacyUpgraded=true rehashStable=true
```

A real sign-in upgraded the seeded administrator row:

```
admin@yopsa.demo | $argon2id$v=19$m=19456
```

Accounts still on the legacy scrypt format because they have not signed in since
the change: 4.

20 concurrent logins (20 distinct accounts, one request each; memory is the API
container's cgroup usage, not just the Node heap):

```
{
  "users": 20,
  "ok": 20,
  "statuses": [200],
  "totalMs": 1072,
  "minMs": 158,
  "medianMs": 952,
  "p95Ms": 1047,
  "maxMs": 1047,
  "memBeforeMB": 119,
  "memPeakMB": 244,
  "memAfterMB": 131,
  "peakDeltaMB": 124
}
```

### 9.2 correction_separation

```
PASS  correction_separation  A correction needs a different approver and only then changes the entry
      — selfApproveBlocked=true requestedBy=selftest-1795813545231-verifier@selftest.invalid
      approvedBy=selftest-1795813545231-verifier2@selftest.invalid different=true status=approved amount=12000
```

### 9.3 is_demo and app-role privileges

Run as `pamoja_app`:

```
    table_name    | can_delete | can_truncate
------------------+------------+--------------
 users            | f          | f
 persons          | f          | f
 groups           | f          | f
 memberships      | f          | f
 role_assignments | f          | f
```

```
-- flipping is_demo on a real (non-demo) row:
ERROR:  is_demo is immutable after insert; only the seed or system path may set it
-- DELETE on users:
ERROR:  permission denied for table users
-- TRUNCATE on users:
ERROR:  permission denied for table users
```

```
PASS  is_demo_immutable  ... — flipBlocked=true code=42501 seedPathAllowed=true
PASS  hard_delete_guard  ... — refused={"person":true,"membership":true,"group":true,"role":true,"user":true} endDated=ended auditRowsForRecord=1
```

The demo purge and the self-test cleanup now run as the owner role, because the
API role has no DELETE on those five tables. A purge probe inside a rolled-back
transaction completed without a privilege or trigger error and left the demo
counts unchanged (`demo_groups 5, demo_users 13, demo_persons 63`).

### 9.4 Branch verifier coverage

`docs/ASSUMPTIONS.md` A-041: at least two active verifier users per branch for
the cash steps. The Administration overview, signed in as `admin@yopsa.demo`:

```
Verifier coverage warning
Each branch needs at least two active verifier users for the cash steps.
Kampala Central — 1 active verifier (minimum 2)
Mbarara — 1 active verifier (minimum 2)
```

### 9.5 Web navigation audit

No `navigate` or redirect target in `web/src` is built from user-supplied
values; the full sink list is in `docs/PROGRESS.md` item 5.

### 9.6 Counts

```
IN-APP SYSTEM CHECKS: 35 passed, 0 failed, 35 total
NEGATIVE TESTS:       12 passed, 0 failed
TEST DATA SAFETY:     demo_groups 5 -> 5, demo_payment_entries 19 -> 19,
                      demo_receipts 4 -> 4, demo_loans 14 -> 14,
                      demo_roles 13 -> 13, audit_rows 32 -> 38
```

## 10. Hardening pass 2 (`security-hardening-2`)

### 10.1 Owner-role credentials

The owner role is now reached through one module, `server/src/owner.js`. It
reads `ADMIN_DATABASE_URL` and nothing else: there is no fallback to
`DATABASE_URL`, so a process without the variable has no owner pool and
`adminTx()` throws. Consumers (full table in `docs/OWNER_ACCESS.md`): the seed,
`createAdmin`, the self-test cleanup, the two non-production admin routes, the
purge and self-test CLI scripts, the negative tests, and the `seed` service in
`deploy/docker-compose.app.yml`. The owner URL was removed from that file's
`api` service.

Demo purge and self-tests are mounted only when the process is not production;
in production they run from `server/scripts/purge-demo.mjs` and
`server/scripts/system-checks.mjs`. The API refuses to start with the owner URL
in a production environment.

A production-mode process with no owner URL, on the same database and code:

```
{"level":"info","message":"pamoja api listening on 8099","ownerCredentials":"absent","demoTooling":"absent"}
GET /api/health -> 200 {"status":"ok","database":true,...}
```

The same process started with `ADMIN_DATABASE_URL` set:

```
{"level":"error","message":"refusing to start","reason":"ADMIN_DATABASE_URL must not be set for the production API; owner access is CLI-only"}
exit=1
```

Both routes, with a valid organisation-administrator session on that
production process, and the same calls against the development process:

```
PRODUCTION POST /api/admin/purge-demo    -> 404 {"error":{"code":"NOT_FOUND","message":"Not found."}}
PRODUCTION POST /api/admin/system-checks -> 404 {"error":{"code":"NOT_FOUND","message":"Not found."}}
PRODUCTION /api/auth/me                  -> 200
DEV        POST /api/admin/purge-demo    -> 403 {"error":{"code":"OTP_REQUIRED",...}}
DEV        POST /api/admin/system-checks -> 200 passed 35 failed 0 total 35
```

The self-tests run from the command line without the HTTP route, against the
same database, with the identity of an active organisation administrator:

```
IN-APP SYSTEM CHECKS
35 passed, 0 failed, 35 total (tag SELFTEST-1795814399249)
```

### 10.2 Argon2id off the main thread

`server/src/argonPool.js` keeps two worker threads (`ARGON_WORKERS` overrides)
running `server/src/argonWorker.js`; `hashPassword` and `verifyPassword`
dispatch there. Legacy scrypt hashes verify with the async `crypto.scrypt`, so
they use the libuv threadpool instead of blocking the loop.

Health-endpoint latency during a 50-login burst, measured from inside the API
container by polling `/api/health` every 50 ms while 50 concurrent sign-ins ran:

| | login burst | health during burst |
|---|---|---|
| before (main thread) | `ok=50/50 wallMs=3168` | `n=2 median=2511ms p95=2511ms max=2511ms` |
| after (worker pool) | `ok=50/50 wallMs=4081` | `n=75 median=3ms p95=4ms max=110ms` |

Idle baseline in both runs: `n=29 median=2ms p95=4ms max=37ms`. Unit tests:
`node --test test/*.test.js` → `# tests 3 / # pass 3 / # fail 0`.

### 10.3 `app.seed` and `app.system` over HTTP

Negative test 13 sends query parameters, headers and SQL-looking body values
that try to switch both settings on, then reads them on an application-role
session and attempts the seed-only `is_demo` change there:

```
PASS  app.seed and app.system cannot be enabled over HTTP  query: 404 NOT_FOUND Group not found. | header: 404 NOT_FOUND Group not found. | body: 201 | app session app.seed='null' app.system='null' | is_demo flip: 42501
```

### 10.4 CI floors

`.github/workflows/verify.yml` now sets `MIN_SYSTEM_CHECKS: '35'` and
`MIN_NEGATIVE_TESTS: '12'` in the job environment; both scripts exit non-zero
below the floor.

### 10.5 Counts

```
IN-APP SYSTEM CHECKS: 35 passed, 0 failed, 35 total
NEGATIVE TESTS:       13 passed, 0 failed
TEST DATA SAFETY:     demo_groups 5 -> 5, demo_payment_entries 19 -> 19,
                      demo_receipts 4 -> 4, demo_loans 14 -> 14,
                      demo_roles 13 -> 13, audit_rows 16 -> 23
PASSWORD TESTS:       3 passed, 0 failed
```
