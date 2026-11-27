import express from 'express';
import { q, tx } from '../db.js';
import { requireCaller, hasRole, requireRole, loadGroup, requireGroupRole, isAdmin } from '../authz.js';
import { asyncHandler, str, isoDate, oneOf, amount as amt, maskPhone, maskId, today } from '../util.js';
import { badRequest, conflict, forbidden, notFound } from '../errors.js';
import { writeAudit } from '../audit.js';
import { withIdempotency } from '../idempotency.js';
import { groupMembers, groupOr404 } from '../services/common.js';
import { attendanceSummary } from '../services/attendance.js';
import { validateUpload, decodeBase64, signedFileUrl, verifyFileSignature } from '../files.js';

export const router = express.Router();

const GROUP_TYPES = ['VSLA', 'InvestmentClub', 'Association', 'Cooperative', 'SACCO'];

export async function canAccessGroup(caller, group) {
  if (caller.assignments.some((a) => (a.scope_type === 'Org') || (a.scope_type === 'Branch' && a.scope_id === group.branch_id) || (a.scope_type === 'Group' && a.scope_id === group.id))) return true;
  const m = await q(
    `SELECT 1 FROM memberships m JOIN persons p ON p.id=m.person_id WHERE m.group_id=$1 AND p.linked_user_id=$2 AND m.status='active'`,
    [group.id, caller.user.id],
  );
  if (m.rowCount) return true;
  const l = await q(
    `SELECT 1 FROM leaderships l JOIN persons p ON p.id=l.person_id WHERE l.group_id=$1 AND p.linked_user_id=$2
       AND l.effective_from<=current_date AND (l.effective_to IS NULL OR l.effective_to>=current_date)`,
    [group.id, caller.user.id],
  );
  return l.rowCount > 0;
}

export async function visibleGroups(caller, filters = {}) {
  const rows = (await q(
    `SELECT g.*, b.name AS branch_name FROM groups g JOIN org_branches b ON b.id=g.branch_id
     WHERE ($1::text IS NULL OR g.type=$1) AND ($2::uuid IS NULL OR g.branch_id=$2) ORDER BY g.name`,
    [filters.groupType || null, filters.branchId || null],
  )).rows;
  const out = [];
  for (const g of rows) if (await canAccessGroup(caller, g)) out.push(g);
  return out;
}

function sensitiveAllowed(caller) {
  return isAdmin(caller) || hasRole(caller, ['field_officer', 'management', 'auditor']);
}

router.get('/groups', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  const groups = await visibleGroups(caller, req.query);
  const enriched = [];
  for (const g of groups) {
    const counts = (await q(
      `SELECT (SELECT COUNT(*)::int FROM memberships m WHERE m.group_id=$1 AND m.status='active') AS members,
              (SELECT COUNT(*)::int FROM meetings mt WHERE mt.group_id=$1) AS meetings,
              (SELECT COUNT(*)::int FROM loan_applications l WHERE l.group_id=$1) AS loans`, [g.id],
    )).rows[0];
    enriched.push({ ...g, counts });
  }
  res.json({ groups: enriched, canCreate: hasRole(caller, ['field_officer']) || isAdmin(caller) });
}));

router.post('/groups', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  const branchId = str(req.body.branchId, 'Branch', { max: 60 });
  const branch = (await q('SELECT * FROM org_branches WHERE id=$1', [branchId])).rows[0];
  if (!branch) throw badRequest('Branch not found.');
  requireRole(caller, ['field_officer'], { branchId: branch.id });
  const name = str(req.body.name, 'Group name', { min: 2, max: 160 });
  const type = oneOf(req.body.type, GROUP_TYPES, 'Group type');
  return tx(async (client) => {
    const g = (await client.query(
      `INSERT INTO groups (name, type, branch_id, status, created_by) VALUES ($1,$2,$3,'Draft',$4) RETURNING *`,
      [name, type, branch.id, caller.user.id],
    )).rows[0];
    await writeAudit(client, caller, { action: 'group.create', entity: 'Group', entityId: g.id, after: { name, type, branchId: branch.id }, scopeType: 'Branch', scopeId: branch.id });
    res.status(201).json({ group: g });
  });
}));

