import express from 'express';
import { q } from '../db.js';
import { requireCaller, hasRole } from '../authz.js';
import { asyncHandler, today } from '../util.js';
import { memberPaymentView } from '../services/payments.js';
import { attendanceSummary } from '../services/attendance.js';

export const router = express.Router();

router.get('/person', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  const person = (await q('SELECT id, full_name, phone, gender, date_of_birth, consent_recorded, linked_user_id FROM persons WHERE linked_user_id=$1', [caller.user.id])).rows[0] || null;
  res.json({ person });
}));

router.get('/groups', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  const person = (await q('SELECT id FROM persons WHERE linked_user_id=$1', [caller.user.id])).rows[0];
  if (!person) return res.json({ groups: [] });
  const rows = (await q(
    `SELECT g.id, g.name, g.type, g.status, m.join_date, m.leave_date FROM memberships m JOIN groups g ON g.id=m.group_id
     WHERE m.person_id=$1 AND m.status='active' ORDER BY g.name`, [person.id],
  )).rows;
  res.json({ groups: rows });
}));

router.get('/payments', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  res.json({ payments: await memberPaymentView(caller) });
}));

router.get('/attendance', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  const person = (await q('SELECT id FROM persons WHERE linked_user_id=$1', [caller.user.id])).rows[0];
  if (!person) return res.json({ groups: [] });
  const groups = (await q("SELECT g.id, g.name FROM memberships m JOIN groups g ON g.id=m.group_id WHERE m.person_id=$1 AND m.status='active'", [person.id])).rows;
  const out = [];
  for (const g of groups) {
    const summary = await attendanceSummary(g.id);
    const mine = summary.people.find((p) => p.personId === person.id);
    out.push({ groupId: g.id, groupName: g.name, ...mine });
  }
  res.json({ groups: out });
}));

router.get('/attendance-records', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  const person = (await q('SELECT id FROM persons WHERE linked_user_id=$1', [caller.user.id])).rows[0];
  if (!person) return res.json({ records: [] });
  const rows = (await q(
    `SELECT a.id, a.status, a.checked_in_at, m.title AS meeting_title, m.scheduled_for, g.name AS group_name
     FROM attendance_records a JOIN meetings m ON m.id=a.meeting_id JOIN groups g ON g.id=m.group_id
     WHERE a.person_id=$1 ORDER BY m.scheduled_for DESC LIMIT 100`, [person.id],
  )).rows;
  res.json({ records: rows });
}));

router.get('/next-meeting', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  const person = (await q('SELECT id FROM persons WHERE linked_user_id=$1', [caller.user.id])).rows[0];
  if (!person) return res.json({ meeting: null });
  const row = (await q(
    `SELECT m.id, m.title, m.scheduled_for, m.status, g.name AS group_name FROM meetings m
     JOIN groups g ON g.id=m.group_id JOIN memberships mm ON mm.group_id=m.group_id
     WHERE mm.person_id=$1 AND mm.status='active' AND m.status IN ('scheduled','open') AND m.scheduled_for >= now()
     ORDER BY m.scheduled_for LIMIT 1`, [person.id],
  )).rows[0] || null;
  res.json({ meeting: row });
}));

