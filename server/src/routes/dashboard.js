import express from 'express';
import { q, tx } from '../db.js';
import { requireCaller, hasRole, isAdmin, requireRole } from '../authz.js';
import { asyncHandler, csv, oneOf, str, intValue, isoDate, today } from '../util.js';
import { forbidden, badRequest } from '../errors.js';
import { requireStepUp } from '../auth.js';
import { guardOtp } from '../otpGuard.js';
import { writeAudit } from '../audit.js';
import { dashboardData, drilldown } from '../services/dashboard.js';
import { computeRanking, activePolicy } from '../services/ranking.js';
import { policy } from '../config.js';

export const router = express.Router();

router.get('/dashboard', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  if (!(hasRole(caller, ['management', 'auditor', 'field_officer', 'credit_approver', 'verifier', 'it_admin']))) throw forbidden();
  res.json(await dashboardData(caller, req.query));
}));

router.get('/dashboard/drill', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  if (!(hasRole(caller, ['management', 'auditor', 'field_officer', 'credit_approver', 'verifier', 'it_admin']))) throw forbidden();
  const card = oneOf(req.query.card, ['groups', 'members', 'loansAdvanced', 'flows', 'arrears'], 'Card');
  res.json({ rows: await drilldown(caller, card, req.query) });
}));

router.get('/dashboard/export', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  if (!(hasRole(caller, ['management', 'auditor', 'field_officer', 'credit_approver', 'verifier', 'it_admin']))) throw forbidden();
  await guardOtp(req, 'export');
  const card = oneOf(req.query.card, ['groups', 'members', 'loansAdvanced', 'flows', 'arrears'], 'Card');
  const rows = await drilldown(caller, card, req.query);
  await tx(async (client) => {
    await writeAudit(client, caller, { action: 'report.export', entity: 'Report', entityId: card, after: { rows: rows.length }, scopeType: 'Org', scopeId: 'org' });
  });
  const body = csv(rows, Object.keys(rows[0] || { group_name: '' }).map((k) => ({ key: k, label: k })));
  res.setHeader('Content-Type', 'text/csv; charset=utf-8');
  res.setHeader('Content-Disposition', `attachment; filename="pamoja-${card}-${today()}.csv"`);
  res.send(body);
}));

router.get('/ranking', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  if (!(hasRole(caller, ['management', 'auditor', 'field_officer', 'credit_approver', 'it_admin']))) throw forbidden();
  const result = await computeRanking({ cutoff: req.query.cutoff || today() });
  res.json(result);
}));

router.post('/ranking/snapshot', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  if (!(hasRole(caller, ['management', 'credit_approver']) || isAdmin(caller))) throw forbidden();
  await guardOtp(req, 'snapshot');
  const cutoff = isoDate(req.body.cutoff || today(), 'Cut-off date');
  const result = await tx(async (client) => computeRanking({ cutoff, persist: true, caller, client }));
  res.status(201).json(result);
}));

router.get('/ranking/policies', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  if (!(hasRole(caller, ['management', 'auditor', 'credit_approver']) || isAdmin(caller))) throw forbidden();
  const rows = (await q('SELECT * FROM ranking_policy_versions ORDER BY version DESC')).rows;
  res.json({ policies: rows });
}));

router.post('/ranking/policies', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  requireRole(caller, ['management']);
  const weights = req.body.weights || (await policy('ranking.weights'));
  const total = Number(weights.repayment) + Number(weights.arrears) + Number(weights.attendance) + Number(weights.records);
  if (total !== 100) throw badRequest('Ranking weights must add up to 100.');
  const version = Number((await q('SELECT COALESCE(MAX(version),0)+1 AS v FROM ranking_policy_versions')).rows[0].v);
  const r = (await q(
    `INSERT INTO ranking_policy_versions (version, weights, min_loans, min_meetings, overdue_exclusion_days, tie_break, status, author_user_id)
     VALUES ($1,$2,$3,$4,$5,$6,'proposed',$7) RETURNING *`,
    [version, weights, intValue(req.body.minLoans || 1, 'Minimum loans', { min: 0, max: 100 }), intValue(req.body.minMeetings || 4, 'Minimum meetings', { min: 0, max: 100 }), intValue(req.body.overdueExclusionDays || 30, 'Overdue exclusion days', { min: 1, max: 365 }), JSON.stringify(['attendance_desc', 'verified_value_desc', 'group_id_asc']), caller.user.id],
  )).rows[0];
  res.status(201).json({ policy: r });
}));

router.post('/ranking/policies/:id/approve', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  requireRole(caller, ['management']);
  await guardOtp(req, 'policy');
  const pol = (await q('SELECT * FROM ranking_policy_versions WHERE id=$1', [req.params.id])).rows[0];
  if (!pol) throw badRequest('Policy version not found.');
  if (pol.author_user_id === caller.user.id) throw forbidden('The author of a ranking policy cannot approve it.');
  if (pol.status !== 'proposed') throw badRequest('Only a proposed policy can be approved.');
  const result = await tx(async (client) => {
    await client.query("UPDATE ranking_policy_versions SET status='approved', approved_by=$2, effective_from=current_date WHERE id=$1", [pol.id, caller.user.id]);
    await writeAudit(client, caller, { action: 'ranking.policy_approve', entity: 'RankingPolicyVersion', entityId: pol.id, before: { status: pol.status }, after: { status: 'approved' }, scopeType: 'Org', scopeId: 'org' });
    return (await client.query('SELECT * FROM ranking_policy_versions WHERE id=$1', [pol.id])).rows[0];
  });
  res.json({ policy: result });
}));
