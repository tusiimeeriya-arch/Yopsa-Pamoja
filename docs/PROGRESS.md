# Build progress — overnight P3 → P10

**Last updated:** 2026-11-27 (Africa/Kampala)
**Working branch:** `overnight-autonomous-mode` (the sandbox's prepared branch; the
platform owns commits and branch switching, so the requested `build/overnight`
name is recorded here rather than checked out — see BLOCKERS B-01).
**Current phase / step:** P7 complete — starting P8.

## How to resume

```sh
cd /app
git log --oneline -5
docker compose --project-name app --project-directory "$BASE44_PROJECT_ROOT" \
  -f "$BASE44_CONFIG_ROOT/docker-compose.base44.yml" up -d --build
curl -fsS http://localhost:3000/readyz
# tests (inside the app container, against the test database)
docker compose --project-name app --project-directory "$BASE44_PROJECT_ROOT" \
  -f "$BASE44_CONFIG_ROOT/docker-compose.base44.yml" exec -T api \
  sh -ec 'TEST_DATABASE_URL="postgres://pamoja:pamoja_dev_password@db:5432/pamoja_test?sslmode=disable" go test -p 1 ./...'
```

Read this file first after a cut-off, then `docs/BLOCKERS.md`.

## Order (strict)

P3 → P4 → P5 → P6 → P7 → P8 → P9 → P10. No reordering.

| Phase | Scope | Status |
|---|---|---|
| G0 | Baseline: P1 stack boots, existing tests, docs index | DONE |
| P3 | Registration dependency, meetings, register, attendance formulas, minutes, action items, controlled amendments | DONE |
| P4 | Payment entries, group submissions, receipts, allocations, postings, finance exceptions, corrections, sandbox ledger | DONE |
| P5 | Loan products, applications, appraisal, mandatory operations review, approval, offers, disbursement | DONE |
| P6 | Dashboard cards/drill-downs, reports, audited exports, ranking snapshots | DONE |
| P7 | Learning, comments, disputes, tasks, notifications | DONE |
| P8 | Offline outbox, idempotent sync, conflicts, server time | IN PROGRESS |
| P9 | Audit verification, metrics, rate limits, backup/restore drill, k6 | TODO |
| P10 | E2E acceptance journeys, seed, runbooks, user guides, handover, morning report | TODO |

## Done

- G0: confirmed the preview stack is healthy (`/healthz` `{"status":"ok"}`,
  `/readyz` database+schema ok), the repository is the P1 build, and the
  combined brief's phase dependencies (P2 registration) are missing from the
  repository. P2's data model is therefore delivered as a dependency inside the
  P3 migration and marked as such.

## P3 — DONE

- Migration `000003_p3_registration_meetings.up.sql`: registration dependency
  (legal entity, branch, group, member, membership, authority, consent),
  versioned config values, meetings/attendance/minutes/resolutions/action
  items, forced RLS, scope helpers, workflow and immutability triggers.
- `internal/attendance`: pure formulas for individual/group percentages,
  eligibility, late/excused policy, amendment resolution and SD-09 validation.
- HTTP surface: create/open/check-in/confirm/close/amend attendance and
  `GET /groups/{id}/attendance`.
- Evidence:
  - `go test ./internal/attendance/` → PASS (11 unit cases, incl. T-ATT-01…04).
  - `TEST_DATABASE_URL=… go test -p 1 ./internal/attendance/` → PASS
    (`TestClosedRegisterAmendmentIsControlled`, `TestMeetingRLSIsolatesGroups`,
    T-ATT-05).
  - `go test ./internal/rbac/ ./internal/httpapi/` → PASS after matrix
    regeneration (`check_in` action; 104 grants, 11 SoD rules).

## P4 — DONE

- Migration `000004_p4_payments.up.sql`: payment entries, group submissions,
  receipts, allocations, ledger postings, finance exceptions, corrections and
  the sandbox ledger; forced RLS; triggers for entry/submission/receipt
  immutability, allocation caps, posting and correction transitions.
- `internal/payments`: pure `PlanVerification` (full vs logged partial, no
  automatic spread), evidence and cash-path checks, verifier independence,
  lock hash; service that writes receipt → allocation → posting in one
  transaction, serialises verification with `FOR UPDATE`, creates a
  `Posting pending` state plus finance exception on ledger failure, and retries.
- `internal/ledger`: `LedgerAdapter` with the working sandbox implementation
  and an explicit Alogsys skeleton that refuses to pretend it works.
- HTTP surface: entries, lock/submit/verify/return, posting retry, summary,
  corrections, finance exceptions.
- Evidence:
  - `go test -p 1 ./internal/payments/` → PASS
    (T-PAY-01, 04, 05, 06, 08, 09; three facts; summary separation).
  - `go test -race -p 1 ./...` → PASS (all packages, including P1 auth
    integration against `pamoja_test`).
  - `go vet ./...` → PASS; `gofmt -l .` → empty.

## P5 — DONE

