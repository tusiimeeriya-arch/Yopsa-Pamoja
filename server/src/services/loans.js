import { q, tx } from '../db.js';
import { policy } from '../config.js';
import { LedgerAdapter } from '../ledger.js';
import { badRequest, forbidden, notFound } from '../errors.js';
import { amount as amt, intValue, isoDate, oneOf, str, today } from '../util.js';
import { withIdempotency } from '../idempotency.js';
import { writeAudit } from '../audit.js';
import { activeLeadership, groupOr404, notify, createTask } from './common.js';
import { hasRole, requireGroupRole, requireRole } from '../authz.js';

const PRODUCTS = ['group_loan'];
const TRANSITIONS = {
  submit: 'GroupAuthorised',
  appraise: 'OfficerAppraisal',
  operations_review: 'OperationsReview',
  approve: 'Approved',
  decline: 'Declined',
  offer: 'OfferIssued',
  accept: 'GroupAccepted',
  withdraw: 'Withdrawn',
  return: 'Returned',
};

async function loadLoan(client, id) {
  const loan = (await client.query('SELECT * FROM loan_applications WHERE id=$1 FOR UPDATE', [id])).rows[0];
  if (!loan) throw notFound('Loan application not found.');
  return loan;
}

async function authorisedRep(caller, groupId, client = { query: q }) {
  const leaders = await activeLeadership(groupId, ['chairperson', 'authorised_representative', 'Chairperson', 'Authorised Representative'], today(), client);
  const match = leaders.find((l) => l.linked_user_id === caller.user.id);
  if (!match) throw forbidden('Only the group chairperson or an authorised representative with valid authority can act for the group on a loan.');
  return match;
}

export async function createLoanApplication(caller, input) {
  return tx(async (client) => {
    const group = await groupOr404(input.groupId, client);
    const rep = await authorisedRep(caller, group.id, client);
    if (group.status !== 'Active') throw badRequest('Only an active group can apply for a loan.');
    const limits = (await policy('loans.product_limits'))[group.type];
    if (!limits) throw badRequest('No loan product is configured for this group type.');
    const value = amt(input.amount);
    if (value < limits.min || value > limits.max) {
      throw badRequest(`For ${group.type}, the loan amount must be between UGX ${limits.min.toLocaleString()} and UGX ${limits.max.toLocaleString()}.`);
    }
    const term = intValue(input.termMonths, 'Term (months)', { min: 1, max: limits.term });
    const purpose = str(input.purpose, 'Purpose', { min: 5, max: 1000 });
    const repaymentSource = str(input.repaymentSource, 'Repayment source', { min: 3, max: 500 });
    const product = oneOf(input.product || 'group_loan', PRODUCTS, 'Product');
    return withIdempotency(client, caller, input.idempotencyKey, 'createLoanApplication', { groupId: group.id, amount: value, term, purpose }, async () => {
      const loan = (await client.query(
        `INSERT INTO loan_applications (group_id, product, amount, purpose, term_months, repayment_source, existing_debts,
           cash_flow, member_allocation, security, guarantees, authorised_person_id, resolution_ref, quorum_evidence_ref, created_by)
         VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15) RETURNING *`,
        [group.id, product, value, purpose, term, repaymentSource, Number(input.existingDebts || 0),
          str(input.cashFlow, 'Cash flow', { optional: true, max: 1000 }), str(input.memberAllocation, 'Member allocation', { optional: true, max: 1000 }),
          str(input.security, 'Security', { optional: true, max: 1000 }), str(input.guarantees, 'Guarantees', { optional: true, max: 1000 }),
          rep.person_id, input.resolutionRef || null, input.quorumEvidenceRef || null, caller.user.id],
      )).rows[0];
      if (input.guarantors?.length) {
        for (const g of input.guarantors) {
          await client.query('INSERT INTO guarantors (loan_id, person_id, amount, evidence_ref) VALUES ($1,$2,$3,$4)',
            [loan.id, g.personId || null, amt(g.amount, 'Guarantee amount'), str(g.evidenceRef, 'Guarantee evidence', { optional: true, max: 200 })]);
        }
      }
      await writeAudit(client, caller, { action: 'loan.create', entity: 'LoanApplication', entityId: loan.id, after: { amount: value, term, status: 'Draft' }, scopeType: 'Group', scopeId: group.id });
      return { loan };
    });
  });
}

