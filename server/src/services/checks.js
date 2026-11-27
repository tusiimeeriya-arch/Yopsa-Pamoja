import crypto from 'node:crypto';
import { q, tx, systemTx } from '../db.js';
import { adminTx } from '../owner.js';
import { policy } from '../config.js';
import { hashPassword, verifyAndUpgrade, PASSWORD_HASH } from '../auth.js';
import { verifyAuditChain, writeAudit } from '../audit.js';
import { loadGroup } from '../authz.js';
import { addDays, today } from '../util.js';
import { attendanceSummary } from './attendance.js';
import { membershipAt } from './common.js';
import {
  recordPaymentEntry, verifySubmission, allocateReceipt, recordReceipt, postToLedger, submitGroup,
  requestCorrection, approveCorrection,
} from './payments.js';
import { recordDisbursement, createLoanApplication, changeLoanMaterial, transitionLoan } from './loans.js';
import { handOverCash, receiveCash, confirmDeposit } from './cash.js';
import { createMemberLoan } from './memberLoans.js';
import { decodeBase64 } from '../files.js';
import { LedgerAdapter } from '../ledger.js';
import { dashboardData } from './dashboard.js';
import { computeRanking } from './ranking.js';

const pass = (id, name, detail) => ({ id, name, status: 'PASS', detail });
const fail = (id, name, detail) => ({ id, name, status: 'FAIL', detail });
// One check failing must never hide the verdict of the checks after it.
const safe = async (id, name, fn) => {
  try { return await fn(); } catch (err) { return fail(id, name, String(err.message)); }
};

function callerFor(user, role, scopeType, scopeId) {
  return { user, sessionId: 'selftest', stepUpAt: new Date(), roleUsed: role, assignments: [{ role, scope_type: scopeType, scope_id: scopeId, effective_from: '2000-01-01', effective_to: null }] };
}

async function expectReject(fn, label) {
  try { await fn(); return { rejected: false, detail: `${label} was NOT rejected.` }; }
  catch (err) { return { rejected: true, detail: `${label} rejected: ${err.code || err.message}` }; }
}

