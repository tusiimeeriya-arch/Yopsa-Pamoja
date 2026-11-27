import { q } from '../db.js';
import { addDays, today } from '../util.js';
import { createTask, notify } from './common.js';

async function ownerForGroup(groupId, role) {
  const r = await q(
    `SELECT COALESCE(
       (SELECT p.linked_user_id FROM leaderships l JOIN persons p ON p.id=l.person_id
        WHERE l.group_id=$1 AND l.position=$2 AND l.effective_from<=current_date AND (l.effective_to IS NULL OR l.effective_to>=current_date) AND p.linked_user_id IS NOT NULL LIMIT 1),
       (SELECT ra.user_id FROM role_assignments ra JOIN groups g ON g.id=$1
        WHERE ra.role=$2 AND ra.status='active' AND (ra.scope_type='Org' OR (ra.scope_type='Branch' AND ra.scope_id=g.branch_id::text) OR (ra.scope_type='Group' AND ra.scope_id=g.id::text)) LIMIT 1),
       (SELECT id FROM users WHERE is_demo=false ORDER BY created_at LIMIT 1)
     ) AS user_id`,
    [groupId, role],
  );
  return r.rows[0]?.user_id;
}

/** Scheduled follow-ups. Every task/notification has an owner and a deadline and a dedupe key, so running twice creates no duplicates. */
export async function runEscalations(client = { query: q }) {
  const created = { missedMeetings: 0, arrears: 0, disputes: 0, stalledLoans: 0, actionItems: 0, escalated: 0 };
  const missed = (await client.query(
    `SELECT m.id, m.group_id, m.title, g.name AS group_name FROM meetings m JOIN groups g ON g.id=m.group_id
     WHERE m.status='scheduled' AND m.scheduled_for < now() - interval '1 day' LIMIT 100`,
  )).rows;
  for (const m of missed) {
    const owner = await ownerForGroup(m.group_id, 'secretary');
    if (!owner) continue;
    const t = await createTask(client, { title: `Record or close missed meeting: ${m.title} (${m.group_name})`, ownerUserId: owner, deadline: addDays(today(), 1), linkedEntity: 'Meeting', linkedId: m.id, dedupeKey: `missed-meeting:${m.id}` });
    if (t) created.missedMeetings += 1;
  }
  const arrears = (await client.query(
    `SELECT a.group_id, g.name AS group_name, MIN(s.due_date) AS oldest FROM mock_ledger_accounts a
     JOIN groups g ON g.id=a.group_id
     CROSS JOIN LATERAL jsonb_to_recordset(a.schedule) AS s(due_date date, paid_date date)
     WHERE s.paid_date IS NULL AND s.due_date < current_date GROUP BY a.group_id, g.name LIMIT 100`,
  )).rows;
  for (const a of arrears) {
    const owner = await ownerForGroup(a.group_id, 'verifier');
    if (!owner) continue;
    const t = await createTask(client, { title: `Follow up arrears: ${a.group_name} (oldest due ${a.oldest})`, ownerUserId: owner, deadline: addDays(today(), 3), linkedEntity: 'Group', linkedId: a.group_id, dedupeKey: `arrears:${a.group_id}:${a.oldest}` });
    if (t) created.arrears += 1;
  }
  const disputes = (await client.query("SELECT * FROM disputes WHERE status='open' AND created_at < now() - interval '7 days' LIMIT 100")).rows;
  for (const d of disputes) {
    const owner = d.assigned_to_user_id || (await ownerForGroup(d.group_id, 'field_officer'));
    if (!owner) continue;
    const t = await createTask(client, { title: `Resolve open dispute from ${new Date(d.created_at).toISOString().slice(0, 10)}`, ownerUserId: owner, deadline: addDays(today(), 2), linkedEntity: 'Dispute', linkedId: d.id, dedupeKey: `dispute-stale:${d.id}` });
    if (t) created.disputes += 1;
  }
  const loans = (await client.query(
    `SELECT l.*, g.name AS group_name FROM loan_applications l JOIN groups g ON g.id=l.group_id
     WHERE l.status NOT IN ('Disbursed','Declined','Withdrawn') AND l.updated_at < now() - interval '7 days' LIMIT 100`,
  )).rows;
  for (const l of loans) {
    const owner = await ownerForGroup(l.group_id, 'credit_approver');
    if (!owner) continue;
    const t = await createTask(client, { title: `Stalled loan application: ${l.group_name} (UGX ${Number(l.amount).toLocaleString()})`, ownerUserId: owner, deadline: addDays(today(), 3), linkedEntity: 'LoanApplication', linkedId: l.id, dedupeKey: `loan-stalled:${l.id}:${l.status}` });
    if (t) created.stalledLoans += 1;
  }
  const actions = (await client.query(
    `SELECT a.*, p.linked_user_id FROM action_items a LEFT JOIN persons p ON p.id=a.owner_person_id
     WHERE a.status='open' AND a.deadline < current_date LIMIT 100`,
  )).rows;
  for (const a of actions) {
    const owner = a.linked_user_id || (await ownerForGroup(a.group_id, 'secretary'));
    if (!owner) continue;
    const t = await createTask(client, { title: `Overdue action item: ${a.title}`, ownerUserId: owner, deadline: addDays(today(), 1), linkedEntity: 'ActionItem', linkedId: a.id, dedupeKey: `action-overdue:${a.id}` });
    if (t) created.actionItems += 1;
  }
  const overdue = (await client.query("SELECT * FROM tasks WHERE status='open' AND deadline < current_date LIMIT 200")).rows;
  const managers = (await client.query("SELECT user_id FROM role_assignments WHERE role='management' AND status='active' AND scope_type='Org' LIMIT 5")).rows;
  for (const t of overdue) {
    for (const m of managers) {
      const n = await notify(client, { userId: m.user_id, title: 'Overdue task needs attention', body: t.title, deadline: addDays(today(), 1), dedupeKey: `escalate:${t.id}:${m.user_id}` });
      if (n) created.escalated += 1;
    }
  }
  return created;
}
