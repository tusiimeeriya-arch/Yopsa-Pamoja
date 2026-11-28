#!/usr/bin/env node
/**
 * Run the in-app system checks from the command line.
 *
 * The checks write real rows through the application role and clean up the
 * protected identity tables with the owner role, so this script runs in-process
 * against the database instead of calling the HTTP route. That route is only
 * mounted outside production; in production this script is the only way to run
 * the checks.
 *
 *   ADMIN_DATABASE_URL=... node scripts/system-checks.mjs
 *
 * The application-role connection is derived from ADMIN_DATABASE_URL (override
 * with CHECK_DATABASE_URL); see quiet-session.mjs.
 *
 * The checks run with the identity of an active organisation administrator so
 * row-level security behaves exactly as it does for the API.
 */
import { quietAppUrl } from './quiet-session.mjs';

// Must happen before db.js builds its pool. The checks deliberately trip
// guards, and a run would otherwise leave one ERROR block per refusal in the
// database log; this keeps the SQL on the application role but silences
// server-side error logging for the session (see quiet-session.mjs).
const quiet = quietAppUrl();
if (quiet) process.env.DATABASE_URL = quiet;

const { als, pool } = await import('../src/db.js');
const { adminPool } = await import('../src/owner.js');
const { runSystemChecks } = await import('../src/services/checks.js');

async function main() {
  const admin = (await adminPool.query(
    `SELECT u.id FROM users u
       JOIN role_assignments ra ON ra.user_id = u.id AND ra.status = 'active'
        AND ra.role IN ('it_admin', 'management') AND ra.scope_type = 'Org'
      WHERE u.status = 'active'
      ORDER BY u.created_at LIMIT 1`,
  )).rows[0];
  if (!admin) throw new Error('No active organisation administrator found. Run `npm run create-admin` first.');

  const body = await als.run({ userId: admin.id }, () => runSystemChecks({ user: { id: admin.id } }));
  const width = Math.max(...body.results.map((r) => r.id.length), 10);
  console.log('\nIN-APP SYSTEM CHECKS');
  console.log('='.repeat(100));
  for (const r of body.results) console.log(`${r.status.padEnd(4)}  ${r.id.padEnd(width)}  ${r.name}  —  ${r.detail}`);
  console.log('='.repeat(100));
  console.log(`${body.passed} passed, ${body.failed} failed, ${body.total} total (tag ${body.tag})`);
  // CI floor: a check may be added, but silently removing one must fail the run.
  const MIN = Number(process.env.MIN_SYSTEM_CHECKS || 35);
  if (body.total < MIN) {
    console.error(`expected at least ${MIN} system checks, got ${body.total}`);
    process.exitCode = 1;
  }
  if (body.failed) process.exitCode = 1;
}

main()
  .then(async () => { await pool.end(); await adminPool.end(); })
  .catch(async (err) => { console.error(err.message); await pool.end(); await adminPool.end(); process.exitCode = 1; });
