# Yopsa Pamoja

**Stronger groups. Informed decisions.**

Group management and financial monitoring for Yopsa Microfinance (Uganda):
VSLAs, investment clubs, associations, cooperatives and SACCOs. Pamoja
complements Yopsa's approved financial ledger (Alogsys or equivalent) and never
replaces it: Pamoja owns meeting and operational records, the core ledger owns
contractual schedules, posted balances and accounting.

> **Delivery status: P6 — domain core and web client implemented.**
> The platform core (P1) is joined by the operational modules field teams need:
> groups and membership, meetings and attendance, savings entries with
> independent verification, group borrowing applications from appraisal to
> disbursement, follow-up tasks, a management dashboard, reports and the audit
> view. A dependency-free web client ships with the API and uses Yopsa's blue
> and green identity. The Flutter client remains the mobile target; it is not
> compiled in this environment.

## What exists today

| Area | State |
|---|---|
| Architecture, ERD, state machines, RBAC matrix, threat model, assumptions, ADRs, test plan | Written ([docs/](docs/)) |
| Go API: config, JSON logging with redaction, security headers, correlation ids, health probes | Implemented, tested |
| Machine-checked RBAC matrix (10 roles, 104 grants, 11 separation-of-duties rules) + authorisation engine | Implemented, tested |
| Authentication: Argon2id passwords, TOTP MFA, rotating refresh tokens, lockout, step-up | Implemented, tested |
| PostgreSQL row-level security, least-privilege roles, append-only audit hash chain, idempotency store | Implemented, tested |
| Domain core: legal entity, branches, groups, members, meetings, attendance, savings entries, borrowing applications, tasks | Implemented, applied by the dev stack |
| Domain API with RBAC and separation-of-duties checks on every write | Implemented, exercised end to end |
| Web client (`backend/internal/webui`): sign-in, dashboard, groups, members, registers, savings, loans, tasks, reports, audit, users, profile | Implemented, verified in the running stack |
| Demo portfolio seed (3 branches, 6 groups, 72 members, ~1,500 savings entries, loans across the workflow) | Implemented, idempotent |
| Flutter client shell (theme, routing, status home) | Written; **not compiled here** (no Flutter SDK) |
| CI pipeline, Makefile, local dev compose | Written; pipeline runs on GitHub, **not verified here** |

## Run it

The Base44 preview environment is configured outside this repository and serves
the application at host port 3000. For a plain local stack:

```sh
docker compose -f deploy/docker-compose.dev.yml up --build
```

| URL | What |
|---|---|
| http://localhost:8080/ | Yopsa Pamoja web client |
| http://localhost:8080/healthz | Liveness (no dependencies) |
| http://localhost:8080/readyz | Readiness (database + schema version) |
| http://localhost:8080/api/v1/meta | Build and environment metadata |

The stack applies three idempotent migrations and then seeds a demo portfolio.
The seed exits immediately if the database already has users.

### Demo sign-in

Every test account uses the password **`Pamoja#2026`**.

| Role | Name | Phone |
|---|---|---|
| Management | Grace Namusoke | +256700000001 |
| Field officer | Daniel Kato | +256700000002 |
| Verifier | Sarah Auma | +256700000003 |
| Credit approver | Robert Mugisha | +256700000004 |
| Treasurer | Esther Nabirye | +256700000005 |
| IT admin | Moses Okello | +256700000006 |
| Auditor | Patricia Atim | +256700000007 |
| Secretary | Joan Kembabazi | +256700000008 |
| Chairperson | Peter Byaruhanga | +256700000009 |

Sign in with different roles to see the workflow: the treasurer records a
payment, the verifier confirms it (the server refuses self-verification), the
field officer appraises a borrowing application, the credit approver decides it,
and the verifier confirms disbursement.

## The web client

`backend/internal/webui` is embedded in the API binary and served from `/`. It is
plain HTML, CSS and JavaScript with no build step, and calls the same JSON API
the Flutter client will use. Screens:

- **Sign in** — phone and password, with one-click test accounts.
- **Dashboard** — portfolio totals, six-month savings trend, items that need the
  caller's role, group table, recent entries and loan pipeline.
- **Groups** — searchable cards with status, savings and attendance; group detail
  with overview, members, meetings, savings, loans and tasks.
