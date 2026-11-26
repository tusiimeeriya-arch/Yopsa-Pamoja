# ADR-0004: Append-only audit log with a SHA-256 hash chain

**Status:** Accepted (P0)
**Date:** 2026-11-26

## Context

Every action must record who did it, in which role and scope, when, and through
which channel. The brief additionally requires tamper evidence: an auditor must
be able to prove that no row was edited or deleted, including by someone with
database access.

## Decision

- One append-only `audit_log` table. `UPDATE` and `DELETE` are revoked from every
  role except the migration owner, and a trigger rejects them anyway.
- Each row stores actor, role and scope used, action, entity type and id,
  before/after snapshots (redacted where required), reason, timestamp, channel
  (self or assisted), IP, device id and correlation id.
- Each row stores `prev_hash` and
  `hash = SHA-256(prev_hash || canonical_row)`, forming a chain.
- A `verify-audit-chain` command walks the chain from a known checkpoint and
  reports the first break. A scheduled job runs it and alerts on failure.
- The latest hash is periodically anchored to an external write-once location
  (documented procedure, A-035), so a full-database rewrite is still detectable.

## Consequences

**Positive**

- Tampering is detectable even by an insider with SQL access.
- The chain is independent of application logic; it can be verified offline from
  a dump.
- Audit rows double as the evidence trail for the acceptance script.

**Negative**

- Append-only growth needs partitioning and archival (retention A-006).
- Canonicalisation must be stable across releases; the canonical form is
  versioned with the row.
- Writing the chain serialises inserts slightly; batching is used for
  high-volume system events.

## Alternatives considered

- **Plain audit table** — rejected: no tamper evidence.
- **External SIEM as system of record** — rejected: availability and cost in a
  single-host deployment; the SIEM receives a copy, not the source of truth.
- **Blockchain** — rejected as disproportionate.

## References

- [THREAT_MODEL.md TM-AUD-01/02](../THREAT_MODEL.md)
- [TEST_PLAN.md T-AUD-01/02](../TEST_PLAN.md)