router.get('/groups/:id', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  const group = await loadGroup(caller, req.params.id);
  const [branch, members, leadership, documents, meetings, submissions, loans, attendance] = await Promise.all([
    q('SELECT * FROM org_branches WHERE id=$1', [group.branch_id]),
    groupMembers(group.id),
    q('SELECT l.*, p.full_name FROM leaderships l JOIN persons p ON p.id=l.person_id WHERE l.group_id=$1 ORDER BY l.effective_from DESC', [group.id]),
    q('SELECT id, kind, filename, mime, size, uploaded_by, created_at FROM file_objects WHERE group_id=$1 ORDER BY created_at DESC', [group.id]),
    q('SELECT * FROM meetings WHERE group_id=$1 ORDER BY scheduled_for DESC LIMIT 50', [group.id]),
    q('SELECT * FROM group_submissions WHERE group_id=$1 ORDER BY submitted_at DESC', [group.id]),
    q('SELECT * FROM loan_applications WHERE group_id=$1 ORDER BY created_at DESC', [group.id]),
    attendanceSummary(group.id),
  ]);
  const privileged = sensitiveAllowed(caller);
  const financial = hasRole(caller, ['treasurer', 'verifier', 'chairperson', 'field_officer', 'management', 'auditor'], { groupId: group.id, branchId: group.branch_id }) || isAdmin(caller);
  const memberRows = members.map((m) => ({
    ...m,
    phone: privileged ? m.phone : maskPhone(m.phone),
    national_id_ref: privileged ? m.national_id_ref : maskId(m.national_id_ref),
  }));
  res.json({
    group: { ...group, branch_name: branch.rows[0]?.name },
    members: memberRows,
    leadership: leadership.rows,
    documents: documents.rows.map((d) => ({ ...d, url: signedFileUrl(d.id, caller.user.id) })),
    meetings: meetings.rows,
    submissions: financial ? submissions.rows : [],
    loans: loans.rows,
    attendance,
    permissions: {
      canManage: hasRole(caller, ['field_officer', 'secretary', 'chairperson'], { branchId: group.branch_id, groupId: group.id }),
      canReview: (hasRole(caller, ['field_officer'], { branchId: group.branch_id }) || hasRole(caller, ['management'])) && group.created_by !== caller.user.id,
      canActivate: (hasRole(caller, ['field_officer'], { branchId: group.branch_id }) || isAdmin(caller)) && group.created_by !== caller.user.id,
      canSeeSensitive: privileged,
    },
  });
}));

router.post('/groups/:id/members', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  const group = await groupOr404(req.params.id);
  requireGroupRole(caller, group, ['field_officer', 'secretary', 'chairperson']);
  if (group.status === 'Closed') throw badRequest('This group is closed.');
  const fullName = str(req.body.fullName, 'Full name', { min: 2, max: 160 });
  const phone = str(req.body.phone, 'Phone', { min: 7, max: 30 });
  const nationalIdRef = str(req.body.nationalIdRef, 'National ID reference', { optional: true, max: 60 });
  const gender = oneOf(req.body.gender || 'NotStated', ['Female', 'Male', 'Other', 'NotStated'], 'Gender');
  const joinDate = isoDate(req.body.joinDate || today(), 'Join date');
  if (!req.body.consentRecorded) throw badRequest('Record the member’s consent before adding them.');
  const existing = (await q(
    `SELECT p.id, p.full_name, g.id AS group_id, g.name AS group_name, g.status FROM persons p
     JOIN memberships m ON m.person_id=p.id JOIN groups g ON g.id=m.group_id
     WHERE m.status='active' AND (p.phone=$1 OR ($2::text IS NOT NULL AND p.national_id_ref=$2)) AND g.id <> $3`,
    [phone, nationalIdRef, group.id],
  )).rows;
  if (existing.length && !req.body.acknowledgeDuplicate) {
    throw conflict('This person is active in another group. Review the existing obligations before continuing.', {
      code: 'DUPLICATE_MEMBER', existing, obligations: existing.map((e) => ({ group: e.group_name, status: e.status })),
    });
  }
  return tx(async (client) => {
    let person = (await client.query('SELECT * FROM persons WHERE phone=$1 LIMIT 1', [phone])).rows[0];
    if (!person) {
      person = (await client.query(
        `INSERT INTO persons (full_name, phone, national_id_ref, gender, date_of_birth, consent_recorded, consent_at, created_by)
         VALUES ($1,$2,$3,$4,$5,true,now(),$6) RETURNING *`,
        [fullName, phone, nationalIdRef, gender, isoDate(req.body.dateOfBirth, 'Date of birth', { optional: true }), caller.user.id],
      )).rows[0];
    } else {
      await client.query('UPDATE persons SET consent_recorded=true, consent_at=now() WHERE id=$1', [person.id]);
    }
    const m = (await client.query(
      `INSERT INTO memberships (person_id, group_id, join_date, status, created_by) VALUES ($1,$2,$3,'active',$4) RETURNING *`,
      [person.id, group.id, joinDate, caller.user.id],
    )).rows[0];
    await writeAudit(client, caller, {
      action: 'membership.add', entity: 'Membership', entityId: m.id,
      after: { personId: person.id, groupId: group.id, joinDate }, scopeType: 'Group', scopeId: group.id,
      reason: existing.length ? `duplicate acknowledged: ${existing.map((e) => e.group_name).join(', ')}` : null,
    });
    res.status(201).json({ membership: m, person: { id: person.id, full_name: person.full_name }, duplicateAcknowledged: existing.length > 0 });
  });
}));

