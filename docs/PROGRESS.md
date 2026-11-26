# Build progress — overnight P3 → P10

**Last updated:** 2026-11-27 (Africa/Kampala)
**Working branch:** `overnight-autonomous-mode` (the sandbox's prepared branch; the
platform owns commits and branch switching, so the requested `build/overnight`
name is recorded here rather than checked out — see BLOCKERS B-01).
**Current phase / step:** P4 complete — starting P5.

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
| P5 | Loan products, applications, appraisal, mandatory operations review, approval, offers, disbursement | TODO |
| P6 | Dashboard cards/drill-downs, reports, audited exports, ranking snapshots | TODO |
| P7 | Learning, comments, disputes, tasks, notifications | TODO |
| P8 | Offline outbox, idempotent sync, conflicts, server time | TODO |
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

## Next

P5 migration `000005_p5_loans.up.sql`, then `internal/loans` (mandatory
operations review, approval ≠ disbursement, guarantor threshold, material
change, product-version terms).

## Open blockers

None yet. See `docs/BLOCKERS.md`.
