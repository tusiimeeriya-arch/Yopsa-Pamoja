# API guide

**Status:** P2 — authentication and the registration domain are implemented.
This guide is the human-readable companion to
[`backend/api/openapi.yaml`](../backend/api/openapi.yaml), which is the
contract. Where they disagree, the OpenAPI document wins and this file is the
bug.

Base URL in development: `http://localhost:3000`. Every response carries an
`X-Request-ID` header (the correlation id used in logs and the audit trail) and
errors include it in the body.

## Authentication

Sign in with `POST /api/v1/auth/login` (phone, password, TOTP code when the
account requires MFA). The response contains a short-lived access token (≤ 15
minutes) and a rotating refresh token. Send the access token on every other
request:

```
Authorization: Bearer <access token>
```

Refresh with `POST /api/v1/auth/refresh`; reuse of a rotated token revokes the
whole session family. See [AUTH.md](AUTH.md) for the token, session and step-up
rules.

## Errors

| Status | `error` | Meaning |
|---|---|---|
| 400 | `invalid_request` | The body or query is malformed. A safe `detail` may name the field. |
| 401 | `unauthorized` | Missing, expired or revoked access token. |
| 403 | `forbidden` | The caller's role bindings do not cover this action at this scope. |
| 404 | `not_found` | No such record in the caller's scopes. A record outside the caller's scopes is indistinguishable from a missing one. |
| 409 | `conflict` | Duplicate record, wrong lifecycle state, or a stale `version`. |
| 500 | `internal_error` | Unexpected failure; the detail is in the log with the request id. |

Authorisation is default deny and scoped. A role binding at a branch or legal
entity satisfies grants at the same or a narrower scope; a member's grants are
limited to their own records. The full matrix is in [RBAC.md](RBAC.md).

## Registration endpoints (P2)

| Method | Path | Module.action | Notes |
|---|---|---|---|
| GET | `/api/v1/group-types` | public | Reference data for registration forms. |
| GET | `/api/v1/legal-entities` | registration.view | Only entities in the caller's scopes. |
| GET | `/api/v1/legal-entities/{id}/branches` | registration.view | Branches of one entity. |
| GET | `/api/v1/groups?branch_id=…` | registration.view | Groups in a branch. |
| GET | `/api/v1/groups?legal_entity_id=…` | registration.view | Groups in an entity. Supply exactly one filter. |
| POST | `/api/v1/groups` | registration.create | Registers a group in `draft`. |
| GET | `/api/v1/groups/{id}` | registration.view | One group. |
| PATCH | `/api/v1/groups/{id}` | registration.edit_draft | Draft or returned groups only; requires `version`. |
| POST | `/api/v1/groups/{id}/submit` | registration.submit | Sends a complete draft for review. |
| POST | `/api/v1/groups/{id}/review` | registration.review_activate | `activate`, `return` or `reject`. |
| GET | `/api/v1/groups/{id}/members` | registration.view | Roster, including end-dated memberships. |
| POST | `/api/v1/groups/{id}/members` | registration.create | Adds a member; creates the member record when the phone is new. |
| POST | `/api/v1/groups/{id}/members/{membershipID}/end` | registration.edit_draft | End-dates a membership. |
| GET | `/api/v1/groups/{id}/authorities` | registration.view | Office holders, past and current. |
| POST | `/api/v1/groups/{id}/authorities` | registration.create | Appoints an active member to chairperson, secretary or treasurer. |
| POST | `/api/v1/groups/{id}/authorities/{authorityID}/end` | registration.edit_draft | End-dates an appointment. |
| POST | `/api/v1/members/{id}/consents` | registration.create | Records consent for a purpose and notice version. |

### Registering a group

```http
POST /api/v1/groups
Authorization: Bearer <access token>
Content-Type: application/json

{
  "branch_id": "0f0c…",
  "type_code": "vsla",
  "name": "Kampala Women's VSLA",
  "formed_on": "2026-01-15",
  "registration_evidence_id": "9a1e…"
}
```

The group is created in `draft` with version `1`. `registration_evidence_id`
refers to the private object-storage record that arrives with the storage
service (P1/P3); until then the value is a forward reference.

Submission requires:

- `formed_on` and registration evidence recorded;
- at least one active membership;
- a granted `membership` consent for every active member.

The reviewer may not be the creator (SD-06), and the database trigger enforces
the same rule. Returning or rejecting requires a note.

### Lifecycle

```
draft → submitted → under_review → active
                  ↘ under_review → returned → submitted
                  ↘ under_review → rejected
active ↔ suspended, active/suspended → closed
```

Only the transitions above are accepted; the trigger in
`000003_p2_registration` rejects anything else. Edits to a group carry the
`version` read from the previous response; a stale version is a `409` so two
people cannot silently overwrite each other.

### Membership, authority and consent

- Memberships are end-dated, never deleted. A person can re-join with a new
  membership row and a new member number.
- One current holder per office per group; end the current appointment before
  appointing a successor.
- Consent is stored per member, purpose and notice version. Repeating the same
  decision updates it; a new notice version creates a new row.
- A member's phone number is encrypted at rest and looked up by keyed hash. It
  is never returned by the API.

## Dates, money and identifiers

- Dates are `YYYY-MM-DD` business dates in Africa/Kampala. A future date is
  rejected where it makes no sense (a group cannot have formed tomorrow).
- Money is whole UGX (`BIGINT`); no registration endpoint moves money.
- Identifiers are UUIDs generated server-side; sequential ids are never exposed.

## Idempotency

The idempotency store and its table exist from P1. The registration endpoints
do not yet require an `Idempotency-Key`; the guard becomes mandatory with the
payments endpoints (P4, test T-PAY-02), where a duplicate submission would
change a financial fact. Clients may send the header now; it is ignored.
