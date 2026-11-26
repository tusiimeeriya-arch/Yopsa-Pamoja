# State machines

**Status:** P0 — delivered for approval. Each transition below is logged with
actor, role, scope, reason and correlation id, and is enforced by a PostgreSQL
transition trigger as well as by the service layer.

Reading the diagrams: `[*]` is the initial state; labels name the actor and the
guard that must hold. An action not drawn is not permitted.

## 1. Group lifecycle

```mermaid
stateDiagram-v2
    [*] --> Draft: Field officer creates (assisted allowed)
    Draft --> Submitted: Field officer submits (complete roster, rules, evidence)
    Submitted --> UnderReview: Reviewer picks it up
    UnderReview --> Active: review_activate by someone other than the creator
    UnderReview --> Returned: reasons recorded
    UnderReview --> Rejected: reasons recorded, terminal for this version
    Returned --> Submitted: creator corrects and resubmits
    Active --> Suspended: management, reason recorded
    Suspended --> Active: management lifts suspension
    Active --> Closed: management, after all obligations settled
    Suspended --> Closed: management, after all obligations settled
    Closed --> [*]
```

| Transition | Actor | Guard |
|---|---|---|
| Draft → Submitted | Field officer (creator) | profile complete, ≥1 member, rules document attached, consent recorded |
| UnderReview → Active | `credit_approver` | SD-06: reviewer ≠ creator; duplicate and obligation checks recorded |
| UnderReview → Returned | `credit_approver` | reason required |
| UnderReview → Rejected | `credit_approver` | reason required |
| Active → Suspended | `management` | reason required |
| Active/Suspended → Closed | `management` | no open loans, no unallocated receipts, final report stored |

## 2. Meeting and register

```mermaid
stateDiagram-v2
    [*] --> Scheduled: Secretary schedules (date, location)
    Scheduled --> Open: Secretary opens
    Open --> Open: check-in (self or assisted)
    Open --> RegisterConfirmed: Secretary confirms register
    RegisterConfirmed --> Closed: Secretary closes (minutes, resolutions, action items)
    Closed --> Amended: controlled amendment (original + new + reason + reviewer)
    Amended --> Closed
    Scheduled --> Cancelled: Secretary, reason recorded
    Cancelled --> [*]
    Closed --> [*]
```

| Transition | Actor | Guard |
|---|---|---|
| Scheduled → Open | `secretary` | date reached (configurable grace), not cancelled |
| check-in | member (self) or `secretary`/`field_officer` (assisted) | member active on the meeting date; assisted actions record the acting person and the member's confirmation method |
| Open → RegisterConfirmed | `secretary` | at least one attendance row; no unconfirmed amendment |
| RegisterConfirmed → Closed | `secretary` | minutes saved; every action item has an owner and a deadline |
| Closed → Amended | `secretary` | SD-09: a different reviewer approves; original, new value, reason and timestamp retained |

Photos never prove attendance and are only accepted when the group's configured
photo policy permits them.

## 3. Attendance record

```mermaid
stateDiagram-v2
    [*] --> Recorded: self check-in or assisted check-in
    Recorded --> Confirmed: register confirmed
    Confirmed --> Disputed: member raises a dispute
    Disputed --> Confirmed: reviewer resolves in favour of the original
    Disputed --> Amended: reviewer resolves in favour of the member
    Confirmed --> Amended: controlled amendment
    Amended --> Confirmed
    Confirmed --> [*]
```

An amendment is a **new row** linked by `amendment_of_id`; the original is never
edited.

## 4. Payment entry

```mermaid
stateDiagram-v2
    [*] --> Draft: Treasurer records (self or assisted)
    Draft --> PendingSync: recorded offline
    PendingSync --> Submitted: outbox sync accepted
    Draft --> Submitted: online, included in a submission
    Submitted --> Verified: entry fully evidenced and matched to a receipt
    Submitted --> Returned: reviewer returns with a reason
    Submitted --> Disputed: member disputes the record
    Verified --> PostingPending: verification succeeded, posting not yet confirmed
    PostingPending --> Posted: ledger confirms posting
    PostingPending --> FinanceException: posting failed
    FinanceException --> PostingPending: retried successfully
    Returned --> Draft: treasurer corrects (new version)
    Disputed --> Submitted: dispute resolved in favour of the record
    Disputed --> Returned: dispute resolved in favour of the member
    Posted --> [*]
```

Pending, returned and disputed entries never change an authoritative balance.

## 5. Group submission, receipt, allocation, posting

```mermaid
stateDiagram-v2
    [*] --> Draft: entries recorded for the period
    Draft --> Locked: Treasurer locks (declared total + entry hash pinned)
    Locked --> UnderReview: Treasurer submits
    UnderReview --> Verified: Verifier confirms full amount received
    UnderReview --> PartiallyVerified: only fully evidenced entries verified (logged)
    UnderReview --> Returned: difference or missing evidence, reasons recorded
    Returned --> Draft: reopened by controlled correction
    Verified --> Posted: every allocation posted and confirmed
    PartiallyVerified --> Posted: every allocation posted and confirmed
    Verified --> PostingPending: posting failed
    PartiallyVerified --> PostingPending: posting failed
    PostingPending --> Posted: retry succeeded
    PostingPending --> FinanceException: retry failed, owner + deadline assigned
    FinanceException --> PostingPending: retried
    Posted --> [*]
```

