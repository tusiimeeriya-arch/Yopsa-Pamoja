import { q, tx } from '../db.js';
import { badRequest, forbidden, notFound } from '../errors.js';
import { amount as amt, isoDate, str, today } from '../util.js';
import { writeAudit } from '../audit.js';
import { groupOr404, membershipAt } from './common.js';
import { hasRole } from '../authz.js';

/**
 * Internal member-to-member loans.
 *
 * This is a separate record from the Yopsa group loan workflow: it never
 * creates a loan_application, a disbursement or a ledger posting, so it can
 * never be counted in "loans advanced".
 */
export async function createMemberLoan(caller, input) {
  return tx(async (client) => {
    const group = await groupOr404(input.groupId, client);
    const allowed = hasRole(caller, ['treasurer', 'secretary', 'chairperson', 'field_officer'], { groupId: group.id, branchId: group.branch_id });
    if (!allowed) throw forbidden('Only a group officer can record an internal member-to-member loan.');
    if (group.status !== 'Active') throw badRequest('Internal member loans can only be recorded for an active group.');
    const lenderPersonId = str(input.lenderPersonId, 'Lender', { max: 60 });
    const borrowerPersonId = str(input.borrowerPersonId, 'Borrower', { max: 60 });
    if (lenderPersonId === borrowerPersonId) throw badRequest('The lender and borrower must be different people.');
    const value = amt(input.amount, 'Loan amount');
    const agreedOn = isoDate(input.agreedOn || today(), 'Agreed on');
    const dueOn = isoDate(input.dueOn, 'Due on', { optional: true });
    if (dueOn && dueOn < agreedOn) throw badRequest('The due date cannot be before the agreed date.');
    for (const [label, personId] of [['lender', lenderPersonId], ['borrower', borrowerPersonId]]) {
      if (!(await membershipAt(personId, group.id, agreedOn, client))) throw badRequest(`The ${label} was not an active member on the agreed date.`);
    }
    const r = (await client.query(
      `INSERT INTO member_loans (group_id, lender_person_id, borrower_person_id, amount, purpose, agreed_on, due_on, witness_person_id, recorded_by)
       VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING *`,
      [group.id, lenderPersonId, borrowerPersonId, value, str(input.purpose, 'Purpose', { min: 5, max: 500 }), agreedOn, dueOn,
        input.witnessPersonId ? str(input.witnessPersonId, 'Witness', { max: 60 }) : null, caller.user.id],
    )).rows[0];
    await writeAudit(client, caller, {
      action: 'member_loan.create', entity: 'MemberLoan', entityId: r.id,
      after: { groupId: group.id, amount: value, lenderPersonId, borrowerPersonId, countedInYopsaLoans: false },
      scopeType: 'Group', scopeId: group.id,
    });
    return { memberLoan: r, note: 'Internal member-to-member loan. Not a Yopsa loan and never counted in loans advanced.' };
  });
}

export async function listMemberLoans(caller, groupId) {
  const group = await groupOr404(groupId);
  const financial = hasRole(caller, ['treasurer', 'secretary', 'chairperson', 'field_officer', 'verifier', 'management', 'auditor'], { groupId: group.id, branchId: group.branch_id });
  const person = (await q('SELECT id FROM persons WHERE linked_user_id=$1', [caller.user.id])).rows[0];
  if (!financial && !person) throw forbidden();
  const rows = (await q(
    `SELECT ml.*, lp.full_name AS lender_name, bp.full_name AS borrower_name, wp.full_name AS witness_name
     FROM member_loans ml
     JOIN persons lp ON lp.id=ml.lender_person_id
     JOIN persons bp ON bp.id=ml.borrower_person_id
     LEFT JOIN persons wp ON wp.id=ml.witness_person_id
     WHERE ml.group_id=$1 ${financial ? '' : 'AND (ml.lender_person_id=$2 OR ml.borrower_person_id=$2)'}
     ORDER BY ml.agreed_on DESC LIMIT 100`,
    financial ? [group.id] : [group.id, person.id],
  )).rows;
  return { memberLoans: rows, countedInYopsaLoans: false };
}

export async function settleMemberLoan(caller, loanId, input = {}) {
  return tx(async (client) => {
    const loan = (await client.query('SELECT * FROM member_loans WHERE id=$1 FOR UPDATE', [loanId])).rows[0];
    if (!loan) throw notFound('Internal member loan not found.');
    const group = await groupOr404(loan.group_id, client);
    const allowed = hasRole(caller, ['treasurer', 'secretary', 'chairperson', 'field_officer'], { groupId: group.id, branchId: group.branch_id });
    if (!allowed) throw forbidden('Only a group officer can settle an internal member loan.');
    const status = input.status === 'defaulted' ? 'defaulted' : 'repaid';
    if (loan.status !== 'agreed') throw badRequest('This internal loan is already closed.');
    await client.query('UPDATE member_loans SET status=$2 WHERE id=$1', [loan.id, status]);
    await writeAudit(client, caller, {
      action: 'member_loan.settle', entity: 'MemberLoan', entityId: loan.id,
      before: { status: loan.status }, after: { status }, reason: str(input.note, 'Note', { optional: true, max: 300 }),
      scopeType: 'Group', scopeId: group.id,
    });
    return { status };
  });
}
