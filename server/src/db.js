import pg from 'pg';
import { AsyncLocalStorage } from 'node:async_hooks';

// The API connects as pamoja_app (not the owner, not a superuser), so row-level
// security is enforced by PostgreSQL itself. Migrations and the demo seed use
// ADMIN_DATABASE_URL, which is the owner role.
export const pool = new pg.Pool({
  connectionString: process.env.DATABASE_URL,
  max: Number(process.env.DB_POOL_MAX || 20),
  idleTimeoutMillis: 30000,
});

export const adminPool = new pg.Pool({
  connectionString: process.env.ADMIN_DATABASE_URL || process.env.DATABASE_URL,
  max: 2,
  idleTimeoutMillis: 30000,
});

// Carries the caller's identity (app.user_id) for the current request, so every
// statement the request issues is filtered by the row-level security policies.
export const als = new AsyncLocalStorage();

async function withIdentity(client, userId, fn) {
  await client.query("SELECT set_config('app.user_id', $1, false)", [userId]);
  try {
    return await fn();
  } finally {
    try { await client.query("RESET app.user_id"); } catch { /* connection gone */ }
  }
}

export function q(text, params) {
  const ctx = als.getStore();
  if (ctx?.txClient) return ctx.txClient.query(text, params);
  if (!ctx?.userId) return pool.query(text, params);
  // One statement on its own connection with the caller's identity set, then
  // the identity is cleared before the connection returns to the pool.
  return pool.connect().then(async (client) => {
    try {
      return await withIdentity(client, ctx.userId, () => client.query(text, params));
    } finally {
      client.release();
    }
  });
}

/**
 * Run fn in a transaction. Inside an HTTP request the caller's identity is set
 * transaction-locally; outside a request (seed, scheduled jobs) a fresh
 * connection is used.
 */
export async function tx(fn) {
  const ctx = als.getStore();
  if (ctx?.txClient) return fn(ctx.txClient);
  const client = await pool.connect();
  try {
    await client.query('BEGIN');
    if (ctx?.userId) await client.query("SELECT set_config('app.user_id', $1, true)", [ctx.userId]);
    if (ctx) ctx.txClient = client;
    const result = await fn(client);
    await client.query('COMMIT');
    return result;
  } catch (err) {
    try { await client.query('ROLLBACK'); } catch { /* connection gone */ }
    throw err;
  } finally {
    if (ctx) ctx.txClient = null;
    client.release();
  }
}

/** Owner-role transaction for migrations, the seed and one-off tooling. */
export async function adminTx(fn) {
  const client = await adminPool.connect();
  try {
    await client.query('BEGIN');
    const result = await fn(client);
    await client.query('COMMIT');
    return result;
  } catch (err) {
    try { await client.query('ROLLBACK'); } catch { /* connection gone */ }
    throw err;
  } finally {
    client.release();
  }
}

/**
 * Scheduled-job transaction. `app.system` is a transaction-local flag that only
 * this function sets; no HTTP route can set it.
 */
export async function systemTx(fn) {
  const client = await pool.connect();
  try {
    await client.query('BEGIN');
    await client.query("SELECT set_config('app.system', 'on', true)");
    const result = await fn(client);
    await client.query('COMMIT');
    return result;
  } catch (err) {
    try { await client.query('ROLLBACK'); } catch { /* connection gone */ }
    throw err;
  } finally {
    client.release();
  }
}

export async function ready() {
  const r = await pool.query("select to_regclass('public.users') is not null as ok");
  return r.rows[0].ok;
}
