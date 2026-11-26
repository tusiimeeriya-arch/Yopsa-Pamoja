# Backend packages

```
cmd/api          HTTP entry point
cmd/worker       asynchronous jobs (river queue)
cmd/auditverify  walks the append-only audit hash chain, exits non-zero on a break
cmd/migrate      schema migrations (deployment phase)
internal/
  platform/  cross-cutting: config, logging, keyring (envelope encryption),
             db (RLS-scoped transactions), clock, ids, errors, telemetry
  httpapi/   router, middleware, route registry, transport encoding
  rbac/      authorisation engine + machine-checked matrix
  auth/      credentials, TOTP, sessions, refresh families
  audit/     append-only hash-chained audit log
  groups/    legal entity, branch, group, membership, authority
  meetings/  meetings, registers, minutes, resolutions, action items
  payments/  entries, submissions, receipts, allocations, postings
  loans/     applications, appraisals, decisions, offers, disbursements
  dashboard/ cards, drill-downs, snapshots
  ranking/   policy, eligibility, scoring, snapshots
  learning/  items, assignments, progress
  comments/  group and restricted comments, moderation
  disputes/  attendance and payment disputes
  notifications/ tasks, in-app alerts, provider adapters
  ledger/    LedgerAdapter interface + sandbox implementation
  storage/   object storage, signed URLs, scanning
```

## Dependency rules

1. `platform` depends on nothing inside `internal`.
2. Domain packages may depend on `platform` and on **interfaces** owned by the
   consuming package. They must not import each other's storage or handlers;
   cross-domain work goes through an explicit interface declared by the caller.
3. `httpapi` depends on domain packages, never the reverse.
4. `rbac` and `audit` are infrastructure for every domain package.
5. No package imports `cmd/*`.

These rules are enforced by `depguard` in `.golangci.yml` as packages land.
