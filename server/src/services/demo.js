import { q } from '../db.js';

export async function demoUsers() {
  const users = (await q(
    `SELECT u.id, u.email, u.full_name, u.is_demo FROM users u WHERE u.is_demo=true ORDER BY u.full_name`,
  )).rows;
  const roles = (await q("SELECT user_id, role, scope_type, scope_id FROM role_assignments WHERE status='active' AND is_demo=true")).rows;
  return users.map((u) => ({ ...u, roles: roles.filter((r) => r.user_id === u.id).map((r) => `${r.role} (${r.scope_type})`) }));
}

/** Deletes only records tagged isDemo (and demo users). AuditLog is never touched. */
export async function purgeDemoData(client) {
  const groups = (await client.query('SELECT id FROM groups WHERE is_demo=true')).rows.map((r) => r.id);
  const persons = (await client.query('SELECT id FROM persons WHERE is_demo=true')).rows.map((r) => r.id);
  const users = (await client.query('SELECT id FROM users WHERE is_demo=true')).rows.map((r) => r.id);
  const branches = (await client.query('SELECT id FROM org_branches WHERE is_demo=true')).rows.map((r) => r.id);
  const g = groups.length ? groups : ['00000000-0000-0000-0000-000000000000'];
  const p = persons.length ? persons : ['00000000-0000-0000-0000-000000000000'];
  const u = users.length ? users : ['00000000-0000-0000-0000-000000000000'];
  const counts = {};
  const del = async (name, sql, params) => { const r = await client.query(sql, params); counts[name] = r.rowCount; };
  await del('ledgerPostings', 'DELETE FROM ledger_postings WHERE submission_id IN (SELECT id FROM group_submissions WHERE group_id = ANY($1::uuid[])) OR created_by = ANY($2::uuid[])', [g, u]);
  await del('exceptions', 'DELETE FROM finance_exceptions WHERE entity_id IN (SELECT id::text FROM group_submissions WHERE group_id = ANY($1::uuid[])) OR owner_user_id = ANY($2::uuid[])', [g, u]);
  await del('allocations', 'DELETE FROM allocations WHERE receipt_id IN (SELECT id FROM receipts WHERE group_id = ANY($1::uuid[])) OR submission_id IN (SELECT id FROM group_submissions WHERE group_id = ANY($1::uuid[]))', [g]);
  await del('corrections', 'DELETE FROM corrections WHERE entity_id IN (SELECT id FROM payment_entries WHERE group_id = ANY($1::uuid[])) OR requested_by = ANY($2::uuid[])', [g, u]);
  // Cash reconciliations reference a receipt, so they must go before it.
  await del('cashReconciliations', 'DELETE FROM cash_reconciliations WHERE group_id = ANY($1::uuid[]) OR handover_by = ANY($2::uuid[]) OR received_by = ANY($2::uuid[]) OR deposited_by = ANY($3::uuid[])', [g, u, u]);
  await del('receipts', 'DELETE FROM receipts WHERE group_id = ANY($1::uuid[]) OR recorded_by = ANY($2::uuid[])', [g, u]);
  await del('paymentEntries', 'DELETE FROM payment_entries WHERE group_id = ANY($1::uuid[]) OR member_person_id = ANY($2::uuid[])', [g, p]);
  await del('submissions', 'DELETE FROM group_submissions WHERE group_id = ANY($1::uuid[])', [g]);
  await del('attendance', 'DELETE FROM attendance_records WHERE person_id = ANY($1::uuid[]) OR meeting_id IN (SELECT id FROM meetings WHERE group_id = ANY($2::uuid[]))', [p, g]);
  await del('minutes', 'DELETE FROM minutes WHERE meeting_id IN (SELECT id FROM meetings WHERE group_id = ANY($1::uuid[]))', [g]);
  await del('resolutions', 'DELETE FROM resolutions WHERE meeting_id IN (SELECT id FROM meetings WHERE group_id = ANY($1::uuid[]))', [g]);
  await del('actionItems', 'DELETE FROM action_items WHERE group_id = ANY($1::uuid[])', [g]);
  await del('meetingDocuments', 'DELETE FROM meeting_documents WHERE meeting_id IN (SELECT id FROM meetings WHERE group_id = ANY($1::uuid[]))', [g]);
  await del('meetingAmendments', 'DELETE FROM meeting_amendments WHERE meeting_id IN (SELECT id FROM meetings WHERE group_id = ANY($1::uuid[]))', [g]);
  await del('meetings', 'DELETE FROM meetings WHERE group_id = ANY($1::uuid[])', [g]);
  await del('approvalSteps', 'DELETE FROM approval_steps WHERE loan_id IN (SELECT id FROM loan_applications WHERE group_id = ANY($1::uuid[]))', [g]);
  await del('guarantors', 'DELETE FROM guarantors WHERE loan_id IN (SELECT id FROM loan_applications WHERE group_id = ANY($1::uuid[]))', [g]);
  await del('disbursements', 'DELETE FROM disbursements WHERE loan_id IN (SELECT id FROM loan_applications WHERE group_id = ANY($1::uuid[]))', [g]);
  await del('loans', 'DELETE FROM loan_applications WHERE group_id = ANY($1::uuid[])', [g]);
  // Internal member-to-member loans reference members, so they go before persons.
  await del('memberLoans', 'DELETE FROM member_loans WHERE group_id = ANY($1::uuid[]) OR lender_person_id = ANY($2::uuid[]) OR borrower_person_id = ANY($2::uuid[]) OR witness_person_id = ANY($2::uuid[]) OR recorded_by = ANY($3::uuid[])', [g, p, u]);
  await del('disputes', 'DELETE FROM disputes WHERE group_id = ANY($1::uuid[]) OR person_id = ANY($2::uuid[]) OR raised_by_user_id = ANY($3::uuid[])', [g, p, u]);
  await del('comments', 'DELETE FROM comments WHERE group_id = ANY($1::uuid[]) OR author_user_id = ANY($2::uuid[])', [g, u]);
  await del('learningProgress', 'DELETE FROM learning_progress WHERE person_id = ANY($1::uuid[])', [p]);
  await del('learningAssignments', 'DELETE FROM learning_assignments WHERE group_id = ANY($1::uuid[]) OR assigned_by = ANY($2::uuid[])', [g, u]);
  await del('learningItems', 'DELETE FROM learning_items WHERE is_demo=true', []);
  await del('tasks', 'DELETE FROM tasks WHERE is_demo=true OR owner_user_id = ANY($1::uuid[])', [u]);
  await del('notifications', 'DELETE FROM notifications WHERE user_id = ANY($1::uuid[])', [u]);
  await del('rankingSnapshots', 'DELETE FROM ranking_snapshots WHERE is_demo=true OR created_by = ANY($1::uuid[])', [u]);
  await del('ledgerAccounts', 'DELETE FROM mock_ledger_accounts WHERE group_id = ANY($1::uuid[])', [g]);
  await del('memberships', 'DELETE FROM memberships WHERE group_id = ANY($1::uuid[]) OR person_id = ANY($2::uuid[])', [g, p]);
  await del('leadership', 'DELETE FROM leaderships WHERE group_id = ANY($1::uuid[]) OR person_id = ANY($2::uuid[])', [g, p]);
  await del('files', 'DELETE FROM file_objects WHERE group_id = ANY($1::uuid[]) OR uploaded_by = ANY($2::uuid[])', [g, u]);
  await del('roleAssignments', 'DELETE FROM role_assignments WHERE user_id = ANY($1::uuid[]) OR is_demo=true', [u]);
  await del('sessions', 'DELETE FROM sessions WHERE user_id = ANY($1::uuid[])', [u]);
  await del('idempotency', 'DELETE FROM idempotency_keys WHERE user_id = ANY($1::uuid[])', [u]);
  await del('persons', 'DELETE FROM persons WHERE id = ANY($1::uuid[])', [p]);
  await del('groups', 'DELETE FROM groups WHERE id = ANY($1::uuid[])', [g]);
  await del('users', 'UPDATE users SET status=$1 WHERE id = ANY($2::uuid[])', ['disabled', u]);
  // A group created by a real user is not demo-tagged, so it can still point at a
  // demo branch. Deleting that branch would violate groups_branch_id_fkey and abort
  // the whole purge, so only remove demo branches nothing references any more.
  await del('branches', 'DELETE FROM org_branches b WHERE b.id = ANY($1::uuid[]) AND NOT EXISTS (SELECT 1 FROM groups gr WHERE gr.branch_id = b.id)', [branches.length ? branches : g]);
  return counts;
}
