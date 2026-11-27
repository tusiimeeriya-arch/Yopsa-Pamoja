import { badRequest, conflict } from './errors.js';
import { canonical, sha256, str } from './util.js';

export function idempotencyKey(value) {
  return str(value, 'Idempotency key', { min: 8, max: 120 });
}

/**
 * Run fn once per (user, key). A repeat with the same payload returns the
 * stored response; a repeat with a different payload is rejected.
 */
export async function withIdempotency(client, caller, key, fnName, payload, fn) {
  const k = idempotencyKey(key);
  const hash = sha256(canonical(payload));
  const inserted = await client.query(
    `INSERT INTO idempotency_keys (user_id, key, function, payload_hash)
     VALUES ($1,$2,$3,$4) ON CONFLICT (user_id, key) DO NOTHING RETURNING key`,
    [caller.user.id, k, fnName, hash],
  );
  if (!inserted.rowCount) {
    const existing = await client.query('SELECT function, payload_hash, response FROM idempotency_keys WHERE user_id=$1 AND key=$2', [caller.user.id, k]);
    const row = existing.rows[0];
    if (row.payload_hash !== hash || row.function !== fnName) {
      throw conflict('This idempotency key was already used with different details. Use a new key.', { code: 'IDEMPOTENCY_MISMATCH' });
    }
    return { ...(row.response || {}), replayed: true };
  }
  if (!key || k.length < 8) throw badRequest('A client-generated idempotency key is required.');
  const result = await fn();
  await client.query('UPDATE idempotency_keys SET response=$3 WHERE user_id=$1 AND key=$2', [caller.user.id, k, result]);
  return { ...result, replayed: false };
}