export async function transitionLoan(caller, loanId, input) {
  return tx(async (client) => {
    const loan = await loadLoan(client, loanId);
    const group = await groupOr404(loan.group_id, client);
    const action = oneOf(input.action, Object.keys(TRANSITIONS), 'Action');
    const next = TRANSITIONS[action];
    const reason = str(input.reason, 'Reason', { optional: true, max: 500 });
    const guard = (ok, msg) => { if (!ok) throw badRequest(msg); };
    const opsStep = await client.query("SELECT 1 FROM approval_steps WHERE loan_id=$1 AND step='operations_review' AND decision='reviewed'", [loan.id]);

    if (action === 'submit') {
      await authorisedRep(caller, group.id, client);
      guard(loan.status === 'Draft', 'Only a draft application can be submitted.');
      if (!loan.resolution_ref || !loan.quorum_evidence_ref) throw badRequest('A borrowing resolution and quorum evidence are required before submission.');
    } else if (action === 'appraise') {
      requireRole(caller, ['field_officer'], { groupId: group.id, branchId: group.branch_id });
      guard(loan.status === 'GroupAuthorised', 'Only a group-authorised application can be appraised.');
    } else if (action === 'operations_review') {
      requireRole(caller, ['credit_approver'], { groupId: group.id, branchId: group.branch_id });
      if (loan.created_by === caller.user.id) throw forbidden('You cannot review an application you created.');
      guard(loan.status === 'OfficerAppraisal', 'Operations review follows the officer appraisal.');
    } else if (action === 'approve' || action === 'decline') {
      requireRole(caller, ['credit_approver'], { groupId: group.id, branchId: group.branch_id });
      if (loan.created_by === caller.user.id) throw forbidden('You cannot approve your own application.');
      if (!opsStep.rowCount) throw forbidden('Operations review is mandatory and has not been completed.');
      const reviewer = (await client.query("SELECT actor_user_id FROM approval_steps WHERE loan_id=$1 AND step='operations_review' ORDER BY created_at DESC LIMIT 1", [loan.id])).rows[0];
      if (reviewer?.actor_user_id === caller.user.id) throw forbidden('The Operations reviewer cannot also give the final approval.');
      guard(loan.status === 'OperationsReview', 'This application is not awaiting a final decision.');
      if (action === 'approve') {
        const threshold = Number(await policy('loans.guarantor_threshold_ugx'));
        if (Number(loan.amount) > threshold) {
          const g = await client.query('SELECT COALESCE(SUM(amount),0) AS total FROM guarantors WHERE loan_id=$1', [loan.id]);
          if (Number(g.rows[0].total) < Number(loan.amount)) throw badRequest(`A guarantor covering the loan is required above UGX ${threshold.toLocaleString()}.`);
        }
      }
    } else if (action === 'offer') {
      requireRole(caller, ['credit_approver', 'management'], { groupId: group.id, branchId: group.branch_id });
      guard(loan.status === 'Approved', 'An offer follows approval.');
    } else if (action === 'accept') {
      await authorisedRep(caller, group.id, client);
      guard(loan.status === 'OfferIssued', 'There is no offer to accept.');
    } else if (action === 'withdraw') {
      await authorisedRep(caller, group.id, client);
      guard(!['Disbursed', 'Withdrawn'].includes(loan.status), 'A disbursed loan cannot be withdrawn.');
    } else if (action === 'return') {
      requireRole(caller, ['credit_approver', 'field_officer'], { groupId: group.id, branchId: group.branch_id });
      guard(!['Disbursed'].includes(loan.status), 'A disbursed loan cannot be returned.');
    }

    const updates = { status: next };
    if (action === 'approve' || action === 'decline') { updates.approved_by = caller.user.id; updates.approved_at_sql = 'now()'; }
    if (action === 'offer') updates.offered_at_sql = 'now()';
    if (action === 'accept') updates.accepted_at_sql = 'now()';
    const cols = [];
    const values = [];
    for (const [k, v] of Object.entries(updates)) {
      if (k.endsWith('_sql')) { cols.push(`${k.replace('_sql', '')}=${v}`); } else { values.push(v); cols.push(`${k}=$${values.length + 1}`); }
    }
    cols.push('updated_at=now()');
    await client.query(`UPDATE loan_applications SET ${cols.join(', ')} WHERE id=$1`, [loan.id, ...values]);
    await client.query(
      `INSERT INTO approval_steps (loan_id, step, actor_user_id, decision, reason) VALUES ($1,$2,$3,$4,$5)`,
      [loan.id, action, caller.user.id, action === 'decline' ? 'declined' : action === 'operations_review' ? 'reviewed' : 'recorded', reason],
    );
    await writeAudit(client, caller, { action: `loan.${action}`, entity: 'LoanApplication', entityId: loan.id, before: { status: loan.status }, after: { status: next }, reason, scopeType: 'Group', scopeId: group.id });
    return { loan: { ...loan, status: next } };
  });
}

