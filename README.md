# Yopsa Pamoja

**Stronger groups. Informed decisions.**

Group management and financial monitoring for Yopsa Microfinance (Uganda):
VSLAs, investment clubs, associations, cooperatives and SACCOs. Pamoja
complements Yopsa's approved financial ledger (Alogsys or equivalent) and never
replaces it: Pamoja owns meeting and operational records, the core ledger owns
contractual schedules, posted balances and accounting.

> **Delivery status: P2 registration core implemented.**
> This repository contains the approved-plan artefacts, the platform skeleton,
> the P1 platform core (Argon2id authentication, TOTP second factors, rotating
> refresh sessions, step-up, PostgreSQL row-level security, the append-only
> hash-chained audit log and the idempotency store) and the P2 registration
> core: legal entities, branches, group types, the group lifecycle, memberships,
> consent records and authority appointments. Meetings, payments, loans and the
> full client arrive in P3–P10. Nothing here is a production deployment.

## What exists today

| Area | State |
|---|---|
| Architecture, ERD, state machines, RBAC matrix, threat model, assumptions, ADRs, test plan | Written ([docs/](docs/)) |
| Go API skeleton: config, JSON logging with redaction, security headers, correlation ids, health probes, status page | Implemented, tested |
| Machine-checked RBAC matrix (10 roles, 104 grants, 11 separation-of-duties rules) + authorisation engine | Implemented, tested |
| Route registry that fails the build when a route omits its authorisation | Implemented, tested |
| Authentication: Argon2id passwords, TOTP MFA, short-lived JWT access tokens, rotating refresh tokens with reuse detection, lockout, step-up | Implemented, tested (unit + database integration) |
| PostgreSQL row-level security, least-privilege roles (`pamoja_app`, `pamoja_readonly`, `pamoja_auth`), append-only audit hash chain, idempotency store | Implemented, tested (database integration) |
| Registration: legal entities, branches, group types, group lifecycle (draft → submitted → review → active/returned/rejected), memberships, consent records, authority appointments | Implemented, tested (unit + database integration) |
| Generated route × role × scope authorisation suite and SQL-injection regression test | Implemented, tested |
| PostgreSQL baseline and migrations (identity, sessions, audit, RLS, registration) | Implemented, applied by the dev stack |
| OpenAPI 3.1 skeleton | Written, linted in CI |
| Flutter client shell (theme, routing, status home) | Written; **not compiled here** (no Flutter SDK) |
| CI pipeline, Makefile, local dev compose | Written; pipeline runs on GitHub, **not verified here** |

## Quick start (local)

Prerequisites: Docker with Compose v2. Nothing else is needed for the API.

```sh
docker compose -f deploy/docker-compose.dev.yml up --build
```

| URL | What |
|---|---|
| http://localhost:8080/ | API status page |
| http://localhost:8080/healthz | Liveness (no dependencies) |
| http://localhost:8080/readyz | Readiness (database + schema version) |
| http://localhost:8080/api/v1/meta | Build and environment metadata |
| `POST http://localhost:8080/api/v1/auth/login` | Password + TOTP sign-in |
| `POST http://localhost:8080/api/v1/auth/refresh` | Rotate the refresh token |
| http://localhost:8080/api/v1/group-types | Group forms reference data |
| `POST http://localhost:8080/api/v1/groups` | Register a group (requires a session) |

Expected readiness response once PostgreSQL and the baseline migration are up:

```json
{"status":"ok","checks":{"database":"ok","schema":"ok"}}
```

### Run the checks

```sh
make test        # go test -race ./... in a Go 1.26 container
make lint        # gofmt, go vet, golangci-lint
make coverage    # per-package coverage gate (payments, loans, rbac, audit, attendance)
make rbac-doc    # regenerate docs/RBAC.md from the RBAC matrix
make audit-verify # walk the append-only audit hash chain and report the first break
```

Database integration tests (row-level security, audit chain, login/refresh
reuse) run when `TEST_DATABASE_URL` points at a PostgreSQL instance; CI provides
one, and locally:

```sh
docker compose -f deploy/docker-compose.dev.yml exec api \
  sh -c 'TEST_DATABASE_URL="$DATABASE_URL" go test ./internal/auth/'
```

The host needs no Go installation: every target runs in a container.

## Repository map

```
backend/        Go API and worker, SQL migrations, OpenAPI spec, RBAC matrix
app/            Flutter client (Android, iOS, Windows)
docs/           architecture, ERD, state machines, RBAC, threat model, ADRs, test plan
deploy/         local dev compose, environment template
scripts/        coverage gate and, from P4, seed and restore-drill helpers
.github/        CI: format, vet, lint, tests, coverage, security scans, SBOM, client
```

## Security posture (what is already true)

- The client is untrusted; every rule is enforced server-side.
- Authorisation is default deny and machine-checked: a route without a module and
  action fails the test suite.
- Separation of duties is data, not prose: no self-verification, no
  self-approval, no self-granted roles.
- Logs are structured JSON and redact passwords, tokens, phone numbers, national
  IDs and similar fields.
- Money is `BIGINT` whole UGX; financial rows are append-only by design
  (enforced from P1 with triggers and role privileges).
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
| 2026-11-27 | Linux sandbox, Go 1.26 (`golang:1.26` container), PostgreSQL 16 | `gofmt -l`, `go build`, `go vet`, `go test -race ./...` including registration integration tests (lifecycle, SD-06, RLS isolation, consent, authority, SQL-injection payload), migration 000003 applied, stack boot, `/healthz`, `/readyz`, status-page render | Passed |
| 2026-11-26 | Linux sandbox, Go 1.26.11 (`golang:1.26` container), golangci-lint v2.15.0, PostgreSQL 16, Node 22 | `gofmt -l`, `go build`, `go vet`, `go test -race`, coverage gate, `golangci-lint run`, OpenAPI lint, stack boot, `/healthz`, `/readyz`, status-page render | Passed |

Not verified in this environment: Flutter analyze/test (no SDK), GitHub Actions
pipeline (runs on GitHub), iOS build (requires macOS), SMS delivery and the
Alogsys connector (vendor access not available).

## Ownership

Built for Yopsa Microfinance. All accounts, domains, signing keys and vendor
relationships must be Yopsa-owned; the handover checklist is produced in P10.
