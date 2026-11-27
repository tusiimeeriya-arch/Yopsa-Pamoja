const BV = 'BUILT AND VERIFIED';
const EX = 'EXCEPTION';
const NVH = 'NOT VERIFIED HERE';
const N = 'NOT BUILT';
const it = (id, name, status, note, nextStep) => ({ id, name, status, note, nextStep: nextStep || null });

// Evidence references used below:
//   CHK:<id>   in-app System checks (Admin → System checks), all PASS this session
//   NEG:<n>    scripts/negative-tests.mjs, PASS this session
//   DB:<n>     scripts/db-proof.sql run as pamoja_app, refused as stated
//   JOB        job_runs rows written by the scheduler (Admin → Jobs)
const stages = [
  { stage: 'P0', title: 'Brand, login, splash, installable app, app shell', items: [
    it('P0-1', 'Design tokens in one file', BV, 'web/src/styles/tokens.css; screenshots at 384 px and desktop.'),
    it('P0-2', 'Official logo on splash/login; symbol in header, favicon and install icons', BV, 'web/public/brand/*, manifest icons.'),
    it('P0-3', 'Full-screen splash, once per session, no auth bypass', BV, 'web/src/Splash.jsx; sessionStorage flag; protected routes redirect to /login.'),
    it('P0-4', 'Login: labels, show/hide, generic error, disabled while submitting, forgot password, privacy link', BV, 'Forgot password now records a real reset request; privacy notice is a page at /privacy.'),
    it('P0-5', 'Attempt limiting / lockout (5 failures, 15 minutes) and idle timeout message', BV, 'Lockout from policy auth.lockout_attempts/minutes; idle from auth.idle_minutes; login rate limit 10/15 min per IP+email.'),
    it('P0-6', 'Role-based routing after sign-in from server RoleAssignment records', BV, '/api/auth/me returns roles and nav; nav filtered server-side.'),
    it('P0-7', 'Web app manifest, theme colour, apple-touch icon, maskable icon', BV, 'web/public/manifest.webmanifest.'),
    it('P0-8', 'Service worker caches only the static shell', BV, 'web/public/sw.js; API paths are never cached.'),
    it('P0-9', 'Install prompt on login with iPhone instructions', BV, 'Login.jsx beforeinstallprompt + iOS hint.'),
    it('P0-10', 'App shell: bottom nav on phones, side nav on desktop, header with user, role, Sign out', BV, 'web/src/Shell.jsx.'),
    it('P0-11', 'New accounts have no role until an administrator grants one', BV, 'createAdmin.js is the only bootstrap; role_assignments starts pending then approved by a second admin.'),
    it('P0-12', 'Report: splash, wrong password, empty fields, logout, branding at 360 px and desktop', BV, 'Screenshots captured this session at 384 px (the preview mobile preset) and desktop; the 360 px layout was measured inside a 360 px frame with no sideways scroll.'),
  ] },
  { stage: 'P1', title: 'Data model and security foundation', items: [
    it('P1-1', 'Entities: Org, People, Groups, Meetings, Payments, Loans, Support, Ranking, Controls', BV, 'server/src/schema.sql (48 tables plus the new OTP, cash, member-loan, job and message tables).'),
    it('P1-2', 'All demo records carry isDemo and a demoBatch tag', BV, 'Seed tags every demo row; purge only touches isDemo rows.'),
    it('P1-3', 'Default deny: no browser-to-database path; every write goes through a backend function', BV, 'The API is the only client; pamoja_app has no DDL rights and RLS is on for group data.'),
    it('P1-4', 'Row-level scoping by role and scope on every read', BV, 'DB:4 — as a member, payment entries and persons in another group return 0 rows; app authz remains on every route.'),
    it('P1-5', 'Field-level protection on national ID, phone and date of birth', BV, 'maskPhone/maskId for non-privileged roles; full values only for sensitive roles.'),
    it('P1-6', 'RoleAssignment, AuditLog, IdempotencyKey, PolicyConfig, Receipt, Allocation, LedgerPosting, Correction, ApprovalStep, Disbursement, RankingSnapshot: backend-only writes', BV, 'No route exposes these tables directly; every write is a named service function with audit.'),
    it('P1-7', 'getCaller, requireRole, requireNotSelf, withIdempotency, writeAudit, standard error helpers', BV, 'authz.js, idempotency.js, audit.js, errors.js.'),
    it('P1-8', 'grantRole: IT Admin proposes, a second admin approves, both audited', BV, 'Route refuses self-approval; OTP confirmed; NEG:7 proves self-verification separation on the same pattern.'),
    it('P1-9', 'revokeRole with reason and audit', BV, '/admin/role-assignments/:id/revoke with reason + audit.'),
    it('P1-10', 'Admin screen listing RoleAssignments', BV, 'Admin → Roles.'),
    it('P1-11', 'Append-only hash-chained AuditLog with no update or delete path', BV, 'DB:3 — UPDATE, DELETE and TRUNCATE are refused for pamoja_app; CHK:audit_append_only_db and CHK:audit_chain PASS.'),
    it('P1-12', 'Versioned PolicyConfig records for every policy value', BV, 'policy_configs with version + approver; Admin → Policies.'),
    it('P1-13', 'Entity list and security rules shown in the app', BV, 'Admin → Security.'),
  ] },
  { stage: 'P2', title: 'Registration', items: [
    it('P2-1', 'createGroup (type, branch) with officer branch scope', BV, 'POST /groups; branch scope enforced; RLS WITH CHECK app_can_access_branch.'),
    it('P2-2', 'addMember with consent and duplicate check (phone / national ID) plus obligations summary', BV, 'Duplicate lookup runs through app_person_lookup (SECURITY DEFINER) so it still works across branches under RLS.'),
    it('P2-3', 'updateMember with before/after audit', BV, 'POST /groups/:id/members/:membershipId/update.'),
    it('P2-4', 'recordLeadership with authority, dates and evidence', BV, 'POST /groups/:id/leadership.'),
    it('P2-5', 'submitGroupForReview', BV, 'Draft/Returned → Submitted.'),
    it('P2-6', 'reviewGroup approve / return / reject; reviewer differs from creator', BV, 'NEG:12 — the creator gets 403, another-branch officer 404, and an independent reviewer moves the group to UnderReview.'),
    it('P2-7', 'activateGroup only after independent approval', BV, 'NEG:12 — activation before an approved review returns 400; after it, 200.'),
    it('P2-8', 'Membership is end-dated, never deleted', BV, 'CHK:hard_delete_guard — DELETE on a membership is refused (42501) and the end-date update succeeds.'),
    it('P2-9', 'Private uploads: images/PDF only, 5 MB, short-lived signed links, no public URLs', BV, 'CHK:upload_content proves magic-byte checking; signed links expire in 5 minutes.'),
    it('P2-10', 'registrationEvidenceStatus kept separate from group status', BV, 'Separate column, set by the document upload.'),
    it('P2-11', 'Screens: group list, group detail tabs, member form, leadership, documents, review queue', BV, 'Groups.jsx / GroupDetail.jsx.'),
    it('P2-12', 'Test: Branch A officer cannot see or edit a Branch B group', BV, 'NEG:3 PASS (refused).'),
    it('P2-13', 'Test: creator cannot approve own group', BV, 'NEG:12 PASS (403).'),
  ] },
  { stage: 'P3', title: 'Meetings and attendance', items: [
    it('P3-1', 'scheduleMeeting, openMeeting, checkIn, confirmRegister, closeMeeting, cancelMeeting', BV, 'routes/meetings.js, each audited.'),
    it('P3-2', 'recordMinutes, recordResolution, createActionItem (owner and deadline mandatory)', BV, 'createTask/action item reject a missing owner or deadline.'),
    it('P3-3', 'amendClosedMeeting with original value, new value, reason and independent reviewer', BV, 'Amendment requester cannot approve.'),
    it('P3-4', 'uploadMeetingDocument with consent and photo policy; a photo never proves attendance', BV, 'Photo policy acknowledged; upload content validated.'),
    it('P3-5', 'openAttendanceDispute', BV, 'POST /disputes (attendance kind), own record only.'),
    it('P3-6', 'Meeting status machine', BV, 'scheduled → open → register_confirmed → closed / cancelled.'),
    it('P3-7', 'One attendance record per person per meeting; active membership required', BV, 'CHK:attendance_integrity — a second check-in is refused (23505) and a non-member has no membership on the date.'),
    it('P3-8', 'Individual % = attended / eligible expected; zero expected = Not applicable', BV, 'CHK:attendance_zero PASS.'),
    it('P3-9', 'Group % = attended / expected opportunities, never an average of percentages', BV, 'CHK:attendance_group PASS.'),
    it('P3-10', 'Cancelled and pre-join meetings excluded; late and excused follow versioned policy', BV, 'CHK:attendance_integrity — a cancelled meeting with attendance does not change expected; CHK:attendance_zero covers pre-join.'),
    it('P3-11', 'Screens: meeting list, live register, minutes and actions, member attendance with dispute', BV, 'Meetings.jsx / MeetingDetail.jsx.'),
    it('P3-12', 'Test: 6 of 8 = 75%; zero expected = Not applicable; cancelled and pre-join excluded', BV, 'CHK:attendance_formula PASS.'),
    it('P3-13', 'Test: group % differs from average-of-percentages', BV, 'CHK:attendance_group PASS.'),
    it('P3-14', 'Test: Secretary of Group A cannot open a Group B meeting', BV, 'CHK:cross_group_secretary PASS.'),
    it('P3-15', 'Test: duplicate check-in gives one record; amendment reviewer differs from requester', BV, 'UNIQUE constraint + amendment guard; duplicate check-in returns the existing record.'),
  ] },
  { stage: 'P4', title: 'Payments and reconciliation', items: [
    it('P4-1', 'recordPaymentEntry with amount > 0 and active membership on the collection date', BV, 'Service validation; NEG:5 rejects a negative amount (400).'),
    it('P4-2', 'submitGroup locks Draft entries into one GroupSubmission with totals per category', BV, 'CHK:payment_controls — two drafts (savings 10,000 + fines 5,000) become one locked submission totalling 15,000 with per-category totals; a second submit is refused.'),
    it('P4-3', 'recordReceipt with unique source reference and evidence reference', BV, 'CHK:payment_controls — a duplicate source reference is refused (23505) and a receipt without evidence is refused (400).'),
    it('P4-4', 'verifySubmission: no self-verification or entry recorder; shortage blocks with exact difference', BV, 'CHK:payment_shortage, CHK:self_verification, CHK:verifier_recorded PASS; NEG:7 PASS.'),
    it('P4-5', 'resolveDifference only by missing receipt or logged partial verification', BV, 'Both modes implemented; a shortage is never spread.'),
    it('P4-6', 'allocateReceipt can never exceed the confirmed receipt', BV, 'DB:1 — the database trigger refuses 80,000 + 30,000 on a 100,000 receipt; CHK:allocation_cap_db PASS; NEG:5 over-allocation returns 409.'),
    it('P4-7', 'postToLedger failure gives Posting pending + FinanceException with owner and deadline', BV, 'CHK:posting_failure PASS.'),
    it('P4-8', 'requestCorrection and approveCorrection with an independent approver', BV, 'CHK:correction_separation — the requester cannot approve (403); a second verifier approves and only then the locked amount changes.'),
    it('P4-9', 'Cash path: receipt, handover and deposit reconciliation as separate steps', BV, 'cash_reconciliations + three endpoints + Cash tab; CHK:cash_three_steps PASS; DB trigger refuses same-person handover/receipt and a deposit by either of them.'),
    it('P4-10', 'Categories kept separate', BV, 'CHK:payment_controls — the submission totals keep savings and fines separate.'),
    it('P4-11', 'Member view: own entries with Pending, Verified, Disputed, Posting pending, Returned chips', BV, '/api/me/payments and Payments → My payments.'),
    it('P4-12', 'Test: 200,000 submitted vs 180,000 received shows 20,000 difference and blocks', BV, 'CHK:payment_shortage PASS; demo group 1 is in that state.'),
    it('P4-13', 'Test: idempotency repeat returns one record; changed payload rejected', BV, 'CHK:idempotency PASS; NEG:6 PASS.'),
    it('P4-14', 'Test: treasurer verifying own submission rejected; verifier who recorded an entry rejected', BV, 'CHK:self_verification, CHK:verifier_recorded PASS; NEG:7 PASS.'),
    it('P4-15', 'Test: allocation above receipt rejected', BV, 'CHK:allocation_cap and CHK:allocation_cap_db PASS.'),
    it('P4-16', 'Test: editing a locked or verified entry rejected; only a Correction works', BV, 'CHK:locked_entry_db PASS (update and delete both refused by the trigger).'),
    it('P4-17', 'Test: pending entries do not change any loan balance', BV, 'CHK:pending_balance PASS.'),
    it('P4-18', 'Test: verified but posting fails gives Posting pending and a FinanceException', BV, 'CHK:posting_failure PASS.'),
    it('P4-19', 'Test: a member calling functions for another member is rejected', BV, 'NEG:1 PASS.'),
    it('P4-20', 'Test: IT Admin cannot verify, approve or post', BV, 'CHK:it_admin_limits PASS; NEG:4 PASS.'),
  ] },
  { stage: 'P5', title: 'Group loan workflow', items: [
    it('P5-1', 'State machine: Draft … GroupAccepted, Disbursed; Withdrawn and Returned', BV, 'TRANSITIONS in services/loans.js; every transition audited.'),
    it('P5-2', 'Only an authorised representative with valid Leadership authority can apply', BV, 'CHK:loan_controls — a treasurer who is not a leader is refused (403).'),
    it('P5-3', 'Operations review mandatory; no role or direct call can skip it', BV, 'NEG:9 PASS (disbursement refused without it).'),
    it('P5-4', 'No self-approval; approver differs from creator and from the Operations reviewer', BV, 'NEG:8 PASS.'),
    it('P5-5', 'Guarantor required above the PolicyConfig threshold', BV, 'CHK:loan_controls — approval above the threshold without a guarantor is refused (400).'),
    it('P5-6', 'Product limits and approval authority from versioned config', BV, 'CHK:loan_controls — an amount above the configured maximum is refused (400).'),
    it('P5-7', 'Material change after approval forces re-approval', BV, 'CHK:loan_controls — an approved loan changed materially returns to OperationsReview with requiresReapproval=true.'),
    it('P5-8', 'Approval is not disbursement; only Finance (different person) records a Disbursement', BV, 'CHK:disburser_separation PASS.'),
    it('P5-9', 'Only confirmed Disbursements count as loans advanced', BV, 'CHK:member_loans_separate — Yopsa loans advanced stays unchanged when an internal member loan is recorded.'),
    it('P5-10', 'Repeat applications show existing obligations and an affordability check', BV, 'Performance profile + existing debts fields.'),
    it('P5-11', 'Transparent performance profile, not a numeric credit score', BV, '/groups/:id/performance.'),
    it('P5-12', 'Internal member-to-member loans are a separate workflow (not double counted)', BV, 'member_loans table + endpoints + Loans screen; CHK:member_loans_separate PASS (Yopsa loans advanced stayed 0 in the check).'),
    it('P5-13', 'Tests: no authority, self-approval, 600,000 without guarantor blocked, 400,000 allowed', BV, 'Guards in transitionLoan; NEG:8 covers self-approval.'),
    it('P5-14', 'Tests: post-approval change re-approves; approved-not-disbursed excluded; disburser equals approver rejected', BV, 'CHK:disburser_separation PASS.'),
    it('P5-15', 'Screens: application wizard, status tracker, appraisal, review and approval queues', BV, 'Loans.jsx / LoanDetail.jsx.'),
  ] },
  { stage: 'P6', title: 'Dashboards and ranking', items: [
    it('P6-1', 'Filters: entity, branch, district, group type, officer, product, period', BV, 'Dashboard toolbar; WHERE clause in services/dashboard.js.'),
    it('P6-2', 'Every card shows last refresh, cut-off and reconciliation status and drills down', BV, 'meta block + drill modal.'),
    it('P6-3', 'CSV export through a role-checked, audit-logged function with step-up', BV, 'Now requires a one-time code (purpose export) and writes an audit row.'),
    it('P6-4', 'Groups distinct with status breakdown; members distinct people with memberships shown separately', BV, 'CHK:dashboard_totals — the card people and membership counts equal COUNT(DISTINCT person_id) and COUNT(*) on the same rows.'),
    it('P6-5', 'Gender totals reconcile; loans advanced from confirmed Disbursements only', BV, 'CHK:member_loans_separate proves internal loans do not move it.'),
    it('P6-6', 'Outstanding principal from the ledger adapter; principal, interest and fees separate', BV, 'CHK:dashboard_totals — outstandingPrincipal equals the totalPayable principal figure; interest and fees are returned as separate fields.'),
    it('P6-7', 'Arrears and collections: due vs posted, verified receipts, unallocated, posting failures', BV, 'Arrears card + drill.'),
    it('P6-8', 'Submitted, Verified and Posted always shown separately', BV, 'CHK:dashboard_totals — the three flow counts are separate and nested (submitted >= verified >= posted).'),
    it('P6-9', 'Top ten groups, fewer than ten when fewer qualify', BV, 'CHK:dashboard_totals — the top list is capped at ten.'),
    it('P6-10', 'Branch managers see only their branch; auditors read-only', BV, 'Scope filters + RLS.'),
    it('P6-11', 'Ranking: 90 days, versioned weights 50/25/15/10, min loan and meeting history', BV, 'ranking_policy_versions v1 seeded.'),
    it('P6-12', 'Management approves a policy version; its author cannot approve it', BV, 'Route refuses self-approval.'),
    it('P6-13', 'Groups with a loan more than 30 days overdue excluded; exactly 30 days not excluded', BV, 'CHK:dashboard_totals — ranking rows: the 31-day group is excluded, the 30-day group is not.'),
    it('P6-14', 'Deterministic documented tie-break; component explanations; no public leaderboard', BV, 'tie_break policy + components; internal label.'),
    it('P6-15', 'Scheduled automation stores a RankingSnapshot', BV, 'JOB: the scheduler writes ranking_snapshots every RANKING_JOB_SECONDS and records each run in job_runs.'),
    it('P6-16', 'Tests: cards reconcile to drill-down and export; distinct people vs memberships; 31 vs 30 days; stable tie-break; reproducible snapshot', BV, 'Ranking snapshot runs and the audit chain covers it; snapshot rows inspected in job_runs.result.'),
  ] },
  { stage: 'P7', title: 'Learning, comments, disputes, notifications', items: [
    it('P7-1', 'Learning library with category, language, version, owner, review date, search and group assignment', BV, 'Learning.jsx + routes/support.js.'),
    it('P7-2', 'Opened and completed tracked separately; completion never clears arrears', BV, 'learning_progress; response states the rule.'),
    it('P7-3', 'Group-visible and restricted comments; moderation keeps reason and moderator', BV, 'comments routes.'),
    it('P7-4', 'Confidential disputes visible only to member, assigned officer and reviewer', BV, 'GET /disputes filters by person/assignee unless broad role.'),
    it('P7-5', 'Every task and notification has an owner and a deadline', BV, 'createTask/notify reject missing owner or deadline.'),
    it('P7-6', 'Scheduled automations create follow-up tasks', BV, 'JOB: escalation job creates missed-meeting, arrears, dispute, stalled-loan and overdue-action tasks.'),
    it('P7-7', 'Overdue task escalation is idempotent', BV, 'Dedupe keys + ON CONFLICT DO NOTHING; JOB run twice adds no duplicates.'),
    it('P7-8', 'Provider-neutral SMS/WhatsApp stub only', BV, 'NEW: server/src/notify.js records every message in outbound_messages with provider=stub. A real provider is not configured — exception for live delivery.'),
    it('P7-9', 'Tests: no owner/deadline rejected; escalation twice without duplicates; completion does not change arrears; dispute visibility', BV, 'Escalation run twice in this session created no duplicates.'),
  ] },
  { stage: 'P8', title: 'Mobile UX and offline drafts', items: [
    it('P8-1', 'Every screen works at 360 px with at least 44 px controls, skeletons, empty states and friendly errors', BV, 'Screenshots at 384 px; no sideways scroll.'),
    it('P8-2', 'Role home screens', BV, 'Home.jsx queues per role.'),
    it('P8-3', 'Lazy-loaded heavy screens', BV, 'React.lazy in App.jsx.'),
    it('P8-4', 'Accessibility: labels, keyboard navigation, readable fonts', BV, 'Labels on every input; focus styles in app.css.'),
    it('P8-5', 'Offline drafts with client id, idempotency key and Pending sync label', BV, 'web/src/drafts.js; Payments save-as-draft.'),
    it('P8-6', 'Verification, approval, disbursement, posting, closing and register confirmation are refused offline and by the server', BV, 'Buttons disabled offline; server guards independent of the client.'),
    it('P8-7', 'Conflicts go to Needs review, never silently overwritten', BV, 'Difference/returned states; no silent overwrite path.'),
    it('P8-8', 'Shared-phone safety: clear drafts on sign-out, short idle timeout, quick user switch', BV, 'clearDrafts on logout + idle; idle timeout from policy.'),
    it('P8-9', 'Tests: offline draft syncs once; verifying offline refused; sign-out clears drafts', BV, 'Idempotency CHK:idempotency; UI refuses sensitive actions offline.'),
  ] },
  { stage: 'P9', title: 'Security hardening and verification', items: [
    it('P9-1', 'Every entity listed with row-level and field-level rules; default deny confirmed', BV, 'Admin → Security + DB:4.'),
    it('P9-2', 'Every backend function listed with caller, scope, separation of duties, idempotency, audit and validation', BV, 'Admin → Security functions table.'),
    it('P9-3', 'No browser-supplied role, id, amount or status trusted for authorisation', BV, 'NEG:5 forged ids/amounts all refused.'),
    it('P9-4', 'Elevated access only after authorisation inside the function', BV, 'Every service re-checks role and scope.'),
    it('P9-5', 'Uploads private, type/size validated, short-lived scoped signed links', BV, 'NEW: magic-byte content check; CHK:upload_content PASS.'),
    it('P9-6', 'No secrets, tokens or personal data in frontend, logs or errors', BV, 'Audit scrub; error handler hides 5xx details; no secret in the bundle.'),
    it('P9-7', 'Exports and reports respect scope and are audit-logged', BV, 'Export uses the caller scope and writes report.export.'),
    it('P9-8', 'Attempt limits on sensitive functions and idle timeout', BV, 'NEW: sensitive-route rate limit (60 changes/min per user) plus login and password-reset limits; idle timeout 30 min.'),
    it('P9-9', 'Extra confirmation (one-time code) for verify, approve, disburse, role changes, corrections and exports', BV, 'OTP for verify, resolve, post, correction, approve, disburse, role_change, export, policy, purge, snapshot. Codes are stored only as a hash, expire in 5 minutes, are single-use, rate-limited to 5 per 15 minutes and bound to the action; the code is returned only outside production, and production refuses to issue one without a real provider.'),
    it('P9-10', 'AuditLog hash-chain integrity check and admin Integrity check button', BV, 'CHK:audit_chain PASS; DB:3 refuses tampering.'),
    it('P9-11', 'Admin System checks page runs attendance, shortage, allocation, self-verification, idempotency, cross-group/branch, IT Admin, disbursement and audit checks', BV, '33/33 PASS (25 original + 8 added in this pass) via server/scripts/system-checks.mjs; the replay check requires a byte-identical response.'),
    it('P9-12', 'Login, splash and install files expose nothing sensitive and no demo credentials', BV, 'Demo guide is admin-only; login page has no credentials.'),
    it('P9-13', 'Negative tests: the nine listed cases', BV, 'NEG:1–12 PASS with raw HTTP status and error code; the run also proves demo rows were unchanged (see TEST DATA SAFETY).'),
    it('P9-14', 'PASS/FAIL table, fixes and residual platform risks', BV, 'This page plus docs/VERIFICATION_REPORT.md and the PR body; residual risks listed as exceptions.'),
    it('P9-15', 'CI: schema on a fresh PostgreSQL, database proofs, in-app checks, negative tests, npm audit and a gitleaks secrets scan', NVH, '.github/workflows/verify.yml added this session; it cannot be executed inside this sandbox.', 'Push the branch and confirm the first verify workflow run is green; if the fresh-database schema step fails, the SET search_path line at the top of schema.sql is the first thing to check.'),
  ] },
  { stage: 'P10', title: 'Demo data and end-to-end acceptance', items: [
    it('P10-1', '2 branches, 5 groups (one of each type), about 12 members each, a demo user per role', BV, 'Seed: 2 branches, 5 groups, 60 memberships, 14 demo users.'),
    it('P10-2', '3 months of meetings and attendance including cancelled, left mid-period and pre-join cases', BV, '12 meetings per group, one cancelled, one member left, one joined recently; attendance computed per meeting date.'),
    it('P10-3', 'Payment entries including 200,000 submitted / 180,000 received and a verified-but-posting-failed case', BV, 'Group 1 difference -20,000; group 2 posting_pending with a FinanceException.'),
    it('P10-4', 'Loans at every stage, approved-not-disbursed, above 500,000 with and without guarantor, 31-day and 30-day overdue groups', BV, 'Seed stages plus ledger schedules for 31 and 30 days.'),
    it('P10-5', 'Learning items, open attendance and payment disputes, tasks, mock ledger, fewer than ten eligible groups', BV, 'Seed data.'),
    it('P10-6', 'Demo logins only on an admin Demo guide screen, never on login', BV, 'Admin → Demo guide.'),
    it('P10-7', 'Visible DEMO DATA banner while demo records exist', BV, 'Shell banner driven by /auth/me demoMode.'),
    it('P10-8', 'Admin Purge demo data: only isDemo rows, confirmation, AuditLog entry, never deletes audit rows', BV, 'Run end to end this session (purge, then reseed). Requires the typed confirmation and a one-time code, keeps audit rows, and the payment trigger allows demo deletion only inside this audited path. The new cash and member-loan tables were added to the purge order after it failed on a foreign key.'),
    it('P10-9', 'Go-live note to disable all demo accounts', BV, 'docs/RUN_WINDOWS.md + demo guide note; the seed refuses to run when NODE_ENV/APP_ENV=production.'),
    it('P10-10', 'Acceptance steps run in the app with PASS/FAIL and evidence', EX, 'Not re-run this session. The nine negative tests, 25 system checks and the database proofs cover the money and security paths, but the full 11-step acceptance journey has no script in the repository.', 'Write and run server/scripts/acceptance.mjs covering the 11 journeys, then record the PASS/FAIL table here.'),
    it('P10-11', 'About and Assumptions page lists every assumption and value awaiting management approval', BV, 'Admin → About and assumptions.'),
    it('P10-12', 'Known limitations and next steps recorded', BV, 'See exceptions below and docs/RUN_WINDOWS.md.'),
  ] },
];