router.get('/home', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  const person = (await q('SELECT id, full_name FROM persons WHERE linked_user_id=$1', [caller.user.id])).rows[0] || null;
  const roles = [...new Set(caller.assignments.map((a) => a.role))];
  const data = { person, roles, payments: [], attendance: [], nextMeeting: null, queues: {}, counts: {} };
  if (person) {
    data.payments = await memberPaymentView(caller);
    const groups = (await q("SELECT g.id, g.name FROM memberships m JOIN groups g ON g.id=m.group_id WHERE m.person_id=$1 AND m.status='active'", [person.id])).rows;
    for (const g of groups) {
      const s = await attendanceSummary(g.id);
      const mine = s.people.find((p) => p.personId === person.id);
      data.attendance.push({ groupId: g.id, groupName: g.name, ...mine });
    }
    data.nextMeeting = (await q(
      `SELECT m.id, m.title, m.scheduled_for, g.name AS group_name FROM meetings m JOIN groups g ON g.id=m.group_id
       JOIN memberships mm ON mm.group_id=m.group_id WHERE mm.person_id=$1 AND mm.status='active'
       AND m.status IN ('scheduled','open') AND m.scheduled_for >= now() ORDER BY m.scheduled_for LIMIT 1`, [person.id],
    )).rows[0] || null;
  }
  if (hasRole(caller, ['secretary', 'chairperson'])) {
    data.queues.myMeetings = (await q(
      `SELECT m.id, m.title, m.scheduled_for, m.status, g.name AS group_name FROM meetings m JOIN groups g ON g.id=m.group_id
       WHERE m.status IN ('scheduled','open') AND (
         EXISTS (SELECT 1 FROM role_assignments ra WHERE ra.user_id=$1 AND ra.status='active' AND ra.role IN ('secretary','chairperson') AND (ra.scope_type='Org' OR ra.scope_id=g.id::text OR ra.scope_id=g.branch_id::text))
         OR EXISTS (SELECT 1 FROM leaderships l JOIN persons p ON p.id=l.person_id WHERE l.group_id=g.id AND p.linked_user_id=$1 AND l.effective_from<=current_date AND (l.effective_to IS NULL OR l.effective_to>=current_date))
       ) ORDER BY m.scheduled_for LIMIT 10`, [caller.user.id],
    )).rows;
  }
  if (hasRole(caller, ['treasurer'])) {
    data.queues.draftEntries = (await q(
      `SELECT e.id, e.amount, e.category, e.collection_date, g.name AS group_name, p.full_name
       FROM payment_entries e JOIN groups g ON g.id=e.group_id JOIN persons p ON p.id=e.member_person_id
       WHERE e.status='draft' ORDER BY e.collection_date DESC LIMIT 20`,
    )).rows;
  }
  if (hasRole(caller, ['verifier'])) {
    data.queues.verification = (await q(
      `SELECT s.id, s.total_amount, s.status, s.submitted_at, g.name AS group_name FROM group_submissions s JOIN groups g ON g.id=s.group_id
       WHERE s.status IN ('submitted','difference','posting_pending') ORDER BY s.submitted_at LIMIT 20`,
    )).rows;
    data.queues.exceptions = (await q("SELECT * FROM finance_exceptions WHERE status='open' ORDER BY deadline LIMIT 20")).rows;
  }
  if (hasRole(caller, ['field_officer'])) {
    data.queues.groups = (await q("SELECT id, name, type, status FROM groups WHERE status IN ('Submitted','UnderReview','Returned') ORDER BY created_at LIMIT 20")).rows;
  }
  if (hasRole(caller, ['credit_approver'])) {
    data.queues.loans = (await q(
      `SELECT l.id, l.amount, l.status, g.name AS group_name FROM loan_applications l JOIN groups g ON g.id=l.group_id
       WHERE l.status IN ('OfficerAppraisal','OperationsReview','Approved') ORDER BY l.updated_at LIMIT 20`,
    )).rows;
  }
  data.counts.tasks = Number((await q("SELECT COUNT(*) AS n FROM tasks WHERE owner_user_id=$1 AND status='open'", [caller.user.id])).rows[0].n);
  data.counts.notifications = Number((await q('SELECT COUNT(*) AS n FROM notifications WHERE user_id=$1 AND read_at IS NULL', [caller.user.id])).rows[0].n);
  data.today = today();
  res.json(data);
}));

router.get('/notifications', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  const rows = (await q('SELECT * FROM notifications WHERE user_id=$1 ORDER BY created_at DESC LIMIT 50', [caller.user.id])).rows;
  res.json({ notifications: rows });
}));

router.post('/notifications/:id/read', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  await q('UPDATE notifications SET read_at=now() WHERE id=$1 AND user_id=$2', [req.params.id, caller.user.id]);
  res.json({ ok: true });
}));
