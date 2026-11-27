import { q } from '../db.js';
import { badRequest, forbidden, notFound } from '../errors.js';

export async function createTask(client, { title, ownerUserId, deadline, linkedEntity, linkedId, dedupeKey }) {
  if (!ownerUserId) throw badRequest('Every task needs an owner.');
  if (!deadline) throw badRequest('Every task needs a deadline.');
  const sql = `INSERT INTO tasks (title, owner_user_id, deadline, linked_entity, linked_id, dedupe_key)
               VALUES ($1,$2,$3,$4,$5,$6)
               ON CONFLICT (dedupe_key) DO NOTHING RETURNING *`;
  const r = await client.query(sql, [title, ownerUserId, deadline, linkedEntity || null, linkedId || null, dedupeKey || null]);
  return r.rows[0] || null;
}

export async function notify(client, { userId, title, body, deadline, dedupeKey }) {
  if (!userId) throw badRequest('Every notification needs an owner.');
  if (!deadline) throw badRequest('Every notification needs a deadline.');
  const r = await client.query(
    `INSERT INTO notifications (user_id, title, body, deadline, dedupe_key)
     VALUES ($1,$2,$3,$4,$5) ON CONFLICT (dedupe_key) DO NOTHING RETURNING *`,
    [userId, title, body, deadline, dedupeKey || null],
  );
  return r.rows[0] || null;
}

export async function membershipAt(personId, groupId, date, client = { query: q }) {
  const r = await client.query(
    `SELECT * FROM memberships WHERE person_id=$1 AND group_id=$2
       AND join_date <= $3 AND (leave_date IS NULL OR leave_date >= $3) AND status <> 'ended'
     ORDER BY join_date DESC LIMIT 1`,
    [personId, groupId, date],
  );
  return r.rows[0] || null;
}

export async function groupMembers(groupId, date = null, client = { query: q }) {
  const sql = `SELECT m.*, p.full_name, p.phone, p.linked_user_id, p.gender, p.national_id_ref
               FROM memberships m JOIN persons p ON p.id=m.person_id
               WHERE m.group_id=$1 AND m.status='active'
                 ${date ? 'AND m.join_date <= $2 AND (m.leave_date IS NULL OR m.leave_date >= $2)' : ''}
               ORDER BY p.full_name`;
  return (await client.query(sql, date ? [groupId, date] : [groupId])).rows;
}

export async function groupOr404(groupId, client = { query: q }) {
  const r = await client.query('SELECT * FROM groups WHERE id=$1', [groupId]);
  if (!r.rowCount) throw notFound('Group not found.');
  return r.rows[0];
}

export async function requireMemberOfGroup(caller, groupId) {
  const r = await q(
    `SELECT p.* FROM persons p JOIN memberships m ON m.person_id=p.id
     WHERE p.linked_user_id=$1 AND m.group_id=$2 AND m.status='active'`,
    [caller.user.id, groupId],
  );
  if (!r.rowCount) throw forbidden('You are not an active member of this group.');
  return r.rows[0];
}

export async function activeLeadership(groupId, position, onDate, client = { query: q }) {
  const r = await client.query(
    `SELECT l.*, p.full_name, p.linked_user_id FROM leaderships l JOIN persons p ON p.id=l.person_id
     WHERE l.group_id=$1 AND l.position = ANY($2) AND l.effective_from <= $3
       AND (l.effective_to IS NULL OR l.effective_to >= $3)`,
    [groupId, position, onDate],
  );
  return r.rows;
}

export async function auditGroupScope(groupId, client = { query: q }) {
  const g = await groupOr404(groupId, client);
  return { scopeType: 'Group', scopeId: g.id, branchId: g.branch_id };
}
