# Test plan

**Status:** P0 — delivered for approval. Test IDs are referenced from
[THREAT_MODEL.md](THREAT_MODEL.md) and from the acceptance script (section 14 of
the brief). A test that is not run is reported as "Not verified here", never as
passing.

## 1. Levels and tools

| Level | Tool | Scope |
|---|---|---|
| Unit | `go test`, `flutter test` | Business rules, formulas, state transitions, RBAC engine, redaction |
| Integration | `go test` + testcontainers (real PostgreSQL 16) | Migrations up/down, RLS policies, triggers, constraints, concurrent transactions |
| Contract | OpenAPI validation + generated client compile; adapter contract tests | Spec drift, provider adapters (SMS, ledger) |
| Authorisation | Generated from `backend/internal/rbac/matrix.yaml` | Every route × role × scope, allowed and denied |
| End-to-end | Flutter `integration_test` + API journey tests | The acceptance script, section 14 of the brief |
| Security negative | `go test` suites + `gosec`, `govulncheck`, `gitleaks`, `trivy` | IDOR, forged ids/amounts, replay, privilege escalation, token reuse, races, upload abuse, SQL injection payloads, rate limits |
| Performance | k6 | p95 < 500 ms on core reads at 100 concurrent users (target, A-024 context) |
| Resilience | Restore drill script + failure injection | Backup restore, posting failure handling |
| Accessibility / UX | Manual checklist per role and platform | WCAG 2.1 AA targets, large fonts, screen-reader labels |

## 2. Gates (CI fails on any red)

| Gate | Threshold | Notes |
|---|---|---|
| `gofmt -l` | empty | Formatting |
| `go vet`, `golangci-lint`, `staticcheck` | clean | Lint and static analysis |
| `go test -race ./...` | pass | All packages |
| Coverage on `payments`, `loans`, `rbac`, `audit`, `attendance` | ≥ 85 % each | Enforced as each package lands; `scripts/check-coverage.sh` names a missing package as pending rather than silently passing |
| `flutter analyze`, `flutter test` | clean, pass | Client |
| `gosec` | no high/critical | SAST |
| `govulncheck` | no known vulnerabilities in reachable code | Dependencies |
| `gitleaks` | no findings | Secrets |
| `trivy` (fs and image) | no high/critical | Container and dependencies |
| OpenAPI lint | valid 3.1 | Spec |
| SBOM | CycloneDX generated and stored | Supply chain |
| Authorisation matrix | all routes covered | Route registry test |

## 3. Catalogue

### Payments and reconciliation (brief §5.3)

| ID | Proves | Level | Phase |
|---|---|---|---|
| T-PAY-01 | 10 × 20,000 = 200,000 submitted, 180,000 received shows a 20,000 difference; full verification is blocked; resolution only by locating funds or a logged split verifying only fully evidenced entries; the shortage is never spread | Integration | P4 |
| T-PAY-02 | Same idempotency key twice → one record; same key with a different payload → 409 | Integration | P4 |
| T-PAY-03 | Duplicate provider callback → one receipt and one posting | Integration | P4 |
| T-PAY-04 | Allocation above confirmed funds is rejected by the service **and** by the database | Integration | P4 |
| T-PAY-05 | Editing a locked or verified entry is rejected by the service **and** by the database | Integration | P4 |
| T-PAY-06 | A treasurer verifying their own submission is rejected | Unit + integration | P4 |
| T-PAY-07 | A screenshot alone does not verify; a source reference plus evidence record is required; the cash path needs different people at each step | Integration | P4 |
| T-PAY-07b | Pending entries never change an authoritative balance in any report | Integration | P4 |
| T-PAY-08 | Posting failure after verification yields `Posting pending` plus a finance exception with owner and deadline | Integration | P4 |
| T-PAY-09 | Concurrent verification attempts on one submission: exactly one succeeds | Integration (real Postgres, two connections) | P4 |
| T-PAY-10 | Cross-group and cross-branch access attempts are rejected | Integration | P4 |
| T-PAY-11 | Every report distinguishes submitted, verified and posted values | Integration | P4/P6 |

### Group borrowing (brief §5.4)

