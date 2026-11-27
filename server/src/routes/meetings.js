import express from 'express';
import { q, tx } from '../db.js';
import { requireCaller, hasRole, isAdmin, requireGroupRole } from '../authz.js';
import { asyncHandler, str, isoDate, isoDateTime, oneOf, today, addDays } from '../util.js';
import { badRequest, forbidden, notFound } from '../errors.js';
import { writeAudit } from '../audit.js';
import { groupMembers, groupOr404, membershipAt, createTask, notify } from '../services/common.js';
import { attendanceSummary } from '../services/attendance.js';
import { visibleGroups, canAccessGroup } from './groups.js';
import { validateUpload, decodeBase64, signedFileUrl } from '../files.js';
import { policy } from '../config.js';

export const router = express.Router();

async function secretaryOf(caller, group) {
  if (hasRole(caller, ['secretary'], { groupId: group.id, branchId: group.branch_id })) return true;
  const l = await q(
    `SELECT 1 FROM leaderships l JOIN persons p ON p.id=l.person_id WHERE l.group_id=$1 AND p.linked_user_id=$2
       AND l.position='secretary' AND l.effective_from<=current_date AND (l.effective_to IS NULL OR l.effective_to>=current_date)`,
    [group.id, caller.user.id],
  );
  return l.rowCount > 0;
}

async function loadMeeting(caller, id) {
  const m = (await q('SELECT * FROM meetings WHERE id=$1', [id])).rows[0];
  if (!m) throw notFound('Meeting not found.');
  const group = await groupOr404(m.group_id);
  if (!(await canAccessGroup(caller, group))) throw forbidden('You do not have access to this meeting.');
  return { meeting: m, group };
}

router.get('/meetings', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  const groups = await visibleGroups(caller, {});
  const ids = groups.map((g) => g.id);
  if (!ids.length) return res.json({ meetings: [] });
  const rows = (await q(
    `SELECT m.*, g.name AS group_name FROM meetings m JOIN groups g ON g.id=m.group_id
     WHERE m.group_id = ANY($1::uuid[]) ${req.query.groupId ? 'AND m.group_id=$2' : ''} ORDER BY m.scheduled_for DESC LIMIT 100`,
    req.query.groupId ? [ids, req.query.groupId] : [ids],
  )).rows;
  res.json({ meetings: rows, groups: groups.map((g) => ({ id: g.id, name: g.name, status: g.status })) });
}));

router.post('/meetings', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  const group = await groupOr404(req.body.groupId);
  if (!(await secretaryOf(caller, group)) && !hasRole(caller, ['chairperson', 'field_officer'], { groupId: group.id, branchId: group.branch_id })) {
    throw forbidden('Only the group secretary, chairperson or assigned officer can schedule a meeting.');
  }
  if (group.status !== 'Active') throw badRequest('Meetings can only be scheduled for an active group.');
  const title = str(req.body.title, 'Title', { min: 3, max: 160 });
  const scheduledFor = isoDateTime(req.body.scheduledFor, 'Scheduled date');
  const locationText = str(req.body.locationText, 'Location', { optional: true, max: 200 });
  return tx(async (client) => {
    const m = (await client.query(
      `INSERT INTO meetings (group_id, title, scheduled_for, location_text, created_by) VALUES ($1,$2,$3,$4,$5) RETURNING *`,
      [group.id, title, scheduledFor, locationText, caller.user.id],
    )).rows[0];
    await writeAudit(client, caller, { action: 'meeting.schedule', entity: 'Meeting', entityId: m.id, after: { title, scheduledFor }, scopeType: 'Group', scopeId: group.id });
    res.status(201).json({ meeting: m });
  });
}));

