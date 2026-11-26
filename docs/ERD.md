# Entity-relationship model

**Status:** P0 logical model — delivered for approval. Physical DDL lands in
P1–P5; every table below carries the scope columns and audit trail described in
[ARCHITECTURE.md](ARCHITECTURE.md).

The model is split by domain so each diagram stays readable. Tables shared
between diagrams are drawn once and referenced by name.

## Conventions

| Concern | Rule |
|---|---|
| Money | `BIGINT` in **whole UGX**. Never float, never decimal. `CHECK (amount_ugx > 0)` on every amount. |
| Identifiers | `UUID` **v7**, generated server-side. Never expose sequential ids. |
| Time | `timestamptz`, stored UTC, rendered in Africa/Kampala. Business dates are `date`. |
| Enumerations | `text` + `CHECK` constraint, never a database enum type (migrations stay simple). |
| Scope | Every scoped table carries `legal_entity_id`, `branch_id` and (where relevant) `group_id`, with RLS policies. |
| History | Financial and audit rows are append-only. Membership and branch assignment are end-dated, never deleted. |
| Optimistic locking | `version integer NOT NULL DEFAULT 1` on every mutable workflow row; updates are `WHERE id = $1 AND version = $2`. |
| Audit | Every state change writes an `audit_log` row in the same transaction. |
| Idempotency | Every state-changing request stores an `idempotency_key` row with a canonical request hash. |

## 1. Identity, organisation and authority

```mermaid
erDiagram
    LEGAL_ENTITY ||--o{ BRANCH : "has"
    BRANCH ||--o{ GROUP_BRANCH_HISTORY : "assigned"
    GROUPS ||--o{ GROUP_BRANCH_HISTORY : "moved through"
    GROUP_TYPE ||--o{ GROUPS : "classifies"
    LEGAL_ENTITY ||--o{ GROUPS : "owns"
    GROUPS ||--o{ MEMBERSHIP : "has"
    MEMBER ||--o{ MEMBERSHIP : "joins"
    MEMBER ||--o{ CONSENT_RECORD : "gives"
    GROUPS ||--o{ AUTHORITY_RECORD : "appoints"
    MEMBER ||--o{ AUTHORITY_RECORD : "holds"
    APP_USER ||--o{ ROLE_GRANT : "receives"
    APP_USER ||--o{ SESSION : "opens"
    APP_USER ||--o{ USER_CREDENTIAL : "has"
    APP_USER ||--o{ TOTP_SECRET : "enrols"
    APP_USER ||--o{ RECOVERY_CODE : "holds"

    LEGAL_ENTITY {
        uuid id PK
        text code UK
        text name
        text status
    }
    BRANCH {
        uuid id PK
        uuid legal_entity_id FK
        text code UK
        text name
        text district
        text status
    }
    GROUP_TYPE {
        text code PK
        text name
        text rules_version
    }
    GROUPS {
        uuid id PK
        uuid legal_entity_id FK
        uuid branch_id FK
        text type_code FK
        text name
        text registration_status
        uuid registration_evidence_id FK
        text status
        date formed_on
        date closed_on
        uuid created_by FK
        integer version
    }
    GROUP_BRANCH_HISTORY {
        uuid id PK
        uuid group_id FK
        uuid branch_id FK
        date effective_from
        date effective_to
    }
    MEMBER {
        uuid id PK
        text full_name
        bytea phone_enc
        text phone_hash
        text gender
        date date_of_birth
        text status
        timestamptz created_at
    }
    MEMBERSHIP {
        uuid id PK
        uuid member_id FK
        uuid group_id FK
        text member_no
        date joined_on
        date left_on
        text status
    }
    CONSENT_RECORD {
        uuid id PK
        uuid member_id FK
        text purpose
        boolean granted
        text notice_version
        timestamptz captured_at
        uuid captured_by FK
        uuid evidence_id FK
    }
    AUTHORITY_RECORD {
        uuid id PK
        uuid group_id FK
        uuid member_id FK
        text office
        date effective_from
        date effective_to
        uuid resolution_evidence_id FK
        uuid recorded_by FK
    }
    APP_USER {
        uuid id PK
        text full_name
        text phone_hash UK
        boolean is_staff
        boolean mfa_required
        text status
        timestamptz created_at
    }
    USER_CREDENTIAL {
        uuid user_id PK
        text password_hash
        text argon2_params
        timestamptz changed_at
    }
    TOTP_SECRET {
        uuid user_id PK
        bytea secret_enc
        timestamptz confirmed_at
    }
    RECOVERY_CODE {
        uuid id PK
        uuid user_id FK
        text code_hash
        timestamptz used_at
    }
    SESSION {
        uuid id PK
        uuid user_id FK
        uuid family_id
        text refresh_hash
        text device_id
        text user_agent
        timestamptz expires_at
        timestamptz revoked_at
        timestamptz last_seen_at
    }
    ROLE_GRANT {
        uuid id PK
        uuid user_id FK
        text role
        uuid legal_entity_id FK
        uuid branch_id FK
        uuid group_id FK
        date effective_from
        date effective_to
        uuid granted_by FK
        uuid approved_by FK
        text status
    }
```

