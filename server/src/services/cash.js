import { q, tx } from '../db.js';
import { badRequest, forbidden, notFound } from '../errors.js';
import { str, today } from '../util.js';
import { writeAudit } from '../audit.js';
import { groupOr404 } from './common.js';
import { hasRole, requireRole } from '../authz.js';

async function loadReceipt(client, receiptId) {
  const receipt = (await client.query('SELECT * FROM receipts WHERE id=$1', [receiptId])).rows[0];
  if (!receipt) throw notFound('Receipt not found.');
  if (receipt.channel !== 'cash') throw badRequest('Only a cash receipt goes through handover and deposit reconciliation.');
  const group = await groupOr404(receipt.group_id, client);
  return { receipt, group };
}

async function ensureRow(client, receipt) {
  const existing = (await client.query('SELECT * FROM cash_reconciliations WHERE receipt_id=$1 FOR UPDATE', [receipt.id])).rows[0];
  if (existing) return existing;
  return (await client.query(
    'INSERT INTO cash_reconciliations (receipt_id, group_id, amount) VALUES ($1,$2,$3) RETURNING *',
    [receipt.id, receipt.group_id, receipt.amount],
  )).rows[0];
}

/** Step 1: the treasurer (or officer) records that the cash left their hands. */
export async function handOverCash(caller, receiptId, input = {}) {
  return tx(async (client) => {
    const { receipt, group } = await loadReceipt(client, receiptId);
    const treasurer = hasRole(caller, ['treasurer'], { groupId: group.id, branchId: group.branch_id });
    const officer = hasRole(caller, ['field_officer'], { groupId: group.id, branchId: group.branch_id });
    if (!treasurer && !officer) throw forbidden('Only the group treasurer or an assigned officer can hand over cash.');
    const row = await ensureRow(client, receipt);
    if (row.handover_by) throw badRequest('This cash has already been handed over.');
    const updated = (await client.query(
      "UPDATE cash_reconciliations SET handover_by=$2, handover_at=now(), status='awaiting_receipt' WHERE id=$1 RETURNING *",
      [row.id, caller.user.id],
    )).rows[0];
    await writeAudit(client, caller, {
      action: 'cash.handover', entity: 'CashReconciliation', entityId: row.id,
      after: { receiptId: receipt.id, amount: Number(receipt.amount), note: str(input.note, 'Note', { optional: true, max: 300 }) },
      scopeType: 'Group', scopeId: group.id,
    });
    return { reconciliation: updated };
  });
}

/** Step 2: a different person confirms the branch received the cash. */
export async function receiveCash(caller, receiptId) {
  return tx(async (client) => {
    const { receipt, group } = await loadReceipt(client, receiptId);
    requireRole(caller, ['verifier', 'management'], { groupId: group.id, branchId: group.branch_id });
    const row = await ensureRow(client, receipt);
    if (!row.handover_by) throw badRequest('Cash must be handed over before it can be received.');
    if (row.received_by) throw badRequest('This cash has already been received.');
    if (row.handover_by === caller.user.id) throw forbidden('The person receiving cash must differ from the person handing it over.');
    const updated = (await client.query(
      "UPDATE cash_reconciliations SET received_by=$2, received_at=now(), status='awaiting_deposit' WHERE id=$1 RETURNING *",
      [row.id, caller.user.id],
    )).rows[0];
    await writeAudit(client, caller, {
      action: 'cash.receive', entity: 'CashReconciliation', entityId: row.id,
      after: { receiptId: receipt.id, amount: Number(receipt.amount) }, scopeType: 'Group', scopeId: group.id,
    });
    return { reconciliation: updated };
  });
}

/** Step 3: a third person confirms the bank deposit with its reference. */
export async function confirmDeposit(caller, receiptId, input) {
  return tx(async (client) => {
    const { receipt, group } = await loadReceipt(client, receiptId);
    requireRole(caller, ['verifier', 'management'], { groupId: group.id, branchId: group.branch_id });
    const row = await ensureRow(client, receipt);
    if (!row.received_by) throw badRequest('Cash must be received by the branch before the deposit is confirmed.');
    if (row.deposited_by) throw badRequest('This deposit has already been confirmed.');
    if (row.handover_by === caller.user.id || row.received_by === caller.user.id) {
      throw forbidden('The deposit must be confirmed by a third person.');
    }
    const bankRef = str(input.bankRef, 'Bank reference', { min: 3, max: 120 });
    const evidenceRef = str(input.evidenceRef, 'Deposit evidence', { optional: true, max: 200 });
    const updated = (await client.query(
      `UPDATE cash_reconciliations SET deposited_by=$2, deposited_at=now(), bank_ref=$3, evidence_ref=$4, status='reconciled'
       WHERE id=$1 RETURNING *`,
      [row.id, caller.user.id, bankRef, evidenceRef],
    )).rows[0];
    await writeAudit(client, caller, {
      action: 'cash.deposit', entity: 'CashReconciliation', entityId: row.id,
      after: { receiptId: receipt.id, bankRef, amount: Number(receipt.amount) }, scopeType: 'Group', scopeId: group.id,
    });
    return { reconciliation: updated };
  });
}

export async function listCash(caller, groupId) {
  const group = await groupOr404(groupId);
  const allowed = hasRole(caller, ['treasurer', 'verifier', 'chairperson', 'field_officer', 'management', 'auditor'], { groupId: group.id, branchId: group.branch_id });
  if (!allowed) throw forbidden();
  const rows = (await q(
    `SELECT c.*, r.source_ref, r.receipt_date,
            h.full_name AS handover_by_name, rc.full_name AS received_by_name, d.full_name AS deposited_by_name
     FROM cash_reconciliations c
     JOIN receipts r ON r.id=c.receipt_id
     LEFT JOIN users h ON h.id=c.handover_by
     LEFT JOIN users rc ON rc.id=c.received_by
     LEFT JOIN users d ON d.id=c.deposited_by
     WHERE c.group_id=$1 ORDER BY c.created_at DESC LIMIT 100`,
    [group.id],
  )).rows;
  const unreconciled = (await q(
    `SELECT r.id, r.amount, r.receipt_date, r.source_ref FROM receipts r
     WHERE r.group_id=$1 AND r.channel='cash' AND NOT EXISTS (SELECT 1 FROM cash_reconciliations c WHERE c.receipt_id=r.id)
     ORDER BY r.receipt_date DESC LIMIT 100`,
    [group.id],
  )).rows;
  return { reconciliations: rows, unreconciled, today: today() };
}