router.get('/meetings/:id', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  const { meeting, group } = await loadMeeting(caller, req.params.id);
  const [members, attendance, minutes, resolutions, actions, documents, amendments] = await Promise.all([
    groupMembers(group.id, new Date(meeting.scheduled_for).toISOString().slice(0, 10)),
    q('SELECT a.*, p.full_name FROM attendance_records a JOIN persons p ON p.id=a.person_id WHERE a.meeting_id=$1 ORDER BY p.full_name', [meeting.id]),
    q('SELECT * FROM minutes WHERE meeting_id=$1', [meeting.id]),
    q('SELECT * FROM resolutions WHERE meeting_id=$1', [meeting.id]),
    q('SELECT * FROM action_items WHERE meeting_id=$1', [meeting.id]),
    q('SELECT * FROM meeting_documents WHERE meeting_id=$1', [meeting.id]),
    q('SELECT * FROM meeting_amendments WHERE meeting_id=$1 ORDER BY created_at DESC', [meeting.id]),
  ]);
  const manage = (await secretaryOf(caller, group)) || hasRole(caller, ['chairperson', 'field_officer'], { groupId: group.id, branchId: group.branch_id }) || isAdmin(caller) || hasRole(caller, ['management', 'auditor']);
  const myPerson = (await q('SELECT id FROM persons WHERE linked_user_id=$1', [caller.user.id])).rows[0];
  const register = manage ? members.map((m) => ({ ...m, attendance: attendance.rows.find((a) => a.person_id === m.person_id) || null })) : members.filter((m) => m.person_id === myPerson?.id).map((m) => ({ ...m, attendance: attendance.rows.find((a) => a.person_id === m.person_id) || null }));
  res.json({
    meeting, group: { id: group.id, name: group.name, branch_id: group.branch_id },
    register,
    attendance: manage ? attendance.rows : attendance.rows.filter((a) => a.person_id === myPerson?.id),
    minutes: minutes.rows[0] || null,
    resolutions: resolutions.rows,
    actionItems: actions.rows,
    documents: documents.rows.map((d) => ({ ...d, url: signedFileUrl(d.file_id, caller.user.id) })),
    amendments: amendments.rows,
    permissions: {
      isSecretary: await secretaryOf(caller, group),
      canManage: manage,
      canCheckIn: meeting.status === 'open' && !!myPerson,
      myPersonId: myPerson?.id || null,
    },
  });
}));

router.post('/meetings/:id/open', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  const { meeting, group } = await loadMeeting(caller, req.params.id);
  if (!(await secretaryOf(caller, group))) throw forbidden('Only the secretary of this group can open its meeting.');
  if (meeting.status !== 'scheduled') throw badRequest('Only a scheduled meeting can be opened.');
  return tx(async (client) => {
    await client.query("UPDATE meetings SET status='open', opened_by=$2, opened_at=now() WHERE id=$1", [meeting.id, caller.user.id]);
    await writeAudit(client, caller, { action: 'meeting.open', entity: 'Meeting', entityId: meeting.id, before: { status: meeting.status }, after: { status: 'open' }, scopeType: 'Group', scopeId: group.id });
    res.json({ status: 'open' });
  });
}));

router.post('/meetings/:id/check-in', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  const { meeting, group } = await loadMeeting(caller, req.params.id);
  if (meeting.status !== 'open') throw badRequest('Check-in is only open while the meeting is open.');
  const myPerson = (await q('SELECT id FROM persons WHERE linked_user_id=$1', [caller.user.id])).rows[0];
  const targetId = req.body.personId || myPerson?.id;
  if (!targetId) throw badRequest('No member was selected.');
  const assisted = targetId !== myPerson?.id;
  if (assisted) {
    const allowed = (await secretaryOf(caller, group)) || hasRole(caller, ['field_officer'], { groupId: group.id, branchId: group.branch_id });
    if (!allowed) throw forbidden('Only the secretary or an officer can check in another member.');
    if (!req.body.confirmationMethod) throw badRequest('Assisted check-in requires how the member confirmed.');
  }
  const meetingDate = new Date(meeting.scheduled_for).toISOString().slice(0, 10);
  const membership = await membershipAt(targetId, group.id, meetingDate);
  if (!membership) throw badRequest('Attendance requires an active membership on the meeting date.');
  const status = oneOf(req.body.status || 'present', ['present', 'late', 'excused'], 'Attendance status');
  return tx(async (client) => {
    const existing = (await client.query('SELECT * FROM attendance_records WHERE meeting_id=$1 AND person_id=$2', [meeting.id, targetId])).rows[0];
    if (existing) return res.json({ attendance: existing, duplicate: true });
    const a = (await client.query(
      `INSERT INTO attendance_records (meeting_id, person_id, status, method, recorded_by, confirmation_method)
       VALUES ($1,$2,$3,$4,$5,$6) RETURNING *`,
      [meeting.id, targetId, status, assisted ? 'assisted' : 'self', caller.user.id, assisted ? str(req.body.confirmationMethod, 'Confirmation method', { max: 120 }) : null],
    )).rows[0];
    await writeAudit(client, caller, { action: 'attendance.check_in', entity: 'AttendanceRecord', entityId: a.id, after: { meetingId: meeting.id, personId: targetId, status, assisted }, scopeType: 'Group', scopeId: group.id });
    res.status(201).json({ attendance: a });
  });
}));

