/**
 * Verify the append-only audit hash chain and exit non-zero on the first break.
 *
 *   node scripts/audit-chain.mjs
 *
 * Runs as the least-privilege application role (DATABASE_URL); audit_logs is
 * readable by that role and has no row-level security.
 */
import process from 'node:process';
import { pool } from '../src/db.js';
import { verifyAuditChain } from '../src/audit.js';

try {
  const result = await verifyAuditChain(pool);
  console.log(JSON.stringify({
    ok: result.ok,
    entries: result.entries,
    firstBreakAt: result.firstBreakAt ?? null,
    checkedAt: new Date().toISOString(),
  }));
  await pool.end();
  process.exit(result.ok ? 0 : 1);
} catch (err) {
  console.error(JSON.stringify({ ok: false, error: err.message }));
  process.exit(2);
}