- **Meeting register** — mark attendance, record minutes, close and lock.
- **Savings** — record, verify or return entries; the recorder and verifier are
  always shown.
- **Loans** — submit, appraise, approve/decline and confirm disbursement.
- **Tasks, reports (print and CSV), audit log (with chain verification), users
  and roles, profile and signed-in devices.**

The client hides actions a role cannot perform, but every rule is enforced again
on the server: the browser is never trusted.

## API surface

Public: `/`, `/assets/*`, `/favicon.svg`, `/healthz`, `/readyz`,
`/api/v1/meta`, `/api/v1/auth/login`, `/api/v1/auth/refresh`.

Authenticated: sessions and step-up under `/api/v1/auth/*`; `/api/v1/me`,
`/dashboard`, `/groups`, `/groups/{id}`, members, meetings, attendance,
contributions (record/verify/return), loans (create/appraise/approve/decline/
disburse), tasks, `/audit`, `/users` and `/reports/portfolio.csv`. Every
non-public route declares the RBAC module and action it requires, and the test
suite fails the build if one is missing.

## Run the checks

```sh
make test        # go test -race ./... in a Go 1.26 container
make lint        # gofmt, go vet, golangci-lint
make coverage    # per-package coverage gate
make rbac-doc    # regenerate docs/RBAC.md from the RBAC matrix
make audit-verify # walk the append-only audit hash chain
```

Database integration tests run when `TEST_DATABASE_URL` points at PostgreSQL;
CI provides one, and locally:

```sh
docker compose -f deploy/docker-compose.dev.yml exec api \
  sh -c 'TEST_DATABASE_URL="$DATABASE_URL" go test ./internal/auth/'
```

## Security posture

- The client is untrusted; every rule is enforced server-side.
- Authorisation is default deny and machine-checked; separation of duties is
  data, not prose (no self-verification, no self-approval).
- Domain tables have row-level security enabled and forced; the API role can
  only read or write while an authenticated actor is set on the transaction.
  Domain records are never deleted by the API, so the trail stays complete.
- Logs are structured JSON and redact passwords, tokens, phone numbers and
  national IDs. Member phone numbers are stored masked; login phones are stored
  as a keyed hash and encrypted form.
- Money is `BIGINT` whole UGX; the ledger remains the source of posted balances.
- No secrets in the repository. Production secrets come from the deployment's
  secret store.

## Documentation

Start at [docs/README.md](docs/README.md). The documents a reviewer needs for P0
approval:

- [Architecture](docs/ARCHITECTURE.md)
- [ERD](docs/ERD.md)
- [State machines](docs/STATE_MACHINES.md)
- [RBAC matrix](docs/RBAC.md) (generated)
- [Threat model](docs/THREAT_MODEL.md)
- [Assumptions for approval](docs/ASSUMPTIONS.md)
- [Test plan](docs/TEST_PLAN.md)
- [ADRs](docs/ADR/)

## Verified on

| Date | Environment | What was run | Result |
|---|---|---|---|
| 2026-11-26 | Linux sandbox, Go 1.26 (`golang:1.26` container), PostgreSQL 16 | `gofmt -l`, `go build ./...`, `go vet ./...`, `go test ./...`, three migrations applied, demo seed, full stack boot, `/healthz`, `/readyz` | Passed |
| 2026-11-26 | Running stack | Sign-in as management, treasurer and verifier; dashboard, groups, group tabs, meeting register; recorded a payment as treasurer and verified it as the verifier (server enforced the split); created, appraised, approved and disbursed a loan; created and activated a group; audit chain verified; portfolio CSV | Passed |
| 2026-11-26 | Live preview | Sign-in, dashboard, groups, group detail, members, meetings, register modal, savings table, payment → verification flow, mobile dashboard (no horizontal overflow) | Passed |

Not verified in this environment: Flutter analyze/test (no SDK), GitHub Actions
pipeline (runs on GitHub), iOS build (requires macOS), SMS delivery and the
Alogsys connector (vendor access not available).

## Ownership

Built for Yopsa Microfinance. All accounts, domains, signing keys and vendor
relationships must be Yopsa-owned; the handover checklist is produced in P10.
