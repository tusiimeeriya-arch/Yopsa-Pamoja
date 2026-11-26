# Yopsa Pamoja

**Stronger groups. Informed decisions.**

Group management and financial monitoring for Yopsa Microfinance (Uganda):
VSLAs, investment clubs, associations, cooperatives and SACCOs. Pamoja
complements Yopsa's approved financial ledger (Alogsys or equivalent) and never
replaces it: Pamoja owns meeting and operational records, the core ledger owns
contractual schedules, posted balances and accounting.

> **Delivery status: P0 complete, awaiting approval.**
> This repository currently contains the approved-plan artefacts and the platform
> skeleton. Authentication, the domain modules and the full client arrive in
> P1–P10. Nothing here is a production deployment.

## What exists today

| Area | State |
|---|---|
| Architecture, ERD, state machines, RBAC matrix, threat model, assumptions, ADRs, test plan | Written ([docs/](docs/)) |
| Go API skeleton: config, JSON logging with redaction, security headers, correlation ids, health probes, status page | Implemented, tested |
| Machine-checked RBAC matrix (10 roles, 94 grants, 11 separation-of-duties rules) + authorisation engine | Implemented, tested |
| Route registry that fails the build when a route omits its authorisation | Implemented, tested |
| PostgreSQL baseline migration (schema, extensions, migration ledger) | Implemented, applied by the dev stack |
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
| 2026-11-26 | Linux sandbox, Go 1.26.11 (`golang:1.26` container), golangci-lint v2.15.0, PostgreSQL 16, Node 22 | `gofmt -l`, `go build`, `go vet`, `go test -race`, coverage gate, `golangci-lint run`, OpenAPI lint, stack boot, `/healthz`, `/readyz`, status-page render | Passed |

Not verified in this environment: Flutter analyze/test (no SDK), GitHub Actions
pipeline (runs on GitHub), iOS build (requires macOS), SMS delivery and the
Alogsys connector (vendor access not available).

## Ownership

Built for Yopsa Microfinance. All accounts, domains, signing keys and vendor
relationships must be Yopsa-owned; the handover checklist is produced in P10.
