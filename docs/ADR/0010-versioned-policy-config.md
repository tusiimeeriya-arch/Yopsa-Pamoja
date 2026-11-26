# ADR-0010: Versioned policy configuration instead of hard-coded rules

**Status:** Accepted (P0)
**Date:** 2026-11-26

## Context

Many rules are policy, not engineering: lateness and excused-absence treatment,
ranking weights, product limits, approval authorities, guarantor thresholds,
photo policy, retention periods, RPO/RTO, four-eyes rules. Management must be
able to approve and change them, auditors must be able to see which value applied
at the time, and the code must not be redeployed to change a weight.

## Decision

- Every such value lives in `config_value` (`key`, `version`, `value` JSON,
  `description`, `approved_by`, `effective_from`) and is listed in
  [ASSUMPTIONS.md](../ASSUMPTIONS.md).
- Code reads values through a typed accessor with a compiled-in **safe default**
  used only when no approved value exists yet (development, tests). Production
  refuses to start a phase's feature if its required policy is unapproved.
- A change creates a new version. History is never overwritten.
- Reports, ranking snapshots and dashboard snapshots store the policy version
  they used, so any historical output can be reproduced and defended.
- Changing a value requires the `configure` action on the owning module and is
  audited with before/after.
- Values that are safety-critical (for example the audit retention, the
  separation-of-duties rules, the append-only invariants) are **not** policy:
  they stay in code and in database constraints.

## Consequences

**Positive**

- Management approves policy; engineers do not invent it.
- Auditors can answer "what rule applied on 12 March?" precisely.
- No redeploy for a weight or threshold change.

**Negative**

- A configuration mistake is a live behaviour change; validation and a
  four-eyes review of sensitive keys are required.
- Two sources (code default, database value) need a clear precedence rule and a
  startup log line stating which is in force.
- Tests must cover both "approved value present" and "safe default" paths.

## Alternatives considered

- **Environment variables** — no history, no approval trail, restart required,
  poor fit for structured values.
- **Hard-coded constants** — violates the brief's "never invent policy" rule.
- **A full rules engine** — overkill and hard to audit for a small set of
  typed values.

## References

- [ASSUMPTIONS.md](../ASSUMPTIONS.md)
- [ERD.md §6](../ERD.md)
