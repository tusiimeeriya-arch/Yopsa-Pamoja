import pg from 'pg';

/**
 * Owner-role database access (ADMIN_DATABASE_URL).
 *
 * This module is for command-line tooling only: the demo seed, create-admin,
 * the demo purge, the system checks and the negative tests. The API process
 * runs as the least-privilege pamoja_app role; in production it must not
 * receive the owner credentials at all. src/index.js refuses to boot when
 * ADMIN_DATABASE_URL is set with NODE_ENV/APP_ENV=production, and the demo
 * tooling routes (purge, system checks) are only mounted outside production.
 *
 * There is deliberately no fallback to DATABASE_URL: without an explicit
 * ADMIN_DATABASE_URL no owner pool exists, so adminTx() throws instead of
 * silently reusing the application connection string.
 */
export const ownerConfigured = Boolean(process.env.ADMIN_DATABASE_URL);

export const adminPool = ownerConfigured
  ? new pg.Pool({ connectionString: process.env.ADMIN_DATABASE_URL, max: 2, idleTimeoutMillis: 30000 })
  : null;

/** Owner-role transaction for migrations, the seed and one-off tooling. */
export async function adminTx(fn) {
  if (!adminPool) throw new Error('Owner credentials (ADMIN_DATABASE_URL) are not available in this process.');
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