export async function recordDisbursement(caller, loanId, input) {
  return tx(async (client) => {
    const loan = await loadLoan(client, loanId);
    const group = await groupOr404(loan.group_id, client);
    requireRole(caller, ['verifier'], { groupId: group.id, branchId: group.branch_id });
    if (loan.approved_by === caller.user.id) throw forbidden('The approver cannot also disburse the loan.');
    if (loan.status !== 'GroupAccepted') throw badRequest('Only an accepted offer can be disbursed.');
    const ops = await client.query("SELECT 1 FROM approval_steps WHERE loan_id=$1 AND step='operations_review'", [loan.id]);
    if (!ops.rowCount) throw forbidden('Disbursement requires a completed Operations review.');
    const value = amt(input.amount, 'Disbursement amount');
    if (value > Number(loan.amount)) throw badRequest('The disbursement cannot exceed the approved amount.');
    const disbursedOn = isoDate(input.disbursedOn || today(), 'Disbursement date');
    return withIdempotency(client, caller, input.idempotencyKey, 'recordDisbursement', { loanId: loan.id, amount: value, disbursedOn }, async () => {
      const d = (await client.query(
        `INSERT INTO disbursements (loan_id, amount, disbursed_on, confirmed_by, ledger_ref) VALUES ($1,$2,$3,$4,$5) RETURNING *`,
        [loan.id, value, disbursedOn, caller.user.id, 'MOCK-DISB-' + loan.id.slice(0, 6).toUpperCase()],
      )).rows[0];
      await client.query("UPDATE loan_applications SET status='Disbursed', disbursed_at=now(), updated_at=now() WHERE id=$1", [loan.id]);
      await client.query(
        `INSERT INTO mock_ledger_accounts (group_id, principal) VALUES ($1,$2)
         ON CONFLICT (group_id) DO UPDATE SET principal = mock_ledger_accounts.principal + $2, updated_at=now()`,
        [group.id, value],
      );
      await client.query(
        `INSERT INTO approval_steps (loan_id, step, actor_user_id, decision, reason) VALUES ($1,'disburse',$2,'confirmed',$3)`,
        [loan.id, caller.user.id, 'Disbursement confirmed by Finance'],
      );
      await writeAudit(client, caller, { action: 'loan.disburse', entity: 'Disbursement', entityId: d.id, after: { amount: value, loanId: loan.id }, scopeType: 'Group', scopeId: group.id });
      return { disbursement: d };
    });
  });
}

