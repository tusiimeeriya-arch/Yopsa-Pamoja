import { q } from '../db.js';
import { policy } from '../config.js';
import { badRequest } from '../errors.js';
import { writeAudit } from '../audit.js';
import { today } from '../util.js';
import { attendanceSummary } from './attendance.js';

export async function activePolicy(client = { query: q }) {
  const r = await client.query("SELECT * FROM ranking_policy_versions WHERE status='approved' ORDER BY version DESC LIMIT 1");
  if (!r.rowCount) throw badRequest('No approved ranking policy version exists.');
  return r.rows[0];
}

async function groupMetrics(group, from, to, client) {
  const att = await attendanceSummary(group.id, { from, to, client });
  const loans = (await client.query(
    `SELECT COUNT(*)::int AS count, COALESCE(SUM(d.amount),0) AS value FROM disbursements d
     JOIN loan_applications l ON l.id=d.loan_id WHERE l.group_id=$1`,
    [group.id],
  )).rows[0];
  const meetings = (await client.query(
    `SELECT COUNT(*)::int AS closed, COUNT(*) FILTER (WHERE EXISTS (SELECT 1 FROM minutes mi WHERE mi.meeting_id=m.id))::int AS with_minutes
     FROM meetings m WHERE m.group_id=$1 AND m.status='closed' AND m.scheduled_for::date BETWEEN $2 AND $3`,
    [group.id, from, to],
  )).rows[0];
  const ledger = (await client.query('SELECT * FROM mock_ledger_accounts WHERE group_id=$1', [group.id])).rows[0];
  const schedule = ledger?.schedule || [];
  let onTime = 0; let paid = 0; let maxOverdue = 0;
  for (const s of schedule) {
    if (s.paid_date) {
      paid += 1;
      if (new Date(s.paid_date) <= new Date(s.due_date)) onTime += 1;
    } else if (s.due_date && new Date(s.due_date) < new Date(to)) {
      const days = Math.floor((new Date(to) - new Date(s.due_date)) / 86400000);
      maxOverdue = Math.max(maxOverdue, days);
    }
  }
  const restructures = (await client.query("SELECT COUNT(*)::int AS n FROM approval_steps s JOIN loan_applications l ON l.id=s.loan_id WHERE l.group_id=$1 AND s.step='return'", [group.id])).rows[0].n;
  const disputes = (await client.query("SELECT COUNT(*)::int AS n FROM disputes WHERE group_id=$1 AND status='open'", [group.id])).rows[0].n;
  const records = meetings.closed ? meetings.with_minutes / meetings.closed : 0;
  const repaymentRatio = paid ? onTime / paid : 0;
  return {
    groupId: group.id, groupName: group.name, groupType: group.type,
    inputs: {
      disbursedLoans: loans.count, disbursedValue: Number(loans.value),
      attendancePercent: att.group.percent, attendanceDisplay: att.group.display,
      expectedMeetings: att.group.expected, attended: att.group.attended,
      closedMeetings: meetings.closed, meetingsWithMinutes: meetings.with_minutes,
      scheduledInstallments: schedule.length, paidInstallments: paid, onTimeInstallments: onTime,
      maxOverdueDays: maxOverdue, openDisputes: disputes, restructures,
    },
    metrics: { repaymentRatio, maxOverdue, records, attendancePercent: att.group.percent || 0 },
  };
}

export async function computeRanking({ cutoff = today(), persist = false, caller = null, client = { query: q } } = {}) {
  const pol = await activePolicy(client);
  const windowDays = Number(await policy('ranking.window_days'));
  const from = new Date(new Date(cutoff).getTime() - windowDays * 86400000).toISOString().slice(0, 10);
  const groups = (await client.query("SELECT * FROM groups WHERE status='Active' ORDER BY name")).rows;
  const weights = pol.weights;
  const rows = [];
  for (const g of groups) {
    const m = await groupMetrics(g, from, cutoff, client);
    const exclusionDays = Number(pol.overdue_exclusion_days);
    const excluded = m.metrics.maxOverdue > exclusionDays;
    const eligible = !excluded && m.inputs.disbursedLoans >= pol.min_loans && m.inputs.expectedMeetings >= pol.min_meetings;
    const repayment = m.metrics.repaymentRatio * Number(weights.repayment);
    const arrears = Math.max(0, Number(weights.arrears) * (1 - Math.min(m.metrics.maxOverdue, exclusionDays) / exclusionDays));
    const attendance = (m.metrics.attendancePercent / 100) * Number(weights.attendance);
    const records = m.metrics.records * Number(weights.records);
    const score = Math.round((repayment + arrears + attendance + records) * 10) / 10;
    rows.push({
      ...m, eligible, excluded,
      exclusionReason: excluded ? `A loan is ${m.metrics.maxOverdue} days overdue (limit ${exclusionDays}).` : null,
      score, components: { repayment: round(repayment), arrears: round(arrears), attendance: round(attendance), records: round(records) },
      flags: { restructured: m.inputs.restructures > 0, materialDisputes: m.inputs.openDisputes > 0 },
    });
  }
  rows.sort((a, b) => {
    if (a.eligible !== b.eligible) return a.eligible ? -1 : 1;
    if (b.score !== a.score) return b.score - a.score;
    if ((b.metrics.attendancePercent || 0) !== (a.metrics.attendancePercent || 0)) return (b.metrics.attendancePercent || 0) - (a.metrics.attendancePercent || 0);
    if (b.inputs.disbursedValue !== a.inputs.disbursedValue) return b.inputs.disbursedValue - a.inputs.disbursedValue;
    return a.groupId.localeCompare(b.groupId);
  });
  const eligibleRows = rows.filter((r) => r.eligible).slice(0, 10);
  const result = {
    policy: { id: pol.id, version: pol.version, weights: pol.weights, minLoans: pol.min_loans, minMeetings: pol.min_meetings, overdueExclusionDays: pol.overdue_exclusion_days, tieBreak: pol.tie_break },
    window: { from, to: cutoff, days: windowDays },
    label: 'Internal operational ranking — not a credit score, public endorsement or loan approval.',
    groups: rows, top: eligibleRows,
    eligibleCount: rows.filter((r) => r.eligible).length,
    generatedAt: new Date().toISOString(),
  };
  if (persist) {
    const snap = await client.query(
      'INSERT INTO ranking_snapshots (policy_version_id, cutoff, payload, created_by) VALUES ($1,$2,$3,$4) RETURNING id, created_at',
      [pol.id, cutoff, result, caller?.user?.id || null],
    );
    result.snapshot = snap.rows[0];
    if (caller) await writeAudit(client, caller, { action: 'ranking.snapshot', entity: 'RankingSnapshot', entityId: snap.rows[0].id, after: { cutoff, eligibleCount: result.eligibleCount }, scopeType: 'Org', scopeId: 'org' });
  }
  return result;
}

const round = (n) => Math.round(n * 10) / 10;
