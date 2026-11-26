# ADR-0009: Money as BIGINT whole UGX

**Status:** Accepted (P0)
**Date:** 2026-11-26

## Context

The system handles member savings, shares, fines, loan principal and repayments
in Ugandan Shillings. Floating point cannot represent money exactly; mixed
representations invite rounding discrepancies that break reconciliation — the
highest-risk area of the system.

## Decision

- All amounts are `BIGINT` in **whole UGX**. The Ugandan shilling has no
  practically used subunit, so the minor unit is 1 UGX.
- Column names always end in `_ugx` (`amount_ugx`, `declared_total_ugx`).
- Every amount column carries `CHECK (amount_ugx > 0)`; signed deltas exist only
  in reversal/adjustment rows and are named accordingly (`delta_ugx`).
- Arithmetic happens in `int64` in Go and `bigint` in SQL. No float, no decimal
  type, no currency formatting in the database.
- Sums are computed in SQL (`SUM(amount_ugx)`) so rounding never depends on the
  application.
- The API serialises amounts as JSON numbers (they fit safely in a JS integer up
  to 2^53; a group total is far below that), and the client formats with `intl`
  using the UGX locale.
- Currency is recorded on the entity (`money.currency = UGX`, A-014). Adding a
  second currency would require a schema change and a new ADR — deliberately not
  supported implicitly.

## Consequences

**Positive**

- Exact arithmetic everywhere; reconciliation differences are real, not
  rounding.
- Triggers can compare sums without casts.
- Simple, fast indexes and aggregation.

**Negative**

- A future currency with subunits (or FX) needs a migration and a new ADR.
- Values above 2^53 would lose precision in JavaScript clients; guarded by a
  domain maximum per amount and a check in the API.

## Alternatives considered

- **`NUMERIC(18,2)`** — exact, but invites fractional shillings that the
  business does not use, and requires careful scale handling in every layer.
- **Integer minor units with a currency table** — more general than needed and
  more ways to get it wrong.
- **Float** — rejected outright.

## References

- [ERD.md conventions](../ERD.md)
- [ASSUMPTIONS.md A-014](../ASSUMPTIONS.md)
