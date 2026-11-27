import express from 'express';
import { q, tx } from '../db.js';
import { adminTx } from '../owner.js';
import { isProduction } from '../env.js';
import { requireCaller, requireOrgAdmin, isAdmin, hasRole, ROLES } from '../authz.js';
import { asyncHandler, str, oneOf, isoDate, today, intValue } from '../util.js';
import { badRequest, conflict, forbidden, notFound } from '../errors.js';
import { requireStepUp } from '../auth.js';
import { guardOtp } from '../otpGuard.js';
import { writeAudit, verifyAuditChain } from '../audit.js';
import { runSystemChecks } from '../services/checks.js';
import { buildProgress } from '../data/buildProgress.js';
import { assumptions } from '../data/assumptions.js';
import { securityModel } from '../data/security.js';
import { invalidatePolicy } from '../config.js';
import { purgeDemoData, demoUsers } from '../services/demo.js';

export const router = express.Router();

const adminOnly = (caller) => { if (!isAdmin(caller)) throw forbidden('Administrator access is required.'); };

router.get('/admin/role-assignments', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  adminOnly(caller);
  const rows = (await q(
    `SELECT ra.*, u.full_name AS user_name, u.email, gb.full_name AS granted_by_name, ab.full_name AS approved_by_name
     FROM role_assignments ra JOIN users u ON u.id=ra.user_id
     LEFT JOIN users gb ON gb.id=ra.granted_by LEFT JOIN users ab ON ab.id=ra.approved_by
     ORDER BY ra.created_at DESC LIMIT 300`,
  )).rows;
  res.json({ assignments: rows, roles: ROLES, canPropose: hasRole(caller, ['it_admin']) });
}));

router.post('/admin/role-assignments', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  requireOrgAdmin(caller);
  if (!hasRole(caller, ['it_admin'])) throw forbidden('IT Admin proposes role changes; a second administrator approves them.');
  await guardOtp(req, 'role_change');
  const userId = str(req.body.userId, 'User', { max: 60 });
  const role = oneOf(req.body.role, ROLES, 'Role');
  const scopeType = oneOf(req.body.scopeType, ['Org', 'Branch', 'Group'], 'Scope type');
  const scopeId = scopeType === 'Org' ? 'org' : str(req.body.scopeId, 'Scope', { max: 60 });
  if (scopeType === 'Branch' && !(await q('SELECT 1 FROM org_branches WHERE id=$1', [scopeId])).rowCount) throw badRequest('Branch not found.');
  if (scopeType === 'Group' && !(await q('SELECT 1 FROM groups WHERE id=$1', [scopeId])).rowCount) throw badRequest('Group not found.');
  const user = (await q('SELECT id FROM users WHERE id=$1', [userId])).rows[0];
  if (!user) throw badRequest('User not found.');
  const dup = await q("SELECT 1 FROM role_assignments WHERE user_id=$1 AND role=$2 AND scope_type=$3 AND scope_id=$4 AND status IN ('pending','active')", [userId, role, scopeType, scopeId]);
  if (dup.rowCount) throw conflict('That role assignment already exists.');
  return tx(async (client) => {
    const ra = (await client.query(
      `INSERT INTO role_assignments (user_id, role, scope_type, scope_id, effective_from, effective_to, status, granted_by)
       VALUES ($1,$2,$3,$4,$5,$6,'pending',$7) RETURNING *`,
      [userId, role, scopeType, scopeId, isoDate(req.body.effectiveFrom || today(), 'Effective from'), isoDate(req.body.effectiveTo, 'Effective to', { optional: true }), caller.user.id],
    )).rows[0];
    await writeAudit(client, caller, { action: 'role.propose', entity: 'RoleAssignment', entityId: ra.id, after: { userId, role, scopeType, scopeId }, scopeType: 'Org', scopeId: 'org' });
    res.status(201).json({ assignment: ra });
  });
}));

router.post('/admin/role-assignments/:id/approve', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  requireOrgAdmin(caller);
  await guardOtp(req, 'role_change');
  return tx(async (client) => {
    const ra = (await client.query('SELECT * FROM role_assignments WHERE id=$1 FOR UPDATE', [req.params.id])).rows[0];
    if (!ra) throw notFound('Role assignment not found.');
    if (ra.status !== 'pending') throw badRequest('This assignment is not pending.');
    if (ra.granted_by === caller.user.id) throw forbidden('The proposer cannot approve their own role change.');
    if (ra.user_id === caller.user.id) throw forbidden('You cannot approve your own role grant.');
    await client.query("UPDATE role_assignments SET status='active', approved_by=$2 WHERE id=$1", [ra.id, caller.user.id]);
    await writeAudit(client, caller, { action: 'role.approve', entity: 'RoleAssignment', entityId: ra.id, before: { status: 'pending' }, after: { status: 'active' }, scopeType: 'Org', scopeId: 'org' });
    res.json({ ok: true });
  });
}));