export async function changeLoanMaterial(caller, loanId, input) {
  return tx(async (client) => {
    const loan = await loadLoan(client, loanId);
    const group = await groupOr404(loan.group_id, client);
    await authorisedRep(caller, group.id, client);
    if (['Disbursed', 'Declined', 'Withdrawn'].includes(loan.status)) throw badRequest('This application can no longer change.');
    const changes = {};
    if (input.amount !== undefined) {
      const limits = (await policy('loans.product_limits'))[group.type];
      const v = amt(input.amount);
      if (v < limits.min || v > limits.max) throw badRequest('The amount is outside the configured product limits.');
      changes.amount = v;
    }
    if (input.termMonths !== undefined) changes.term_months = intValue(input.termMonths, 'Term (months)', { min: 1, max: 24 });
    if (input.purpose !== undefined) changes.purpose = str(input.purpose, 'Purpose', { min: 5, max: 1000 });
    if (!Object.keys(changes).length) throw badRequest('No material change was supplied.');
    const material = ['Approved', 'OfferIssued', 'GroupAccepted', 'OperationsReview', 'OfficerAppraisal', 'GroupAuthorised'].includes(loan.status);
    const sets = Object.keys(changes).map((k, i) => `${k}=$${i + 2}`).join(', ');
    await client.query(`UPDATE loan_applications SET ${sets}${material ? ", status='OperationsReview', approved_by=NULL, approved_at=NULL, offered_at=NULL, accepted_at=NULL" : ''}, updated_at=now() WHERE id=$1`, [loan.id, ...Object.values(changes)]);
    await writeAudit(client, caller, { action: 'loan.material_change', entity: 'LoanApplication', entityId: loan.id, before: { amount: loan.amount, term: loan.term_months, purpose: loan.purpose, status: loan.status }, after: { ...changes, status: material ? 'OperationsReview' : loan.status }, reason: input.reason || 'Material change', scopeType: 'Group', scopeId: group.id });
    return { status: material ? 'OperationsReview' : loan.status, requiresReapproval: material };
  });
}

export async function performanceProfile(groupId, client = { query: q }) {
  const group = await groupOr404(groupId, client);
  const ledger = await LedgerAdapter.getBalances(groupId);
  const disbursed = await client.query('SELECT COUNT(*)::int AS count, COALESCE(SUM(d.amount),0) AS total FROM disbursements d JOIN loan_applications l ON l.id=d.loan_id WHERE l.group_id=$1', [groupId]);
  const attendance = await (await import('./attendance.js')).attendanceSummary(groupId, { client });
  const disputes = await client.query('SELECT COUNT(*)::int AS count FROM disputes WHERE group_id=$1 AND status=\'open\'', [groupId]);
  const submissions = await client.query("SELECT COUNT(*)::int AS total, COUNT(*) FILTER (WHERE status='posted')::int AS posted FROM group_submissions WHERE group_id=$1", [groupId]);
  const arrears = await client.query(
    `SELECT COALESCE(SUM(GREATEST(0, EXTRACT(DAY FROM now() - s.due_date)::int)),0) AS days
     FROM mock_ledger_accounts a CROSS JOIN LATERAL jsonb_to_recordset(a.schedule) AS s(due_date date, amount bigint, paid_date date)
     WHERE a.group_id=$1 AND s.paid_date IS NULL AND s.due_date < current_date`,
    [groupId],
  );
  return {
    group: { id: group.id, name: group.name, type: group.type, status: group.status },
    verifiedRepaymentBehaviour: { postedSubmissions: submissions.rows[0].posted, totalSubmissions: submissions.rows[0].total },
    attendance: { percent: attendance.group.percent, display: attendance.group.display },
    completedCycles: disbursed.rows[0].count,
    arrearsDays: Number(arrears.rows[0].days),
    openDisputes: disputes.rows[0].count,
    outstandingPrincipal: ledger.principal,
    dataCompleteness: { hasMeetings: attendance.group.expected > 0, hasLedger: ledger.found, hasDisbursement: disbursed.rows[0].count > 0 },
    note: 'Transparent performance profile. This is not a numeric credit score.',
  };
}
