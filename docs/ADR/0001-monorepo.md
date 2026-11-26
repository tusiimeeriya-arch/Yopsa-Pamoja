# ADR-0001: Monorepo for backend, client, docs and deployment

**Status:** Accepted (P0)
**Date:** 2026-11-26

## Context

Pamoja is delivered by a small team to a single organisation. The Go API, the
Flutter client, the database migrations, the deployment assets and a large body
of security documentation must stay in lockstep: an RBAC matrix change, a state
machine change and a migration must land together, and the audit trail must show
that they did.

## Decision

One repository with the layout:

```
backend/   Go service (cmd, internal, db, api)
app/       Flutter client
docs/      architecture, ERD, state machines, RBAC, threat model, ADRs, runbooks
deploy/    compose, proxy config, env template
scripts/   dev, seed, backup, restore drill, coverage
.github/   CI/CD
```

## Consequences

**Positive**

- One pull request can change a rule, its migration, its tests and its
  documentation, and CI checks all of them together.
- One version number describes the whole system; a release tag pins API,
  client and schema.
- Onboarding is one clone and one `make` target.

**Negative**

- CI must be path-filtered so a docs change does not rebuild the client.
- Toolchain versions for Go and Flutter must coexist; the Makefile pins both.
- Repository grows; `CODEOWNERS` per directory is needed as the team grows.

## Alternatives considered

- **Polyrepo per component** — stronger independent release cadence, but the
  rules, tests and docs that make this system defensible would drift apart. The
  compliance value of atomic change outweighs release independence here.
- **Backend-only repo with client generated later** — rejected: the client's
  offline and authorisation behaviour must be tested against the same rules.

## References

- [ARCHITECTURE.md §4](../ARCHITECTURE.md)
