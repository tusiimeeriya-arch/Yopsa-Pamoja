import express from 'express';
import { q } from '../db.js';
import { requireCaller, hasRole, isAdmin } from '../authz.js';
import { asyncHandler } from '../util.js';
import { forbidden, notFound } from '../errors.js';
import { requireStepUp } from '../auth.js';
import { changeLoanMaterial, createLoanApplication, performanceProfile, recordDisbursement, transitionLoan } from '../services/loans.js';
import { canAccessGroup, visibleGroups } from './groups.js';

export const router = express.Router();

router.get('/loans', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  const groups = await visibleGroups(caller, {});
  const ids = groups.map((g) => g.id);
  if (!ids.length) return res.json({ loans: [] });
  const rows = (await q(
    `SELECT l.*, g.name AS group_name, b.name AS branch_name FROM loan_applications l
     JOIN groups g ON g.id=l.group_id JOIN org_branches b ON b.id=g.branch_id
     WHERE l.group_id = ANY($1::uuid[]) ORDER BY l.updated_at DESC`, [ids],
  )).rows;
  res.json({ loans: rows, groups: groups.filter((g) => g.status === 'Active').map((g) => ({ id: g.id, name: g.name, type: g.type })) });
}));

router.post('/loans', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  const result = await createLoanApplication(caller, req.body);
  res.status(201).json(result);
}));

router.get('/loans/:id', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  const loan = (await q('SELECT * FROM loan_applications WHERE id=$1', [req.params.id])).rows[0];
  if (!loan) throw notFound('Loan application not found.');
  const group = (await q('SELECT * FROM groups WHERE id=$1', [loan.group_id])).rows[0];
  if (!(await canAccessGroup(caller, group))) throw forbidden();
  const [steps, guarantors, disbursements, profile, ledger] = await Promise.all([
    q('SELECT s.*, u.full_name AS actor_name FROM approval_steps s JOIN users u ON u.id=s.actor_user_id WHERE s.loan_id=$1 ORDER BY s.created_at', [loan.id]),
    q('SELECT g.*, p.full_name FROM guarantors g LEFT JOIN persons p ON p.id=g.person_id WHERE g.loan_id=$1', [loan.id]),
    q('SELECT d.*, u.full_name AS confirmed_by_name FROM disbursements d JOIN users u ON u.id=d.confirmed_by WHERE d.loan_id=$1', [loan.id]),
    performanceProfile(group.id),
    q('SELECT * FROM mock_ledger_accounts WHERE group_id=$1', [group.id]),
  ]);
  res.json({
    loan, group, steps: steps.rows, guarantors: guarantors.rows, disbursements: disbursements.rows,
    profile, ledger: ledger.rows[0] || null,
    permissions: {
      canTransition: hasRole(caller, ['credit_approver', 'field_officer', 'chairperson', 'verifier'], { groupId: group.id, branchId: group.branch_id }),
      isChairperson: hasRole(caller, ['chairperson'], { groupId: group.id, branchId: group.branch_id }),
      isCreditApprover: hasRole(caller, ['credit_approver'], { groupId: group.id, branchId: group.branch_id }),
      isVerifier: hasRole(caller, ['verifier'], { groupId: group.id, branchId: group.branch_id }),
    },
  });
}));

router.post('/loans/:id/transition', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  if (['approve', 'decline'].includes(req.body.action)) requireStepUp(caller);
  const result = await transitionLoan(caller, req.params.id, req.body);
  res.json(result);
}));

router.post('/loans/:id/disburse', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  requireStepUp(caller);
  const result = await recordDisbursement(caller, req.params.id, req.body);
  res.status(201).json(result);
}));

router.patch('/loans/:id', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  const result = await changeLoanMaterial(caller, req.params.id, req.body);
  res.json(result);
}));

router.get('/groups/:id/performance', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  const group = (await q('SELECT * FROM groups WHERE id=$1', [req.params.id])).rows[0];
  if (!group) throw notFound('Group not found.');
  if (!(await canAccessGroup(caller, group))) throw forbidden();
  res.json(await performanceProfile(group.id));
}));