router.post('/admin/role-assignments/:id/revoke', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  requireOrgAdmin(caller);
  await guardOtp(req, 'role_change');
  return tx(async (client) => {
    const ra = (await client.query('SELECT * FROM role_assignments WHERE id=$1 FOR UPDATE', [req.params.id])).rows[0];
    if (!ra) throw notFound('Role assignment not found.');
    if (ra.status === 'revoked') throw badRequest('Already revoked.');
    await client.query("UPDATE role_assignments SET status='revoked', revoked_at=now() WHERE id=$1", [ra.id]);
    await writeAudit(client, caller, { action: 'role.revoke', entity: 'RoleAssignment', entityId: ra.id, before: { status: ra.status }, after: { status: 'revoked' }, reason: req.body.reason || null, scopeType: 'Org', scopeId: 'org' });
    res.json({ ok: true });
  });
}));

router.get('/admin/users', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  adminOnly(caller);
  const users = (await q('SELECT id, email, full_name, status, is_demo, created_at FROM users ORDER BY full_name')).rows;
  const roles = (await q("SELECT user_id, role, scope_type, scope_id, status FROM role_assignments WHERE status='active'")).rows;
  res.json({ users: users.map((u) => ({ ...u, roles: roles.filter((r) => r.user_id === u.id) })) });
}));

// Each branch needs two active verifier users so the cash steps (handover,
// receipt, deposit) can be performed by different people. Admins are warned
// when a branch has fewer than two.
router.get('/admin/verifier-coverage', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  adminOnly(caller);
  const branches = (await q(
    `SELECT b.id AS branch_id, b.name, COUNT(DISTINCT ra.user_id)::int AS verifiers
       FROM org_branches b
       LEFT JOIN role_assignments ra
         ON ra.role = 'verifier' AND ra.status = 'active'
        AND ra.effective_from <= current_date
        AND (ra.effective_to IS NULL OR ra.effective_to >= current_date)
        AND (ra.scope_type = 'Org' OR (ra.scope_type = 'Branch' AND ra.scope_id = b.id::text))
      GROUP BY b.id, b.name
      ORDER BY b.name`,
  )).rows;
  res.json({ minimum: 2, branches, warnings: branches.filter((b) => b.verifiers < 2) });
}));

router.get('/admin/audit', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  adminOnly(caller);
  const limit = Math.min(Number(req.query.limit || 100), 500);
  const rows = (await q(
    `SELECT a.*, u.full_name AS actor_name FROM audit_logs a LEFT JOIN users u ON u.id=a.actor_user_id
     ORDER BY a.seq DESC LIMIT $1`, [limit],
  )).rows;
  res.json({ entries: rows });
}));

router.get('/admin/audit/integrity', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  adminOnly(caller);
  res.json(await verifyAuditChain({ query: q }));
}));

// Demo purge and the in-app self-tests are development tools. They need the
// owner role to clean up the protected identity tables, so the production API
// does not mount them at all (the paths 404). There they run from the command
// line: scripts/purge-demo.mjs and scripts/system-checks.mjs.
if (!isProduction()) {
  router.post('/admin/system-checks', asyncHandler(async (req, res) => {
    const caller = requireCaller(req);
    adminOnly(caller);
    const result = await runSystemChecks(caller);
    res.json(result);
  }));
}

router.get('/admin/build-progress', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  adminOnly(caller);
  res.json(buildProgress());
}));

router.get('/admin/assumptions', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  adminOnly(caller);
  res.json(assumptions());
}));

router.get('/admin/security', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  adminOnly(caller);
  res.json(securityModel());
}));

router.get('/admin/demo-guide', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  adminOnly(caller);
  const users = await demoUsers();
  const demoExists = Number((await q('SELECT COUNT(*) AS n FROM groups WHERE is_demo=true')).rows[0].n) > 0;
  res.json({
    users,
    // No literal fallback: a demo password exists only while demo data exists
    // and only when the operator set DEMO_PASSWORD for this environment.
    password: demoExists ? (process.env.DEMO_PASSWORD || null) : null,
    note: 'Demo accounts exist only while demo data exists. Disable or delete every demo account before go-live.',
    demoExists,
  });
}));

