import express from 'express';
import { q } from '../db.js';
import { requireCaller, hasRole, isAdmin, requireRole } from '../authz.js';
import { asyncHandler } from '../util.js';
import { forbidden } from '../errors.js';
import { requireStepUp } from '../auth.js';
import { groupOr404 } from '../services/common.js';
import {
  allocateReceipt, approveCorrection, memberPaymentView, postToLedger, recordPaymentEntry,
  recordReceipt, requestCorrection, resolveDifference, submitGroup, verifySubmission,
} from '../services/payments.js';
import { canAccessGroup, visibleGroups } from './groups.js';

export const router = express.Router();

router.get('/payments/entries', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  const group = await groupOr404(req.query.groupId);
  if (!(await canAccessGroup(caller, group))) throw forbidden();
  const financial = hasRole(caller, ['treasurer', 'verifier', 'chairperson', 'field_officer', 'management', 'auditor'], { groupId: group.id, branchId: group.branch_id }) || isAdmin(caller);
  let ownPerson = null;
  if (!financial) {
    ownPerson = (await q('SELECT id FROM persons WHERE linked_user_id=$1', [caller.user.id])).rows[0];
    if (!ownPerson) throw forbidden();
  }
  const rows = (await q(
    `SELECT e.*, p.full_name, s.status AS submission_status FROM payment_entries e
     JOIN persons p ON p.id=e.member_person_id LEFT JOIN group_submissions s ON s.id=e.submission_id
     WHERE e.group_id=$1 ${financial ? '' : 'AND e.member_person_id=$2'}
     ORDER BY e.collection_date DESC, e.created_at DESC LIMIT 300`,
    financial ? [group.id] : [group.id, ownPerson.id],
  )).rows;
  res.json({ entries: rows, canRecord: hasRole(caller, ['treasurer', 'field_officer'], { groupId: group.id, branchId: group.branch_id }) });
}));

router.post('/payments/entries', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  const result = await recordPaymentEntry(caller, req.body);
  res.status(201).json(result);
}));

router.post('/payments/submissions', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  const result = await submitGroup(caller, req.body);
  res.status(201).json(result);
}));

router.get('/payments/submissions', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  if (!(hasRole(caller, ['treasurer', 'verifier', 'chairperson', 'field_officer', 'management', 'auditor']) || isAdmin(caller))) throw forbidden('Only group officers and finance roles can list submissions.');
  const groups = await visibleGroups(caller, {});
  const ids = groups.map((g) => g.id);
  if (!ids.length) return res.json({ submissions: [] });
  const rows = (await q(
    `SELECT s.*, g.name AS group_name, u.full_name AS submitted_by_name FROM group_submissions s
     JOIN groups g ON g.id=s.group_id JOIN users u ON u.id=s.submitted_by
     WHERE s.group_id = ANY($1::uuid[]) ${req.query.groupId ? 'AND s.group_id=$2' : ''} ORDER BY s.submitted_at DESC LIMIT 100`,
    req.query.groupId ? [ids, req.query.groupId] : [ids],
  )).rows;
  const out = [];
  for (const s of rows) {
    const receipts = (await q('SELECT * FROM receipts WHERE submission_id=$1 ORDER BY created_at', [s.id])).rows;
    const allocations = (await q('SELECT * FROM allocations WHERE submission_id=$1 ORDER BY created_at', [s.id])).rows;
    const postings = (await q('SELECT * FROM ledger_postings WHERE submission_id=$1 ORDER BY created_at', [s.id])).rows;
    out.push({ ...s, receipts, allocations, postings });
  }
  res.json({ submissions: out, canVerify: hasRole(caller, ['verifier']) });
}));

