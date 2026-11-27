/**
 * Negative tests. Each test drives the real HTTP API as a real seeded user and
 * asserts that the action is refused. Fixtures that the tests need (a loan at a
 * given stage, a temporary role grant) are created directly by the owner role
 * and removed again at the end.
 *
 * Run inside the stack:
 *   docker compose run --rm --no-deps verify node scripts/negative-tests.mjs
 */
import pg from 'pg';

const BASE = process.env.API_BASE || 'http://api:8080/api';
const PASSWORD = process.env.DEMO_PASSWORD || 'PamojaDemo#2026';
const owner = new pg.Client({ connectionString: process.env.ADMIN_DATABASE_URL || process.env.DATABASE_URL });
// A separate connection as the least-privilege application role, used to prove
// that no HTTP request left app.system switched on for the API's own pool.
const appDb = new pg.Client({ connectionString: process.env.DATABASE_URL });

const TAG = 'NEG-' + Date.now();
const STARTED_AT = new Date();
const results = [];
const cleanup = [];

// Every fixture this run creates is either deleted by the id the API returned
// (cleanup below) or, for the append-only audit log, left in place by design.
// This snapshot proves that no demo (non-test) row changed.
const SAFETY_QUERY = `SELECT
  (SELECT count(*)::int FROM groups WHERE is_demo) AS demo_groups,
  (SELECT count(*)::int FROM payment_entries WHERE is_demo) AS demo_payment_entries,
  (SELECT count(*)::int FROM receipts WHERE is_demo) AS demo_receipts,
  (SELECT count(*)::int FROM loan_applications WHERE is_demo) AS demo_loans,
  (SELECT count(*)::int FROM role_assignments WHERE is_demo) AS demo_roles,
  (SELECT count(*)::int FROM audit_logs) AS audit_rows`;
let safetyBefore = null;

function pass(name, detail) { results.push({ name, pass: true, detail }); }
function fail(name, detail) { results.push({ name, pass: false, detail }); }
const describe = (r) => `${r.status} ${r.body?.error?.code || ''} ${r.body?.error?.message || ''}`.trim();
// Row-level security hides a group the caller has no scope over, so a refused
// cross-group request is either 403 (app check) or 404 (invisible row).
const refused = (r) => r.status === 403 || r.status === 404;

async function login(email) {
  const res = await fetch(`${BASE}/auth/login`, {
    method: 'POST',
    headers: { 'content-type': 'application/json', 'x-pamoja-client': 'web' },
    body: JSON.stringify({ email, password: PASSWORD }),
  });
  const cookies = res.headers.getSetCookie?.() || [];
  const cookie = (cookies[0] || res.headers.get('set-cookie') || '').split(';')[0];
  if (!res.ok || !cookie) throw new Error(`login failed for ${email}: ${res.status} ${await res.text()}`);
  return cookie;
}

