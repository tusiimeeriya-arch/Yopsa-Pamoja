import express from 'express';
import { q } from '../db.js';
import { destroySession, login, requestOtp, requestPasswordReset, stepUp } from '../auth.js';
import { requireCaller, navFor } from '../authz.js';
import { asyncHandler } from '../util.js';

export const router = express.Router();

router.post('/login', asyncHandler(async (req, res) => {
  const user = await login(req.body.email, req.body.password, req, res);
  res.json({ user });
}));

router.post('/logout', asyncHandler(async (req, res) => {
  await destroySession(req, res);
  res.json({ ok: true });
}));

router.get('/me', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  res.json({
    user: caller.user,
    roles: caller.assignments,
    nav: navFor(caller),
    isAdmin: caller.assignments.some((a) => ['it_admin', 'management'].includes(a.role) && a.scope_type === 'Org'),
    demoMode: (await q('SELECT EXISTS(SELECT 1 FROM org_branches WHERE is_demo=true) AS demo')).rows[0].demo,
  });
}));

router.post('/step-up', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  await stepUp(caller, req.body.password);
  res.json({ ok: true, stepUpAt: new Date().toISOString() });
}));

router.post('/otp/request', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  res.json(await requestOtp(caller, req.body.purpose || 'sensitive'));
}));

router.post('/password-reset', asyncHandler(async (req, res) => {
  res.json(await requestPasswordReset(req.body.email));
}));

router.get('/sessions', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  const rows = (await q(
    'SELECT id, created_at, last_seen_at, expires_at, ip FROM sessions WHERE user_id=$1 AND revoked_at IS NULL ORDER BY last_seen_at DESC',
    [caller.user.id],
  )).rows;
  res.json({ sessions: rows });
}));

router.post('/sessions/:id/revoke', asyncHandler(async (req, res) => {
  const caller = requireCaller(req);
  await q('UPDATE sessions SET revoked_at=now() WHERE id=$1 AND user_id=$2', [req.params.id, caller.user.id]);
  res.json({ ok: true });
}));