router.get('/payments/submissions/:id', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  const s = (await q('SELECT * FROM group_submissions WHERE id=$1', [req.params.id])).rows[0];
  if (!s) throw forbidden('Submission not found.');
  const group = await groupOr404(s.group_id);
  if (!(await canAccessGroup(caller, group))) throw forbidden();
  if (!(hasRole(caller, ['treasurer', 'verifier', 'chairperson', 'field_officer', 'management', 'auditor'], { groupId: group.id, branchId: group.branch_id }) || isAdmin(caller))) throw forbidden('Only group officers and finance roles can open a submission.');
  const [entries, receipts, allocations, postings] = await Promise.all([
    q('SELECT e.*, p.full_name FROM payment_entries e JOIN persons p ON p.id=e.member_person_id WHERE e.submission_id=$1 ORDER BY p.full_name', [s.id]),
    q('SELECT * FROM receipts WHERE submission_id=$1', [s.id]),
    q('SELECT * FROM allocations WHERE submission_id=$1', [s.id]),
    q('SELECT * FROM ledger_postings WHERE submission_id=$1 ORDER BY created_at', [s.id]),
  ]);
  res.json({ submission: s, group, entries: entries.rows, receipts: receipts.rows, allocations: allocations.rows, postings: postings.rows });
}));

router.post('/payments/receipts', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  const result = await recordReceipt(caller, req.body);
  res.status(201).json(result);
}));

router.post('/payments/submissions/:id/verify', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  requireStepUp(caller);
  const result = await verifySubmission(caller, { submissionId: req.params.id });
  res.json(result);
}));

router.post('/payments/submissions/:id/resolve', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  requireStepUp(caller);
  const result = await resolveDifference(caller, { ...req.body, submissionId: req.params.id });
  res.json(result);
}));

router.post('/payments/allocations', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  const result = await allocateReceipt(caller, req.body);
  res.status(201).json(result);
}));

router.post('/payments/submissions/:id/post', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  requireStepUp(caller);
  const result = await postToLedger(caller, { submissionId: req.params.id });
  res.json(result);
}));

router.post('/payments/corrections', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  const result = await requestCorrection(caller, req.body);
  res.status(201).json(result);
}));

router.post('/payments/corrections/:id/approve', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  requireStepUp(caller);
  const result = await approveCorrection(caller, { correctionId: req.params.id });
  res.json(result);
}));

router.get('/payments/corrections', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  if (!(hasRole(caller, ['verifier', 'treasurer']) || isAdmin(caller))) throw forbidden();
  const rows = (await q(
    `SELECT c.*, u.full_name AS requested_by_name FROM corrections c JOIN users u ON u.id=c.requested_by ORDER BY c.created_at DESC LIMIT 100`,
  )).rows;
  res.json({ corrections: rows });
}));

router.get('/payments/queues', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  if (!hasRole(caller, ['verifier'])) throw forbidden();
  const [subs, exceptions, receipts, corrections] = await Promise.all([
    q(`SELECT s.*, g.name AS group_name FROM group_submissions s JOIN groups g ON g.id=s.group_id
       WHERE s.status IN ('submitted','difference','verified','posting_pending') ORDER BY s.submitted_at`),
    q(`SELECT e.*, u.full_name AS owner_name FROM finance_exceptions e JOIN users u ON u.id=e.owner_user_id WHERE e.status='open' ORDER BY e.deadline`),
    q(`SELECT r.*, g.name AS group_name, (SELECT COALESCE(SUM(a.amount),0) FROM allocations a WHERE a.receipt_id=r.id) AS allocated FROM receipts r JOIN groups g ON g.id=r.group_id ORDER BY r.receipt_date DESC LIMIT 100`),
    q(`SELECT c.*, u.full_name AS requested_by_name FROM corrections c JOIN users u ON u.id=c.requested_by WHERE c.status='pending'`),
  ]);
  res.json({ submissions: subs.rows, exceptions: exceptions.rows, receipts: receipts.rows, corrections: corrections.rows });
}));

router.get('/payments/ledger', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  const group = await groupOr404(req.query.groupId);
  if (!(await canAccessGroup(caller, group))) throw forbidden();
  if (!(hasRole(caller, ['treasurer', 'verifier', 'chairperson', 'field_officer', 'management', 'auditor'], { groupId: group.id, branchId: group.branch_id }) || isAdmin(caller))) throw forbidden();
  const account = (await q('SELECT * FROM mock_ledger_accounts WHERE group_id=$1', [group.id])).rows[0] || null;
  const postings = (await q(
    `SELECT p.* FROM ledger_postings p JOIN group_submissions s ON s.id=p.submission_id WHERE s.group_id=$1 ORDER BY p.created_at DESC`, [group.id],
  )).rows;
  res.json({ account, postings });
}));

router.get('/payments/me', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  res.json({ payments: await memberPaymentView(caller) });
}));