router.post('/meetings/:id/confirm-register', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  const { meeting, group } = await loadMeeting(caller, req.params.id);
  if (!(await secretaryOf(caller, group))) throw forbidden('Only the secretary can confirm the register.');
  if (meeting.status !== 'open') throw badRequest('Only an open meeting has a register to confirm.');
  return tx(async (client) => {
    await client.query("UPDATE meetings SET status='register_confirmed', register_confirmed_by=$2, register_confirmed_at=now() WHERE id=$1", [meeting.id, caller.user.id]);
    await writeAudit(client, caller, { action: 'attendance.confirm_register', entity: 'Meeting', entityId: meeting.id, before: { status: meeting.status }, after: { status: 'register_confirmed' }, scopeType: 'Group', scopeId: group.id });
    res.json({ status: 'register_confirmed' });
  });
}));

router.post('/meetings/:id/close', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  const { meeting, group } = await loadMeeting(caller, req.params.id);
  if (!(await secretaryOf(caller, group))) throw forbidden('Only the secretary can close the meeting.');
  if (meeting.status !== 'register_confirmed') throw badRequest('Confirm the register before closing the meeting.');
  return tx(async (client) => {
    await client.query("UPDATE meetings SET status='closed', closed_by=$2, closed_at=now() WHERE id=$1", [meeting.id, caller.user.id]);
    await writeAudit(client, caller, { action: 'meeting.close', entity: 'Meeting', entityId: meeting.id, before: { status: meeting.status }, after: { status: 'closed' }, scopeType: 'Group', scopeId: group.id });
    res.json({ status: 'closed' });
  });
}));

router.post('/meetings/:id/cancel', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  const { meeting, group } = await loadMeeting(caller, req.params.id);
  if (!(await secretaryOf(caller, group)) && !hasRole(caller, ['chairperson', 'field_officer'], { groupId: group.id, branchId: group.branch_id })) throw forbidden();
  if (!['scheduled', 'open'].includes(meeting.status)) throw badRequest('Only a scheduled or open meeting can be cancelled.');
  const reason = str(req.body.reason, 'Reason', { min: 3, max: 300 });
  return tx(async (client) => {
    await client.query("UPDATE meetings SET status='cancelled', cancel_reason=$2 WHERE id=$1", [meeting.id, reason]);
    await writeAudit(client, caller, { action: 'meeting.cancel', entity: 'Meeting', entityId: meeting.id, before: { status: meeting.status }, after: { status: 'cancelled' }, reason, scopeType: 'Group', scopeId: group.id });
    res.json({ status: 'cancelled' });
  });
}));

router.post('/meetings/:id/minutes', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  const { meeting, group } = await loadMeeting(caller, req.params.id);
  if (!(await secretaryOf(caller, group))) throw forbidden('Only the secretary can record minutes.');
  if (meeting.status === 'closed') throw badRequest('Closed minutes can only be changed through an approved amendment.');
  if (!['open', 'register_confirmed'].includes(meeting.status)) throw badRequest('Minutes can be recorded while the meeting is open.');
  const body = str(req.body.body, 'Minutes', { min: 10, max: 20000 });
  return tx(async (client) => {
    const existing = (await client.query('SELECT * FROM minutes WHERE meeting_id=$1', [meeting.id])).rows[0];
    if (existing) await client.query('UPDATE minutes SET body=$2, recorded_by=$3, recorded_at=now() WHERE meeting_id=$1', [meeting.id, body, caller.user.id]);
    else await client.query('INSERT INTO minutes (meeting_id, body, recorded_by) VALUES ($1,$2,$3)', [meeting.id, body, caller.user.id]);
    await writeAudit(client, caller, { action: 'minutes.record', entity: 'Minutes', entityId: meeting.id, after: { length: body.length }, scopeType: 'Group', scopeId: group.id });
    res.json({ ok: true });
  });
}));

