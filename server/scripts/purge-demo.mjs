#!/usr/bin/env node
/**
 * Purge demo data from the command line.
 *
 * The HTTP route is only mounted outside production. This script is the
 * supported path everywhere and the only one in production. It needs the owner
 * role, so run it as a one-off container or with ADMIN_DATABASE_URL in the
 * environment — never with the API's environment.
 *
 *   ADMIN_DATABASE_URL=... node scripts/purge-demo.mjs --confirm "PURGE DEMO DATA"
 */
import { adminPool, adminTx } from '../src/owner.js';
import { purgeDemoData } from '../src/services/demo.js';
import { writeAudit } from '../src/audit.js';

const CONFIRM = 'PURGE DEMO DATA';
const at = process.argv.indexOf('--confirm');
const typed = at === -1 ? null : process.argv[at + 1];

async function main() {
  if (typed !== CONFIRM) {
    console.error(`Refusing to purge: pass --confirm "${CONFIRM}".`);
    process.exitCode = 1;
    return;
  }
  const counts = await adminTx(async (client) => {
    await client.query("SELECT set_config('app.system', 'on', true)");
    await client.query("SELECT set_config('app.demo_purge', 'on', true)");
    const result = await purgeDemoData(client);
    await writeAudit(client, null, {
      action: 'demo.purge', entity: 'DemoBatch', entityId: 'demo', after: result,
      reason: 'Purge demo data (CLI)', role: 'cli', scopeType: 'Org', scopeId: 'org', channel: 'cli',
    });
    return result;
  });
  console.log(JSON.stringify({ result: counts, note: 'AuditLog rows were retained. Demo accounts are disabled.' }, null, 2));
}

main()
  .then(() => adminPool.end())
  .catch(async (err) => { console.error(err.message); await adminPool.end(); process.exitCode = 1; });
