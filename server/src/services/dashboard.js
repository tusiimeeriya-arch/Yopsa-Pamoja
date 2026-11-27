import { q } from '../db.js';
import { policy } from '../config.js';
import { today } from '../util.js';
import { computeRanking } from './ranking.js';

function filters(input) {
  const f = { branchId: input.branchId || null, groupType: input.groupType || null, officerId: input.officerId || null, from: input.from || null, to: input.to || today() };
  return f;
}

const WHERE = `($1::uuid IS NULL OR g.branch_id=$1) AND ($2::text IS NULL OR g.type=$2) AND ($3::uuid IS NULL OR EXISTS (
  SELECT 1 FROM role_assignments ra WHERE ra.user_id=$3 AND ra.status='active' AND (ra.scope_type='Org' OR (ra.scope_type='Branch' AND ra.scope_id=g.branch_id::text) OR (ra.scope_type='Group' AND ra.scope_id=g.id::text))))`;

export async function dashboardData(caller, input = {}) {
  const f = filters(input);
  const p = [f.branchId, f.groupType, f.officerId];
  const groups = (await q(`SELECT g.* FROM groups g WHERE ${WHERE} ORDER BY g.name`, p)).rows;
  const groupIds = groups.map((g) => g.id);
  const ids = groupIds.length ? groupIds : ['00000000-0000-0000-0000-000000000000'];
  const statusBreakdown = {};
  for (const g of groups) statusBreakdown[g.status] = (statusBreakdown[g.status] || 0) + 1;
  const members = (await q(
    `SELECT COUNT(DISTINCT m.person_id)::int AS people, COUNT(m.id)::int AS memberships
     FROM memberships m JOIN groups g ON g.id=m.group_id WHERE m.status='active' AND g.id = ANY($1::uuid[])`, [ids],
  )).rows[0];
  const gender = (await q(
    `SELECT p.gender, COUNT(DISTINCT p.id)::int AS n FROM persons p JOIN memberships m ON m.person_id=p.id
     WHERE m.status='active' AND m.group_id = ANY($1::uuid[]) GROUP BY p.gender`, [ids],
  )).rows;
  const loans = (await q(
    `SELECT COUNT(*)::int AS count, COALESCE(SUM(d.amount),0) AS value FROM disbursements d
     JOIN loan_applications l ON l.id=d.loan_id WHERE l.group_id = ANY($1::uuid[])`, [ids],
  )).rows[0];
  const ledger = (await q(
    `SELECT COALESCE(SUM(principal),0) AS principal, COALESCE(SUM(interest_due),0) AS interest, COALESCE(SUM(fees_due),0) AS fees
     FROM mock_ledger_accounts WHERE group_id = ANY($1::uuid[])`, [ids],
  )).rows[0];
  const submitted = (await q(
    `SELECT COALESCE(SUM(total_amount),0) AS value, COUNT(*)::int AS count FROM group_submissions
     WHERE group_id = ANY($1::uuid[]) AND status IN ('submitted','difference','verified','partially_verified','posting_pending','posted')`, [ids],
  )).rows[0];
  const verified = (await q(
    `SELECT COALESCE(SUM(total_amount),0) AS value, COUNT(*)::int AS count FROM group_submissions
     WHERE group_id = ANY($1::uuid[]) AND status IN ('verified','partially_verified','posting_pending','posted')`, [ids],
  )).rows[0];
  const posted = (await q(
    `SELECT COALESCE(SUM(amount),0) AS value, COUNT(*)::int AS count FROM ledger_postings
     WHERE status='posted' AND submission_id IN (SELECT id FROM group_submissions WHERE group_id = ANY($1::uuid[]))`, [ids],
  )).rows[0];
  const receipts = (await q(
    `SELECT COALESCE(SUM(amount),0) AS value, COUNT(*)::int AS count FROM receipts WHERE group_id = ANY($1::uuid[])`, [ids],
  )).rows[0];
  const allocated = (await q(
    `SELECT COALESCE(SUM(a.amount),0) AS value FROM allocations a JOIN receipts r ON r.id=a.receipt_id WHERE r.group_id = ANY($1::uuid[])`, [ids],
  )).rows[0];
  const failures = (await q(
    `SELECT COUNT(*)::int AS n FROM finance_exceptions WHERE status='open' AND entity_id IN (SELECT id::text FROM group_submissions WHERE group_id = ANY($1::uuid[]))`, [ids],
  )).rows[0];
  const arrears = (await q(
    `SELECT COALESCE(SUM(s.amount),0) AS due, COALESCE(SUM(CASE WHEN s.due_date < current_date THEN s.amount ELSE 0 END),0) AS overdue
     FROM mock_ledger_accounts a
     CROSS JOIN LATERAL jsonb_to_recordset(a.schedule) AS s(due_date date, amount bigint, paid_date date)
     WHERE a.group_id = ANY($1::uuid[]) AND s.paid_date IS NULL`, [ids],
  )).rows[0];
  const ranking = await computeRanking({ cutoff: f.to, client: { query: q } });
  const top = ranking.top.filter((r) => groupIds.includes(r.groupId)).slice(0, 10);
  return {
    meta: { lastRefreshed: new Date().toISOString(), cutoff: f.to, reconciliationStatus: Number(failures.n) ? 'attention' : 'reconciled', filters: f },
    cards: {
      groups: { total: groups.length, statusBreakdown },
      members: { people: members.people, memberships: members.memberships, gender },
      loansAdvanced: { count: loans.count, value: Number(loans.value) },
      outstandingPrincipal: Number(ledger.principal),
      totalPayable: { principal: Number(ledger.principal), interest: Number(ledger.interest), fees: Number(ledger.fees) },
      arrears: { due: Number(arrears.due), overdue: Number(arrears.overdue), verifiedReceipts: Number(receipts.value), unallocated: Number(receipts.value) - Number(allocated.value), postingFailures: failures.n },
      flows: {
        submitted: { value: Number(submitted.value), count: submitted.count },
        verified: { value: Number(verified.value), count: verified.count },
        posted: { value: Number(posted.value), count: posted.count },
      },
      topTen: top,
    },
  };
}

