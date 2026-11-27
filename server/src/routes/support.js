import express from 'express';
import { q, tx } from '../db.js';
import { requireCaller, hasRole, isAdmin, requireRole } from '../authz.js';
import { asyncHandler, str, isoDate, oneOf, today } from '../util.js';
import { badRequest, forbidden, notFound } from '../errors.js';
import { writeAudit } from '../audit.js';
import { groupOr404, createTask, notify } from '../services/common.js';
import { runEscalations } from '../services/escalation.js';
import { canAccessGroup } from './groups.js';

export const router = express.Router();

// ---------- Learning ----------
router.get('/learning', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  const person = (await q('SELECT id FROM persons WHERE linked_user_id=$1', [caller.user.id])).rows[0];
  const items = (await q('SELECT * FROM learning_items ORDER BY category, title')).rows;
  const progress = person ? (await q('SELECT * FROM learning_progress WHERE person_id=$1', [person.id])).rows : [];
  const assignments = (await q('SELECT la.item_id, la.group_id, g.name AS group_name FROM learning_assignments la JOIN groups g ON g.id=la.group_id')).rows;
  res.json({
    items: items.map((i) => ({
      ...i,
      progress: progress.find((p) => p.item_id === i.id) || null,
      assignedGroups: assignments.filter((a) => a.item_id === i.id).map((a) => a.group_name),
    })),
    canManage: hasRole(caller, ['management']),
  });
}));

router.post('/learning', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  requireRole(caller, ['management']);
  const item = (await q(
    `INSERT INTO learning_items (title, category, language, version, content_owner, review_date, kind, url, body)
     VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING *`,
    [str(req.body.title, 'Title', { min: 3, max: 200 }), oneOf(req.body.category, ['financial_literacy', 'business_cash_flow', 'borrowing_responsibilities', 'group_governance', 'recordkeeping', 'fraud_prevention'], 'Category'), str(req.body.language || 'English', 'Language', { max: 40 }), Number(req.body.version || 1), str(req.body.contentOwner || 'Yopsa', 'Content owner', { max: 100 }), isoDate(req.body.reviewDate, 'Review date', { optional: true }), oneOf(req.body.kind || 'text', ['video', 'pdf', 'text'], 'Kind'), str(req.body.url, 'Link', { optional: true, max: 500 }), str(req.body.body, 'Content', { optional: true, max: 20000 })],
  )).rows[0];
  res.status(201).json({ item });
}));

router.post('/learning/:id/assign', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  requireRole(caller, ['management']);
  const group = await groupOr404(req.body.groupId);
  const item = (await q('SELECT * FROM learning_items WHERE id=$1', [req.params.id])).rows[0];
  if (!item) throw notFound('Learning item not found.');
  const r = (await q('INSERT INTO learning_assignments (item_id, group_id, assigned_by) VALUES ($1,$2,$3) ON CONFLICT (item_id, group_id) DO NOTHING RETURNING *', [item.id, group.id, caller.user.id])).rows[0];
  await tx(async (client) => writeAudit(client, caller, { action: 'learning.assign', entity: 'LearningAssignment', entityId: r?.id || item.id, after: { itemId: item.id, groupId: group.id }, scopeType: 'Group', scopeId: group.id }));
  res.status(201).json({ assigned: !!r });
}));

router.post('/learning/:id/progress', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  const person = (await q('SELECT id FROM persons WHERE linked_user_id=$1', [caller.user.id])).rows[0];
  if (!person) throw badRequest('No member profile is linked to this account.');
  const status = oneOf(req.body.status, ['opened', 'completed'], 'Status');
  const item = (await q('SELECT * FROM learning_items WHERE id=$1', [req.params.id])).rows[0];
  if (!item) throw notFound('Learning item not found.');
  const r = (await q(
    `INSERT INTO learning_progress (item_id, person_id, status, opened_at, completed_at)
     VALUES ($1,$2,$3, CASE WHEN $3='opened' THEN now() ELSE NULL END, CASE WHEN $3='completed' THEN now() ELSE NULL END)
     ON CONFLICT (item_id, person_id) DO UPDATE SET
       status = CASE WHEN learning_progress.status='completed' THEN 'completed' ELSE EXCLUDED.status END,
       opened_at = COALESCE(learning_progress.opened_at, EXCLUDED.opened_at),
       completed_at = COALESCE(learning_progress.completed_at, EXCLUDED.completed_at)
     RETURNING *`, [item.id, person.id, status],
  )).rows[0];
  res.json({ progress: r, note: 'Learning completion never clears arrears or changes lending eligibility.' });
}));