router.post('/meetings/:id/resolutions', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  const { meeting, group } = await loadMeeting(caller, req.params.id);
  if (!(await secretaryOf(caller, group))) throw forbidden('Only the secretary can record resolutions.');
  if (meeting.status === 'cancelled') throw badRequest('A cancelled meeting has no resolutions.');
  const body = str(req.body.body, 'Resolution', { min: 5, max: 5000 });
  return tx(async (client) => {
    const r = (await client.query('INSERT INTO resolutions (meeting_id, body, recorded_by) VALUES ($1,$2,$3) RETURNING *', [meeting.id, body, caller.user.id])).rows[0];
    await writeAudit(client, caller, { action: 'resolution.record', entity: 'Resolution', entityId: r.id, after: { meetingId: meeting.id }, scopeType: 'Group', scopeId: group.id });
    res.status(201).json({ resolution: r });
  });
}));

router.post('/meetings/:id/action-items', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  const { meeting, group } = await loadMeeting(caller, req.params.id);
  if (!(await secretaryOf(caller, group)) && !hasRole(caller, ['chairperson', 'field_officer'], { groupId: group.id, branchId: group.branch_id })) throw forbidden();
  const title = str(req.body.title, 'Action', { min: 3, max: 300 });
  const ownerPersonId = str(req.body.ownerPersonId, 'Owner', { max: 60 });
  const deadline = isoDate(req.body.deadline, 'Deadline');
  const owner = (await q('SELECT * FROM persons WHERE id=$1', [ownerPersonId])).rows[0];
  if (!owner) throw badRequest('The action owner must be a person.');
  return tx(async (client) => {
    const a = (await client.query(
      `INSERT INTO action_items (meeting_id, group_id, title, owner_person_id, deadline, created_by) VALUES ($1,$2,$3,$4,$5,$6) RETURNING *`,
      [meeting.id, group.id, title, ownerPersonId, deadline, caller.user.id],
    )).rows[0];
    if (owner.linked_user_id) {
      await notify(client, { userId: owner.linked_user_id, title: 'New action item', body: title, deadline, dedupeKey: `action:${a.id}` });
    }
    await writeAudit(client, caller, { action: 'action_item.create', entity: 'ActionItem', entityId: a.id, after: { title, ownerPersonId, deadline }, scopeType: 'Group', scopeId: group.id });
    res.status(201).json({ actionItem: a });
  });
}));

router.post('/meetings/:id/amendments', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  const { meeting, group } = await loadMeeting(caller, req.params.id);
  if (!(await secretaryOf(caller, group)) && !hasRole(caller, ['chairperson', 'field_officer'], { groupId: group.id, branchId: group.branch_id })) throw forbidden();
  if (meeting.status !== 'closed') throw badRequest('Only a closed meeting can be amended.');
  const field = str(req.body.field, 'Field', { max: 80 });
  const newValue = str(req.body.newValue, 'New value', { min: 1, max: 20000 });
  const reason = str(req.body.reason, 'Reason', { min: 10, max: 500 });
  let originalValue = null;
  if (field === 'minutes') originalValue = (await q('SELECT body FROM minutes WHERE meeting_id=$1', [meeting.id])).rows[0]?.body || null;
  else if (field.startsWith('attendance:')) {
    const personId = field.split(':')[1];
    originalValue = (await q('SELECT status FROM attendance_records WHERE meeting_id=$1 AND person_id=$2', [meeting.id, personId])).rows[0]?.status || null;
  } else throw badRequest('Unsupported amendment field.');
  return tx(async (client) => {
    const a = (await client.query(
      `INSERT INTO meeting_amendments (meeting_id, field, original_value, new_value, reason, requested_by) VALUES ($1,$2,$3,$4,$5,$6) RETURNING *`,
      [meeting.id, field, originalValue, newValue, reason, caller.user.id],
    )).rows[0];
    await writeAudit(client, caller, { action: 'meeting.amendment_request', entity: 'MeetingAmendment', entityId: a.id, before: { [field]: originalValue }, after: { [field]: newValue }, reason, scopeType: 'Group', scopeId: group.id });
    res.status(201).json({ amendment: a });
  });
}));