**Notes**

- `groups` is plural because `group` is a reserved SQL word.
- A group type label never proves legal registration: `registration_status` and
  `registration_evidence_id` are separate, and the lifecycle gate requires
  review by someone other than the creator.
- `phone_enc` is envelope-encrypted; `phone_hash` is a keyed hash used for
  lookup and duplicate detection without exposing the number.
- A role grant is only valid inside its scope and date range; `approved_by` is
  the second administrator required by SD-10.

## 2. Meetings, attendance, minutes

```mermaid
erDiagram
    GROUPS ||--o{ MEETING : "holds"
    MEETING ||--o{ MEETING_ATTENDANCE : "records"
    MEMBER ||--o{ MEETING_ATTENDANCE : "attends"
    MEETING ||--o{ MEETING_MINUTES : "has"
    MEETING ||--o{ MEETING_RESOLUTION : "passes"
    MEETING ||--o{ ACTION_ITEM : "produces"
    APP_USER ||--o{ ACTION_ITEM : "owns"
    MEETING_ATTENDANCE ||--o{ MEETING_ATTENDANCE : "amended by"

    MEETING {
        uuid id PK
        uuid group_id FK
        timestamptz scheduled_for
        text location
        text status
        uuid opened_by FK
        timestamptz opened_at
        uuid register_confirmed_by FK
        timestamptz register_confirmed_at
        uuid closed_by FK
        timestamptz closed_at
        integer version
    }
    MEETING_ATTENDANCE {
        uuid id PK
        uuid meeting_id FK
        uuid member_id FK
        text status
        text checkin_channel
        uuid recorded_by FK
        timestamptz recorded_at
        uuid confirmed_by FK
        uuid amendment_of_id FK
        text amendment_reason
        integer version
    }
    MEETING_MINUTES {
        uuid id PK
        uuid meeting_id FK
        text body
        integer version
        uuid confirmed_by FK
        timestamptz confirmed_at
        uuid amendment_of_id FK
    }
    MEETING_RESOLUTION {
        uuid id PK
        uuid meeting_id FK
        text body
        integer votes_for
        integer votes_against
        integer abstentions
        uuid recorded_by FK
    }
    ACTION_ITEM {
        uuid id PK
        uuid meeting_id FK
        uuid owner_user_id FK
        text description
        date due_on
        text status
        timestamptz closed_at
    }
```

**Attendance formulas** (unit-tested in P3):