export async function runSystemChecks(actor) {
  // Per run, not per process: two runs in one API process must not collide on
  // the same fixture e-mail addresses.
  const TAG = 'SELFTEST-' + Date.now();
  const results = [];
  const ids = { users: [], persons: [], memberships: [], meetings: [], submissions: [], receipts: [], entries: [], loans: [], branches: [], groups: [] };
  const cleanupErrors = [];
  let client;
  try {
    const pwd = await hashPassword(crypto.randomBytes(18).toString('hex'));
    const mkUser = async (label) => {
      const r = await q('INSERT INTO users (email, password_hash, full_name, is_demo) VALUES ($1,$2,$3,true) RETURNING id, email, full_name',
        [`${TAG.toLowerCase()}-${label}@selftest.invalid`, pwd, `Self-test ${label}`]);
      ids.users.push(r.rows[0].id);
      return r.rows[0];
    };
    const treasurer = await mkUser('treasurer');
    const verifier = await mkUser('verifier');
    const verifier2 = await mkUser('verifier2');
    const officer = await mkUser('officer');
    const chair = await mkUser('chair');
    const approver = await mkUser('approver');
    const itadmin = await mkUser('itadmin');
    const manager = await mkUser('manager');
    const dual = await mkUser('dual');
    const other = await mkUser('other');

    const branchA = (await q('INSERT INTO org_branches (name, district, code) VALUES ($1,$2,$3) RETURNING *', [`Self-test Branch A ${TAG}`, 'Test', TAG + '-A'])).rows[0];
    const branchB = (await q('INSERT INTO org_branches (name, district, code) VALUES ($1,$2,$3) RETURNING *', [`Self-test Branch B ${TAG}`, 'Test', TAG + '-B'])).rows[0];
    ids.branches.push(branchA.id, branchB.id);
    const hiddenGroup = (await q(
      `INSERT INTO groups (name, type, branch_id, status, created_by, is_demo) VALUES ($1,'VSLA',$2,'Draft',$3,true) RETURNING *`,
      [`Self-test Hidden Group ${TAG}`, branchA.id, officer.id],
    )).rows[0];
    ids.groups.push(hiddenGroup.id);
    const group = (await q(
      `INSERT INTO groups (name, type, branch_id, status, created_by, registration_evidence_status, review_decision, is_demo)
       VALUES ($1,'VSLA',$2,'Active',$3,'Provided','approve',true) RETURNING *`,
      [`Self-test Group ${TAG}`, branchA.id, officer.id],
    )).rows[0];
    ids.groups.push(group.id);
    const actorId = actor?.user?.id || officer.id;
    const mkPerson = async (name, join) => {
      const p = (await q('INSERT INTO persons (full_name, phone, consent_recorded, consent_at, created_by, is_demo) VALUES ($1,$2,true,now(),$3,true) RETURNING *', [name, '+2567' + Math.floor(10000000 + Math.random() * 89999999), actorId])).rows[0];
      ids.persons.push(p.id);
      const m = (await q("INSERT INTO memberships (person_id, group_id, join_date, status, created_by, is_demo) VALUES ($1,$2,$3,'active',$4,true) RETURNING *", [p.id, group.id, join, officer.id])).rows[0];
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
    const mgrCaller = callerFor(manager, 'management', 'Org', 'org');
    // Management is read-only: it may watch a cash reconciliation, never perform a step.
    const mgrReceive = await expectReject(() => receiveCash(mgrCaller, cashReceipt.id), 'Management receiving cash');
    const mgrDeposit = await expectReject(() => confirmDeposit(mgrCaller, cashReceipt.id, { bankRef: 'SELFTEST-BANK' }), 'Management confirming a deposit');
    const hand = await handOverCash(dualCaller, cashReceipt.id, {});
    const sameReceive = await expectReject(() => receiveCash(dualCaller, cashReceipt.id), 'Handing over and receiving the same cash');
    const recv = await receiveCash(vcaller, cashReceipt.id);
    const sameDeposit = await expectReject(() => confirmDeposit(vcaller, cashReceipt.id, { bankRef: 'SELFTEST-BANK' }), 'Receiving and depositing the same cash');
    const v2caller = callerFor(verifier2, 'verifier', 'Group', group.id);
    const dep = await confirmDeposit(v2caller, cashReceipt.id, { bankRef: 'SELFTEST-BANK', evidenceRef: 'self-test slip' });
    results.push(hand.reconciliation && sameReceive.rejected && recv.reconciliation && sameDeposit.rejected && dep.reconciliation?.status === 'reconciled'
      ? pass('cash_three_steps', 'Cash: handover, branch receipt and deposit are three steps by three people', `final ${dep.reconciliation.status}; same-person steps rejected`)
      : fail('cash_three_steps', 'Cash: handover, receipt and deposit by three people', `hand=${!!hand.reconciliation} sameReceive=${sameReceive.rejected} recv=${!!recv.reconciliation} sameDeposit=${sameDeposit.rejected} final=${dep.reconciliation?.status}`));
    results.push(mgrReceive.rejected && mgrDeposit.rejected
      ? pass('cash_readonly_management', 'Management cannot perform a cash step (read-only role)', `${mgrReceive.detail}; ${mgrDeposit.detail}`)
      : fail('cash_readonly_management', 'Management cannot perform a cash step', `receive=${mgrReceive.detail} deposit=${mgrDeposit.detail}`));

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

    // app_system() is the scheduler's transaction-local switch. An ordinary
    // request runs with it off, and it does not survive the transaction.
    results.push(await safe('app_system_gate', 'app_system() is off for requests and only the scheduler sets it', async () => {
      const inRequest = (await q('SELECT app_system() AS on')).rows[0].on;
      const raw = (await q("SELECT COALESCE(current_setting('app.system', true), '') AS v")).rows[0].v;
      let inSystem = null;
      try {
        await systemTx(async (client) => {
          inSystem = (await client.query('SELECT app_system() AS on')).rows[0].on;
          throw new Error('rollback self-test');
        });
      } catch { /* expected: the transaction is rolled back */ }
      const after = (await q('SELECT app_system() AS on')).rows[0].on;
      const detail = `request=${inRequest} app.system='${raw}' systemTx=${inSystem} afterTx=${after}`;
      return inRequest === false && raw === '' && inSystem === true && after === false
        ? pass('app_system_gate', 'app_system() is off for requests and only the scheduler sets it', detail)
        : fail('app_system_gate', 'app_system() is off for requests and only the scheduler sets it', detail);
    }));

    // Password hashing: Argon2id with the approved parameters, and a legacy
    // scrypt hash is upgraded only after a successful verification.
    results.push(await safe('password_hash_params', 'Passwords use Argon2id (m=19456, t=2, p=1); legacy hashes upgrade on login', async () => {
      const fresh = await hashPassword('SelfTest#Pass2026');
      const salt = crypto.randomBytes(16).toString('base64');
      const legacy = `scrypt$16384$8$1$${salt}$${crypto.scryptSync('SelfTest#Pass2026', salt, 64, { N: 16384, r: 8, p: 1 }).toString('base64')}`;
      const upgraded = await verifyAndUpgrade('SelfTest#Pass2026', legacy);
      const stable = await verifyAndUpgrade('SelfTest#Pass2026', upgraded.upgraded || '');
      const params = `algorithm=${PASSWORD_HASH.algorithm} m=${PASSWORD_HASH.m} t=${PASSWORD_HASH.t} p=${PASSWORD_HASH.p}`;
      const ok = fresh.startsWith(`$argon2id$v=19$m=${PASSWORD_HASH.m},t=${PASSWORD_HASH.t},p=${PASSWORD_HASH.p}$`)
        && upgraded.ok && !!upgraded.upgraded && stable.ok && stable.upgraded === null;
      const detail = `${params} legacyUpgraded=${!!upgraded.upgraded} rehashStable=${stable.upgraded === null}`;
      return ok
        ? pass('password_hash_params', 'Passwords use Argon2id (m=19456, t=2, p=1); legacy hashes upgrade on login', detail)
        : fail('password_hash_params', 'Passwords use Argon2id (m=19456, t=2, p=1); legacy hashes upgrade on login', detail);
    }));

    // Real records are never hard-deleted; the audit row that names one must
    // still resolve after a refused delete.
    results.push(await safe('hard_delete_guard', 'Users, persons, groups, memberships and role assignments cannot be hard-deleted', async () => {
      let out = null;
      try {
        await tx(async (client) => {
          // Non-demo fixtures: these are exactly the rows the guard must
          // refuse. The whole transaction is rolled back, so nothing persists.
          // created_by is the acting admin so the row stays visible to the
          // caller for INSERT ... RETURNING under the persons SELECT policy.
          const dg = (await client.query("INSERT INTO groups (name, type, branch_id, status, created_by) VALUES ($1,'VSLA',$2,'Draft',$3) RETURNING id", [`Delete guard group ${TAG}`, branchA.id, actorId])).rows[0];
          const p = (await client.query("INSERT INTO persons (full_name, consent_recorded, consent_at, created_by) VALUES ($1,true,now(),$2) RETURNING id", [`Delete guard ${TAG}`, actorId])).rows[0];
          const m = (await client.query("INSERT INTO memberships (person_id, group_id, join_date, status, created_by) VALUES ($1,$2,$3,'active',$4) RETURNING id", [p.id, dg.id, today(), actorId])).rows[0];
          const ra = (await client.query("INSERT INTO role_assignments (user_id, role, scope_type, scope_id, status, granted_by, approved_by) VALUES ($1,'auditor','Org','org','active',$2,$2) RETURNING id", [officer.id, officer.id])).rows[0];
          const u = (await client.query("INSERT INTO users (email, password_hash, full_name) VALUES ($1,$2,$3) RETURNING id", [`${TAG.toLowerCase()}-del@selftest.invalid`, pwd, 'Delete guard'])).rows[0];
          await writeAudit(client, { user: { id: officer.id }, roleUsed: 'field_officer' }, {
            action: 'selftest.delete_guard', entity: 'Person', entityId: p.id, after: { tag: TAG }, scopeType: 'Group', scopeId: group.id,
          });
          const attempts = {};
          for (const [name, sql, id] of [
            ['person', 'DELETE FROM persons WHERE id=$1', p.id],
            ['membership', 'DELETE FROM memberships WHERE id=$1', m.id],
            ['group', 'DELETE FROM groups WHERE id=$1', dg.id],
            ['role', 'DELETE FROM role_assignments WHERE id=$1', ra.id],
            ['user', 'DELETE FROM users WHERE id=$1', u.id],
          ]) {
            try {
              await client.query('SAVEPOINT d');
              await client.query(sql, [id]);
              await client.query('RELEASE SAVEPOINT d');
              attempts[name] = false;
            } catch (err) {
              attempts[name] = err.code === '42501';
              await client.query('ROLLBACK TO SAVEPOINT d');
            }
          }
          // The allowed alternative: end-date the membership instead.
          await client.query("UPDATE memberships SET status='ended', leave_date=current_date WHERE id=$1", [m.id]);
          const ended = (await client.query('SELECT status, leave_date FROM memberships WHERE id=$1', [m.id])).rows[0];
          const auditKept = (await client.query('SELECT count(*)::int AS n FROM audit_logs WHERE entity_id=$1', [p.id])).rows[0].n;
          const rollback = new Error('rollback hard delete self-test');
          rollback.selfTest = { attempts, ended, auditKept };
          throw rollback;
        });
      } catch (err) { out = err.selfTest || { error: err.message }; }
      if (!out || !out.attempts) return fail('hard_delete_guard', 'Hard deletes are refused', `fixture transaction failed: ${out?.error || 'unknown'}`);
      const allRefused = ['person', 'membership', 'group', 'role', 'user'].every((k) => out.attempts[k] === true);
      const detail = `refused=${JSON.stringify(out.attempts)} endDated=${out.ended?.status} auditRowsForRecord=${out.auditKept}`;
      return allRefused && out.ended?.status === 'ended' && out.auditKept >= 1
        ? pass('hard_delete_guard', 'Users, persons, groups, memberships and role assignments cannot be hard-deleted', detail)
        : fail('hard_delete_guard', 'Users, persons, groups, memberships and role assignments cannot be hard-deleted', detail);
    }));

    // is_demo is immutable after insert. A real (non-demo) row cannot be
    // flipped; the seed and system paths are the only exceptions.
    results.push(await safe('is_demo_immutable', 'is_demo cannot be flipped on a real row; only the seed or system path may set it', async () => {
      let out = null;
      try {
        await tx(async (client) => {
          const u = (await client.query(
            "INSERT INTO users (email, password_hash, full_name, is_demo) VALUES ($1,$2,$3,false) RETURNING id",
            [`${TAG.toLowerCase()}-immutable@selftest.invalid`, pwd, 'Immutable guard'],
          )).rows[0];
          let blocked = false;
          let code = null;
          try {
            await client.query('SAVEPOINT imm');
            await client.query('UPDATE users SET is_demo=true WHERE id=$1', [u.id]);
            await client.query('RELEASE SAVEPOINT imm');
          } catch (err) {
            code = err.code;
            blocked = err.code === '42501';
            await client.query('ROLLBACK TO SAVEPOINT imm');
          }
          // The seed path may set it.
          await client.query("SELECT set_config('app.seed','on',true)");
          await client.query('UPDATE users SET is_demo=true WHERE id=$1', [u.id]);
          const seedAllowed = (await client.query('SELECT is_demo FROM users WHERE id=$1', [u.id])).rows[0].is_demo;
          const rollback = new Error('rollback is_demo self-test');
          rollback.selfTest = { blocked, code, seedAllowed };
          throw rollback;
        });
      } catch (err) { out = err.selfTest || { error: err.message }; }
      const detail = `flipBlocked=${out.blocked === true} code=${out.code} seedPathAllowed=${out.seedAllowed === true}`;
      return out.blocked === true && out.code === '42501' && out.seedAllowed === true
        ? pass('is_demo_immutable', 'is_demo cannot be flipped on a real row; only the seed or system path may set it', detail)
        : fail('is_demo_immutable', 'is_demo cannot be flipped on a real row; only the seed or system path may set it', detail);
    }));

    // Attendance: one record per person per meeting, membership required on the
    // meeting date, cancelled meetings never count.
    results.push(await safe('attendance_integrity', 'One record per person per meeting, membership required, cancelled excluded', async () => {
      const dup = await expectReject(() => q(
        "INSERT INTO attendance_records (meeting_id, person_id, status, method, recorded_by) VALUES ($1,$2,'present','self',$3)",
        [meetingIds[0], personA.id, officer.id],
      ), 'A second check-in for the same meeting');
      const nonMember = await membershipAt(personC.id, hiddenGroup.id, today());
      const cancelled = (await q(
        "INSERT INTO meetings (group_id, title, scheduled_for, status, created_by, is_demo) VALUES ($1,$2,$3,'cancelled',$4,true) RETURNING id",
        [group.id, `Cancelled self-test ${TAG}`, addDays(today(), -100) + 'T10:00:00Z', officer.id],
      )).rows[0];
      ids.meetings.push(cancelled.id);
      await q("INSERT INTO attendance_records (meeting_id, person_id, status, method, recorded_by) VALUES ($1,$2,'present','self',$3)", [cancelled.id, personA.id, officer.id]);
      const after = (await attendanceSummary(group.id)).people.find((p) => p.personId === personA.id);
      const detail = `duplicate=${dup.detail} nonMember=${nonMember ? 'found' : 'null'} expectedAfterCancelled=${after.expected}`;
      return dup.rejected && nonMember === null && after.expected === 8
        ? pass('attendance_integrity', 'One record per person per meeting, membership required, cancelled excluded', detail)
        : fail('attendance_integrity', 'One record per person per meeting, membership required, cancelled excluded', detail);
    }));

    // Payments: submission locks the drafts and totals stay per category, a
    // receipt source reference is unique, evidence is mandatory.
    const controls = await safe('payment_controls', 'Submission locks drafts with per-category totals; receipts need a unique reference and evidence', async () => {
      const tcaller = callerFor(treasurer, 'treasurer', 'Group', group.id);
      const day = addDays(today(), -3);
      const a1 = await recordPaymentEntry(tcaller, { groupId: group.id, memberPersonId: personA.id, category: 'savings', amount: 10000, collectionDate: day, idempotencyKey: TAG + '-pc1' });
      const a2 = await recordPaymentEntry(tcaller, { groupId: group.id, memberPersonId: personA.id, category: 'fines', amount: 5000, collectionDate: day, idempotencyKey: TAG + '-pc2' });
      const subR = await submitGroup(tcaller, { groupId: group.id, periodStart: day, periodEnd: day, idempotencyKey: TAG + '-pcsub' });
      ids.submissions.push(subR.submission.id);
      const locked = (await q('SELECT status FROM payment_entries WHERE id = ANY($1::uuid[])', [[a1.entry.id, a2.entry.id]])).rows.every((r) => r.status === 'locked');
      const totals = subR.submission.totals || {};
      const totalsOk = totals.savings === 10000 && totals.fines === 5000 && Number(subR.submission.total_amount) === 15000;
      const resub = await expectReject(() => submitGroup(tcaller, { groupId: group.id, periodStart: day, periodEnd: day, idempotencyKey: TAG + '-pcsub2' }), 'Submitting the same drafts twice');
      const vc = callerFor(verifier, 'verifier', 'Group', group.id);
      const rcpt = await recordReceipt(vc, { groupId: group.id, amount: 5000, receiptDate: today(), sourceRef: TAG + '-SRC', destination: 'Bank', channel: 'bank', evidenceRef: 'proof', idempotencyKey: TAG + '-rc1' });
      ids.receipts.push(rcpt.receipt.id);
      const dupRcpt = await expectReject(() => recordReceipt(vc, { groupId: group.id, amount: 5000, receiptDate: today(), sourceRef: TAG + '-SRC', destination: 'Bank', channel: 'bank', evidenceRef: 'proof', idempotencyKey: TAG + '-rc2' }), 'A duplicate receipt source reference');
      const noEvidence = await expectReject(() => recordReceipt(vc, { groupId: group.id, amount: 5000, receiptDate: today(), sourceRef: TAG + '-SRC2', destination: 'Bank', channel: 'bank', idempotencyKey: TAG + '-rc3' }), 'A receipt without evidence');
      const detail = `locked=${locked} totals=${JSON.stringify(totals)} resubmit=${resub.rejected} duplicateRef=${dupRcpt.rejected} noEvidence=${noEvidence.rejected}`;
      return { result: locked && totalsOk && resub.rejected && dupRcpt.rejected && noEvidence.rejected
        ? pass('payment_controls', 'Submission locks drafts with per-category totals; receipts need a unique reference and evidence', detail)
        : fail('payment_controls', 'Submission locks drafts with per-category totals; receipts need a unique reference and evidence', detail), entryId: a1.entry.id };
    });
    results.push(controls.result || controls);

    // A correction needs a different approver, and the locked entry changes only
    // through that approved correction.
    results.push(await safe('correction_separation', 'A correction needs a different approver and only then changes the entry', async () => {
      const entryId = controls.entryId;
      if (!entryId) return fail('correction_separation', 'A correction needs a different approver', 'no locked entry from the payment check');
      const vc = callerFor(verifier, 'verifier', 'Group', group.id);
      const v2 = callerFor(verifier2, 'verifier', 'Group', group.id);
      const corr = await requestCorrection(vc, { entryId, field: 'amount', newValue: '12000', reason: 'Member receipt corrected after recount', idempotencyKey: TAG + '-corr' });
      const selfApprove = await expectReject(() => approveCorrection(vc, { correctionId: corr.correction.id }), 'Approving your own correction');
      const approved = await approveCorrection(v2, { correctionId: corr.correction.id });
      const after = Number((await q('SELECT amount FROM payment_entries WHERE id=$1', [entryId])).rows[0].amount);
      const requestedBy = verifier.email || verifier.id;
      const approvedBy = verifier2.email || verifier2.id;
      const detail = `selfApproveBlocked=${selfApprove.rejected} requestedBy=${requestedBy} approvedBy=${approvedBy} different=${verifier.id !== verifier2.id} status=${approved.correction?.status} amount=${after}`;
      return selfApprove.rejected && approved.correction?.status === 'approved' && after === 12000
        ? pass('correction_separation', 'A correction needs a different approver and only then changes the entry', detail)
        : fail('correction_separation', 'A correction needs a different approver and only then changes the entry', detail);
    }));

    // Loans: only a leader may apply, product limits apply, a guarantor is
    // required above the threshold, and a material change forces re-approval.
    results.push(await safe('loan_controls', 'Loan limits, guarantor threshold, authorised representative and re-approval', async () => {
      const chairPerson = (await q(
        'INSERT INTO persons (full_name, linked_user_id, consent_recorded, consent_at, created_by, is_demo) VALUES ($1,$2,true,now(),$3,true) RETURNING *',
        [`Self-test Chair ${TAG}`, chair.id, actorId],
      )).rows[0];
      ids.persons.push(chairPerson.id);
      const chairMembership = (await q(
        "INSERT INTO memberships (person_id, group_id, join_date, status, created_by, is_demo) VALUES ($1,$2,$3,'active',$4,true) RETURNING id",
        [chairPerson.id, group.id, addDays(today(), -200), officer.id],
      )).rows[0];
      ids.memberships.push(chairMembership.id);
      await q(
        "INSERT INTO leaderships (group_id, person_id, position, authority_scope, effective_from, created_by, is_demo) VALUES ($1,$2,'chairperson','group',$3,$4,true)",
        [group.id, chairPerson.id, addDays(today(), -200), officer.id],
      );
      const chairCaller = callerFor(chair, 'chairperson', 'Group', group.id);
      const tcaller = callerFor(treasurer, 'treasurer', 'Group', group.id);
      const noAuthority = await expectReject(() => createLoanApplication(tcaller, {
        groupId: group.id, amount: 400000, termMonths: 6, purpose: 'Self-test without authority', repaymentSource: 'Sales',
      }), 'A treasurer who is not a leader applying for a loan');
      const limits = (await policy('loans.product_limits'))[group.type];
      const overLimit = await expectReject(() => createLoanApplication(chairCaller, {
        groupId: group.id, amount: limits.max + 1, termMonths: 6, purpose: 'Self-test above the product limit', repaymentSource: 'Sales',
      }), 'A loan above the configured product limit');
      const threshold = Number(await policy('loans.guarantor_threshold_ugx'));
      const bigLoan = (await q(
        `INSERT INTO loan_applications (group_id, product, amount, purpose, term_months, repayment_source, status, created_by, authorised_person_id)
         VALUES ($1,'group_loan',$2,'Self-test guarantor','6','Sales','OperationsReview',$3,$4) RETURNING *`,
        [group.id, threshold + 100000, officer.id, chairPerson.id],
      )).rows[0];
      ids.loans.push(bigLoan.id);
      await q("INSERT INTO approval_steps (loan_id, step, actor_user_id, decision) VALUES ($1,'operations_review',$2,'reviewed')", [bigLoan.id, verifier.id]);
      const noGuarantor = await expectReject(() => transitionLoan(callerFor(approver, 'credit_approver', 'Org', 'org'), bigLoan.id, { action: 'approve' }), 'Approving above the threshold without a guarantor');
      const approvedLoan = (await q(
        `INSERT INTO loan_applications (group_id, product, amount, purpose, term_months, repayment_source, status, created_by, approved_by, approved_at, authorised_person_id)
         VALUES ($1,'group_loan',400000,'Self-test material change','6','Sales','Approved',$2,$3,now(),$4) RETURNING *`,
        [group.id, officer.id, approver.id, chairPerson.id],
      )).rows[0];
      ids.loans.push(approvedLoan.id);
      const changed = await changeLoanMaterial(chairCaller, approvedLoan.id, { amount: 450000, reason: 'Members changed the approved amount' });
      const detail = `noAuthority=${noAuthority.rejected} overLimit=${overLimit.rejected} noGuarantor=${noGuarantor.rejected} reapproval=${changed.status}/${changed.requiresReapproval}`;
      return noAuthority.rejected && overLimit.rejected && noGuarantor.rejected && changed.status === 'OperationsReview' && changed.requiresReapproval === true
        ? pass('loan_controls', 'Loan limits, guarantor threshold, authorised representative and re-approval', detail)
        : fail('loan_controls', 'Loan limits, guarantor threshold, authorised representative and re-approval', detail);
    }));

    // Dashboard totals: distinct people, the three flows kept apart, and the
    // 31-day group excluded while the 30-day group is not.
    results.push(await safe('dashboard_totals', 'Dashboard totals reconcile and ranking excludes only more than 30 days', async () => {
      const dash = await dashboardData(actor || {}, {});
      const dbMembers = (await q("SELECT COUNT(*)::int AS rows, COUNT(DISTINCT m.person_id)::int AS people FROM memberships m WHERE m.status='active'")).rows[0];
      const f = dash.cards.flows;
      const rank = await computeRanking({ client: { query: q } });
      const g31 = rank.groups.find((g) => g.groupName === 'Tukolere Wamu VSLA');
      const g30 = rank.groups.find((g) => g.groupName === 'Nkumba Investment Club');
      const peopleOk = dash.cards.members.people === dbMembers.people && dash.cards.members.memberships === dbMembers.rows;
      const flowsOk = f.submitted.count >= f.verified.count && f.verified.count >= f.posted.count
        && dash.cards.totalPayable.principal === dash.cards.outstandingPrincipal;
      const overdueOk = g31?.excluded === true && g30?.excluded === false;
      const topOk = dash.cards.topTen.length <= 10;
      const detail = `people=${dash.cards.members.people}/${dbMembers.people} memberships=${dash.cards.members.memberships}/${dbMembers.rows} flows=${f.submitted.count}/${f.verified.count}/${f.posted.count} top=${dash.cards.topTen.length} 31d=${g31?.metrics.maxOverdue}:${g31?.excluded} 30d=${g30?.metrics.maxOverdue}:${g30?.excluded}`;
      return peopleOk && flowsOk && overdueOk && topOk
        ? pass('dashboard_totals', 'Dashboard totals reconcile and ranking excludes only more than 30 days', detail)
        : fail('dashboard_totals', 'Dashboard totals reconcile and ranking excludes only more than 30 days', detail);
    }));

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
        // A locked entry is immutable by design. These are demo-tagged test
        // rows, so cleanup uses the same audited-purge escape the demo purge
        // uses (app.demo_purge) instead of loosening the entry guard.
        await tx(async (client) => {
          await client.query("SELECT set_config('app.demo_purge','on',true)");
          // These are this run's own test rows, so tag them as such before
          // using the demo-purge escape; the entry guard is unchanged.
          await client.query('UPDATE payment_entries SET is_demo=true WHERE group_id=$1', [g]);
          await client.query('DELETE FROM payment_entries WHERE group_id=$1', [g]);
        });
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
        await q('DELETE FROM leaderships WHERE group_id=$1', [g]);
        await q('DELETE FROM file_objects WHERE group_id=$1', [g]);
        await q('DELETE FROM learning_assignments WHERE group_id=$1', [g]);
      }
      // The API role has no DELETE on the five protected identity tables, so
      // the owner role removes this run's demo rows (and only them).
      await adminTx(async (c) => {
        for (const g of ids.groups) {
          await c.query('DELETE FROM memberships WHERE group_id=$1', [g]);
          await c.query('DELETE FROM groups WHERE id=$1', [g]);
        }
        for (const id of ids.persons) await c.query('DELETE FROM persons WHERE id=$1', [id]);
        for (const id of ids.users) {
          await c.query('DELETE FROM idempotency_keys WHERE user_id=$1', [id]);
          await c.query('DELETE FROM users WHERE id=$1', [id]);
        }
      });
      for (const id of ids.branches) await q('DELETE FROM org_branches WHERE id=$1', [id]);
      await q("DELETE FROM idempotency_keys WHERE key LIKE $1", [TAG + '%']);
    } catch (err) {
      cleanupErrors.push(String(err.message));
    }
  }
  if (cleanupErrors.length) results.push(fail('cleanup', 'Self-test data cleaned up', cleanupErrors.join('; ')));
  else results.push(pass('cleanup', 'Self-test data cleaned up', 'All self-test rows removed; audit entries retained.'));
  const passed = results.filter((r) => r.status === 'PASS').length;
  return { ranAt: new Date().toISOString(), ranBy: actor?.user?.id || null, results, passed, failed: results.length - passed, total: results.length, tag: TAG };
}
