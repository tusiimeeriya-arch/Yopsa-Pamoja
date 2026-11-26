# ADR-0006: PostgreSQL-backed job queue (river) instead of a message broker

**Status:** Accepted (P0)
**Date:** 2026-11-26

## Context

Scheduled and background work: dashboard and ranking snapshots, reminders,
reconciliation checks, integration delivery and retries, audit-chain
verification, session expiry, task escalation. The deployment target is a single
host for a microfinance institution; every extra moving part is another thing to
patch, back up and monitor.

## Decision

Use **river**, a PostgreSQL-backed job queue, running inside the existing
`cmd/worker` process.

- Jobs are rows in the same database, enqueued in the same transaction as the
  business change. A posting that must happen after a verification cannot be
  lost by a broker outage.
- Unique jobs with an idempotency key prevent duplicate scheduled work.
- Retries use exponential backoff with a maximum attempt count; exhausted jobs
  surface as operational alerts.
- Periodic jobs are registered with a cron-like schedule inside the worker.

## Consequences

**Positive**

- One datastore: transactional enqueue, no dual-write problem, backup and restore
  cover jobs too.
- No broker to operate, secure or monitor.
- Job history is queryable with SQL for incident review.

**Negative**

- Throughput is bounded by PostgreSQL; acceptable at this scale (thousands of
  jobs per day), not suitable for high-volume streaming.
- Long-running jobs can hold connections; handlers must be short and resumable.
- Worker and API must be deployed together when job payloads change.

## Alternatives considered

- **RabbitMQ / Redis + sidekiq-style worker** — operational overhead and a
  second persistence model without a transactional guarantee.
- **Cron + shell scripts** — no retries, no visibility, no idempotency.
- **Kafka** — disproportionate for this scale.

## References

- [ARCHITECTURE.md §3](../ARCHITECTURE.md)
- `backend/cmd/worker`
