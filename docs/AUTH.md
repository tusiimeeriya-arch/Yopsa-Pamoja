# Authentication and row-level security (P1)

**Status:** implemented. This document describes what the P1 build actually does;
the policy values it reads are tracked in [ASSUMPTIONS.md](ASSUMPTIONS.md)
(A-008, A-009, A-019, A-029, A-030, A-036, A-037).

## 1. Endpoints

| Method | Path | Auth | Purpose |
|---|---|---|---|
| `POST` | `/api/v1/auth/login` | none | Password (+ TOTP for staff) sign-in |
| `POST` | `/api/v1/auth/refresh` | refresh token in body | Rotate the refresh token |
| `POST` | `/api/v1/auth/logout` | bearer | Revoke the caller's session family |
| `GET` | `/api/v1/auth/sessions` | bearer | List the caller's sessions |
| `POST` | `/api/v1/auth/sessions/{id}/revoke` | bearer | Revoke one of the caller's families |
| `POST` | `/api/v1/auth/step-up` | bearer | Fresh password + TOTP before a sensitive action |

Every credential failure returns the same `401 invalid_credentials`; the account
is never named (T-SEC-04). Rate limits return `429` with `Retry-After`.

## 2. Credentials and tokens

| Concern | Implementation |
|---|---|
| Password hash | Argon2id, PHC-encoded (`$argon2id$v=19$m=65536,t=3,p=2$…`); parameters stored with the credential; `NeedsRehash` drives rehashing on login |
| Timing | An unknown account still performs a dummy Argon2 verification |
| Phone | Normalised, then keyed-HMAC hashed for lookup; the number itself is envelope-encrypted when stored |
| Access token | JWT HS256, ≤ 15 minutes, `iss`/`aud`/`exp`/`nbf` checked, `env` claim prevents cross-environment use, `alg: none` rejected |
| Refresh token | 32 random bytes, opaque, stored only as SHA-256; rotated on every use |
| Reuse | Presenting an already-rotated token revokes the whole session family and is audited |
| Session lifetime | Absolute 14 days (`AUTH_REFRESH_TTL`); idle limits 30 min staff / 5 min shared phone; at most 5 live devices |
| Step-up | `POST /auth/step-up` records `step_up_at`; `Principal.StepUpFresh(window, now)` gates sensitive actions |

Roles and scopes are snapshotted into the access token; the session is re-checked
on every request, so revocation is immediate and a role change takes effect
within one access-token lifetime.

## 3. Lockout and rate limiting

- Five failures (`AUTH_LOCKOUT_FAILURES`) lock the account; the delay doubles
  from one minute to fifteen (`AUTH_LOCKOUT_BASE` / `AUTH_LOCKOUT_MAX`).
- Login is also throttled per IP address (5/min) and per account (10/hour) in
  process. The database lockout is the durable control across instances.

## 4. Database roles and row-level security

Migration `000002_p1_auth_rls` creates four roles:

| Role | Rights |
|---|---|
| `pamoja_owner` | owns the schema, runs migrations |
| `pamoja_app` | the API/worker role: no DDL, no `DELETE`, not owner, not superuser, not `BYPASSRLS` |
| `pamoja_readonly` | `SELECT` on the audit log, still subject to RLS |
| `pamoja_auth` | **no login**, `BYPASSRLS`; owns only the fixed `SECURITY DEFINER` functions that must read identity rows before a session exists (login, refresh, session rotation, audit append/verify) |

RLS is **enabled and forced** on `app_user`, `user_credential`, `totp_secret`,
`recovery_code`, `session`, `role_grant`, `audit_log` and `idempotency_key`.

- `pamoja_app` can read only its own user row, sessions and role grants.
- Credentials, TOTP secrets and recovery codes are never directly readable by
  the API role; they are reached through the auth functions.
- `audit_log` has no `SELECT` policy for the API role and no direct `INSERT`
  grant: rows are written through `pamoja.audit_append` and verified through
  `pamoja.audit_verify`.
- Every transaction sets `pamoja.actor_id`, `pamoja.scope_ids` and
  `pamoja.request_id` with `set_config(…, true)` (`internal/platform/db`), so the
  settings never leak between pooled requests. A transaction without an actor
  sees no rows (proved by `TestRLSWithoutActorSeesNothing`).

## 5. Audit

Each authentication action appends one row in the same transaction as the change:
`auth.login`, `auth.login_failed` (including `unknown_account`), `auth.refresh`,
`auth.session_revoked` (`logout`, `refresh_reuse`, `expired`, `device_limit`,
`user_revoked`), `auth.step_up`.

`hash = SHA-256(prev_hash || canonical_row)` is computed by a `BEFORE INSERT`
trigger under a transaction-scoped advisory lock, so every insert is chained
regardless of the code path. `UPDATE`, `DELETE` and `TRUNCATE` are rejected by
triggers for every role. `cmd/auditverify` (or `make audit-verify`) walks the
chain and exits non-zero at the first break.

## 6. Configuration

All values are environment variables with defaults from ASSUMPTIONS.md; see
`deploy/.env.example`. Three key materials are required and must never be
committed:

| Variable | Used for |
|---|---|
| `AUTH_TOKEN_SECRET` | HMAC key that signs access tokens (stretched with SHA-256) |
| `PAMOJA_LOOKUP_KEY` | keyed hash for phone-number lookup |
| `PAMOJA_MASTER_KEY` | envelope-encryption master key (AES-256-GCM, `v1:<keyid>:<nonce>:<ct>`) |

## 7. Tests

- Unit: Argon2id, TOTP against the RFC 6238 vectors, JWT (expiry, wrong
  key/audience/environment, `alg: none`), rate limiter, keyring, redaction,
  HTTP handler mapping, route coverage.
- Database integration (run when `TEST_DATABASE_URL` is set; CI provides
  PostgreSQL 16): RLS visibility with and without an actor, append-only audit and
  chain verification including a simulated tamper, login → refresh → reuse
  revocation → step-up → logout, and lockout.

## 8. Not in this phase

Recovery-code generation and redemption (table exists, flow lands with the user
administration module), member SMS OTP (A-020), per-device idle lock in the
client, and the storage service (evidence uploads).
