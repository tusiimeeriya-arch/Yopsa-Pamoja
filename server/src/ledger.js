import { q } from './db.js';

/**
 * LedgerAdapter: the only place Pamoja talks to a core ledger. The mock keeps
 * balances in mock_ledger_accounts so the real Alogsys adapter can replace it
 * without touching payment or loan code.
 */
export const LedgerAdapter = {
  async getBalances(groupId) {
    const r = await q('SELECT * FROM mock_ledger_accounts WHERE group_id=$1', [groupId]);
    const a = r.rows[0];
    if (!a) return { principal: 0, interestDue: 0, feesDue: 0, found: false };
    return { principal: Number(a.principal), interestDue: Number(a.interest_due), feesDue: Number(a.fees_due), found: true };
  },
  async getSchedule(groupId) {
    const r = await q('SELECT schedule FROM mock_ledger_accounts WHERE group_id=$1', [groupId]);
    return r.rows[0]?.schedule || [];
  },
  async postRepayment({ groupId, amount, reference }) {
    const account = await q('SELECT * FROM mock_ledger_accounts WHERE group_id=$1 FOR UPDATE', [groupId]);
    if (!account.rowCount) throw new Error('No ledger account for this group.');
    const schedule = account.rows[0].schedule || [];
    const fail = schedule.some((s) => s.failNextPosting) || process.env.LEDGER_FORCE_FAIL === '1';
    if (fail) throw new Error('Ledger unavailable: posting rejected by the core ledger.');
    const principal = Math.max(0, Number(account.rows[0].principal) - amount);
    await q('UPDATE mock_ledger_accounts SET principal=$2, updated_at=now() WHERE group_id=$1', [groupId, principal]);
    return { ledgerRef: 'MOCK-' + reference.slice(0, 8).toUpperCase(), principalAfter: principal };
  },
  async confirmPosting(reference) {
    return { reference, confirmed: true };
  },
  async listDisbursements(groupId) {
    return (await q(
      `SELECT d.*, l.group_id FROM disbursements d JOIN loan_applications l ON l.id=d.loan_id WHERE l.group_id=$1`,
      [groupId],
    )).rows;
  },
};