export async function drilldown(caller, card, input = {}) {
  const f = filters(input);
  const p = [f.branchId, f.groupType, f.officerId];
  const groupIds = (await q(`SELECT g.id FROM groups g WHERE ${WHERE}`, p)).rows.map((r) => r.id);
  const ids = groupIds.length ? groupIds : ['00000000-0000-0000-0000-000000000000'];
  const base = `SELECT g.name AS group_name, g.type, b.name AS branch_name, g.status`;
  switch (card) {
    case 'groups':
      return (await q(`SELECT g.id, g.name, g.type, g.status, b.name AS branch, g.registration_evidence_status FROM groups g JOIN org_branches b ON b.id=g.branch_id WHERE g.id = ANY($1::uuid[]) ORDER BY g.name`, [ids])).rows;
    case 'members':
      return (await q(`SELECT DISTINCT p.id, p.full_name, p.gender, g.name AS group_name FROM persons p JOIN memberships m ON m.person_id=p.id JOIN groups g ON g.id=m.group_id WHERE m.status='active' AND g.id = ANY($1::uuid[]) ORDER BY p.full_name`, [ids])).rows;
    case 'loansAdvanced':
      return (await q(`SELECT d.id, d.amount, d.disbursed_on, l.id AS loan_id, g.name AS group_name FROM disbursements d JOIN loan_applications l ON l.id=d.loan_id JOIN groups g ON g.id=l.group_id WHERE l.group_id = ANY($1::uuid[]) ORDER BY d.disbursed_on DESC`, [ids])).rows;
    case 'flows':
      return (await q(`SELECT s.id, s.status, s.total_amount, s.submitted_at, g.name AS group_name FROM group_submissions s JOIN groups g ON g.id=s.group_id WHERE s.group_id = ANY($1::uuid[]) ORDER BY s.submitted_at DESC`, [ids])).rows;
    case 'arrears':
      return (await q(`SELECT g.name AS group_name, s.due_date, s.amount, s.paid_date FROM mock_ledger_accounts a CROSS JOIN LATERAL jsonb_to_recordset(a.schedule) AS s(due_date date, amount bigint, paid_date date) JOIN groups g ON g.id=a.group_id WHERE a.group_id = ANY($1::uuid[]) AND s.paid_date IS NULL ORDER BY s.due_date`, [ids])).rows;
    default:
      return [];
  }
}