router.post('/groups/:id/members/:membershipId/end', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  const group = await groupOr404(req.params.id);
  requireGroupRole(caller, group, ['field_officer', 'secretary', 'chairperson']);
  const leaveDate = isoDate(req.body.leaveDate, 'Leave date');
  return tx(async (client) => {
    const m = (await client.query('SELECT * FROM memberships WHERE id=$1 AND group_id=$2 FOR UPDATE', [req.params.membershipId, group.id])).rows[0];
    if (!m) throw notFound('Membership not found.');
    if (m.leave_date) throw badRequest('This membership has already ended.');
    if (leaveDate < m.join_date) throw badRequest('The leave date cannot be before the join date.');
    await client.query("UPDATE memberships SET leave_date=$2, status='ended' WHERE id=$1", [m.id, leaveDate]);
    await writeAudit(client, caller, { action: 'membership.end', entity: 'Membership', entityId: m.id, after: { leaveDate }, scopeType: 'Group', scopeId: group.id });
    res.json({ ok: true });
  });
}));

router.post('/groups/:id/members/:membershipId/update', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  const group = await groupOr404(req.params.id);
  requireGroupRole(caller, group, ['field_officer', 'secretary', 'chairperson']);
  return tx(async (client) => {
    const m = (await client.query('SELECT * FROM memberships WHERE id=$1 AND group_id=$2', [req.params.membershipId, group.id])).rows[0];
    if (!m) throw notFound('Membership not found.');
    const person = (await client.query('SELECT * FROM persons WHERE id=$1 FOR UPDATE', [m.person_id])).rows[0];
    const changes = {};
    if (req.body.fullName !== undefined) changes.full_name = str(req.body.fullName, 'Full name', { min: 2, max: 160 });
    if (req.body.phone !== undefined) changes.phone = str(req.body.phone, 'Phone', { min: 7, max: 30 });
    if (req.body.nationalIdRef !== undefined) changes.national_id_ref = str(req.body.nationalIdRef, 'National ID reference', { optional: true, max: 60 });
    if (req.body.gender !== undefined) changes.gender = oneOf(req.body.gender, ['Female', 'Male', 'Other', 'NotStated'], 'Gender');
    if (req.body.dateOfBirth !== undefined) changes.date_of_birth = isoDate(req.body.dateOfBirth, 'Date of birth', { optional: true });
    if (!Object.keys(changes).length) throw badRequest('No changes were supplied.');
    const sets = Object.keys(changes).map((k, i) => `${k}=$${i + 2}`).join(', ');
    await client.query(`UPDATE persons SET ${sets} WHERE id=$1`, [person.id, ...Object.values(changes)]);
    await writeAudit(client, caller, {
      action: 'membership.update_person', entity: 'Person', entityId: person.id,
      before: { full_name: person.full_name, phone: person.phone, national_id_ref: person.national_id_ref, gender: person.gender, date_of_birth: person.date_of_birth },
      after: changes, scopeType: 'Group', scopeId: group.id,
    });
    res.json({ ok: true });
  });
}));

