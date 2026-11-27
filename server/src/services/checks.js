import crypto from 'node:crypto';
import { q, tx } from '../db.js';
import { hashPassword } from '../auth.js';
import { verifyAuditChain } from '../audit.js';
import { loadGroup } from '../authz.js';
import { addDays, today } from '../util.js';
import { attendanceSummary } from './attendance.js';
import { recordPaymentEntry, verifySubmission, allocateReceipt, recordReceipt, postToLedger } from './payments.js';
import { recordDisbursement } from './loans.js';
import { handOverCash, receiveCash, confirmDeposit } from './cash.js';
import { createMemberLoan } from './memberLoans.js';
import { decodeBase64 } from '../files.js';
import { LedgerAdapter } from '../ledger.js';

const TAG = 'SELFTEST-' + Date.now();
const pass = (id, name, detail) => ({ id, name, status: 'PASS', detail });
const fail = (id, name, detail) => ({ id, name, status: 'FAIL', detail });

function callerFor(user, role, scopeType, scopeId) {
  return { user, sessionId: 'selftest', stepUpAt: new Date(), roleUsed: role, assignments: [{ role, scope_type: scopeType, scope_id: scopeId, effective_from: '2000-01-01', effective_to: null }] };
}

async function expectReject(fn, label) {
  try { await fn(); return { rejected: false, detail: `${label} was NOT rejected.` }; }
  catch (err) { return { rejected: true, detail: `${label} rejected: ${err.code || err.message}` }; }
}