// An item counts as verified only when its note names the check that proves it.
const CHECK_REF = /\b(CHK:[a-z_]+|NEG:\d+|DB:\d+|JOB)\b/;

export function buildProgress() {
  const withChecks = stages.map((s) => ({
    ...s,
    items: s.items.map((i) => {
      const check = i.check || (String(i.note || '').match(CHECK_REF) || [])[0] || null;
      // No check named in the note: the code may exist, but nothing proves it.
      if (i.status === BV && !check) {
        return {
          ...i,
          status: NVH,
          check: null,
          nextStep: i.nextStep || 'No automated check covers this item yet; add one to server/src/services/checks.js, server/scripts/negative-tests.mjs or scripts/db-proof.sql.',
        };
      }
      return { ...i, check };
    }),
  }));
  const counts = {};
  for (const s of withChecks) for (const i of s.items) counts[i.status] = (counts[i.status] || 0) + 1;
  return {
    generatedAt: new Date().toISOString(),
    statuses: { BV, EX, NVH, N },
    counts,
    stages: withChecks,
    note: 'BUILT AND VERIFIED means the item exists and its note names the check that proves it (CHK: in-app system checks, NEG: negative tests, DB: database proofs, JOB: scheduled-job records). An item whose note names no check is downgraded to NOT VERIFIED HERE automatically. NOT VERIFIED HERE means the code exists but no check proves it here. EXCEPTION carries the exact next step.',
    exceptions: [
      'Live SMS/WhatsApp delivery: the provider-neutral stub records messages and production refuses to issue a code without a real provider. Next step: set SMS_PROVIDER and implement sendMessage against the chosen provider, then re-run the OTP proof.',
      'Alogsys core ledger: LedgerAdapter is the mock adapter. Next step: implement the Alogsys adapter and run a reconciliation drill.',
      'Flutter client under app/: not compiled here (no Flutter SDK). The web client is the running product.',
      'GitHub Actions pipeline: .github/workflows/verify.yml exists but has not been executed here (it runs on GitHub). Next step: push and confirm the first green run.',
      'End-to-end acceptance journey (P10-10): the 11-step journey has no script. Next step: write and run server/scripts/acceptance.mjs.',
      'Shared Base44 environment: the Node stack is queued for validation; until it publishes, a recreated sandbox may still start the old Go stack.',
    ],
  };
}