// ---------- Comments ----------
router.get('/comments', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  const group = await groupOr404(req.query.groupId);
  if (!(await canAccessGroup(caller, group))) throw forbidden();
  const privileged = hasRole(caller, ['field_officer', 'management', 'auditor', 'chairperson', 'secretary'], { groupId: group.id, branchId: group.branch_id }) || isAdmin(caller);
  const rows = (await q(
    `SELECT c.*, u.full_name AS author_name FROM comments c JOIN users u ON u.id=c.author_user_id
     WHERE c.group_id=$1 AND (c.visibility='group' OR $2) ORDER BY c.created_at DESC LIMIT 100`, [group.id, privileged],
  )).rows;
  res.json({ comments: rows, canModerate: privileged });
}));

router.post('/comments', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  const group = await groupOr404(req.body.groupId);
  if (!(await canAccessGroup(caller, group))) throw forbidden();
  const body = str(req.body.body, 'Comment', { min: 2, max: 2000 });
  const visibility = oneOf(req.body.visibility || 'group', ['group', 'restricted'], 'Visibility');
  return tx(async (client) => {
    const c = (await client.query(
      'INSERT INTO comments (group_id, body, visibility, author_user_id) VALUES ($1,$2,$3,$4) RETURNING *',
      [group.id, body, visibility, caller.user.id],
    )).rows[0];
    await writeAudit(client, caller, { action: 'comment.create', entity: 'Comment', entityId: c.id, after: { groupId: group.id, visibility }, scopeType: 'Group', scopeId: group.id });
    res.status(201).json({ comment: c });
  });
}));

router.post('/comments/:id/moderate', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  const c = (await q('SELECT * FROM comments WHERE id=$1', [req.params.id])).rows[0];
  if (!c) throw notFound('Comment not found.');
  const group = await groupOr404(c.group_id);
  if (!(hasRole(caller, ['field_officer', 'management'], { groupId: group.id, branchId: group.branch_id }) || isAdmin(caller))) throw forbidden();
  const reason = str(req.body.reason, 'Reason', { min: 3, max: 300 });
  return tx(async (client) => {
    await client.query("UPDATE comments SET status='removed', moderation_reason=$2, moderated_by=$3 WHERE id=$1", [c.id, reason, caller.user.id]);
    await writeAudit(client, caller, { action: 'comment.moderate', entity: 'Comment', entityId: c.id, before: { status: c.status }, after: { status: 'removed' }, reason, scopeType: 'Group', scopeId: group.id });
    res.json({ ok: true });
  });
}));

// ---------- Disputes ----------
router.get('/disputes', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  const person = (await q('SELECT id FROM persons WHERE linked_user_id=$1', [caller.user.id])).rows[0];
  const broad = hasRole(caller, ['management', 'auditor', 'field_officer', 'verifier']);
  const rows = broad
    ? (await q(`SELECT d.*, u.full_name AS raised_by_name FROM disputes d JOIN users u ON u.id=d.raised_by_user_id ORDER BY d.created_at DESC LIMIT 100`)).rows
    : (await q(`SELECT d.*, u.full_name AS raised_by_name FROM disputes d JOIN users u ON u.id=d.raised_by_user_id
         WHERE d.raised_by_user_id=$1 OR d.assigned_to_user_id=$1 OR d.reviewer_user_id=$1 OR d.person_id=$2 ORDER BY d.created_at DESC LIMIT 100`, [caller.user.id, person?.id || null])).rows;
  res.json({ disputes: rows, canResolve: hasRole(caller, ['field_officer', 'verifier', 'management']) || isAdmin(caller) });
}));

router.post('/disputes', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  const kind = oneOf(req.body.kind, ['attendance', 'payment'], 'Dispute kind');
  const entity = kind === 'attendance' ? 'AttendanceRecord' : 'PaymentEntry';
  const entityId = str(req.body.entityId, 'Record', { max: 60 });
  const reason = str(req.body.reason, 'Reason', { min: 10, max: 1000 });
  const person = (await q('SELECT id FROM persons WHERE linked_user_id=$1', [caller.user.id])).rows[0];
  let groupId;
  let originalValue;
  if (kind === 'attendance') {
    const a = (await q('SELECT a.*, m.group_id FROM attendance_records a JOIN meetings m ON m.id=a.meeting_id WHERE a.id=$1', [entityId])).rows[0];
    if (!a) throw notFound('Attendance record not found.');
    if (a.person_id !== person?.id) throw forbidden('You can only dispute your own attendance.');
    groupId = a.group_id; originalValue = a.status;
  } else {
    const e = (await q('SELECT * FROM payment_entries WHERE id=$1', [entityId])).rows[0];
    if (!e) throw notFound('Payment entry not found.');
    if (e.member_person_id !== person?.id) throw forbidden('You can only dispute your own payment entry.');
    groupId = e.group_id; originalValue = String(e.amount);
  }
  const group = await groupOr404(groupId);
  const assignee = (await q(
    `SELECT ra.user_id FROM role_assignments ra WHERE ra.role=$1 AND ra.status='active' AND (ra.scope_type='Org' OR ra.scope_id=$2 OR ra.scope_id=$3) LIMIT 1`,
    [kind === 'attendance' ? 'field_officer' : 'verifier', group.branch_id, group.id],
  )).rows[0]?.user_id;
  return tx(async (client) => {
    const d = (await client.query(
      `INSERT INTO disputes (kind, entity, entity_id, group_id, person_id, raised_by_user_id, reason, original_value, assigned_to_user_id)
       VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING *`,
      [kind, entity, entityId, group.id, person?.id || null, caller.user.id, reason, originalValue, assignee || null],
    )).rows[0];
    await writeAudit(client, caller, { action: 'dispute.raise', entity: 'Dispute', entityId: d.id, after: { kind, entity, entityId, groupId: group.id }, reason, scopeType: 'Group', scopeId: group.id });
    res.status(201).json({ dispute: d });
  });
}));