router.post('/meetings/:id/amendments/:amendmentId/approve', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  const { meeting, group } = await loadMeeting(caller, req.params.id);
  if (!(await secretaryOf(caller, group)) && !hasRole(caller, ['field_officer'], { groupId: group.id, branchId: group.branch_id })) throw forbidden('Only the secretary or an officer can approve an amendment.');
  return tx(async (client) => {
    const a = (await client.query('SELECT * FROM meeting_amendments WHERE id=$1 AND meeting_id=$2 FOR UPDATE', [req.params.amendmentId, meeting.id])).rows[0];
    if (!a) throw notFound('Amendment not found.');
    if (a.requested_by === caller.user.id) throw forbidden('The requester cannot approve their own amendment.');
    if (a.status !== 'pending') throw badRequest('This amendment is not pending.');
    if (a.field === 'minutes') {
      await client.query('UPDATE minutes SET body=$2, recorded_by=$3, recorded_at=now() WHERE meeting_id=$1', [meeting.id, a.new_value, caller.user.id]);
    } else if (a.field.startsWith('attendance:')) {
      const personId = a.field.split(':')[1];
      oneOf(a.new_value, ['present', 'late', 'excused', 'absent'], 'Attendance status');
      await client.query(
        `INSERT INTO attendance_records (meeting_id, person_id, status, method, recorded_by)
         VALUES ($1,$2,$3,'amendment',$4)
         ON CONFLICT (meeting_id, person_id) DO UPDATE SET status=EXCLUDED.status, recorded_by=EXCLUDED.recorded_by`,
        [meeting.id, personId, a.new_value, caller.user.id],
      );
    }
    await client.query("UPDATE meeting_amendments SET status='approved', reviewed_by=$2, applied_at=now() WHERE id=$1", [a.id, caller.user.id]);
    await writeAudit(client, caller, { action: 'meeting.amendment_approve', entity: 'MeetingAmendment', entityId: a.id, before: { [a.field]: a.original_value }, after: { [a.field]: a.new_value }, reason: a.reason, scopeType: 'Group', scopeId: group.id });
    res.json({ ok: true });
  });
}));

router.post('/meetings/:id/documents', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  const { meeting, group } = await loadMeeting(caller, req.params.id);
  if (!(await secretaryOf(caller, group))) throw forbidden('Only the secretary can upload meeting documents.');
  const photosEnabled = await policy('photos.enabled');
  if (req.body.mime?.startsWith('image/') && !photosEnabled) throw badRequest('Meeting photos are disabled by policy.');
  if (!req.body.consentFlag) throw badRequest('Record consent before uploading a meeting document.');
  if (!req.body.photoPolicyAck) throw badRequest('Acknowledge the group photo policy.');
  validateUpload(req.body);
  const buf = decodeBase64(req.body.data);
  return tx(async (client) => {
    const f = (await client.query(
      `INSERT INTO file_objects (group_id, kind, filename, mime, size, data, uploaded_by) VALUES ($1,'meeting_document',$2,$3,$4,$5,$6) RETURNING id`,
      [group.id, req.body.filename, req.body.mime, buf.length, buf, caller.user.id],
    )).rows[0];
    await client.query('INSERT INTO meeting_documents (meeting_id, file_id, consent_flag, photo_policy_ack, uploaded_by) VALUES ($1,$2,$3,$4,$5)', [meeting.id, f.id, true, true, caller.user.id]);
    await writeAudit(client, caller, { action: 'meeting.document_upload', entity: 'FileObject', entityId: f.id, after: { meetingId: meeting.id }, scopeType: 'Group', scopeId: group.id });
    res.status(201).json({ file: { id: f.id, url: signedFileUrl(f.id, caller.user.id) }, note: 'A photo never proves attendance.' });
  });
}));

router.get('/groups/:id/attendance', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  const group = await groupOr404(req.params.id);
  if (!(await canAccessGroup(caller, group))) throw forbidden();
  const summary = await attendanceSummary(group.id, { from: req.query.from || null, to: req.query.to || null });
  const myPerson = (await q('SELECT id FROM persons WHERE linked_user_id=$1', [caller.user.id])).rows[0];
  const leader = await q(
    `SELECT 1 FROM leaderships l JOIN persons p ON p.id=l.person_id WHERE l.group_id=$1 AND p.linked_user_id=$2
       AND l.effective_from<=current_date AND (l.effective_to IS NULL OR l.effective_to>=current_date)`,
    [group.id, caller.user.id],
  );
  const manage = leader.rowCount > 0 || hasRole(caller, ['secretary', 'chairperson', 'treasurer', 'field_officer', 'management', 'auditor', 'verifier'], { groupId: group.id, branchId: group.branch_id }) || isAdmin(caller);
  if (!manage) summary.people = summary.people.filter((p) => p.personId === myPerson?.id);
  res.json(summary);
}));