export async function runSystemChecks(actor) {
  const results = [];
  const ids = { users: [], persons: [], memberships: [], meetings: [], submissions: [], receipts: [], entries: [], loans: [], branches: [], groups: [] };
  const cleanupErrors = [];
  let client;
  try {
    const pwd = hashPassword(crypto.randomBytes(18).toString('hex'));
    const mkUser = async (label) => {
      const r = await q('INSERT INTO users (email, password_hash, full_name) VALUES ($1,$2,$3) RETURNING id, email, full_name',
        [`${TAG.toLowerCase()}-${label}@selftest.invalid`, pwd, `Self-test ${label}`]);
      ids.users.push(r.rows[0].id);
      return r.rows[0];
    };
    const treasurer = await mkUser('treasurer');
    const verifier = await mkUser('verifier');
    const officer = await mkUser('officer');
    const approver = await mkUser('approver');
    const itadmin = await mkUser('itadmin');
    const manager = await mkUser('manager');
    const dual = await mkUser('dual');
    const other = await mkUser('other');

    const branchA = (await q('INSERT INTO org_branches (name, district, code) VALUES ($1,$2,$3) RETURNING *', [`Self-test Branch A ${TAG}`, 'Test', TAG + '-A'])).rows[0];
    const branchB = (await q('INSERT INTO org_branches (name, district, code) VALUES ($1,$2,$3) RETURNING *', [`Self-test Branch B ${TAG}`, 'Test', TAG + '-B'])).rows[0];
    ids.branches.push(branchA.id, branchB.id);
    const hiddenGroup = (await q(
      `INSERT INTO groups (name, type, branch_id, status, created_by) VALUES ($1,'VSLA',$2,'Draft',$3) RETURNING *`,
      [`Self-test Hidden Group ${TAG}`, branchA.id, officer.id],
    )).rows[0];
    ids.groups.push(hiddenGroup.id);
    const group = (await q(
      `INSERT INTO groups (name, type, branch_id, status, created_by, registration_evidence_status, review_decision)
       VALUES ($1,'VSLA',$2,'Active',$3,'Provided','approve') RETURNING *`,
      [`Self-test Group ${TAG}`, branchA.id, officer.id],
    )).rows[0];
    ids.groups.push(group.id);
    const actorId = actor?.user?.id || officer.id;
    const mkPerson = async (name, join) => {
      const p = (await q('INSERT INTO persons (full_name, phone, consent_recorded, consent_at, created_by) VALUES ($1,$2,true,now(),$3) RETURNING *', [name, '+2567' + Math.floor(10000000 + Math.random() * 89999999), actorId])).rows[0];
      ids.persons.push(p.id);
      const m = (await q("INSERT INTO memberships (person_id, group_id, join_date, status, created_by) VALUES ($1,$2,$3,'active',$4) RETURNING *", [p.id, group.id, join, officer.id])).rows[0];
      ids.memberships.push(m.id);
      return p;
    };
    const personA = await mkPerson('Self-test Member A', addDays(today(), -120));
    const personB = await mkPerson('Self-test Member B', addDays(today(), -55));
    const personC = await mkPerson('Self-test Member C', today());

    const meetingIds = [];
    for (let i = 0; i < 8; i += 1) {
      const m = (await q(
        `INSERT INTO meetings (group_id, title, scheduled_for, status, created_by, closed_at) VALUES ($1,$2,$3,'closed',$4,now()) RETURNING id`,
        [group.id, `Self-test meeting ${i + 1}`, addDays(today(), -110 + i * 10) + 'T10:00:00Z', officer.id],
      )).rows[0];
      meetingIds.push(m.id); ids.meetings.push(m.id);
    }
    for (let i = 0; i < 6; i += 1) {
      await q("INSERT INTO attendance_records (meeting_id, person_id, status, method, recorded_by) VALUES ($1,$2,'present','self',$3)", [meetingIds[i], personA.id, officer.id]);
    }
    const att = await attendanceSummary(group.id);
    const a = att.people.find((p) => p.personId === personA.id);
    const b = att.people.find((p) => p.personId === personB.id);
    const c = att.people.find((p) => p.personId === personC.id);
    results.push(a.percent === 75 ? pass('attendance_formula', 'Attendance 6 of 8 = 75%', `Member A: ${a.display}`) : fail('attendance_formula', 'Attendance 6 of 8 = 75%', `Got ${a.display}`));
    results.push(c.display === 'Not applicable' ? pass('attendance_zero', 'Zero expected = Not applicable', `Member C: ${c.display}`) : fail('attendance_zero', 'Zero expected = Not applicable', `Got ${c.display}`));
    const avg = Math.round(((a.percent + b.percent) / 2) * 10) / 10;
    results.push(att.group.percent === 60 && avg !== att.group.percent
      ? pass('attendance_group', 'Group % uses opportunities, not average of people', `Group ${att.group.percent}% vs average ${avg}%`)
      : fail('attendance_group', 'Group % uses opportunities, not average of people', `Group ${att.group.percent}% vs average ${avg}%`));

    // Payment shortage case
    const sub = (await q(
      `INSERT INTO group_submissions (group_id, period_start, period_end, totals, total_amount, submitted_by)
       VALUES ($1,$2,$3,$4,200000,$5) RETURNING *`,
      [group.id, addDays(today(), -40), today(), JSON.stringify({ savings: 200000 }), treasurer.id],
    )).rows[0];
    ids.submissions.push(sub.id);
    const receipt = (await q(
      `INSERT INTO receipts (group_id, submission_id, amount, receipt_date, source_ref, destination, channel, evidence_ref, recorded_by)
       VALUES ($1,$2,180000,$3,$4,'Bank','bank','selftest',$5) RETURNING *`,
      [group.id, sub.id, today(), TAG + '-R1', verifier.id],
    )).rows[0];
    ids.receipts.push(receipt.id);
    const vcaller = callerFor(verifier, 'verifier', 'Group', group.id);
    const vr = await verifySubmission(vcaller, { submissionId: sub.id });
    results.push(vr.status === 'difference' && vr.difference === -20000
      ? pass('payment_shortage', '200,000 submitted vs 180,000 received shows 20,000 difference and blocks', vr.message)
      : fail('payment_shortage', 'Payment shortage blocks verification', JSON.stringify(vr)));

    // Allocation cap
    const r2 = (await q(
      `INSERT INTO receipts (group_id, amount, receipt_date, source_ref, destination, channel, evidence_ref, recorded_by)
       VALUES ($1,100000,$2,$3,'Bank','bank','selftest',$4) RETURNING *`, [group.id, today(), TAG + '-R2', verifier.id],
    )).rows[0];
    ids.receipts.push(r2.id);
    await allocateReceipt(vcaller, { receiptId: r2.id, amount: 80000, category: 'savings', idempotencyKey: TAG + '-alloc-1' });
    const over = await expectReject(() => allocateReceipt(vcaller, { receiptId: r2.id, amount: 30000, category: 'savings', idempotencyKey: TAG + '-alloc-2' }), 'Allocation above the receipt');
    results.push(over.rejected ? pass('allocation_cap', 'Allocation can never exceed the receipt', over.detail) : fail('allocation_cap', 'Allocation can never exceed the receipt', over.detail));

    // Self verification
    const selfSub = (await q(
      `INSERT INTO group_submissions (group_id, period_start, period_end, totals, total_amount, submitted_by)
       VALUES ($1,$2,$3,'{}',50000,$4) RETURNING *`, [group.id, addDays(today(), -5), today(), verifier.id],
    )).rows[0];
    ids.submissions.push(selfSub.id);
    const self = await expectReject(() => verifySubmission(vcaller, { submissionId: selfSub.id }), 'Treasurer verifying own submission');
    results.push(self.rejected ? pass('self_verification', 'Nobody verifies their own submission', self.detail) : fail('self_verification', 'Self-verification blocked', self.detail));

    // Verifier recorded an entry
    const recSub = (await q(
      `INSERT INTO group_submissions (group_id, period_start, period_end, totals, total_amount, submitted_by)
       VALUES ($1,$2,$3,'{}',30000,$4) RETURNING *`, [group.id, addDays(today(), -5), today(), treasurer.id],
    )).rows[0];
    ids.submissions.push(recSub.id);
    const entry = (await q(
      `INSERT INTO payment_entries (group_id, member_person_id, category, amount, collection_date, status, submission_id, created_by)
       VALUES ($1,$2,'savings',30000,$3,'draft',$4,$5) RETURNING *`, [group.id, personA.id, today(), recSub.id, verifier.id],
    )).rows[0];
    ids.entries.push(entry.id);
    const recorder = await expectReject(() => verifySubmission(vcaller, { submissionId: recSub.id }), 'Verifier who recorded an entry');
    results.push(recorder.rejected ? pass('verifier_recorded', 'A verifier who recorded an entry cannot verify', recorder.detail) : fail('verifier_recorded', 'Verifier who recorded an entry blocked', recorder.detail));

    // Idempotency
    const tcaller = callerFor(treasurer, 'treasurer', 'Group', group.id);
    const key = TAG + '-entry-1';
    const e1 = await recordPaymentEntry(tcaller, { groupId: group.id, memberPersonId: personA.id, category: 'savings', amount: 20000, collectionDate: today(), idempotencyKey: key });
    const e2 = await recordPaymentEntry(tcaller, { groupId: group.id, memberPersonId: personA.id, category: 'savings', amount: 20000, collectionDate: today(), idempotencyKey: key });
    if (e1.entry?.id) ids.entries.push(e1.entry.id);
    const same = e1.entry?.id && e1.entry.id === e2.entry?.id && JSON.stringify(e1) === JSON.stringify(e2);
    const diff = await expectReject(() => recordPaymentEntry(tcaller, { groupId: group.id, memberPersonId: personA.id, category: 'savings', amount: 21000, collectionDate: today(), idempotencyKey: key }), 'Same key with a different payload');
    results.push(same && diff.rejected ? pass('idempotency', 'Idempotency: repeat returns the identical response, changed payload rejected', 'One entry, byte-identical replay, mismatch rejected') : fail('idempotency', 'Idempotency', `same=${same} diffRejected=${diff.rejected}`));

    // Cross-group / cross-branch
    const branchBCaller = callerFor(officer, 'field_officer', 'Branch', branchB.id);
    const crossBranch = await expectReject(() => loadGroup(branchBCaller, group.id), 'Branch B officer reading a Branch A group');
    results.push(crossBranch.rejected ? pass('cross_branch', 'Branch A officer cannot touch a Branch B group', crossBranch.detail) : fail('cross_branch', 'Cross-branch access denied', crossBranch.detail));
    const memberCaller = callerFor(other, 'member', 'Group', group.id);
    const crossGroup = await expectReject(() => loadGroup(memberCaller, hiddenGroup.id), 'Unrelated member reading a group');
    results.push(crossGroup.rejected ? pass('cross_group', 'A member cannot read an unrelated group', crossGroup.detail) : fail('cross_group', 'Cross-group access denied', crossGroup.detail));

    const loan = (await q(
      `INSERT INTO loan_applications (group_id, product, amount, purpose, term_months, repayment_source, status, created_by, approved_by, approved_at, accepted_at, authorised_person_id)
       VALUES ($1,'group_loan',400000,'Self-test','6','Sales','GroupAccepted',$2,$3,now(),now(),$4) RETURNING *`,
      [group.id, officer.id, approver.id, personA.id],
    )).rows[0];
    ids.loans.push(loan.id);

    // IT admin cannot verify or approve
    const itc = callerFor(itadmin, 'it_admin', 'Org', 'org');
    const itVerify = await expectReject(() => verifySubmission(itc, { submissionId: sub.id }), 'IT Admin verifying');
    const itApprove = await expectReject(async () => {
      const { transitionLoan } = await import('./loans.js');
      return transitionLoan(itc, loan.id, { action: 'approve' });
    }, 'IT Admin approving');
    results.push(itVerify.rejected && itApprove.rejected ? pass('it_admin_limits', 'IT Admin cannot verify or approve', `${itVerify.detail}; ${itApprove.detail}`) : fail('it_admin_limits', 'IT Admin cannot verify or approve', `${itVerify.detail}; ${itApprove.detail}`));

    // Disbursement requires Operations review
    const disb = await expectReject(() => recordDisbursement(vcaller, loan.id, { amount: 400000, disbursedOn: today(), idempotencyKey: TAG + '-disb' }), 'Disbursement without Operations review');
    results.push(disb.rejected ? pass('disbursement_ops', 'Disbursement requires Operations review', disb.detail) : fail('disbursement_ops', 'Disbursement requires Operations review', disb.detail));

    // Posting failure -> posting pending + exception
    const postSub = (await q(
      `INSERT INTO group_submissions (group_id, period_start, period_end, totals, total_amount, submitted_by, status, verified_by, verified_at)
       VALUES ($1,$2,$3,'{}',70000,$4,'verified',$5,now()) RETURNING *`, [group.id, addDays(today(), -10), today(), treasurer.id, verifier.id],
    )).rows[0];
    ids.submissions.push(postSub.id);
    await q(`INSERT INTO mock_ledger_accounts (group_id, principal, schedule) VALUES ($1,0,$2)
             ON CONFLICT (group_id) DO UPDATE SET schedule=$2`, [group.id, JSON.stringify([{ due_date: today(), amount: 1000, failNextPosting: true }])]);
    const posted = await postToLedger(vcaller, { submissionId: postSub.id });
    const exc = await q("SELECT 1 FROM finance_exceptions WHERE entity_id=$1 AND status='open'", [postSub.id]);
    results.push(posted.status === 'posting_pending' && exc.rowCount
      ? pass('posting_failure', 'Posting failure gives Posting pending and a FinanceException', posted.message)
      : fail('posting_failure', 'Posting failure handled', JSON.stringify(posted)));

    // Pending entries never change a loan balance
    await q(`INSERT INTO mock_ledger_accounts (group_id, principal, schedule) VALUES ($1,50000,'[]')
             ON CONFLICT (group_id) DO UPDATE SET principal=50000`, [group.id]);
    const beforeBal = await LedgerAdapter.getBalances(group.id);
    await recordPaymentEntry(tcaller, { groupId: group.id, memberPersonId: personA.id, category: 'savings', amount: 15000, collectionDate: today(), idempotencyKey: TAG + '-pending' });
    const afterBal = await LedgerAdapter.getBalances(group.id);
    results.push(beforeBal.principal === 50000 && afterBal.principal === 50000
      ? pass('pending_balance', 'Pending entries do not change a loan balance', `principal stayed ${afterBal.principal} (expected 50000)`)
      : fail('pending_balance', 'Pending entries do not change a loan balance', `${beforeBal.principal} -> ${afterBal.principal} (expected 50000 both)`));

    // Treasurer of Group A cannot record in Group B
    const crossTreasurer = await expectReject(() => recordPaymentEntry(tcaller, { groupId: hiddenGroup.id, memberPersonId: personA.id, category: 'savings', amount: 1000, collectionDate: today(), idempotencyKey: TAG + '-cross-treasurer' }), 'Treasurer of Group A recording in Group B');
    results.push(crossTreasurer.rejected ? pass('cross_group_treasurer', 'A treasurer cannot touch another group', crossTreasurer.detail) : fail('cross_group_treasurer', 'Treasurer cross-group blocked', crossTreasurer.detail));

    // Secretary of Group A cannot open a Group B meeting
    const otherMeeting = (await q(`INSERT INTO meetings (group_id, title, scheduled_for, status, created_by) VALUES ($1,'Hidden meeting',now(),'scheduled',$2) RETURNING id`, [hiddenGroup.id, officer.id])).rows[0];
    const secCaller = callerFor(treasurer, 'secretary', 'Group', group.id);
    const crossSecretary = await expectReject(() => loadGroup(secCaller, hiddenGroup.id), 'Secretary of Group A opening a Group B meeting');
    results.push(crossSecretary.rejected ? pass('cross_group_secretary', 'Secretary of Group A cannot open Group B', crossSecretary.detail) : fail('cross_group_secretary', 'Secretary cross-group blocked', crossSecretary.detail));
    await q('DELETE FROM meetings WHERE id=$1', [otherMeeting.id]);

    // Disburser cannot be the approver
    const disbLoan = (await q(
      `INSERT INTO loan_applications (group_id, product, amount, purpose, term_months, repayment_source, status, created_by, approved_by, approved_at, accepted_at, authorised_person_id)
       VALUES ($1,'group_loan',300000,'Self-test disburser','6','Sales','GroupAccepted',$2,$3,now(),now(),$4) RETURNING *`,
      [group.id, officer.id, verifier.id, personA.id],
    )).rows[0];
    await q(`INSERT INTO approval_steps (loan_id, step, actor_user_id, decision) VALUES ($1,'operations_review',$2,'reviewed')`, [disbLoan.id, approver.id]);
    const sameDisburser = await expectReject(() => recordDisbursement(vcaller, disbLoan.id, { amount: 300000, disbursedOn: today(), idempotencyKey: TAG + '-disb-same' }), 'Approver also disbursing');
    results.push(sameDisburser.rejected ? pass('disburser_separation', 'The approver cannot also disburse', sameDisburser.detail) : fail('disburser_separation', 'Disburser separation', sameDisburser.detail));

    // Allocation cap enforced by the database trigger, not only by the service
    const rawAlloc = await expectReject(
      () => q('INSERT INTO allocations (receipt_id, amount, category, created_by) VALUES ($1,30000,$2,$3)', [r2.id, 'savings', verifier.id]),
      'Raw allocation above the receipt',
    );
    results.push(rawAlloc.rejected ? pass('allocation_cap_db', 'Database trigger rejects an allocation above the receipt', rawAlloc.detail) : fail('allocation_cap_db', 'Database trigger rejects an allocation above the receipt', rawAlloc.detail));

    // Locked payment rows are immutable at database level (update and delete)
    const dbGuard = await (async () => {
      let out = null;
      try {
        await tx(async (client) => {
          const locked = (await client.query(
            `INSERT INTO payment_entries (group_id, member_person_id, category, amount, collection_date, status, created_by)
             VALUES ($1,$2,'savings',1000,$3,'locked',$4) RETURNING id`,
            [group.id, personA.id, today(), officer.id],
          )).rows[0];
          let updateBlocked = false;
          let deleteBlocked = false;
          try {
            await client.query('SAVEPOINT s1');
            await client.query('UPDATE payment_entries SET amount=2000 WHERE id=$1', [locked.id]);
            await client.query('RELEASE SAVEPOINT s1');
          } catch (err) { updateBlocked = err.code === '23514'; await client.query('ROLLBACK TO SAVEPOINT s1'); }
          try {
            await client.query('SAVEPOINT s2');
            await client.query('DELETE FROM payment_entries WHERE id=$1', [locked.id]);
            await client.query('RELEASE SAVEPOINT s2');
          } catch (err) { deleteBlocked = err.code === '23514'; await client.query('ROLLBACK TO SAVEPOINT s2'); }
          const rollback = new Error('rollback self-test');
          rollback.selfTest = { updateBlocked, deleteBlocked };
          throw rollback;
        });
      } catch (err) { out = err.selfTest || null; }
      return out;
    })();
    results.push(dbGuard?.updateBlocked && dbGuard?.deleteBlocked
      ? pass('locked_entry_db', 'Database trigger blocks editing and deleting a locked entry', 'update and delete both rejected with 23514')
      : fail('locked_entry_db', 'Database trigger blocks editing and deleting a locked entry', JSON.stringify(dbGuard)));

    // Audit rows cannot be changed or removed by the application role
    const auditUpd = await expectReject(() => q("UPDATE audit_logs SET action='tampered' WHERE seq=(SELECT max(seq) FROM audit_logs)"), 'Updating an audit row');
    const auditDel = await expectReject(() => q('DELETE FROM audit_logs WHERE seq=(SELECT max(seq) FROM audit_logs)'), 'Deleting an audit row');
    results.push(auditUpd.rejected && auditDel.rejected
      ? pass('audit_append_only_db', 'AuditLog rows cannot be updated or deleted', `${auditUpd.detail}; ${auditDel.detail}`)
      : fail('audit_append_only_db', 'AuditLog rows cannot be updated or deleted', `${auditUpd.detail}; ${auditDel.detail}`));

    // Cash: handover, receipt and deposit are three steps by three people
    const cashReceipt = (await q(
      `INSERT INTO receipts (group_id, amount, receipt_date, source_ref, destination, channel, evidence_ref, recorded_by)
       VALUES ($1,60000,$2,$3,'Branch cash box','cash','selftest-cash',$4) RETURNING *`,
      [group.id, today(), TAG + '-CASH1', verifier.id],
    )).rows[0];
    ids.receipts.push(cashReceipt.id);
    const dualCaller = callerFor(dual, 'verifier', 'Org', 'org');
    dualCaller.assignments.push({ role: 'treasurer', scope_type: 'Group', scope_id: group.id, effective_from: '2000-01-01', effective_to: null });
    const hand = await handOverCash(dualCaller, cashReceipt.id, {});
    const sameReceive = await expectReject(() => receiveCash(dualCaller, cashReceipt.id), 'Handing over and receiving the same cash');
    const recv = await receiveCash(vcaller, cashReceipt.id);
    const sameDeposit = await expectReject(() => confirmDeposit(vcaller, cashReceipt.id, { bankRef: 'SELFTEST-BANK' }), 'Receiving and depositing the same cash');
    const mgrCaller = callerFor(manager, 'management', 'Org', 'org');
    const dep = await confirmDeposit(mgrCaller, cashReceipt.id, { bankRef: 'SELFTEST-BANK', evidenceRef: 'self-test slip' });
    results.push(hand.reconciliation && sameReceive.rejected && recv.reconciliation && sameDeposit.rejected && dep.reconciliation?.status === 'reconciled'
      ? pass('cash_three_steps', 'Cash: handover, branch receipt and deposit are three steps by three people', `final ${dep.reconciliation.status}; same-person steps rejected`)
      : fail('cash_three_steps', 'Cash: handover, receipt and deposit by three people', `hand=${!!hand.reconciliation} sameReceive=${sameReceive.rejected} recv=${!!recv.reconciliation} sameDeposit=${sameDeposit.rejected} final=${dep.reconciliation?.status}`));

    // Internal member loans never enter Yopsa loan totals
    const loansBefore = Number((await q(
      'SELECT COALESCE(SUM(d.amount),0) AS v FROM disbursements d JOIN loan_applications l ON l.id=d.loan_id WHERE l.group_id=$1', [group.id],
    )).rows[0].v);
    const ml = await createMemberLoan(tcaller, {
      groupId: group.id, lenderPersonId: personA.id, borrowerPersonId: personB.id,
      amount: 50000, purpose: 'School fees between members', agreedOn: today(),
    });
    const loansAfter = Number((await q(
      'SELECT COALESCE(SUM(d.amount),0) AS v FROM disbursements d JOIN loan_applications l ON l.id=d.loan_id WHERE l.group_id=$1', [group.id],
    )).rows[0].v);
    results.push(ml.memberLoan && loansBefore === loansAfter
      ? pass('member_loans_separate', 'Internal member loans are never counted in Yopsa loans', `Yopsa loans advanced stayed ${loansAfter}`)
      : fail('member_loans_separate', 'Internal member loans are never counted in Yopsa loans', `${loansBefore} -> ${loansAfter}`));

    // Upload content is checked, not only the declared type
    const badUpload = await expectReject(
      () => decodeBase64(Buffer.from('this is not a picture at all').toString('base64'), 'image/png'),
      'Upload whose content is not a PNG',
    );
    let goodUpload = false;
    try {
      decodeBase64(Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0]).toString('base64'), 'image/png');
      goodUpload = true;
    } catch { goodUpload = false; }
    results.push(badUpload.rejected && goodUpload
      ? pass('upload_content', 'Upload content must match its declared type', badUpload.detail)
      : fail('upload_content', 'Upload content must match its declared type', `badRejected=${badUpload.rejected} goodAccepted=${goodUpload}`));

    // Audit chain
    const chain = await verifyAuditChain({ query: q });
    results.push(chain.ok ? pass('audit_chain', 'Audit hash chain integrity', `${chain.entries} entries verified`) : fail('audit_chain', 'Audit hash chain integrity', `Break at seq ${chain.firstBreakAt}`));
  } catch (err) {
    results.push(fail('check_runner', 'Self-test runner', String(err.message)));
  } finally {
    // Clean up every test record. Audit entries stay (append-only) and contain ids only.
    try {
      for (const g of ids.groups) {
        await q('DELETE FROM ledger_postings WHERE submission_id IN (SELECT id FROM group_submissions WHERE group_id=$1)', [g]);
        await q('DELETE FROM finance_exceptions WHERE entity_id IN (SELECT id::text FROM group_submissions WHERE group_id=$1)', [g]);
        await q('DELETE FROM allocations WHERE receipt_id IN (SELECT id FROM receipts WHERE group_id=$1)', [g]);
        await q('DELETE FROM cash_reconciliations WHERE group_id=$1', [g]);
        await q('DELETE FROM member_loans WHERE group_id=$1', [g]);
        await q('DELETE FROM receipts WHERE group_id=$1', [g]);
        await q('DELETE FROM corrections WHERE entity_id IN (SELECT id FROM payment_entries WHERE group_id=$1)', [g]);
        await q('DELETE FROM payment_entries WHERE group_id=$1', [g]);
        await q('DELETE FROM group_submissions WHERE group_id=$1', [g]);
        await q('DELETE FROM attendance_records WHERE meeting_id IN (SELECT id FROM meetings WHERE group_id=$1)', [g]);
        await q('DELETE FROM minutes WHERE meeting_id IN (SELECT id FROM meetings WHERE group_id=$1)', [g]);
        await q('DELETE FROM resolutions WHERE meeting_id IN (SELECT id FROM meetings WHERE group_id=$1)', [g]);
        await q('DELETE FROM action_items WHERE group_id=$1', [g]);
        await q('DELETE FROM meeting_documents WHERE meeting_id IN (SELECT id FROM meetings WHERE group_id=$1)', [g]);
        await q('DELETE FROM meeting_amendments WHERE meeting_id IN (SELECT id FROM meetings WHERE group_id=$1)', [g]);
        await q('DELETE FROM meetings WHERE group_id=$1', [g]);
        await q('DELETE FROM approval_steps WHERE loan_id IN (SELECT id FROM loan_applications WHERE group_id=$1)', [g]);
        await q('DELETE FROM guarantors WHERE loan_id IN (SELECT id FROM loan_applications WHERE group_id=$1)', [g]);
        await q('DELETE FROM disbursements WHERE loan_id IN (SELECT id FROM loan_applications WHERE group_id=$1)', [g]);
        await q('DELETE FROM loan_applications WHERE group_id=$1', [g]);
        await q('DELETE FROM mock_ledger_accounts WHERE group_id=$1', [g]);
        await q('DELETE FROM tasks WHERE linked_id IN (SELECT id::text FROM group_submissions WHERE group_id=$1) OR linked_id IN (SELECT id::text FROM loan_applications WHERE group_id=$1)', [g]);
        await q('DELETE FROM memberships WHERE group_id=$1', [g]);
        await q('DELETE FROM leaderships WHERE group_id=$1', [g]);
        await q('DELETE FROM file_objects WHERE group_id=$1', [g]);
        await q('DELETE FROM learning_assignments WHERE group_id=$1', [g]);
        await q('DELETE FROM groups WHERE id=$1', [g]);
      }
      for (const id of ids.persons) await q('DELETE FROM persons WHERE id=$1', [id]);
      for (const id of ids.branches) await q('DELETE FROM org_branches WHERE id=$1', [id]);
      await q("DELETE FROM idempotency_keys WHERE key LIKE $1", [TAG + '%']);
      for (const id of ids.users) await q('DELETE FROM users WHERE id=$1', [id]);
    } catch (err) {
      cleanupErrors.push(String(err.message));
    }
  }
  if (cleanupErrors.length) results.push(fail('cleanup', 'Self-test data cleaned up', cleanupErrors.join('; ')));
  else results.push(pass('cleanup', 'Self-test data cleaned up', 'All self-test rows removed; audit entries retained.'));
  const passed = results.filter((r) => r.status === 'PASS').length;
  return { ranAt: new Date().toISOString(), ranBy: actor?.user?.id || null, results, passed, failed: results.length - passed, total: results.length, tag: TAG };
}