| Transition | Actor | Guard |
|---|---|---|
| Draft → Locked | `treasurer` | declared total = Σ entries; entry set hashed into `locked_hash` |
| Locked → UnderReview | `treasurer` | at least one entry; period not already submitted |
| UnderReview → Verified | `verifier` | SD-01/SD-02: verifier is not the treasurer or any recorder; received amount = declared total; receipt evidence present |
| UnderReview → PartiallyVerified | `verifier` | difference exists; every verified entry individually evidenced; decision and amount logged; **no automatic spreading** |
| UnderReview → Returned | `verifier` | reason required; difference shown exactly |
| → Posted | system, confirmed by `verifier` | every allocation has a confirmed ledger posting |
| → FinanceException | system | posting failed after verification; exception carries owner and deadline |

**Receipt** states: `Recorded → Confirmed → Voided (by linked correction)`.
A receipt is never edited. Its `UNIQUE (source, provider_ref)` makes a duplicate
provider callback a no-op.

**Allocation** states: `Created → Posted | Failed`, with a trigger enforcing
`Σ allocations ≤ receipt.amount`.

## 6. Finance exception

```mermaid
stateDiagram-v2
    [*] --> Open: created with owner and deadline
    Open --> InProgress: owner picks it up
    InProgress --> Resolved: resolution note + resolver recorded
    Open --> Escalated: deadline passed (scheduled job)
    Escalated --> InProgress: owner resumes
    Resolved --> [*]
```

## 7. Group borrowing

```mermaid
stateDiagram-v2
    [*] --> Draft: authorised representative starts
    Draft --> GroupAuthorised: borrowing resolution + quorum + signatories attached
    GroupAuthorised --> OfficerAppraised: field officer appraisal
    OfficerAppraised --> OperationsReview: mandatory operations review
    OperationsReview --> Approved: approver within authority (SD-03)
    OperationsReview --> Declined: reason recorded
    Approved --> OfferIssued: terms from the approved product only
    OfferIssued --> OfferAccepted: group accepts
    OfferIssued --> Withdrawn: group withdraws or offer expires
    OfferAccepted --> Disbursed: Finance confirms disbursement (SD-04)
    Draft --> Withdrawn: applicant withdraws
    OperationsReview --> Returned: information required
    Returned --> GroupAuthorised: corrected and resubmitted
    Disbursed --> [*]
    Declined --> [*]
    Withdrawn --> [*]
```

| Transition | Actor | Guard |
|---|---|---|
| Draft → GroupAuthorised | `chairperson` (or another authorised representative) | authority record valid on the application date; resolution, quorum evidence and signatories attached |
| GroupAuthorised → OfficerAppraised | `field_officer` | borrower check recorded; existing obligations and affordability recorded |
| OfficerAppraised → OperationsReview | `credit_approver` | appraisal complete; SD-05 applies at review |
| OperationsReview → Approved | `credit_approver` | amount within product limit and approver's authority; guarantor check when above the threshold (A-003); SD-03 |
| Approved → OfferIssued | `credit_approver` | terms come from the approved product version, never inferred |
| OfferIssued → OfferAccepted | `chairperson` | offer not expired |
| OfferAccepted → Disbursed | `verifier` (Finance) | SD-04: disburser ≠ approver; external reference recorded |
| any → Re-approval | system | material change (amount, term, security, guarantors) after approval resets to `OperationsReview` |

`Approved` but not `Disbursed` is **not** counted as a loan advanced anywhere.

## 8. Dispute

```mermaid
stateDiagram-v2
    [*] --> Open: member raises (confidential)
    Open --> UnderReview: reviewer assigned
    UnderReview --> Resolved: outcome + reviewer + time recorded
    UnderReview --> Rejected: reason recorded
    Open --> Withdrawn: member withdraws
    Resolved --> [*]
    Rejected --> [*]
```

The disputed record keeps its original value throughout; the resolution is a
linked record.

## 9. Correction

```mermaid
stateDiagram-v2
    [*] --> Requested: requester states reason and links the original
    Requested --> Approved: independent approver (SD-07)
    Requested --> Rejected: reason recorded
    Approved --> Applied: reversal, adjustment or new version written
    Applied --> [*]
```

## 10. Task and notification

```mermaid
stateDiagram-v2
    [*] --> Open: created with owner and deadline (both mandatory)
    Open --> Done: owner completes
    Open --> Escalated: deadline passed (scheduled job)
    Escalated --> Done: owner completes
    Done --> [*]
```

A task row cannot be inserted without `owner_user_id` and `due_on`; a trigger
rejects it. The same applies to every notification's originating task.

## 11. Role grant

```mermaid
stateDiagram-v2
    [*] --> PendingApproval: IT administrator proposes (scope + dates)
    PendingApproval --> Active: second administrator approves (SD-10)
    PendingApproval --> Rejected: second administrator rejects
    Active --> Revoked: IT administrator revokes (SD-11, second approval)
    Active --> Expired: effective_to reached (scheduled job)
    Revoked --> [*]
    Expired --> [*]
    Rejected --> [*]
```

An IT administrator cannot grant themselves anything: SD-10/SD-11 require a
different administrator, and no role that can handle finance is grantable
without that second approval.

## 12. Refresh token family

```mermaid
stateDiagram-v2
    [*] --> Active: login issues access + refresh token
    Active --> Rotated: refresh used once, new token issued
    Rotated --> Active: new token in use
    Active --> Revoked: logout, device revocation or idle timeout
    Rotated --> Revoked: reuse of a rotated token detected — whole family revoked
    Revoked --> [*]
```

Reuse detection revokes the **entire family**, forcing re-authentication on every
device in that family.

## 13. Comment moderation

```mermaid
stateDiagram-v2
    [*] --> Visible: author posts
    Visible --> PendingReview: reported or flagged
    PendingReview --> Visible: moderator restores
    PendingReview --> Removed: moderator removes, reason + moderator retained
    Visible --> Removed: moderator removes, reason + moderator retained
    Removed --> [*]
```

A removed comment is never deleted; its body, reason, moderator and timestamp
are retained for audit.
