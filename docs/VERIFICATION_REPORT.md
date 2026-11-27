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
