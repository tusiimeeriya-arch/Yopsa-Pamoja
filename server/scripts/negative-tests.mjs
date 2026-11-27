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

const TAG = 'NEG-' + Date.now();
const results = [];
const cleanup = [];

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
    otp: async (purpose) => {
      const r = await call('POST', '/auth/otp/request', { purpose });
      if (!r.ok) throw new Error(`otp request failed: ${describe(r)}`);
      return r.body.devCode;
    },
  };
}

async function main() {
  await owner.connect();

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
  const admin = client(await login('admin@yopsa.demo'));

  // 1. A member reading another member's payments
  {
    const [memberPerson] = await q('SELECT id FROM persons WHERE linked_user_id=$1', [users['member@yopsa.demo']]);
    const own = await member.get(`/payments/entries?groupId=${g1.id}`);
    const others = (own.body?.entries || []).filter((e) => e.member_person_id !== memberPerson.id);
    const otherGroup = await member.get(`/payments/entries?groupId=${g2.id}`);
    const ok = own.status === 200 && others.length === 0 && refused(otherGroup);
    (ok ? pass : fail)('Member reading another member\'s payments',
      `own group returned ${own.body?.entries?.length ?? 0} rows, all the member's own: ${others.length === 0}; other group: ${describe(otherGroup)}`);
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

  // 6. Replayed requests
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
    const ok = first.status === 201 && replay.ok && replay.body?.replayed === true
      && replay.body?.entry?.id === entryId && changed.status === 409;
    (ok ? pass : fail)('Replayed request', `first: ${first.status}, replay: ${replay.status} replayed=${replay.body?.replayed} sameId=${replay.body?.entry?.id === entryId}, changed payload: ${describe(changed)}`);
  }

  // 7. Treasurer verifying their own submission
  {
    await owner.query(
      `INSERT INTO role_assignments (user_id, role, scope_type, scope_id, effective_from, status, granted_by, approved_by)
       VALUES ($1,'verifier','Org','org',current_date,'active',$1,$1)`,
      [users['treasurer@yopsa.demo']],
    );
    cleanup.push(["DELETE FROM role_assignments WHERE user_id=$1 AND role='verifier'", [users['treasurer@yopsa.demo']]]);
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

  // member2 is used only to prove a second member cannot read group 1 entries.
  {
    const r = await member2.get(`/payments/entries?groupId=${g1.id}`);
    (refused(r) ? pass : fail)('Member of another group reading Group A entries', describe(r));
  }
}

async function run() {
  try {
    await main();
  } catch (err) {
    fail('test harness', String(err.message));
  } finally {
    // Cleanup entries are queued child-first, so delete in the same order.
    for (const [sql, params] of cleanup) {
      try { await owner.query(sql, params); } catch (err) { console.error('cleanup failed:', sql, err.message); }
    }
    await owner.end();
  }
  const width = Math.max(...results.map((r) => r.name.length), 10);
  console.log('\nNEGATIVE TESTS');
  console.log('='.repeat(100));
  for (const r of results) console.log(`${r.pass ? 'PASS' : 'FAIL'}  ${r.name.padEnd(width)}  ${r.detail}`);
  const failed = results.filter((r) => !r.pass).length;
  console.log('='.repeat(100));
  console.log(`${results.length - failed} passed, ${failed} failed`);
  process.exit(failed ? 1 : 0);
}

run();
