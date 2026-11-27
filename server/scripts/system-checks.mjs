/**
 * Run the in-app system checks from the command line.
 *
 * Signs in as the demo administrator, calls POST /admin/system-checks, prints
 * every result and exits non-zero if any check failed.
 *
 *   API_BASE=http://localhost:8080/api node scripts/system-checks.mjs
 */
const BASE = process.env.API_BASE || 'http://localhost:8080/api';
const EMAIL = process.env.CHECK_ADMIN_EMAIL || 'admin@yopsa.demo';
const PASSWORD = process.env.DEMO_PASSWORD || 'PamojaDemo#2026';

async function main() {
  const login = await fetch(`${BASE}/auth/login`, {
    method: 'POST',
    headers: { 'content-type': 'application/json', 'x-pamoja-client': 'web' },
    body: JSON.stringify({ email: EMAIL, password: PASSWORD }),
  });
  const cookie = (login.headers.getSetCookie?.()[0] || login.headers.get('set-cookie') || '').split(';')[0];
  if (!login.ok || !cookie) throw new Error(`login failed: ${login.status} ${await login.text()}`);

  const res = await fetch(`${BASE}/admin/system-checks`, {
    method: 'POST',
    headers: { 'content-type': 'application/json', 'x-pamoja-client': 'web', cookie },
    body: '{}',
  });
  const text = await res.text();
  if (!res.ok) throw new Error(`system checks failed to run: ${res.status} ${text}`);
  const body = JSON.parse(text);

  const width = Math.max(...body.results.map((r) => r.id.length), 10);
  console.log('\nIN-APP SYSTEM CHECKS');
  console.log('='.repeat(100));
  for (const r of body.results) console.log(`${r.status.padEnd(4)}  ${r.id.padEnd(width)}  ${r.name}  —  ${r.detail}`);
  console.log('='.repeat(100));
  console.log(`${body.passed} passed, ${body.failed} failed, ${body.total} total (tag ${body.tag})`);
  // CI floor: a check may be added, but silently removing one must fail the run.
  const MIN = Number(process.env.MIN_SYSTEM_CHECKS || 33);
  if (body.total < MIN) {
    console.error(`expected at least ${MIN} system checks, got ${body.total}`);
    process.exit(1);
  }
  process.exit(body.failed ? 1 : 0);
}

main().catch((err) => { console.error(err.message); process.exit(1); });
