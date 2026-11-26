# ADR-0008: Sandbox LedgerAdapter plus an Alogsys connector skeleton

**Status:** Accepted (P0)
**Date:** 2026-11-26

## Context

Pamoja must post repayments to Yopsa's approved ledger (Alogsys or equivalent)
and read contractual schedules and balances from it. The vendor's API details,
availability and cost are not confirmed (A-023). Development, testing and the
demo cannot wait for that, and the workflow must be provably correct before any
real posting happens.

## Decision

Define one `LedgerAdapter` interface:

```go
GetContractSchedule(ctx, loanRef) ([]ScheduleLine, error)
GetBalances(ctx, groupRef, asOf) (Balances, error)
PostRepayment(ctx, posting) (PostingResult, error)
ConfirmPosting(ctx, postingRef) (PostingStatus, error)
ListDisbursements(ctx, filter) ([]Disbursement, error)
```

Two implementations:

1. **Sandbox** — backed by PostgreSQL tables, fully working, used by
   development, all tests and the demo. It implements the same state machine
   (pending → posted | failed) so posting-failure tests are real.
2. **Alogsys connector skeleton** — configuration-driven field mapping, retries,
   idempotency keys, signature/auth hooks and a documented list of the endpoints
   Yopsa must obtain from the vendor. It is not claimed to work until the vendor
   contract and credentials exist.

Posting is always asynchronous and idempotent: the allocation is recorded first,
a job posts it, and the result updates `ledger_posting`. A failure creates a
finance exception; it never silently disappears.

## Consequences

**Positive**

- The full payments workflow is testable today, including posting failure.
- Swapping to the real ledger touches one adapter, not the domain.
- The vendor requirement list is concrete and can be sent to Alogsys.

**Negative**

- The sandbox is a simulation; it proves Pamoja's behaviour, not the vendor's.
- Two adapters must be kept in contract-test parity.
- Real-world ledger quirks may force interface changes; the interface is
  versioned and reviewed.

## Alternatives considered

- **Wait for the vendor API** — blocks P4 and the acceptance script.
- **Direct SQL into the vendor's database** — rejected: unsupported, unsafe and
  unauditable.
- **Make the sandbox the production ledger** — rejected by the brief: Pamoja
  never replaces the approved ledger.

## References

- [ARCHITECTURE.md §10](../ARCHITECTURE.md)
- [ASSUMPTIONS.md A-023](../ASSUMPTIONS.md)
