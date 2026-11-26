# Documentation

Yopsa Pamoja — internal group management and financial monitoring for Yopsa
Microfinance (Uganda). This index states what each document is and whether it
is complete for the current phase.

| Document | Purpose | Status |
|---|---|---|
| [ARCHITECTURE.md](ARCHITECTURE.md) | System context, containers, module boundaries, request lifecycle, deployment topology | P0 — delivered |
| [ERD.md](ERD.md) | Logical data model per domain, invariants, money and identifier conventions | P0 — delivered |
| [STATE_MACHINES.md](STATE_MACHINES.md) | Every workflow state, its transitions, guards and who may trigger them | P0 — delivered |
| [RBAC.md](RBAC.md) | Generated role × module × action × scope matrix and separation-of-duties rules | P0 — generated from `backend/internal/rbac/matrix.yaml` |
| [AUTH.md](AUTH.md) | Authentication, tokens, sessions, step-up, database roles and row-level security | P1 — implemented |
| [THREAT_MODEL.md](THREAT_MODEL.md) | STRIDE analysis for payments, loans, auth, uploads and offline sync, with test mapping | P0 — delivered |
| [ASSUMPTIONS.md](ASSUMPTIONS.md) | Every policy value not fixed by the brief, with its safe default and approval owner | P0 — delivered, awaiting management approval |
| [TEST_PLAN.md](TEST_PLAN.md) | Test levels, coverage gates and the required test catalogue with IDs | P0 — delivered |
| [ADR/](ADR/) | Architecture decision records | P0 — 10 records |
| SECURITY_CONTROLS.md | OWASP ASVS L2 / MASVS control map with proving test per control | P1 onwards |
| DATA_PROTECTION.md | Uganda DPPA notices, lawful basis, retention, correction, export, deletion | P2 onwards |
| API.md | Human-readable API guide generated alongside `backend/api/openapi.yaml` | P2 onwards |
| OPERATIONS_RUNBOOK.md | Daily/weekly/monthly routines, incident response, key rotation, restore | P9 |
| USER_GUIDES/ | Plain-language guides per role | P10 |
| VERIFICATION_REPORT.md | Command, date and output for every gate | P10 |
| HANDOVER_CHECKLIST.md | Ownership of accounts, domains, keys and vendor relationships | P10 |
| STORE_POLICY_REGISTER.md | Google Play and Apple policy decisions for group lending and meeting photos | P10 |

## Conventions

- Every document is dated and states the phase it belongs to.
- A claim of "implemented" requires a named test or command that proves it.
- Policy values are never decided in prose: they live in
  [ASSUMPTIONS.md](ASSUMPTIONS.md) and in versioned configuration.