function client(cookie) {
  const call = async (method, path, body, headers = {}) => {
    const res = await fetch(BASE + path, {
      method,
      headers: { 'content-type': 'application/json', 'x-pamoja-client': 'web', ...(cookie ? { cookie } : {}), ...headers },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    const text = await res.text();
    let json = null;
    try { json = JSON.parse(text); } catch { /* not json */ }
    return { status: res.status, ok: res.ok, body: json, text };
  };
  return {
    get: (p, h) => call('GET', p, undefined, h),
    post: (p, b, h) => call('POST', p, b ?? {}, h),
    patch: (p, b, h) => call('PATCH', p, b ?? {}, h),
    del: (p, h) => call('DELETE', p, undefined, h),
    otp: async (purpose) => {
      const r = await call('POST', '/auth/otp/request', { purpose });
      if (!r.ok) throw new Error(`otp request failed: ${describe(r)}`);
      return r.body.devCode;
    },
  };
}

async function main() {
  await owner.connect();
  await appDb.connect();
  safetyBefore = (await owner.query(SAFETY_QUERY)).rows[0];

  const q = async (sql, params) => (await owner.query(sql, params)).rows;
  const [g1] = await q("SELECT * FROM groups WHERE name='Tukolere Wamu VSLA'");
  const [g2] = await q("SELECT * FROM groups WHERE name='Nkumba Investment Club'");
  const [g3] = await q("SELECT * FROM groups WHERE name='Mbarara Dairy Association'");
  const members = await q(
    'SELECT m.person_id, p.full_name FROM memberships m JOIN persons p ON p.id=m.person_id WHERE m.group_id=$1 ORDER BY p.full_name LIMIT 3',
    [g1.id],
  );
  const [sub1] = await q("SELECT * FROM group_submissions WHERE group_id=$1 AND status='difference' LIMIT 1", [g1.id]);
  const [reviewLoan] = await q("SELECT * FROM loan_applications WHERE group_id=$1 AND status='OperationsReview' LIMIT 1", [g1.id]);
  const users = Object.fromEntries((await q('SELECT id, email FROM users')).map((u) => [u.email, u.id]));

  const member = client(await login('member@yopsa.demo'));
  const member2 = client(await login('member2@yopsa.demo'));
  const treasurer = client(await login('treasurer@yopsa.demo'));
  const verifier = client(await login('verifier@yopsa.demo'));
  const approver = client(await login('approver@yopsa.demo'));
  const officer = client(await login('officer@yopsa.demo'));
  const officer2 = client(await login('officer2@yopsa.demo'));
  const manager = client(await login('manager@yopsa.demo'));
  const admin = client(await login('admin@yopsa.demo'));

  // One-time codes requested during this run are test artefacts. Only rows for
  // the accounts these tests drive, created after the run started, are removed.
  const otpUsers = ['member', 'member2', 'treasurer', 'verifier', 'approver', 'officer', 'admin']
    .map((name) => users[`${name}@yopsa.demo`]);
  cleanup.push(['DELETE FROM otp_codes WHERE user_id = ANY($1::uuid[]) AND created_at >= $2', [otpUsers, STARTED_AT]]);

  // 1. A member reading another member's payments, and another group's entries
  {
    const [memberPerson] = await q('SELECT id FROM persons WHERE linked_user_id=$1', [users['member@yopsa.demo']]);
    const own = await member.get(`/payments/entries?groupId=${g1.id}`);
    const others = (own.body?.entries || []).filter((e) => e.member_person_id !== memberPerson.id);
    const otherGroup = await member.get(`/payments/entries?groupId=${g2.id}`);
    // member2 belongs to Group B; Group A must stay invisible to them.
    const otherMembersGroup = await member2.get(`/payments/entries?groupId=${g1.id}`);
    const ok = own.status === 200 && others.length === 0 && refused(otherGroup) && refused(otherMembersGroup);
    (ok ? pass : fail)('Member reading another member\'s payments',
      `own group returned ${own.body?.entries?.length ?? 0} rows, all the member's own: ${others.length === 0}; other group: ${describe(otherGroup)}; member of another group: ${describe(otherMembersGroup)}`);
  }

  // 2. A treasurer from Group A touching Group B
  {
    const r = await treasurer.post('/payments/entries', {
      groupId: g2.id, memberPersonId: members[0].person_id, category: 'savings', amount: 1000,
      collectionDate: new Date().toISOString().slice(0, 10), idempotencyKey: TAG + '-xgroup',
    });
    (refused(r) ? pass : fail)('Treasurer of Group A recording in Group B', describe(r));
  }

  // 3. An officer from Branch A touching Branch B
  {
    const r = await officer.get(`/groups/${g3.id}`);
    (refused(r) ? pass : fail)('Kampala officer opening an Mbarara group', describe(r));
  }

  // 4. IT Admin verifying or approving
  {
    const v = await admin.post(`/payments/submissions/${sub1.id}/verify`, {}, { 'x-pamoja-otp': await admin.otp('verify') });
    const a = await admin.post(`/loans/${reviewLoan.id}/transition`, { action: 'approve' }, { 'x-pamoja-otp': await admin.otp('approve') });
    (v.status === 403 && a.status === 403 ? pass : fail)('IT Admin verifying and approving', `verify: ${describe(v)} | approve: ${describe(a)}`);
  }

  // 5. Forged ids and amounts
  {
    const receipt = await verifier.post('/payments/receipts', {
      groupId: g1.id, amount: 50000, receiptDate: new Date().toISOString().slice(0, 10),
      sourceRef: TAG + '-RCPT', destination: 'Proof', channel: 'bank', evidenceRef: 'proof',
      idempotencyKey: TAG + '-rcpt',
    });
    const receiptId = receipt.body?.receipt?.id;
    cleanup.push(['DELETE FROM allocations WHERE receipt_id=$1', [receiptId]]);
    cleanup.push(['DELETE FROM receipts WHERE id=$1', [receiptId]]);
    await verifier.post('/payments/allocations', { receiptId, amount: 40000, category: 'savings', idempotencyKey: TAG + '-alloc' });
    const over = await verifier.post('/payments/allocations', { receiptId, amount: 20000, category: 'savings', idempotencyKey: TAG + '-alloc2' });
    const negative = await treasurer.post('/payments/entries', {
      groupId: g1.id, memberPersonId: members[0].person_id, category: 'savings', amount: -100,
      collectionDate: new Date().toISOString().slice(0, 10), idempotencyKey: TAG + '-neg',
    });
    const forgedGroup = await treasurer.post('/payments/entries', {
      groupId: '99999999-9999-4999-8999-999999999999', memberPersonId: members[0].person_id, category: 'savings', amount: 1000,
      collectionDate: new Date().toISOString().slice(0, 10), idempotencyKey: TAG + '-forge',
    });
    const ok = over.status === 409 && negative.status === 400 && [403, 404].includes(forgedGroup.status);
    (ok ? pass : fail)('Forged ids and amounts', `over-allocate: ${describe(over)} | negative amount: ${describe(negative)} | forged group: ${describe(forgedGroup)}`);
  }

  // 6. Replayed requests: same key and payload -> same status and identical
  //    body; same key with a different payload -> rejected.
  {
    const key = TAG + '-idem';
    const payload = {
      groupId: g1.id, memberPersonId: members[0].person_id, category: 'savings', amount: 20000,
      collectionDate: new Date().toISOString().slice(0, 10), idempotencyKey: key,
    };
    const first = await treasurer.post('/payments/entries', payload);
    const entryId = first.body?.entry?.id;
    cleanup.push(['DELETE FROM payment_entries WHERE id=$1', [entryId]]);
    cleanup.push(['DELETE FROM idempotency_keys WHERE key=$1', [key]]);
    const replay = await treasurer.post('/payments/entries', payload);
    const changed = await treasurer.post('/payments/entries', { ...payload, amount: 21000 });
    const identical = replay.text === first.text;
    const ok = first.status === 201 && replay.status === first.status && identical && changed.status === 409;
    (ok ? pass : fail)('Replayed request',
      `first: ${first.status}, replay: ${replay.status}, identical body: ${identical}, sameId: ${replay.body?.entry?.id === entryId}, changed payload: ${describe(changed)}`);
  }

  // 7. Treasurer verifying their own submission
  {
    const granted = (await owner.query(
      `INSERT INTO role_assignments (user_id, role, scope_type, scope_id, effective_from, status, granted_by, approved_by, is_demo)
       VALUES ($1,'verifier','Org','org',current_date,'active',$1,$1,true) RETURNING id`,
      [users['treasurer@yopsa.demo']],
    )).rows[0];
    // Clean up exactly the row this run created, never a broader filter.
    cleanup.push(['DELETE FROM role_assignments WHERE id=$1', [granted.id]]);
    const r = await treasurer.post(`/payments/submissions/${sub1.id}/verify`, {}, { 'x-pamoja-otp': await treasurer.otp('verify') });
    (r.status === 403 ? pass : fail)('Treasurer verifying their own submission', describe(r));
  }

  // 8. Approver approving their own application
  {
    const loanId = (await owner.query(
      `INSERT INTO loan_applications (group_id, product, amount, purpose, term_months, repayment_source, status, created_by, approved_by, approved_at, accepted_at, authorised_person_id)
       VALUES ($1,'group_loan',300000,'Negative test self approval','6','Sales','OperationsReview',$2,NULL,NULL,NULL,$3) RETURNING id`,
      [g1.id, users['approver@yopsa.demo'], members[0].person_id],
    )).rows[0].id;
    cleanup.push(['DELETE FROM approval_steps WHERE loan_id=$1', [loanId]]);
    cleanup.push(['DELETE FROM loan_applications WHERE id=$1', [loanId]]);
    await owner.query("INSERT INTO approval_steps (loan_id, step, actor_user_id, decision) VALUES ($1,'operations_review',$2,'reviewed')", [loanId, users['verifier@yopsa.demo']]);
    const r = await approver.post(`/loans/${loanId}/transition`, { action: 'approve' }, { 'x-pamoja-otp': await approver.otp('approve') });
    (r.status === 403 ? pass : fail)('Approver approving their own application', describe(r));
  }

  // 9. Disbursing without Operations review
  {
    const loanId = (await owner.query(
      `INSERT INTO loan_applications (group_id, product, amount, purpose, term_months, repayment_source, status, created_by, approved_by, approved_at, accepted_at, authorised_person_id)
       VALUES ($1,'group_loan',250000,'Negative test no operations review','6','Sales','GroupAccepted',$2,$3,now(),now(),$4) RETURNING id`,
      [g1.id, users['officer@yopsa.demo'], users['approver@yopsa.demo'], members[0].person_id],
    )).rows[0].id;
    cleanup.push(['DELETE FROM disbursements WHERE loan_id=$1', [loanId]]);
    cleanup.push(['DELETE FROM approval_steps WHERE loan_id=$1', [loanId]]);
    cleanup.push(['DELETE FROM loan_applications WHERE id=$1', [loanId]]);
    const r = await verifier.post(`/loans/${loanId}/disburse`, {
      amount: 250000, disbursedOn: new Date().toISOString().slice(0, 10), idempotencyKey: TAG + '-disb',
    }, { 'x-pamoja-otp': await verifier.otp('disburse') });
    (r.status === 403 ? pass : fail)('Disbursing without Operations review', describe(r));
  }

  // 10. app_system() cannot be turned on by an ordinary HTTP request: the
  //     parameter, a header and a SQL-looking body value all leave the
  //     row-level policy in force, and a fresh application-role session has
  //     the switch off.
  {
    const day = new Date().toISOString().slice(0, 10);
    const plain = await member.get(`/groups/${g2.id}`);
    const viaQuery = await member.get(`/groups/${g2.id}?app.system=on&system=on`);
    const viaHeader = await member.get(`/groups/${g2.id}`, { 'x-pamoja-system': 'on', 'x-pamoja-app-system': 'on' });
    const inj = await treasurer.post('/payments/entries', {
      groupId: g1.id, memberPersonId: members[0].person_id, category: 'savings', amount: 1000,
      collectionDate: day, idempotencyKey: TAG + '-sysinj',
      note: "x'); SELECT set_config('app.system','on',true); --",
    });
    cleanup.push(['DELETE FROM payment_entries WHERE id=$1', [inj.body?.entry?.id]]);
    cleanup.push(['DELETE FROM idempotency_keys WHERE key=$1', [TAG + '-sysinj']]);
    const after = await member.get(`/groups/${g2.id}`);
    const appSystem = (await appDb.query('SELECT app_system() AS on')).rows[0].on;
    const ok = refused(plain) && refused(viaQuery) && refused(viaHeader) && refused(after) && appSystem === false;
    (ok ? pass : fail)('app_system cannot be enabled over HTTP',
      `plain: ${describe(plain)} | query: ${describe(viaQuery)} | header: ${describe(viaHeader)} | after payload: ${describe(after)} | fresh app session app_system(): ${appSystem}`);
  }

  // 11. There is no HTTP route that hard-deletes a protected record.
  {
    const [person] = await q('SELECT id FROM persons LIMIT 1');
    const [ra] = await q('SELECT id FROM role_assignments LIMIT 1');
    const dGroup = await admin.del(`/groups/${g1.id}`);
    const dPerson = await admin.del(`/persons/${person.id}`);
    const dRole = await admin.del(`/admin/role-assignments/${ra.id}`);
    const dUser = await admin.del(`/admin/users/${users['member@yopsa.demo']}`);
    const stillThere = (await q('SELECT count(*)::int AS n FROM groups WHERE id=$1', [g1.id]))[0].n;
    const ok = [dGroup, dPerson, dRole, dUser].every((r) => r.status === 404) && stillThere === 1;
    (ok ? pass : fail)('No hard-delete route for protected records',
      `group: ${describe(dGroup)} | person: ${describe(dPerson)} | role: ${describe(dRole)} | user: ${describe(dUser)} | group still present: ${stillThere}`);
  }

  // 12. Registration review: the creator cannot review their own group, a
  //     branch officer cannot review another branch, and activation needs an
  //     independent approval first.
  {
    const [kla] = await q("SELECT id FROM org_branches WHERE code='KLA'");
    const mk = async (suffix) => (await owner.query(
      `INSERT INTO groups (name, type, branch_id, status, created_by, is_demo, submitted_at)
       VALUES ($1,'VSLA',$2,'Submitted',$3,true,now()) RETURNING id`,
      [`${TAG} registration ${suffix}`, kla.id, users['officer@yopsa.demo']],
    )).rows[0].id;
    const own = await mk('own');
    const early = await mk('early');
    cleanup.push(['DELETE FROM groups WHERE id = ANY($1::uuid[])', [[own, early]]]);
    const creatorReview = await officer.post(`/groups/${own}/review`, { decision: 'approve' });
    const otherBranchReview = await officer2.post(`/groups/${own}/review`, { decision: 'approve' });
    const activateBeforeApproval = await manager.post(`/groups/${early}/activate`, {});
    const review = await manager.post(`/groups/${own}/review`, { decision: 'approve' });
    const activate = await manager.post(`/groups/${own}/activate`, {});
    const ok = refused(creatorReview) && refused(otherBranchReview)
      && activateBeforeApproval.status === 400 && review.status === 200 && activate.status === 200;
    (ok ? pass : fail)('Registration review separation',
      `creator review: ${describe(creatorReview)} | other branch: ${describe(otherBranchReview)} | activate before approval: ${describe(activateBeforeApproval)} | independent review: ${describe(review)} | activate: ${describe(activate)}`);
  }

  // 13. app.seed and app.system cannot be switched on by an HTTP request. Both
  //     gate the privileged paths (is_demo, the system/RLS bypass) and are only
  //     ever set transaction-locally by the seed, the scheduler and the demo
  //     purge. A request that tries query parameters, headers and SQL-looking
  //     body values leaves an application-role session with neither setting,
  //     and the seed-only is_demo flip is still refused there.
  {
    const day = new Date().toISOString().slice(0, 10);
    const plain = await member.get(`/groups/${g2.id}`);
    const viaQuery = await member.get(`/groups/${g2.id}?app.seed=on&app.system=on&seed=on&system=on`);
    const viaHeader = await member.get(`/groups/${g2.id}`, {
      'x-pamoja-app-seed': 'on', 'x-pamoja-app-system': 'on', 'x-pamoja-seed': 'on', 'x-pamoja-system': 'on',
    });
    const viaBody = await treasurer.post('/payments/entries', {
      groupId: g1.id, memberPersonId: members[0].person_id, category: 'savings', amount: 1000,
      collectionDate: day, idempotencyKey: TAG + '-seedinj',
      app: { seed: 'on', system: 'on' },
      set_config: { 'app.seed': 'on', 'app.system': 'on' },
      note: "x'); SELECT set_config('app.seed','on',true); SELECT set_config('app.system','on',true); --",
    }, { 'x-pamoja-app-seed': 'on', 'x-pamoja-app-system': 'on' });
    cleanup.push(['DELETE FROM payment_entries WHERE id=$1', [viaBody.body?.entry?.id]]);
    cleanup.push(['DELETE FROM idempotency_keys WHERE key=$1', [TAG + '-seedinj']]);
    const settings = (await appDb.query(
      "SELECT current_setting('app.seed', true) AS seed, current_setting('app.system', true) AS system",
    )).rows[0];
    // The same role as the API, with an ordinary member's identity: the
    // seed/system-only is_demo change must still be refused.
    await appDb.query("SELECT set_config('app.user_id', $1, false)", [users['member@yopsa.demo']]);
    let seedPath = 'allowed';
    try { await appDb.query('UPDATE groups SET is_demo=false WHERE id=$1', [g1.id]); }
    catch (err) { seedPath = err.code || err.message; }
    await appDb.query('RESET app.user_id');
    const ok = refused(plain) && refused(viaQuery) && refused(viaHeader) && viaBody.status === 201
      && !settings.seed && !settings.system && seedPath === '42501';
    (ok ? pass : fail)('app.seed and app.system cannot be enabled over HTTP',
      `query: ${describe(viaQuery)} | header: ${describe(viaHeader)} | body: ${describe(viaBody)} | app session app.seed='${settings.seed}' app.system='${settings.system}' | is_demo flip: ${seedPath}`);
  }
}

async function run() {
  let safetyAfter = null;
  try {
    await main();
  } catch (err) {
    fail('test harness', String(err.message));
  } finally {
    // Cleanup entries are queued child-first, so delete in the same order. A
    // missing id is skipped: a delete must never fall back to a broad filter.
    for (const [sql, params] of cleanup) {
      if (params.some((v) => v === null || v === undefined || v === '')) {
        console.error('cleanup skipped, missing id:', sql);
        continue;
      }
      try { await owner.query(sql, params); } catch (err) { console.error('cleanup failed:', sql, err.message); }
    }
    try { safetyAfter = (await owner.query(SAFETY_QUERY)).rows[0]; } catch { /* reported below */ }
    await owner.end();
    await appDb.end();
  }
  const width = Math.max(...results.map((r) => r.name.length), 10);
  console.log('\nNEGATIVE TESTS');
  console.log('='.repeat(100));
  for (const r of results) console.log(`${r.pass ? 'PASS' : 'FAIL'}  ${r.name.padEnd(width)}  ${r.detail}`);
  const failed = results.filter((r) => !r.pass).length;
  console.log('='.repeat(100));
  console.log(`${results.length - failed} passed, ${failed} failed`);

  console.log('\nTEST DATA SAFETY (demo rows before -> after; audit log may only grow)');
  console.log('='.repeat(100));
  let unsafe = false;
  if (!safetyBefore || !safetyAfter) {
    console.log('FAIL  could not read the before/after snapshot');
    unsafe = true;
  } else {
    for (const key of Object.keys(safetyBefore)) {
      const b = Number(safetyBefore[key]);
      const a = Number(safetyAfter[key]);
      const ok = key === 'audit_rows' ? a >= b : a === b;
      if (!ok) unsafe = true;
      console.log(`${ok ? 'PASS' : 'FAIL'}  ${key.padEnd(width)}  ${b} -> ${a}`);
    }
  }
  console.log('='.repeat(100));
  // CI floor: a test may be added, but silently removing one must fail the run.
  const MIN_TESTS = Number(process.env.MIN_NEGATIVE_TESTS || 12);
  if (results.length < MIN_TESTS) {
    console.error(`expected at least ${MIN_TESTS} negative tests, got ${results.length}`);
  }
  process.exit(failed || unsafe || results.length < MIN_TESTS ? 1 : 0);
}

run();
