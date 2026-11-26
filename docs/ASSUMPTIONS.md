# Assumptions and policy values

**Status:** P0 — every value below is **proposed**. Nothing here is a business
decision taken by the engineering team. Management approves each value before
go-live, and the approved value is stored in `config_value` with a version, an
`approved_by` and an `effective_from` date.

Rule: if a rule is not fixed by the concept document, it is a configuration
value listed here — never a constant buried in code.

| ID | Configuration key | Proposed default | What it controls | Approver | Status |
|---|---|---|---|---|---|
| A-001 | `org.structure` | One legal entity ("Yopsa Microfinance Uganda") with branches per district; groups belong to exactly one branch at a time, with history | Every scope filter and report | Management | Proposed |
| A-002 | `attendance.late_counts_as_present` | `true` — a late arrival counts as attended but is flagged | Individual and group attendance % | Management | Proposed |
| A-003 | `attendance.excused_counts_as_present` | `true` — an excused absence does not reduce the percentage | Attendance % | Management | Proposed |
| A-004 | `attendance.expected_rule` | Meetings while the membership is active; cancelled meetings excluded; meetings before `joined_on` or after `left_on` excluded | Denominator of every attendance figure | Management | Proposed |
| A-005 | `photos.enabled` | `false` on every platform until approved per platform | Whether meeting photos may be uploaded at all | Management + DPO | Proposed |
| A-006 | `retention.*` | Financial records 7 years; audit 7 years; disputes 3 years after resolution; learning progress 2 years; sessions 90 days; application logs 90 days | Retention, deletion and anonymisation schedules | Management + legal | Proposed — legal review required |
| A-007 | `backup.rpo_hours` / `backup.rto_hours` | 24 h / 8 h | Backup frequency and restore target | Management | Proposed |
| A-008 | `auth.access_ttl` / `auth.refresh_ttl` / `auth.idle_staff` / `auth.idle_shared_phone` / `auth.step_up_window` | 15 min / 14 days / 30 min / 5 min / 5 min | Session lifetime and step-up freshness | Management + IT | Proposed |
| A-009 | `auth.lockout` | 5 failures, exponential backoff from 1 min to 15 min, then 15 min lockout | Brute-force resistance | IT | Proposed |
| A-010 | `loans.guarantor_threshold_ugx` | 500,000 | When a guarantor check is mandatory | Management | Proposed (value named in the brief) |
| A-011 | `loans.product_limits` | Per type: VSLA 1,000,000–10,000,000; investment club 2,000,000–20,000,000; association 5,000,000–50,000,000; cooperative/SACCO 10,000,000–100,000,000; term ≤ 24 months | Product eligibility and amount limits | Management | Proposed — placeholder until product policy is signed |
| A-012 | `loans.approval_authority` | `credit_approver` up to 5,000,000; committee 5,000,001–20,000,000; two approvers above 20,000,000 | Who may approve what | Management | Proposed |
| A-013 | `ranking.window_days` / `min_loans` / `min_meetings` / `weights` / `overdue_exclusion_days` / `tie_break` | 90 days; ≥1 disbursed loan; ≥4 meetings; on-time repayment 50, arrears control 25, attendance 15, complete records 10; exclude any group with a loan >30 days overdue; tie-break by attendance %, then verified repayment value, then group id | Group ranking | Management | Proposed — **not activated** |
| A-014 | `money.currency` | UGX, minor unit 1, stored as `BIGINT` whole shillings | Every amount in the system | Finance | Fixed by design (ADR-0009) |
| A-015 | `timezone` | `Africa/Kampala`; business dates are local dates | Reporting periods, meeting dates, cut-offs | Management | Proposed |
| A-016 | `payments.partial_verification` | Allowed only for entries that are individually and fully evidenced; the decision and amount are logged; shortages are never spread | Resolving a declared/received difference | Finance | Fixed by the brief; switch recorded as config |
| A-017 | `payments.cash_path` | Receipt → handover → deposit reconciliation, performed by different people | Verification of cash payments | Finance | Proposed |
| A-018 | `meetings.quorum_evidence_required` | `true` for any borrowing resolution | Whether a group may bind itself to a loan | Management | Proposed |
| A-019 | `auth.password_policy` | Staff: minimum 12 characters, breach-list checked offline, no forced composition; members: 6-digit PIN with lockout, or a password | Credential strength | IT | Proposed |
| A-020 | `auth.member_otp` | Optional SMS OTP; provider stub in development | Member login hardening | IT | Proposed |
| A-021 | `notifications.sms_provider` | Stub adapter; real provider and sender ID to be contracted | SMS/WhatsApp delivery | IT + Management | Proposed |
| A-022 | `tasks.default_due_days` / `tasks.escalate_after_days` | 7 days / on the due date | Follow-up discipline | Management | Proposed |
| A-023 | `ledger.adapter` | `sandbox` in development and tests; Alogsys connector skeleton only | Posting and balance source | Finance + IT | Proposed — vendor endpoints not yet obtained |
| A-024 | `dashboard.snapshot_interval` / `dashboard.cut_off_rule` | Hourly snapshots; financial cut-off = last confirmed posting | Freshness labels on every card | Management | Proposed |
| A-025 | `exports.max_rows` / `exports.watermark` | 50,000 rows; exports watermarked with exporter, scope and timestamp | Abuse of exports | Management | Proposed |
| A-026 | `seed.guard` | Refuse to run unless `SEED_ALLOWED=1` **and** `APP_ENV != production` | Demo data safety | IT | Fixed by design |
| A-027 | `comments.default_visibility` | `group` (members of the group); `restricted` available to officers and reviewers | Comment audience | Management | Proposed |
| A-028 | `disputes.visibility` | Member, assigned reviewer and auditor only | Confidentiality of disputes | Management + DPO | Proposed |
| A-029 | `idempotency.ttl` | 24 hours | Replay window for retries | IT | Proposed |
| A-030 | `sessions.max_devices` | 5 active devices per user | Session sprawl | IT | Proposed |
| A-031 | `uploads.max_bytes` / `uploads.allowed_types` | 10 MiB; PDF, JPEG, PNG, HEIC | Evidence uploads | IT | Proposed |
| A-032 | `ranking.min_eligible_groups` | Rank whatever qualifies; fewer than ten is expected and shown as such | Top-ten behaviour | Management | Fixed by the brief |
| A-033 | `meetings.register_grace` | Register may be confirmed on the meeting date only | Register integrity | Management | Proposed |
| A-034 | `loans.material_change_fields` | Amount, term, security, guarantors, purpose | When re-approval is forced | Management | Proposed |
| A-035 | `audit.anchor_interval` | Weekly external anchoring of the latest hash (optional, documented procedure) | Tamper evidence beyond the database | IT | Proposed |
| A-036 | `api.rate_limits` | Login 5/min per IP and 10/hour per account; OTP 3 per 5 min; disputes 5/hour; exports 10/hour | Abuse resistance | IT | Proposed |
| A-037 | `auth.recovery_codes` | 10 single-use codes, hashed, regenerated on use of the last one | Staff MFA recovery | IT | Proposed |
| A-038 | `org.branch_assignment` | A group may move branch; the move is dated and audited, never rewritten | Report reproducibility | Management | Proposed |
| A-039 | `learning.completion` | Completion never clears arrears and never changes lending eligibility | Learning integrity | Management | Fixed by the brief |
| A-040 | `support.assisted_actions` | Assisted actions record the acting staff member and the member's confirmation method (verbal, written, OTP) | Assisted service evidence | Management | Proposed |

## How these are applied

1. Approved values are written to `config_value` (`key`, `version`, `value`,
   `approved_by`, `effective_from`) and read through a typed accessor.
2. A change creates a **new version**; history is never overwritten, so a report
   can state which policy produced it.
3. Ranking and dashboard snapshots store the policy version and inputs they used.
4. A policy change is audited and requires the approver named above.

## Decisions that block later phases

| Decision | Blocks |
|---|---|
| A-001 org structure | P2 registration, every scope filter and seed |
| A-005 photo policy | P3 meeting photos |
| A-011 / A-012 product limits and authorities | P5 loan approval |
| A-013 ranking activation | P6 ranking |
| A-006 retention | P2 data protection documentation and P9 hardening |
| A-007 RPO/RTO | P10 backup design and restore drill |
| A-023 Alogsys endpoints | P4 ledger adapter beyond sandbox |