router.post('/disputes/:id/resolve', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  const d = (await q('SELECT * FROM disputes WHERE id=$1', [req.params.id])).rows[0];
  if (!d) throw notFound('Dispute not found.');
  if (d.raised_by_user_id === caller.user.id) throw forbidden('You cannot review your own dispute.');
  if (!(d.assigned_to_user_id === caller.user.id || hasRole(caller, ['verifier', 'field_officer', 'management']) || isAdmin(caller))) throw forbidden();
  const resolution = str(req.body.resolution, 'Resolution', { min: 5, max: 1000 });
  const status = oneOf(req.body.status || 'resolved', ['resolved', 'dismissed'], 'Status');
  return tx(async (client) => {
    await client.query("UPDATE disputes SET status=$2, resolution=$3, reviewer_user_id=$4, resolved_at=now() WHERE id=$1", [d.id, status, resolution, caller.user.id]);
    await writeAudit(client, caller, { action: 'dispute.resolve', entity: 'Dispute', entityId: d.id, before: { status: d.status }, after: { status }, reason: resolution, scopeType: 'Group', scopeId: d.group_id });
    res.json({ ok: true });
  });
}));

// ---------- Tasks ----------
router.get('/tasks', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  const rows = (await q(
    `SELECT t.*, u.full_name AS owner_name FROM tasks t JOIN users u ON u.id=t.owner_user_id
     WHERE t.owner_user_id=$1 ${hasRole(caller, ['management', 'field_officer']) || isAdmin(caller) ? 'OR true' : ''}
     ORDER BY t.status='open' DESC, t.deadline LIMIT 200`, [caller.user.id],
  )).rows;
  res.json({ tasks: rows, canCreate: hasRole(caller, ['field_officer', 'management', 'verifier', 'credit_approver']) || isAdmin(caller) });
}));

router.post('/tasks', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  if (!(hasRole(caller, ['field_officer', 'management', 'verifier', 'credit_approver']) || isAdmin(caller))) throw forbidden();
  const ownerUserId = str(req.body.ownerUserId, 'Owner', { max: 60 });
  const deadline = isoDate(req.body.deadline, 'Deadline');
  const owner = (await q('SELECT id FROM users WHERE id=$1', [ownerUserId])).rows[0];
  if (!owner) throw badRequest('The task owner must be an existing user.');
  return tx(async (client) => {
    const t = await createTask(client, { title: str(req.body.title, 'Title', { min: 3, max: 300 }), ownerUserId, deadline, linkedEntity: req.body.linkedEntity, linkedId: req.body.linkedId, dedupeKey: req.body.dedupeKey });
    if (!t) throw badRequest('That task already exists.');
    await writeAudit(client, caller, { action: 'task.create', entity: 'Task', entityId: t.id, after: { ownerUserId, deadline }, scopeType: 'Org', scopeId: 'org' });
    res.status(201).json({ task: t });
  });
}));

router.post('/tasks/:id/complete', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  const t = (await q('SELECT * FROM tasks WHERE id=$1', [req.params.id])).rows[0];
  if (!t) throw notFound('Task not found.');
  if (t.owner_user_id !== caller.user.id && !isAdmin(caller)) throw forbidden();
  await q("UPDATE tasks SET status='completed', completed_at=now() WHERE id=$1", [t.id]);
  res.json({ ok: true });
}));

router.post('/jobs/escalate', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  if (!isAdmin(caller)) throw forbidden();
  const result = await tx(async (client) => runEscalations(client));
  res.json({ result, ranAt: new Date().toISOString() });
}));
