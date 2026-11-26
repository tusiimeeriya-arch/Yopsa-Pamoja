# ADR-0003: PostgreSQL row-level security with separate owner and application roles

**Status:** Accepted (P0)
**Date:** 2026-11-26

## Context

The brief requires defence in depth: service-layer authorisation **plus**
PostgreSQL RLS **plus** constraints and triggers **plus** least-privilege roles.
A single application bug must not expose another branch's or group's data, and
the application must not be able to disable its own guard rails.

## Decision

Four database roles:

| Role | Purpose | Rights |
|---|---|---|
| `pamoja_owner` | Owns the schema; runs migrations | DDL, owns tables |
| `pamoja_app` | Used by the API and worker | `SELECT/INSERT/UPDATE` on permitted tables, no `DELETE` on protected tables, **no** DDL, **not** owner, **not** superuser, **not** `BYPASSRLS` |
| `pamoja_readonly` | Reporting and integrity verification | `SELECT` only, still subject to RLS |
| `pamoja_auth` | **No login.** Owns only the fixed `SECURITY DEFINER` functions that must read identity rows before a session exists (login lookup, refresh rotation, audit append and verification) | `BYPASSRLS`, confined to those functions; the API role cannot borrow it |

The `pamoja_auth` role was added in P1 because authentication necessarily reads
`app_user`/`user_credential` before any actor exists. Keeping those reads in
reviewed, fixed functions is narrower than granting the API role a permissive
policy on the identity tables.

- RLS is enabled and **forced** on every tenant/scope table.
- Policies read session settings set at the start of each transaction:
  `pamoja.actor_id`, `pamoja.scope_ids`, `pamoja.request_id`.
- Scope containment is resolved from the actor's active role bindings, not from
  anything the client sends.
- Triggers enforce financial invariants and valid state transitions, so even a
  compromised service cannot write an invalid state.

## Consequences

**Positive**

- A missing `WHERE group_id = …` in a handler cannot leak rows: the policy
  filters them.
- The audit chain and append-only rules hold even against direct SQL from the
  application role.
- Migrations and runtime are separated, so a runtime compromise cannot alter the
  schema.

**Negative**

- Every connection must set the session variables; the pool wrapper enforces
  this, and a test proves an unset transaction sees nothing.
- Policies add planning overhead on hot queries; indexes on scope columns are
  mandatory.
- Debugging requires `SET ROLE` discipline; documented in the runbook.

## Alternatives considered

- **Application-only filtering** — rejected: one missed predicate is a data
  breach.
- **Database-per-tenant** — rejected: hundreds of groups, one installation, and
  cross-entity management reporting is required.

## References

- [ARCHITECTURE.md §7](../ARCHITECTURE.md)
- [ERD.md conventions](../ERD.md)
