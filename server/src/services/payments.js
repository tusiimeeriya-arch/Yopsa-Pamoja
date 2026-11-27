import { q, tx } from '../db.js';
import { policy } from '../config.js';
import { LedgerAdapter } from '../ledger.js';
import { badRequest, conflict, forbidden, notFound } from '../errors.js';
import { addDays, amount as amt, isoDate, oneOf, str, today } from '../util.js';
import { withIdempotency } from '../idempotency.js';
import { writeAudit } from '../audit.js';
import { createTask, groupMembers, groupOr404, membershipAt, notify } from './common.js';
import { hasRole, requireGroupRole, requireRole } from '../authz.js';

const CATEGORIES = ['loan_repayment', 'savings', 'shares', 'fines', 'subscriptions', 'social_fund', 'other'];

export async function recordPaymentEntry(caller, input) {
  return tx(async (client) => {
    const group = await groupOr404(input.groupId, client);
    const treasurer = hasRole(caller, ['treasurer'], { groupId: group.id, branchId: group.branch_id });
    const officer = hasRole(caller, ['field_officer'], { groupId: group.id, branchId: group.branch_id });
    if (!treasurer && !officer) throw forbidden('Only the group treasurer or an assigned officer can record payment entries.');
    if (group.status !== 'Active') throw badRequest('Payments can only be recorded for an active group.');
    const memberPersonId = str(input.memberPersonId, 'Member', { max: 60 });
    const category = oneOf(input.category, CATEGORIES, 'Category');
    const value = amt(input.amount);
    const collectionDate = isoDate(input.collectionDate, 'Collection date');
    const membership = await membershipAt(memberPersonId, group.id, collectionDate, client);
    if (!membership) throw badRequest('That person was not an active member on the collection date.');
    const assisted = officer && !treasurer;
    if (assisted) {
      if (!input.confirmationMethod) throw badRequest('Assisted entry requires how the member confirmed.');
      str(input.confirmationMethod, 'Confirmation method', { max: 120 });
    }
    return withIdempotency(client, caller, input.idempotencyKey, 'recordPaymentEntry', {
      groupId: group.id, memberPersonId, category, amount: value, collectionDate,
    }, async () => {
      const r = await client.query(
        `INSERT INTO payment_entries (group_id, member_person_id, category, amount, collection_date, created_by)
         VALUES ($1,$2,$3,$4,$5,$6) RETURNING *`,
        [group.id, memberPersonId, category, value, collectionDate, caller.user.id],
      );
      const entry = r.rows[0];
      await writeAudit(client, caller, {
        action: 'payment_entry.create', entity: 'PaymentEntry', entityId: entry.id,
        after: { amount: value, category, groupId: group.id }, scopeType: 'Group', scopeId: group.id,
        reason: assisted ? `assisted: ${input.confirmationMethod}` : null,
      });
      return { entry };
    });
  });
}

export async function submitGroup(caller, input) {
  return tx(async (client) => {
    const group = await groupOr404(input.groupId, client);
    requireGroupRole(caller, group, ['treasurer']);
    const periodStart = isoDate(input.periodStart, 'Period start');
    const periodEnd = isoDate(input.periodEnd, 'Period end');
    if (periodStart > periodEnd) throw badRequest('The period start must be before the end.');
    return withIdempotency(client, caller, input.idempotencyKey, 'submitGroup', { groupId: group.id, periodStart, periodEnd }, async () => {
      const drafts = (await client.query(
        `SELECT * FROM payment_entries WHERE group_id=$1 AND status='draft'
           AND collection_date BETWEEN $2 AND $3 FOR UPDATE`,
        [group.id, periodStart, periodEnd],
      )).rows;
      if (!drafts.length) throw badRequest('There are no draft entries in that period to submit.');
      const totals = {};
      let total = 0;
      for (const e of drafts) {
        totals[e.category] = (totals[e.category] || 0) + Number(e.amount);
        total += Number(e.amount);
      }
      const sub = (await client.query(
        `INSERT INTO group_submissions (group_id, period_start, period_end, totals, total_amount, submitted_by)
         VALUES ($1,$2,$3,$4,$5,$6) RETURNING *`,
        [group.id, periodStart, periodEnd, totals, total, caller.user.id],
      )).rows[0];
      await client.query(
        `UPDATE payment_entries SET status='locked', submission_id=$2
         WHERE group_id=$1 AND status='draft' AND collection_date BETWEEN $3 AND $4`,
        [group.id, sub.id, periodStart, periodEnd],
      );
      await writeAudit(client, caller, {
        action: 'submission.submit', entity: 'GroupSubmission', entityId: sub.id,
        after: { total, totals }, scopeType: 'Group', scopeId: group.id,
      });
      return { submission: sub };
    });
  });
}

