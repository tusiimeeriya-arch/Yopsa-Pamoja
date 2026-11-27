import { canonical, sha256 } from './util.js';

const PERSONAL_KEYS = new Set(['full_name', 'name', 'phone', 'national_id_ref', 'date_of_birth', 'dob', 'address', 'body', 'password', 'password_hash']);

function scrub(value, depth = 0) {
  if (!value || typeof value !== 'object' || depth > 4) return value;
  if (Array.isArray(value)) return value.map((v) => scrub(v, depth + 1));
  const out = {};
  for (const [k, v] of Object.entries(value)) {
    if (PERSONAL_KEYS.has(k)) continue;
    out[k] = scrub(v, depth + 1);
  }
  return out;
}

/**
 * Append one hash-chained audit entry. Call inside the same transaction as the
 * change it records. The chain is serialised with an advisory lock.
 */
export async function writeAudit(client, caller, entry) {
  await client.query('SELECT pg_advisory_xact_lock(918273)');
  const last = await client.query('SELECT hash FROM audit_logs ORDER BY seq DESC LIMIT 1');
  const prev = last.rows[0]?.hash || 'GENESIS';
  const at = new Date().toISOString();
  const payload = {
    actor_user_id: caller?.user?.id || null,
    role: entry.role || caller?.roleUsed || null,
    scope_type: entry.scopeType || null,
    scope_id: entry.scopeId ? String(entry.scopeId) : null,
    action: entry.action,
    entity: entry.entity,
    entity_id: entry.entityId ? String(entry.entityId) : null,
    before: entry.before ? scrub(entry.before) : null,
    after: entry.after ? scrub(entry.after) : null,
    reason: entry.reason || null,
    channel: entry.channel || 'web',
    at,
  };
  const hash = sha256(prev + '|' + canonical(payload));
  const r = await client.query(
    `INSERT INTO audit_logs (actor_user_id, role, scope_type, scope_id, action, entity, entity_id, before, after, reason, channel, at, prev_hash, hash)
     VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14) RETURNING seq, id`,
    [payload.actor_user_id, payload.role, payload.scope_type, payload.scope_id, payload.action, payload.entity, payload.entity_id,
      payload.before, payload.after, payload.reason, payload.channel, at, prev, hash],
  );
  return r.rows[0];
}

export async function verifyAuditChain(client) {
  const rows = (await client.query('SELECT seq, prev_hash, hash, actor_user_id, role, scope_type, scope_id, action, entity, entity_id, before, after, reason, channel, at FROM audit_logs ORDER BY seq')).rows;
  let prev = 'GENESIS';
  for (const row of rows) {
    const payload = {
      actor_user_id: row.actor_user_id,
      role: row.role,
      scope_type: row.scope_type,
      scope_id: row.scope_id,
      action: row.action,
      entity: row.entity,
      entity_id: row.entity_id,
      before: row.before,
      after: row.after,
      reason: row.reason,
      channel: row.channel,
      at: new Date(row.at).toISOString(),
    };
    const expected = sha256(prev + '|' + canonical(payload));
    if (row.prev_hash !== prev || row.hash !== expected) {
      return { ok: false, entries: rows.length, firstBreakAt: row.seq };
    }
    prev = row.hash;
  }
  return { ok: true, entries: rows.length };
}