| ID | Proves | Level | Phase |
|---|---|---|---|
| T-LOAN-01 | A member without a valid authority record cannot apply for a group loan | Unit + integration | P5 |
| T-LOAN-02 | 600,000 without a guarantor is blocked; 400,000 is allowed (threshold A-010) | Unit | P5 |
| T-LOAN-03 | A material change after approval forces re-approval | Unit + integration | P5 |
| T-LOAN-04 | Finance confirms disbursement; approved-but-not-disbursed is excluded from "loans advanced" | Integration | P5 |
| T-LOAN-04b | Dashboard "loans advanced" counts each tranche once and only when confirmed | Integration | P6 |
| T-LOAN-05 | Self-approval is blocked, as is approval by the appraising officer | Unit + integration | P5 |
| T-LOAN-06 | Offer terms come only from the approved product version | Unit | P5 |
| T-LOAN-07 | Group loans and internal member loans never appear in the same total | Integration | P5 |

### Meetings and attendance (brief §5.2)

| ID | Proves | Level | Phase |
|---|---|---|---|
| T-ATT-01 | 6 of 8 meetings = 75 % | Unit | P3 |
| T-ATT-02 | 0 expected meetings = "Not applicable", not 0 % or 100 % | Unit | P3 |
| T-ATT-03 | Cancelled meetings and meetings before joining are excluded; a member who left mid-period is handled correctly | Unit | P3 |
| T-ATT-04 | Group % uses total opportunities, never the average of individual percentages; photos never count as attendance | Unit | P3 |
| T-ATT-05 | A closed register is amended only through the controlled path (original, new, reason, reviewer, timestamp) | Integration | P3 |

### Authorisation and separation of duties

| ID | Proves | Level | Phase |
|---|---|---|---|
| T-RBAC-01 | Every non-public route declares a module and action, and at least one role can perform it | Unit (runs now) | P0 |
| T-RBAC-02 | Matrix allow/deny cases, including scope containment and self scope | Unit (runs now) | P0 |
| T-RBAC-03 | IT administrator has no financial rights; role grants need a second administrator | Unit (runs now) | P0 |
| T-RBAC-04 | Exports require the `export` action and are audited with actor, scope, filter and row count | Integration | P6 |
| T-RBAC-05 | Generated route × role × scope suite: allowed and denied for every combination | Generated | P2 |

### Security negatives

| ID | Proves | Level | Phase |
|---|---|---|---|
| T-SEC-01 | Login rate limits, lockout backoff and generic errors under brute force | Integration | P1 |
| T-SEC-02 | Refresh rotation; reuse revokes the whole family; revoked device cannot refresh | Integration | P1 |
| T-SEC-03 | Staff without MFA cannot reach verify/approve/disburse; step-up is required and expires | Integration | P1 |
| T-SEC-04 | No user enumeration via errors or timing | Integration | P1 |
| T-SEC-05 | Logout revokes the session family; access token expiry ≤ 15 min | Integration | P1 |
| T-SEC-06 | Forged, expired and cross-environment tokens are rejected; `alg: none` rejected | Unit + integration | P1 |
| T-SEC-07 | Malware upload is quarantined and never served | Integration (ClamAV) | P1 |
| T-SEC-08 | Content-type spoofing rejected by magic-byte validation; oversized file rejected | Integration | P1 |
| T-SEC-09 | Private files are unreachable without authorisation; signed URLs expire | Integration | P1 |
| T-SEC-10 | EXIF metadata is stripped from uploaded images | Integration | P1 |
| T-SEC-11 | Oversized bodies and uploads are rejected without exhausting memory | Integration | P1 |
| T-SEC-12 | Rate limits and timeouts hold under a burst | k6 | P9 |
| T-SEC-13 | SQL injection payloads are inert (parameterised queries only) | Integration | P2 |

### Offline sync

| ID | Proves | Level | Phase |
|---|---|---|---|
| T-SYNC-01 | Duplicate sync of the same draft creates one record | Integration | P8 |
| T-SYNC-02 | Partial sync is retried without duplication or loss | Integration | P8 |
| T-SYNC-03 | Clock skew cannot backdate a payment; server time wins | Integration | P8 |
| T-SYNC-04 | A revoked session during sync is refused and the local partition is wiped | Integration | P8 |
| T-SYNC-05 | Conflicting edits are queued for human review, never overwritten | Integration | P8 |
| T-SYNC-06 | Verification, approval, disbursement and posting are not available offline | Client test | P8 |
| T-SYNC-07 | Local database is encrypted and wipes after repeated failed unlocks | Client test | P8 |

### Audit, reporting, ranking