router.post('/groups/:id/leadership', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  const group = await groupOr404(req.params.id);
  requireGroupRole(caller, group, ['field_officer', 'secretary', 'chairperson']);
  const personId = str(req.body.personId, 'Person', { max: 60 });
  const position = oneOf(req.body.position, ['chairperson', 'secretary', 'treasurer', 'committee_member', 'authorised_representative'], 'Position');
  const effectiveFrom = isoDate(req.body.effectiveFrom || today(), 'Effective from');
  const effectiveTo = isoDate(req.body.effectiveTo, 'Effective to', { optional: true });
  const authorityScope = oneOf(req.body.authorityScope || 'group', ['group', 'borrowing', 'signatory'], 'Authority scope');
  const evidenceFileRef = str(req.body.evidenceFileRef, 'Evidence', { optional: true, max: 60 });
  const member = await q('SELECT 1 FROM memberships WHERE person_id=$1 AND group_id=$2', [personId, group.id]);
  if (!member.rowCount) throw badRequest('Leadership must be an existing group member.');
  return tx(async (client) => {
    const l = (await client.query(
      `INSERT INTO leaderships (group_id, person_id, position, authority_scope, effective_from, effective_to, evidence_file_ref, created_by)
       VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING *`,
      [group.id, personId, position, authorityScope, effectiveFrom, effectiveTo, evidenceFileRef, caller.user.id],
    )).rows[0];
    await writeAudit(client, caller, { action: 'leadership.record', entity: 'Leadership', entityId: l.id, after: { personId, position, authorityScope }, scopeType: 'Group', scopeId: group.id });
    res.status(201).json({ leadership: l });
  });
}));

router.post('/groups/:id/submit', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  const group = await groupOr404(req.params.id);
  requireGroupRole(caller, group, ['field_officer', 'secretary', 'chairperson']);
  if (!['Draft', 'Returned'].includes(group.status)) throw badRequest('Only a draft or returned group can be submitted.');
  return tx(async (client) => {
    await client.query("UPDATE groups SET status='Submitted', submitted_at=now(), review_decision=NULL WHERE id=$1", [group.id]);
    await writeAudit(client, caller, { action: 'group.submit', entity: 'Group', entityId: group.id, before: { status: group.status }, after: { status: 'Submitted' }, scopeType: 'Group', scopeId: group.id });
    res.json({ status: 'Submitted' });
  });
}));

router.post('/groups/:id/review', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  const group = await groupOr404(req.params.id);
  if (!(hasRole(caller, ['field_officer'], { branchId: group.branch_id }) || hasRole(caller, ['management']))) throw forbidden('Only a reviewing officer can review this group.');
  if (group.created_by === caller.user.id) throw forbidden('You cannot review a group you created.');
  if (!['Submitted', 'UnderReview'].includes(group.status)) throw badRequest('This group is not awaiting review.');
  const decision = oneOf(req.body.decision, ['approve', 'return', 'reject'], 'Decision');
  const note = str(req.body.note, 'Review note', { optional: true, max: 500 });
  const status = decision === 'approve' ? 'UnderReview' : decision === 'return' ? 'Returned' : 'Rejected';
  return tx(async (client) => {
    await client.query(
      `UPDATE groups SET status=$2, reviewed_by=$3, reviewed_at=now(), review_decision=$4, review_note=$5 WHERE id=$1`,
      [group.id, status, caller.user.id, decision, note],
    );
    await writeAudit(client, caller, { action: `group.review_${decision}`, entity: 'Group', entityId: group.id, before: { status: group.status }, after: { status, decision }, reason: note, scopeType: 'Group', scopeId: group.id });
    res.json({ status, decision });
  });
}));