- Migration `000005_p5_loans.up.sql`: approved product versions (six seeded
  products), applications, appraisals, borrower checks, checked guarantees,
  decisions, offers, disbursements, installments, repayments and material
  changes; forced RLS; transition guard, disbursement guard (SD-04, accepted
  offer, cap), repayment guards.
- `internal/loans/rules.go`: pure product limits, guarantor threshold,
  approval authority, SD-03/SD-04 checks, product-version offer terms and
  group/internal-member totals kept apart.
- `internal/loans/service.go`: create (with the group's current chairperson
  authority record) → appraise → operations review → decision (one or two
  credit approvers per A-012) → offer → acceptance → disbursement (builds the
  installment schedule) → repayment; material change returns the application
  to operations review and clears the approver.
- HTTP surface: application create/list/get, appraise, review, decision, offer,
  offer acceptance, disburse, material change, repayment.
- Evidence:
  - `go test ./internal/loans/ ./internal/httpapi/ ./internal/rbac/` → PASS.
  - `TEST_DATABASE_URL=… go test -p 1 ./internal/loans/` → PASS
    (full journey with a 12-installment schedule and an applied repayment;
    T-LOAN-03 self-approval, T-LOAN-04 approver disbursing, A-010 missing
    guarantee, A-034 material change, product minimum, missing authority).
  - `TEST_DATABASE_URL=… go test -p 1 ./...` → PASS (every package).
  - `go vet ./...` → PASS; `gofmt -l .` → empty.

### Environment repairs made this phase

- The preview database had only migrations 1–2 applied because the `migrate`
  service runs once per stack start. Re-running it applies the (idempotent)
  files: `docker compose … up -d migrate`. Do this after adding a migration.
- Integration tests no longer rewrite the shared `pamoja_app` password unless
  the expected one fails to authenticate; a needless `ALTER ROLE` raced the
  live API and produced a transient `password authentication failed` in the
  logs.

## P6 — DONE

- Migration `000006_p6_reports.up.sql`: `ranking_snapshot` and `report_export`,
  forced RLS, append-only guard trigger (a recorded snapshot or export cannot be
  updated or deleted), grants for the app and read-only roles.
- `internal/reporting`:
  - `Dashboard` — seven cards (members, attendance over 90 days, posted
    savings, loans outstanding, arrears, submissions awaiting verification,
    open finance exceptions), each naming its drill-down.
  - `DrillDown` — the rows behind one card, always group-scoped.
  - `Generate`/`GetExport` — CSV from members, attendance, payments, loans or
    arrears; the exact bytes are stored with their SHA-256, so a download can be
    re-verified.
  - `ComputeScore`/`Policy` — pure weighted ranking (weights normalised,
    arrears inverted, metrics clamped) plus `Snapshot`/`LatestSnapshot`, one
    immutable snapshot per group and period with its branch rank.
- HTTP surface: dashboard, dashboard card, export create/download, ranking read,
  ranking snapshot create.
- Evidence:
  - `go test ./internal/reporting/` → PASS (T-RANK-01…04, policy fallback).
  - `TEST_DATABASE_URL=… go test -p 1 ./internal/reporting/` → PASS
    (T-DASH-01/02, T-EXP-01 including the hash match and the immutability
    trigger, T-RANK-05 idempotent snapshot and `ErrNoSnapshot`).
  - `TEST_DATABASE_URL=… go test -p 1 ./...` → PASS (every package).
  - `go vet ./...` → PASS; `gofmt -l .` → empty.

## P7 — DONE

- Migration `000007_p7_engagement.up.sql`: learning items (three published,
  shipped with the build), per-member progress with a completion stamp,
  comments with moderation evidence, disputes whose closure needs a resolver and
  a note, tasks whose closure records who closed them, and user-owned
  notifications; forced RLS throughout (notifications are owner-only).
- `internal/engagement`: pure rules (`ValidateCommentBody`,
  `CanTransitionDispute`, `RequiresResolutionNote`) plus the service for
  progress, comments, disputes, tasks and notifications.
- RBAC: added the `complete` action and granted it for learning to member
  (self) and secretary/treasurer/chairperson/field officer (group/branch);
  `docs/RBAC.md` regenerated (10 roles, 104 grants, 11 separation rules).
- HTTP surface: learning progress record/list, comment create/list/moderate,
  dispute raise/list/resolve, task create/list/close, notifications list and
  mark-read.
- Evidence:
  - `go test ./internal/engagement/` → PASS (T-ENG-01/02).
  - `TEST_DATABASE_URL=… go test -p 1 ./internal/engagement/` → PASS
    (T-ENG-03…07: upsert with completion stamp, moderation hiding the comment
    from the member view, dispute closure refused without a note and final once
    closed, one-way task closure, notification ownership).
  - `TEST_DATABASE_URL=… go test -p 1 ./...` → PASS (every package).
  - `go vet ./...` → PASS; `gofmt -l .` → empty.

## Next

P8 — offline outbox and idempotent sync with conflict detection and server time.

## Open blockers

None yet. See `docs/BLOCKERS.md`.