| ID | Proves | Level | Phase |
|---|---|---|---|
| T-AUD-01 | Every step of the acceptance script leaves an audit row with actor, role, scope, channel and correlation id | Integration | P1/P9 |
| T-AUD-02 | Tampering with an audit row (manual edit in a test database) is detected by the chain verifier | Integration | P1/P9 |
| T-DASH-01 | Every dashboard card reconciles to its drill-down and export | Integration | P6 |
| T-RANK-01 | Only eligible groups are ranked; a group with a loan >30 days overdue is excluded; the policy version and snapshot are stored | Integration | P6 |

### End-to-end and operations

| ID | Proves | Level | Phase |
|---|---|---|---|
| T-E2E-01…12 | The twelve acceptance-script journeys, on API and client | E2E | P10 |
| T-OPS-01 | Backup taken, database destroyed, restored, application verified; timing recorded against A-007 | Script | P10 |
| T-OPS-02 | README followed literally in a clean environment succeeds end to end | Manual + script | P10 |
| T-A11Y-01 | Large fonts, contrast and screen-reader labels on the primary journeys | Manual | P8/P10 |

## 4. Evidence and reporting

`docs/VERIFICATION_REPORT.md` records, for every gate: the exact command, the
date, the environment (OS, Go/Flutter version, image digest) and the real output
summary. A gate that could not run is listed with the reason and marked
**Not verified here**.

## 5. Test data rules

- Seed data is clearly marked demo data, blocked in production by the A-026
  guard, and safe to purge.
- Staging uses synthetic data only; no real member data is copied into any
  non-production environment.
- Security tests create their own records and clean them up; they never mutate
  records they did not create.

## 6. Status at P2

| Check | Command | Result |
|---|---|---|
| Go build | `go build ./...` | Passed (Go 1.26.11, golang:1.26 container) |
| Go vet | `go vet ./...` | Passed |
| gofmt | `gofmt -l .` | Empty |
| golangci-lint | `golangci-lint run` (v2.15.0) | Passed — 0 issues |
| Go tests | `go test ./...` (also `-race`) | Passed — `audit`, `auth`, `groups`, `httpapi`, `platform/config`, `platform/logging`, `rbac` |
| Coverage gate | `scripts/check-coverage.sh` | `rbac` 87.6 % (threshold 85 %); `payments`, `loans`, `audit`, `attendance` reported pending |
| OpenAPI lint | `@redocly/cli lint backend/api/openapi.yaml` | Valid, 4 warnings (no 4xx responses declared on platform probes) |
| Dev stack boot | `docker compose -f deploy/docker-compose.dev.yml` equivalent | `db` healthy, `migrate` exit 0, `api` healthy, `/healthz` 200, `/readyz` `database: ok, schema: ok` |
| Status page render | Preview browser | Rendered with 0 console errors |
| RBAC matrix validity | `TestEmbeddedMatrixIsValid` | Passed — 10 roles, 94 grants, 11 separation rules |
| RBAC allow/deny | `TestAuthorize` | Passed — 20 cases |
| Separation of duties | `TestSeparationOfDuties` | Passed — 10 cases |
| Doc drift | `TestDocMatchesMatrix` | Passed |
| Route authorisation coverage | `TestEveryRouteDeclaresAuthorization` | Passed — 7 public routes, no undeclared route |
| Generated route × role × scope suite (T-RBAC-05) | `TestRouteRoleScopeMatrix` | Passed — every route, 10 roles, 5 binding scopes |
| Registration lifecycle (P2) | `TestGroupRegistrationLifecycle`, `TestReviewerCannotActivateOwnGroup`, `TestMembershipAndAuthorityLifecycle`, `TestConsentUpsertKeepsOneRowPerNotice` | Passed — database integration |
| RLS isolation | `TestRLSHidesRowsWithoutScopes`, `TestGroupRegistrationLifecycle` (cross-branch) | Passed |
| SQL injection payload inert (T-SEC-13) | `TestSQLInjectionPayloadIsInert` | Passed — payload stored literally, table intact |
| Log redaction | `TestRedactSensitiveAttributes` | Passed |
| Config validation | `TestLoad*` | Passed |
| Flutter analyze/test | — | **Not verified here** (no Flutter SDK in this environment) |
| GitHub Actions pipeline | — | **Not verified here** (runs on GitHub, not in this sandbox) |
| Coverage gate | `scripts/check-coverage.sh` | Not yet meaningful — domain packages land in P1–P5 |
| k6, restore drill, E2E | — | Not attempted (P9/P10) |
