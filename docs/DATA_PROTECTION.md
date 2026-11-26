# Data protection

**Status:** P2 — member data, consent capture and the supporting controls are
implemented. Retention periods, the legal entity's registration details and the
approved notice wording remain management decisions (A-001, A-006); this
document records the proposed position, not a legal sign-off.

Pamoja processes personal data for Yopsa Microfinance (Uganda) under the Data
Protection and Privacy Act, 2019 (DPPA) and its regulations. The Data
Protection Officer named by management owns this document and the notices
published to members.

## 1. What we process and why

| Data | Purpose | Storage |
|---|---|---|
| Member full name, gender, date of birth | Identify the member, compute age-related eligibility, produce the register | `pamoja.member` |
| Member phone number | Contact, duplicate detection, sign-in for member accounts | `pamoja.member.phone_enc` (AES-256-GCM envelope encryption); `phone_hash` (HMAC-SHA256 keyed hash) for lookup |
| Membership number, join/leave dates | Group roster and attendance eligibility | `pamoja.membership` |
| Consent decisions, purpose, notice version, capture time and officer | Prove lawful basis and honour withdrawal | `pamoja.consent_record` |
| Authority appointments (office, term, resolution reference) | Know who may bind the group | `pamoja.authority_record` |
| Audit entries (actor, role, scope, action, before/after redacted) | Accountability, tamper evidence | `pamoja.audit_log` (append-only hash chain) |

No national ID, photograph, biometric template, location trail or financial
account number is collected in P2. Photos, when a later phase enables them, are
governed by the photo policy (A-005) and are never proof of attendance.

## 2. Lawful basis

- **Consent** for membership registration and for optional purposes
  (`data_processing`, `photos`, `notifications`). Consent is captured per
  purpose and notice version, with the officer who captured it.
- **Contract / legitimate interest** for operating the member's group records
  and for the audit trail that protects members and Yopsa.
- **Legal obligation** for records Yopsa must retain under financial and
  cooperative regulation.

Consent is never a condition of a service that does not need it, and refusing
an optional purpose does not block membership.

## 3. Notice and consent

The current notice version is **2026-11**, defined as `ConsentNoticeVersion` in
`backend/internal/groups/types.go`. The notice must state, in plain language:

1. who the controller is and how to contact the DPO;
2. what is collected and why;
3. who it is shared with (Yopsa staff and, where applicable, the approved
   ledger and messaging providers);
4. how long it is kept;
5. the member's rights and how to exercise them;
6. that consent can be withdrawn.

Bump the version when the notice changes materially; new captures use the new
version and old records keep the version they were captured against. A
withdrawal is recorded as a new decision (`granted: false`), not by deleting
history.

## 4. Retention

Proposed defaults, awaiting approval under A-006:

| Record | Proposed retention |
|---|---|
| Member and membership records | Life of the group membership plus 7 years |
| Consent records | Life of the membership plus 7 years |
| Authority records | Life of the group plus 7 years |
| Audit log | 7 years, append-only |
| Application logs | 90 days, no personal data in the message |

Deletion after the retention period is a controlled, audited purge; financial
and audit rows are not deleted outside that process. Backups expire on the
schedule approved with A-007.

## 5. Member rights

| Right | How Pamoja supports it |
|---|---|
| Access | A member sees their own member record through their role binding; staff with `registration.view` produce a report |
| Correction | End-date the incorrect membership or authority record and create the corrected row; the audit trail shows both |
| Withdraw consent | Record a denied decision for the purpose; dependent processing stops |
| Object / restrict | Recorded as a dispute or a note on the member record, with an owner and deadline |
| Portability | A structured export of the member's own records (P6 reports/export) |
| Erasure | Applied where no legal retention duty applies; otherwise explained to the member |

Requests are logged, answered within the period set by the DPPA, and never
handled by editing the audit trail.

## 6. Security controls in place

- Envelope encryption for phone numbers; keyed hashes for lookup so the number
  is not needed to search (`ADR-0007`).
- PostgreSQL row-level security on every registration table, with scope helpers
  that cannot recurse into each other; the API role is not the owner and cannot
  bypass RLS (`ADR-0003`).
- Machine-checked RBAC with default deny and separation of duties (`RBAC.md`).
- Append-only audit hash chain with an offline verifier (`ADR-0004`).
- Structured logs that redact phone numbers, tokens and identifiers.
- No secrets in the repository; production key material comes from the
  deployment secret store.

## 7. Sharing and processors

Yopsa staff access records through scoped roles. The approved ledger
(Alogsys or equivalent) receives contractual schedules and postings, not member
contact details. SMS/WhatsApp delivery, when enabled, receives only the phone
number and message needed for that delivery. Object storage holds evidence in a
private bucket with signed, expiring URLs. Any cross-border processing needs a
DPPA transfer assessment before it is enabled.

## 8. Incidents

A suspected personal-data breach is reported to the DPO immediately, contained,
assessed for risk to members, and notified to the Authority and affected members
where the DPPA requires it. The audit chain and access logs are preserved as
evidence. The full runbook arrives in P9.

## 9. Review

Review this document whenever a phase adds a data category, a processor or a new
purpose, and at least annually. Each change updates the notice version and the
consent records capture that version.