router.post('/groups/:id/activate', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  const group = await groupOr404(req.params.id);
  if (!(hasRole(caller, ['field_officer'], { branchId: group.branch_id }) || isAdmin(caller))) throw forbidden('Only an officer can activate a group.');
  if (group.created_by === caller.user.id) throw forbidden('You cannot activate a group you created.');
  if (group.status !== 'UnderReview' || group.review_decision !== 'approve') throw badRequest('A group becomes active only after an independent approval.');
  return tx(async (client) => {
    await client.query("UPDATE groups SET status='Active', activated_at=now() WHERE id=$1", [group.id]);
    await writeAudit(client, caller, { action: 'group.activate', entity: 'Group', entityId: group.id, before: { status: group.status }, after: { status: 'Active' }, scopeType: 'Group', scopeId: group.id });
    res.json({ status: 'Active' });
  });
}));

router.post('/groups/:id/documents', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  const group = await groupOr404(req.params.id);
  requireGroupRole(caller, group, ['field_officer', 'secretary', 'chairperson']);
  const kind = oneOf(req.body.kind, ['registration_evidence', 'rules', 'leadership_evidence', 'other'], 'Document kind');
  validateUpload(req.body);
  const buf = decodeBase64(req.body.data);
  return tx(async (client) => {
    const f = (await client.query(
      `INSERT INTO file_objects (group_id, kind, filename, mime, size, data, uploaded_by) VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id`,
      [group.id, kind, req.body.filename, req.body.mime, buf.length, buf, caller.user.id],
    )).rows[0];
    if (kind === 'registration_evidence') {
      await client.query("UPDATE groups SET registration_evidence_status='Provided' WHERE id=$1", [group.id]);
    }
    await writeAudit(client, caller, { action: 'document.upload', entity: 'FileObject', entityId: f.id, after: { kind, mime: req.body.mime, size: buf.length }, scopeType: 'Group', scopeId: group.id });
    res.status(201).json({ file: { id: f.id, url: signedFileUrl(f.id, caller.user.id) } });
  });
}));

router.get('/files/:id', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  if (!verifyFileSignature(req.params.id, caller.user.id, req.query.expires, req.query.sig)) throw forbidden('This file link has expired.');
  const file = (await q('SELECT * FROM file_objects WHERE id=$1', [req.params.id])).rows[0];
  if (!file) throw notFound('File not found.');
  if (file.group_id) {
    const group = await groupOr404(file.group_id);
    if (!(await canAccessGroup(caller, group))) throw forbidden();
  }
  res.setHeader('Content-Type', file.mime);
  res.setHeader('Content-Disposition', `inline; filename="${file.filename.replace(/"/g, '')}"`);
  res.setHeader('Cache-Control', 'private, max-age=60');
  res.send(file.data);
}));

router.get('/persons', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  if (!(hasRole(caller, ['field_officer', 'secretary', 'treasurer', 'chairperson', 'verifier']) || isAdmin(caller))) throw forbidden();
  const term = String(req.query.q || '').slice(0, 80);
  const rows = (await q(
    `SELECT id, full_name, phone, gender, national_id_ref FROM persons
     WHERE ($1='' OR full_name ILIKE '%'||$1||'%' OR phone ILIKE '%'||$1||'%') ORDER BY full_name LIMIT 30`, [term],
  )).rows;
  const privileged = sensitiveAllowed(caller);
  res.json({ persons: rows.map((p) => ({ ...p, phone: privileged ? p.phone : maskPhone(p.phone), national_id_ref: privileged ? p.national_id_ref : maskId(p.national_id_ref) })) });
}));

router.get('/branches', asyncHandler(async (req, res) => {
  requireCaller(req);
  res.json({ branches: (await q('SELECT id, name, district, code FROM org_branches ORDER BY name')).rows });
}));

router.get('/directory/users', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  if (!(hasRole(caller, ['field_officer', 'management', 'verifier', 'credit_approver', 'it_admin']) || isAdmin(caller))) throw forbidden();
  const users = (await q('SELECT id, full_name, email FROM users WHERE status=$1 ORDER BY full_name', ['active'])).rows;
  const roles = (await q("SELECT user_id, role FROM role_assignments WHERE status='active'")).rows;
  res.json({ users: users.map((u) => ({ ...u, roles: roles.filter((r) => r.user_id === u.id).map((r) => r.role) })) });
}));