if (!isProduction()) {
  router.post('/admin/purge-demo', asyncHandler(async (req, res) => {
    const caller = requireCaller(req);
    requireOrgAdmin(caller);
    await guardOtp(req, 'purge');
    if (req.body.confirm !== 'PURGE DEMO DATA') throw badRequest('Type PURGE DEMO DATA to confirm.');
    const result = await adminTx(async (client) => {
      // The purge deletes across every tenant table by explicit demo id. It runs
      // as the owner because the API role deliberately has no DELETE on the five
      // protected identity tables. The is_demo / demo-id filters below are what
      // limit it to demo data.
      await client.query("SELECT set_config('app.system', 'on', true)");
      // Allows the payment-immutability trigger to remove demo rows only.
      await client.query("SELECT set_config('app.demo_purge', 'on', true)");
      const counts = await purgeDemoData(client);
      await writeAudit(client, caller, { action: 'demo.purge', entity: 'DemoBatch', entityId: 'demo', after: counts, reason: 'Purge demo data', scopeType: 'Org', scopeId: 'org' });
      return counts;
    });
    res.json({ result, note: 'AuditLog rows were retained. Demo accounts are disabled.' });
  }));
}

router.get('/admin/policies', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  adminOnly(caller);
  const rows = (await q("SELECT DISTINCT ON (key) * FROM policy_configs ORDER BY key, version DESC")).rows;
  res.json({ policies: rows, canEdit: hasRole(caller, ['management']) });
}));

router.post('/admin/policies', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  if (!hasRole(caller, ['management'])) throw forbidden('Only management can change policy values.');
  await guardOtp(req, 'policy');
  const key = str(req.body.key, 'Policy key', { max: 120 });
  const value = req.body.value;
  if (value === undefined || value === null) throw badRequest('A policy value is required.');
  const version = Number((await q('SELECT COALESCE(MAX(version),0)+1 AS v FROM policy_configs WHERE key=$1', [key])).rows[0].v);
  return tx(async (client) => {
    const p = (await client.query(
      `INSERT INTO policy_configs (key, value, version, effective_from, approved_by, status) VALUES ($1,$2,$3,current_date,$4,'active') RETURNING *`,
      [key, JSON.stringify(value), version, caller.user.id],
    )).rows[0];
    await writeAudit(client, caller, { action: 'policy.update', entity: 'PolicyConfig', entityId: p.id, after: { key, version, value }, scopeType: 'Org', scopeId: 'org' });
    invalidatePolicy();
    res.status(201).json({ policy: p });
  });
}));

router.get('/admin/jobs', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  adminOnly(caller);
  const runs = (await q('SELECT * FROM job_runs ORDER BY started_at DESC LIMIT 50')).rows;
  const counts = (await q(
    `SELECT job, COUNT(*)::int AS runs, MAX(started_at) AS last_run,
            COUNT(*) FILTER (WHERE status='ok')::int AS ok,
            COUNT(*) FILTER (WHERE status='failed')::int AS failed
     FROM job_runs GROUP BY job ORDER BY job`,
  )).rows;
  res.json({ runs, summary: counts });
}));

router.get('/admin/exceptions', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  adminOnly(caller);
  const rows = (await q('SELECT e.*, u.full_name AS owner_name FROM finance_exceptions e JOIN users u ON u.id=e.owner_user_id ORDER BY e.created_at DESC LIMIT 100')).rows;
  res.json({ exceptions: rows });
}));

router.post('/admin/exceptions/:id/resolve', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  if (!(hasRole(caller, ['verifier', 'management']) || isAdmin(caller))) throw forbidden();
  const reason = str(req.body.reason, 'Resolution note', { min: 3, max: 300 });
  return tx(async (client) => {
    const e = (await client.query('SELECT * FROM finance_exceptions WHERE id=$1', [req.params.id])).rows[0];
    if (!e) throw notFound('Exception not found.');
    await client.query("UPDATE finance_exceptions SET status='resolved', resolved_at=now() WHERE id=$1", [e.id]);
    await writeAudit(client, caller, { action: 'exception.resolve', entity: 'FinanceException', entityId: e.id, before: { status: e.status }, after: { status: 'resolved' }, reason, scopeType: 'Org', scopeId: 'org' });
    res.json({ ok: true });
  });
}));