- Individual % = meetings attended ÷ eligible expected meetings × 100.
  Eligible = meetings on or after `joined_on`, on or before `left_on`, not
  cancelled, and (for the group's configured policy) late/excused treated as
  configured. Zero expected meetings ⇒ **"Not applicable"**, never 0% or 100%.
- Group % = Σ attended member-meeting opportunities ÷ Σ expected opportunities.
  It is **never** the average of individual percentages.
- Amendments keep `amendment_of_id`, `amendment_reason`, the reviewer and the
  timestamp; the original row is never overwritten.

## 3. Payments, receipts and reconciliation

```mermaid
erDiagram
    GROUPS ||--o{ PAYMENT_ENTRY : "records"
    MEMBER ||--o{ PAYMENT_ENTRY : "pays"
    MEETING ||--o{ PAYMENT_ENTRY : "at"
    GROUPS ||--o{ GROUP_SUBMISSION : "submits"
    GROUP_SUBMISSION ||--o{ PAYMENT_ENTRY : "locks"
    GROUP_SUBMISSION ||--o{ RECEIPT : "confirmed by"
    RECEIPT ||--o{ ALLOCATION : "allocated by"
    PAYMENT_ENTRY ||--o{ ALLOCATION : "settled by"
    ALLOCATION ||--o{ LEDGER_POSTING : "posted as"
    GROUP_SUBMISSION ||--o{ FINANCE_EXCEPTION : "raises"
    RECEIPT ||--o{ FINANCE_EXCEPTION : "raises"
    LEDGER_POSTING ||--o{ FINANCE_EXCEPTION : "raises"
    CORRECTION }o--|| PAYMENT_ENTRY : "corrects"
    CORRECTION }o--|| RECEIPT : "reverses"

    PAYMENT_ENTRY {
        uuid id PK
        uuid group_id FK
        uuid member_id FK
        uuid meeting_id FK
        uuid submission_id FK
        text category
        bigint amount_ugx
        date paid_on
        text channel
        text source_ref
        uuid evidence_id FK
        text status
        uuid recorded_by FK
        text recorded_via
        text idempotency_key UK
        integer version
    }
    GROUP_SUBMISSION {
        uuid id PK
        uuid group_id FK
        uuid meeting_id FK
        date period_start
        date period_end
        bigint declared_total_ugx
        integer entry_count
        text status
        uuid locked_by FK
        timestamptz locked_at
        text locked_hash
        uuid reviewer_id FK
        timestamptz reviewed_at
        text review_note
        integer version
    }
    RECEIPT {
        uuid id PK
        uuid group_id FK
        uuid submission_id FK
        date received_on
        bigint amount_ugx
        text source
        text provider_ref
        uuid evidence_id FK
        text status
        uuid recorded_by FK
        uuid verified_by FK
        timestamptz confirmed_at
    }
    ALLOCATION {
        uuid id PK
        uuid receipt_id FK
        uuid payment_entry_id FK
        bigint amount_ugx
        text status
        uuid created_by FK
        timestamptz created_at
    }
    LEDGER_POSTING {
        uuid id PK
        uuid allocation_id FK
        text adapter
        text external_ref UK
        text status
        timestamptz posted_at
        integer attempts
        text last_error
    }
    FINANCE_EXCEPTION {
        uuid id PK
        text kind
        uuid submission_id FK
        uuid receipt_id FK
        uuid posting_id FK
        uuid owner_user_id FK
        date due_on
        text status
        text resolution_note
        uuid resolved_by FK
        timestamptz resolved_at
    }
    CORRECTION {
        uuid id PK
        text entity_type
        uuid entity_id
        text kind
        text reason
        uuid requested_by FK
        uuid approved_by FK
        timestamptz approved_at
        jsonb original_snapshot
        jsonb new_snapshot
        text status
    }
```

**Database-enforced invariants**

| Invariant | Mechanism |
|---|---|
| Allocation total never exceeds the confirmed receipt | trigger on `allocation` (INSERT/UPDATE) + `CHECK (amount_ugx > 0)` |
| Submission total equals the sum of its entries | trigger on lock; `locked_hash` pins the entry set |
| Locked/verified rows cannot be edited or deleted | trigger + revoked `UPDATE`/`DELETE` for the API role |
| Only valid workflow transitions | transition trigger per table |
| A receipt source reference is unique | `UNIQUE (source, provider_ref)` |
| Pending entries never affect balances | balances are derived from postings only, never from entries |
| Corrections are linked, never destructive | `correction` rows + immutable originals |

**Verification rules**

- The verifier must be independent of the treasurer and of every recorder of
  the entries in the submission (SD-01, SD-02).
- A screenshot is not verification. A receipt needs a source reference and an
  evidence record; a cash path needs a receipt, a handover and a deposit
  reconciliation performed by different people.
- A 200,000 declared / 180,000 received case shows a 20,000 difference, blocks
  full verification, and is resolved only by locating funds or by a logged
  partial verification of fully evidenced entries. **The shortage is never
  spread automatically.**
- Verification succeeded but posting failed ⇒ status **Posting pending** plus an
  auto-created finance exception with owner and deadline.

## 4. Group borrowing

```mermaid
erDiagram
    LOAN_PRODUCT ||--o{ LOAN_APPLICATION : "prices"
    GROUPS ||--o{ LOAN_APPLICATION : "applies"
    AUTHORITY_RECORD ||--o{ LOAN_APPLICATION : "authorises"
    LOAN_APPLICATION ||--o{ LOAN_APPRAISAL : "appraised by"
    LOAN_APPLICATION ||--o{ BORROWER_CHECK : "checked by"
    LOAN_APPLICATION ||--o{ LOAN_GUARANTEE : "guaranteed by"
    LOAN_APPLICATION ||--o{ LOAN_DECISION : "decided by"
    LOAN_APPLICATION ||--o{ LOAN_OFFER : "offered"
    LOAN_OFFER ||--o{ LOAN_DISBURSEMENT : "disbursed as"
    MEMBER ||--o{ LOAN_GUARANTEE : "guarantees"

    LOAN_PRODUCT {
        uuid id PK
        text code UK
        text name
        bigint min_amount_ugx
        bigint max_amount_ugx
        integer max_term_months
        bigint guarantor_required_above_ugx
        integer version
        uuid approved_by FK
        date effective_from
        text status
    }
    LOAN_APPLICATION {
        uuid id PK
        uuid group_id FK
        uuid product_id FK
        bigint requested_amount_ugx
        text purpose
        integer term_months
        text repayment_source
        uuid authority_record_id FK
        uuid applied_by FK
        text status
        integer version
        timestamptz created_at
    }
    LOAN_APPRAISAL {
        uuid id PK
        uuid application_id FK
        uuid officer_id FK
        text cash_flow_notes
        bigint existing_obligations_ugx
        text recommendation
        timestamptz created_at
    }
    BORROWER_CHECK {
        uuid id PK
        uuid application_id FK
        text kind
        text result
        uuid checked_by FK
        timestamptz checked_at
    }
    LOAN_GUARANTEE {
        uuid id PK
        uuid application_id FK
        uuid guarantor_member_id FK
        bigint amount_ugx
        uuid checked_by FK
        timestamptz checked_at
    }
    LOAN_DECISION {
        uuid id PK
        uuid application_id FK
        text stage
        text decision
        bigint approved_amount_ugx
        text conditions
        text reason
        uuid decided_by FK
        timestamptz decided_at
    }
    LOAN_OFFER {
        uuid id PK
        uuid application_id FK
        bigint amount_ugx
        integer term_months
        text schedule_ref
        uuid issued_by FK
        timestamptz issued_at
        date expires_on
        uuid accepted_by FK
        timestamptz accepted_at
    }
    LOAN_DISBURSEMENT {
        uuid id PK
        uuid application_id FK
        uuid offer_id FK
        integer tranche_no
        bigint amount_ugx
        date disbursed_on
        uuid confirmed_by FK
        text external_ref UK
        text status
    }
```

**Rules**

- Only a group's authorised representative, with an authority record valid on
  the application date, may bind the group.
- Every application passes the mandatory **operations review** before a decision.
- Approval ≠ disbursement. A loan counts as advanced only after a confirmed
  disbursement by Finance, independent of the approver (SD-04).
- A guarantor check is required above the configured threshold
  (UGX 500,000, A-003). Product limits and approval authorities come from
  versioned, approved configuration — never inferred from another product.
- Material change after approval forces re-approval.
- Group loans to groups and internal member loans are separate workflows and
  separate data; they are never summed.

## 5. Learning, comments, disputes, tasks, notifications

```mermaid
erDiagram
    LEARNING_ITEM ||--o{ LEARNING_ASSIGNMENT : "assigned"
    LEARNING_ITEM ||--o{ LEARNING_PROGRESS : "tracked"
    APP_USER ||--o{ LEARNING_PROGRESS : "progresses"
    GROUPS ||--o{ COMMENT : "discusses"
    APP_USER ||--o{ COMMENT : "writes"
    MEMBER ||--o{ DISPUTE : "raises"
    GROUPS ||--o{ DISPUTE : "concerns"
    APP_USER ||--o{ DISPUTE : "reviews"
    APP_USER ||--o{ TASK : "owns"
    TASK ||--o{ NOTIFICATION : "triggers"
    APP_USER ||--o{ NOTIFICATION : "receives"

    LEARNING_ITEM {
        uuid id PK
        text title
        text category
        text language
        integer version
        text content_type
        text storage_key
        text content_owner
        date review_due_on
        timestamptz published_at
    }
    LEARNING_ASSIGNMENT {
        uuid id PK
        uuid item_id FK
        uuid group_id FK
        uuid assigned_by FK
        date due_on
    }
    LEARNING_PROGRESS {
        uuid id PK
        uuid user_id FK
        uuid item_id FK
        timestamptz opened_at
        timestamptz downloaded_at
        timestamptz completed_at
        integer version_seen
    }
    COMMENT {
        uuid id PK
        uuid group_id FK
        uuid meeting_id FK
        uuid author_id FK
        text body
        text visibility
        text status
        uuid moderated_by FK
        text removal_reason
        timestamptz created_at
    }
    DISPUTE {
        uuid id PK
        uuid member_id FK
        uuid group_id FK
        text kind
        text target_type
        uuid target_id
        text description
        jsonb original_value
        text status
        uuid reviewer_id FK
        text resolution
        timestamptz resolved_at
    }
    TASK {
        uuid id PK
        text kind
        uuid owner_user_id FK
        date due_on
        text entity_type
        uuid entity_id
        text status
        text priority
        timestamptz escalated_at
    }
    NOTIFICATION {
        uuid id PK
        uuid user_id FK
        uuid task_id FK
        text channel
        text template
        jsonb payload_redacted
        text status
        timestamptz sent_at
        timestamptz read_at
    }
```

**Rules**

- Opening or downloading a learning item is recorded separately from
  completion. Completion never clears arrears and never changes lending
  eligibility.
- Disputes retain the original value, the member's reason, the reviewer and the
  time. Resolution never overwrites the disputed record.
- Every notification and task has an owner and a deadline — both `NOT NULL`, and
  a trigger rejects an insert without them.

## 6. Platform: audit, idempotency, sync, configuration, snapshots

```mermaid
erDiagram
    AUDIT_LOG ||--o| AUDIT_LOG : "chains to"
    OUTBOX_EVENT ||--o{ SYNC_CONFLICT : "may raise"
    INTEGRATION_MESSAGE ||--o{ FINANCE_EXCEPTION : "may raise"
    RANKING_POLICY ||--o{ RANKING_SNAPSHOT : "scores"
    CONFIG_VALUE ||--o{ RANKING_POLICY : "supplies weights"

    AUDIT_LOG {
        bigint seq PK
        timestamptz occurred_at
        uuid actor_user_id
        text actor_role
        text scope_type
        uuid scope_id
        text action
        text entity_type
        uuid entity_id
        jsonb before_redacted
        jsonb after_redacted
        text reason
        text channel
        inet ip
        text device_id
        text correlation_id
        bytea prev_hash
        bytea hash
    }
    IDEMPOTENCY_KEY {
        uuid id PK
        uuid actor_user_id FK
        text key
        text route
        text request_hash
        integer response_status
        jsonb response_body_redacted
        timestamptz created_at
        timestamptz expires_at
    }
    OUTBOX_EVENT {
        uuid id PK
        uuid client_uuid UK
        uuid user_id FK
        text op_type
        jsonb payload
        text status
        integer attempts
        text last_error
        timestamptz created_at
    }
    SYNC_CONFLICT {
        uuid id PK
        uuid outbox_event_id FK
        text entity_type
        uuid entity_id
        integer client_version
        integer server_version
        text status
        uuid resolved_by FK
        timestamptz resolved_at
    }
    INTEGRATION_MESSAGE {
        uuid id PK
        text provider
        text external_id UK
        text direction
        jsonb payload_redacted
        boolean signature_verified
        timestamptz received_at
        timestamptz processed_at
        text status
        text error
    }
    CONFIG_VALUE {
        uuid id PK
        text key
        integer version
        jsonb value
        text description
        uuid approved_by FK
        date effective_from
    }
    RANKING_POLICY {
        uuid id PK
        integer version
        integer window_days
        integer min_loans
        integer min_meetings
        jsonb weights
        integer overdue_exclusion_days
        text tie_break
        uuid approved_by FK
        date effective_from
    }
    RANKING_SNAPSHOT {
        uuid id PK
        integer policy_version
        date period_end
        jsonb payload
        bytea hash
        timestamptz created_at
    }
    DASHBOARD_SNAPSHOT {
        uuid id PK
        text scope_type
        uuid scope_id
        text period
        date cut_off
        jsonb payload
        timestamptz generated_at
    }
```

**Rules**

- `audit_log` is append-only. `hash = SHA-256(prev_hash || canonical_row)`; a
  verifier command walks the chain, and a scheduled job alerts on any break.
  The latest hash is periodically anchored to an external write-once location.
- `idempotency_key` is unique per `(actor_user_id, key)`. Same key + same
  request hash replays the stored response; same key + different payload is
  `409`.
- `outbox_event.client_uuid` is the client-generated idempotency anchor for
  offline drafts; a duplicate sync is a no-op.
- `config_value` is the storage for [ASSUMPTIONS.md](ASSUMPTIONS.md): every
  policy value is versioned, dated and approved. A code constant is never the
  source of a policy rule.
- Ranking and dashboard snapshots store the policy version and the exact inputs
  so any historical ranking can be reproduced and defended.