export async function recordReceipt(caller, input) {
  return tx(async (client) => {
    const group = await groupOr404(input.groupId, client);
    requireRole(caller, ['verifier'], { groupId: group.id, branchId: group.branch_id });
    const value = amt(input.amount);
    const receiptDate = isoDate(input.receiptDate, 'Receipt date');
    const sourceRef = str(input.sourceRef, 'Source reference', { max: 120 });
    const destination = str(input.destination, 'Destination', { max: 120 });
    const channel = oneOf(input.channel || 'bank', ['bank', 'cash', 'mobile_money'], 'Channel');
    const evidenceRef = str(input.evidenceRef, 'Evidence reference', { max: 200 });
    const submissionId = input.submissionId ? str(input.submissionId, 'Submission', { max: 60 }) : null;
    return withIdempotency(client, caller, input.idempotencyKey, 'recordReceipt', {
      groupId: group.id, amount: value, receiptDate, sourceRef, channel, submissionId,
    }, async () => {
      const r = await client.query(
        `INSERT INTO receipts (group_id, submission_id, amount, receipt_date, source_ref, destination, channel, evidence_ref, recorded_by)
         VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING *`,
        [group.id, submissionId, value, receiptDate, sourceRef, destination, channel, evidenceRef, caller.user.id],
      );
      await writeAudit(client, caller, {
        action: 'receipt.record', entity: 'Receipt', entityId: r.rows[0].id,
        after: { amount: value, channel, sourceRef }, scopeType: 'Group', scopeId: group.id,
      });
      return { receipt: r.rows[0] };
    });
  });
}

export async function verifySubmission(caller, input) {
  return tx(async (client) => {
    const sub = (await client.query('SELECT * FROM group_submissions WHERE id=$1 FOR UPDATE', [input.submissionId])).rows[0];
    if (!sub) throw notFound('Submission not found.');
    const group = await groupOr404(sub.group_id, client);
    requireRole(caller, ['verifier'], { groupId: group.id, branchId: group.branch_id });
    if (sub.submitted_by === caller.user.id) throw forbidden('You cannot verify your own submission.');
    const recorded = await client.query('SELECT 1 FROM payment_entries WHERE submission_id=$1 AND created_by=$2 LIMIT 1', [sub.id, caller.user.id]);
    if (recorded.rowCount) throw forbidden('You recorded an entry in this submission, so you cannot verify it.');
    if (!['submitted', 'difference'].includes(sub.status)) throw badRequest('This submission is not awaiting verification.');
    const receipts = await client.query("SELECT COALESCE(SUM(amount),0) AS total FROM receipts WHERE submission_id=$1 AND status='confirmed'", [sub.id]);
    const received = Number(receipts.rows[0].total);
    const submitted = Number(sub.total_amount);
    const difference = received - submitted;
    if (difference !== 0) {
      await client.query("UPDATE group_submissions SET status='difference', difference=$2, receipt_total=$3 WHERE id=$1", [sub.id, difference, received]);
      await writeAudit(client, caller, {
        action: 'submission.verify_blocked', entity: 'GroupSubmission', entityId: sub.id,
        after: { submitted, received, difference }, scopeType: 'Group', scopeId: group.id,
        reason: 'Submitted total does not match confirmed receipts.',
      });
      return { status: 'difference', submitted, received, difference, message: `Received is UGX ${Math.abs(difference).toLocaleString()} ${difference < 0 ? 'short' : 'over'}. Full verification is blocked until this is resolved.` };
    }
    await client.query("UPDATE group_submissions SET status='verified', verified_by=$2, verified_at=now(), difference=0, receipt_total=$3 WHERE id=$1", [sub.id, caller.user.id, received]);
    await writeAudit(client, caller, {
      action: 'submission.verify', entity: 'GroupSubmission', entityId: sub.id,
      after: { submitted, received }, scopeType: 'Group', scopeId: group.id,
    });
    return { status: 'verified', submitted, received, difference: 0 };
  });
}

