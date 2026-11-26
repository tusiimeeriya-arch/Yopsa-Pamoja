# ADR-0002: Go service stack — chi, pgx/sqlc, golang-migrate, slog

**Status:** Accepted (P0)
**Date:** 2026-11-26

## Context

The API carries financial workflow rules that must be explicit, testable and
auditable. The team needs a small dependency surface, strong typing at the SQL
boundary and predictable performance on modest hardware.

## Decision

- **Go** (latest stable; 1.26 in this environment) for the API and worker.
- **chi** for routing: `net/http` compatible, small, composable middleware.
- **pgx/v5** as the PostgreSQL driver and **sqlc** to generate typed Go from
  hand-written SQL. No ORM, no string-built queries.
- **golang-migrate** for versioned SQL migrations with tested down paths.
- **slog** for structured JSON logging with attribute redaction.
- **go-playground/validator** for request validation after strict JSON decoding.
- **OpenTelemetry** for traces; Prometheus client for metrics.

## Consequences

**Positive**

- Every query is visible SQL, reviewable by a finance-minded reviewer and
  explainable with `EXPLAIN`.
- Generated types remove a whole class of scan/assign bugs.
- Small dependency tree keeps `govulncheck` and SBOM review tractable.
- `net/http`-compatible routing keeps middleware and testing simple.

**Negative**

- sqlc requires regenerating code after query changes; `make generate` is part
  of the workflow.
- Hand-written SQL means more code than an ORM for simple CRUD.
- Migrations are not auto-applied at boot by design; deployment must run them.

## Alternatives considered

- **GORM / ent** — faster CRUD, but hides SQL and makes RLS session handling and
  locking semantics harder to reason about.
- **Echo / Gin** — viable; chi chosen for its `net/http` fidelity and minimal
  middleware.
- **Prisma/TypeScript backend** — rejected: the chosen stack was fixed by the
  brief and Go's concurrency and deployment story suit a single-host deployment.

## References

- [ARCHITECTURE.md §5](../ARCHITECTURE.md)
- `backend/internal/README.md`