export async function resolveDifference(caller, input) {
  return tx(async (client) => {
    const sub = (await client.query('SELECT * FROM group_submissions WHERE id=$1 FOR UPDATE', [input.submissionId])).rows[0];
    if (!sub) throw notFound('Submission not found.');
    if (sub.status !== 'difference') throw badRequest('This submission is not in a difference state.');
    const group = await groupOr404(sub.group_id, client);
    requireRole(caller, ['verifier'], { groupId: group.id, branchId: group.branch_id });
    const reason = str(input.reason, 'Reason', { min: 5, max: 500 });
    if (input.mode === 'receipt') {
      const value = amt(input.amount);
      const sourceRef = str(input.sourceRef, 'Source reference', { max: 120 });
      return withIdempotency(client, caller, input.idempotencyKey, 'resolveDifference', { submissionId: sub.id, amount: value, sourceRef, mode: 'receipt' }, async () => {
        await client.query(
          `INSERT INTO receipts (group_id, submission_id, amount, receipt_date, source_ref, destination, channel, evidence_ref, recorded_by)
           VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
          [group.id, sub.id, value, isoDate(input.receiptDate, 'Receipt date'), sourceRef, str(input.destination || 'Yopsa collection account', 'Destination'), 'bank', str(input.evidenceRef || 'difference-resolution', 'Evidence reference'), caller.user.id],
        );
        await writeAudit(client, caller, { action: 'submission.resolve_receipt', entity: 'GroupSubmission', entityId: sub.id, after: { amount: value }, scopeType: 'Group', scopeId: group.id, reason });
        const receipts = await client.query("SELECT COALESCE(SUM(amount),0) AS total FROM receipts WHERE submission_id=$1 AND status='confirmed'", [sub.id]);
        const received = Number(receipts.rows[0].total);
        const difference = received - Number(sub.total_amount);
        if (difference === 0) {
          await client.query("UPDATE group_submissions SET status='verified', verified_by=$2, verified_at=now(), difference=0, receipt_total=$3 WHERE id=$1", [sub.id, caller.user.id, received]);
          return { status: 'verified', received };
        }
        await client.query('UPDATE group_submissions SET difference=$2, receipt_total=$3 WHERE id=$1', [sub.id, difference, received]);
        return { status: 'difference', received, difference };
      });
    }
    if (input.mode === 'partial') {
      if (!(await policy('payments.partial_verification'))) throw badRequest('Partial verification is disabled by policy.');
      const ids = Array.isArray(input.entryIds) ? input.entryIds : [];
      if (!ids.length) throw badRequest('Select the fully evidenced entries to verify.');
      const entries = await client.query(
        `SELECT * FROM payment_entries WHERE submission_id=$1 AND id = ANY($2::uuid[])`, [sub.id, ids],
      );
      if (entries.rowCount !== ids.length) throw badRequest('One or more selected entries are not in this submission.');
      await client.query("UPDATE payment_entries SET status='verified' WHERE submission_id=$1 AND id = ANY($2::uuid[])", [sub.id, ids]);
      await client.query("UPDATE payment_entries SET status='returned' WHERE submission_id=$1 AND NOT (id = ANY($2::uuid[]))", [sub.id, ids]);
      await client.query("UPDATE group_submissions SET status='partially_verified', verified_by=$2, verified_at=now() WHERE id=$1", [sub.id, caller.user.id]);
      await writeAudit(client, caller, { action: 'submission.resolve_partial', entity: 'GroupSubmission', entityId: sub.id, after: { verifiedEntries: ids.length }, scopeType: 'Group', scopeId: group.id, reason });
      return { status: 'partially_verified', verifiedEntries: ids.length };
    }
    throw badRequest("Resolution mode must be 'receipt' or 'partial'.");
  });
}

export async function allocateReceipt(caller, input) {
  return tx(async (client) => {
    const receipt = (await client.query('SELECT * FROM receipts WHERE id=$1 FOR UPDATE', [input.receiptId])).rows[0];
    if (!receipt) throw notFound('Receipt not found.');
    const group = await groupOr404(receipt.group_id, client);
    requireRole(caller, ['verifier'], { groupId: group.id, branchId: group.branch_id });
    const value = amt(input.amount);
    const category = oneOf(input.category, CATEGORIES, 'Category');
    const allocated = Number((await client.query('SELECT COALESCE(SUM(amount),0) AS total FROM allocations WHERE receipt_id=$1', [receipt.id])).rows[0].total);
    if (allocated + value > Number(receipt.amount)) {
      throw conflict(`Allocations cannot exceed the receipt. UGX ${(Number(receipt.amount) - allocated).toLocaleString()} remains unallocated.`, { code: 'ALLOCATION_EXCEEDS_RECEIPT' });
    }
    return withIdempotency(client, caller, input.idempotencyKey, 'allocateReceipt', { receiptId: receipt.id, amount: value, category }, async () => {
      const r = await client.query(
        `INSERT INTO allocations (receipt_id, submission_id, entry_id, amount, category, created_by)
         VALUES ($1,$2,$3,$4,$5,$6) RETURNING *`,
        [receipt.id, receipt.submission_id, input.entryId || null, value, category, caller.user.id],
      );
      await writeAudit(client, caller, { action: 'receipt.allocate', entity: 'Allocation', entityId: r.rows[0].id, after: { amount: value, category }, scopeType: 'Group', scopeId: group.id });
      return { allocation: r.rows[0] };
    });
  });
}

export async function postToLedger(caller, input) {
  return tx(async (client) => {
    const sub = (await client.query('SELECT * FROM group_submissions WHERE id=$1 FOR UPDATE', [input.submissionId])).rows[0];
    if (!sub) throw notFound('Submission not found.');
    const group = await groupOr404(sub.group_id, client);
    requireRole(caller, ['verifier'], { groupId: group.id, branchId: group.branch_id });
    if (!['verified', 'partially_verified'].includes(sub.status)) throw badRequest('Only a verified submission can be posted.');
    const allocations = (await client.query('SELECT * FROM allocations WHERE submission_id=$1', [sub.id])).rows;
    const postingAmount = allocations.length ? allocations.reduce((s, a) => s + Number(a.amount), 0) : Number(sub.total_amount);
    try {
      const result = await LedgerAdapter.postRepayment({ groupId: group.id, amount: postingAmount, reference: sub.id });
      await client.query(
        `INSERT INTO ledger_postings (submission_id, amount, status, ledger_ref, created_by, posted_at)
         VALUES ($1,$2,'posted',$3,$4,now())`,
        [sub.id, postingAmount, result.ledgerRef, caller.user.id],
      );
      await client.query("UPDATE group_submissions SET status='posted', posting_status='posted' WHERE id=$1", [sub.id]);
      await client.query("UPDATE payment_entries SET status='posted' WHERE submission_id=$1 AND status IN ('locked','verified')", [sub.id]);
      await writeAudit(client, caller, { action: 'submission.post', entity: 'GroupSubmission', entityId: sub.id, after: { amount: postingAmount, ledgerRef: result.ledgerRef }, scopeType: 'Group', scopeId: group.id });
      return { status: 'posted', ledgerRef: result.ledgerRef, amount: postingAmount };
    } catch (err) {
      await client.query(
        `INSERT INTO ledger_postings (submission_id, amount, status, error, created_by)
         VALUES ($1,$2,'failed',$3,$4)`,
        [sub.id, postingAmount, String(err.message).slice(0, 300), caller.user.id],
      );
      await client.query("UPDATE group_submissions SET status='posting_pending', posting_status='failed' WHERE id=$1", [sub.id]);
      const owner = (await client.query(
        `SELECT ra.user_id FROM role_assignments ra WHERE ra.role='verifier' AND ra.status='active' AND (ra.scope_type='Org' OR ra.scope_id=$1 OR ra.scope_id=$2) LIMIT 1`,
        [group.branch_id, group.id],
      )).rows[0]?.user_id || caller.user.id;
      const deadline = addDays(today(), 1);
      await client.query(
        `INSERT INTO finance_exceptions (kind, entity, entity_id, detail, owner_user_id, deadline)
         VALUES ('posting_failure','GroupSubmission',$1,$2,$3,$4)`,
        [sub.id, `Verified submission could not be posted: ${String(err.message).slice(0, 200)}`, owner, deadline],
      );
      await createTask(client, {
        title: `Ledger posting failed for ${group.name}`,
        ownerUserId: owner, deadline: addDays(today(), 1), linkedEntity: 'GroupSubmission', linkedId: sub.id,
        dedupeKey: `posting-failed:${sub.id}`,
      });
      await notify(client, { userId: owner, title: 'Posting pending', body: 'A verified submission could not be posted to the ledger.', deadline: addDays(today(), 1), dedupeKey: `notify-posting:${sub.id}` });
      await writeAudit(client, caller, { action: 'submission.post_failed', entity: 'GroupSubmission', entityId: sub.id, after: { amount: postingAmount, error: String(err.message).slice(0, 200) }, scopeType: 'Group', scopeId: group.id });
      return { status: 'posting_pending', message: 'Verification succeeded but posting failed. A finance exception has been created.' };
    }
  });
}

export async function requestCorrection(caller, input) {
  return tx(async (client) => {
    const entry = (await client.query('SELECT * FROM payment_entries WHERE id=$1', [input.entryId])).rows[0];
    if (!entry) throw notFound('Payment entry not found.');
    const group = await groupOr404(entry.group_id, client);
    requireGroupRole(caller, group, ['treasurer', 'verifier']);
    if (!['locked', 'verified', 'posted'].includes(entry.status)) throw badRequest('Only a locked or verified entry needs a correction.');
    const field = oneOf(input.field, ['amount', 'category', 'collection_date'], 'Field');
    const reason = str(input.reason, 'Reason', { min: 10, max: 500 });
    const newValue = str(input.newValue, 'New value', { max: 100 });
    if (field === 'amount') amt(newValue);
    if (field === 'category') oneOf(newValue, CATEGORIES, 'Category');
    if (field === 'collection_date') isoDate(newValue, 'Collection date');
    return withIdempotency(client, caller, input.idempotencyKey, 'requestCorrection', { entryId: entry.id, field, newValue, reason }, async () => {
      const r = await client.query(
        `INSERT INTO corrections (entity, entity_id, field, original_value, new_value, reason, requested_by)
         VALUES ('PaymentEntry',$1,$2,$3,$4,$5,$6) RETURNING *`,
        [entry.id, field, String(entry[field]), newValue, reason, caller.user.id],
      );
      await writeAudit(client, caller, { action: 'correction.request', entity: 'Correction', entityId: r.rows[0].id, before: { [field]: entry[field] }, after: { [field]: newValue }, reason, scopeType: 'Group', scopeId: group.id });
      return { correction: r.rows[0] };
    });
  });
}

export async function approveCorrection(caller, input) {
  return tx(async (client) => {
    const c = (await client.query('SELECT * FROM corrections WHERE id=$1 FOR UPDATE', [input.correctionId])).rows[0];
    if (!c) throw notFound('Correction not found.');
    if (c.status !== 'pending') throw badRequest('This correction is not pending.');
    if (c.requested_by === caller.user.id) throw forbidden('You cannot approve your own correction.');
    const entry = (await client.query('SELECT * FROM payment_entries WHERE id=$1', [c.entity_id])).rows[0];
    if (!entry) throw notFound('Payment entry not found.');
    const group = await groupOr404(entry.group_id, client);
    requireRole(caller, ['verifier'], { groupId: group.id, branchId: group.branch_id });
    const value = c.field === 'amount' ? amt(c.new_value) : c.new_value;
    await client.query(`UPDATE payment_entries SET ${c.field}=$2 WHERE id=$1`, [entry.id, value]);
    await client.query("UPDATE corrections SET status='approved', approved_by=$2, applied_at=now() WHERE id=$1", [c.id, caller.user.id]);
    await writeAudit(client, caller, { action: 'correction.approve', entity: 'Correction', entityId: c.id, before: { [c.field]: c.original_value }, after: { [c.field]: c.new_value }, reason: c.reason, scopeType: 'Group', scopeId: group.id });
    return { correction: { ...c, status: 'approved' } };
  });
}

export async function memberPaymentView(caller) {
  const person = (await q('SELECT * FROM persons WHERE linked_user_id=$1', [caller.user.id])).rows[0];
  if (!person) return [];
  return (await q(
    `SELECT e.id, e.amount, e.category, e.collection_date, g.name AS group_name,
       CASE
         WHEN e.status='draft' THEN 'Pending'
         WHEN e.status='locked' AND s.status='posting_pending' THEN 'Posting pending'
         WHEN e.status='locked' THEN 'Pending'
         WHEN e.status='verified' THEN 'Verified'
         WHEN e.status='posted' THEN 'Verified'
         WHEN e.status='returned' THEN 'Returned'
         WHEN e.status='disputed' THEN 'Disputed'
         ELSE e.status END AS chip,
       e.status, s.status AS submission_status
     FROM payment_entries e JOIN groups g ON g.id=e.group_id
     LEFT JOIN group_submissions s ON s.id=e.submission_id
     WHERE e.member_person_id=$1 ORDER BY e.collection_date DESC LIMIT 100`,
    [person.id],
  )).rows;
}
