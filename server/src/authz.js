import { q } from './db.js';
import { forbidden, notFound } from './errors.js';
import { sessionFromRequest } from './auth.js';

export const ROLES = ['member', 'secretary', 'treasurer', 'chairperson', 'field_officer', 'verifier', 'credit_approver', 'management', 'auditor', 'it_admin'];
export const READ_ALL = ['management', 'auditor'];
export const ADMIN_ROLES = ['it_admin', 'management'];

export async function attachCaller(req, res, next) {
  try {
    const session = await sessionFromRequest(req);
    if (!session) return next();
    const assignments = (await q(
      `SELECT id, role, scope_type, scope_id, effective_from, effective_to
       FROM role_assignments
       WHERE user_id=$1 AND status='active'
         AND effective_from <= current_date
         AND (effective_to IS NULL OR effective_to >= current_date)`,
      [session.user.id],
    )).rows;
    req.caller = {
      user: session.user,
      sessionId: session.id,
      stepUpAt: session.stepUpAt,
      assignments,
      roleUsed: assignments[0]?.role || 'member',
    };
    next();
  } catch (err) { next(err); }
}

export function requireCaller(req) {
  if (!req.caller) throw Object.assign(new Error('Sign in to continue.'), { status: 401, code: 'UNAUTHENTICATED' });
  return req.caller;
}

function covers(assignment, scope = {}) {
  if (assignment.scope_type === 'Org') return true;
  const hasBranch = scope.branchId !== undefined && scope.branchId !== null && scope.branchId !== '';
  const hasGroup = scope.groupId !== undefined && scope.groupId !== null && scope.groupId !== '';
  // With no scope supplied, hasRole means "holds this role anywhere".
  if (!hasBranch && !hasGroup) return true;
  if (assignment.scope_type === 'Branch' && hasBranch) return String(assignment.scope_id) === String(scope.branchId);
  if (assignment.scope_type === 'Group' && hasGroup) return String(assignment.scope_id) === String(scope.groupId);
  return false;
}

export function hasRole(caller, roles, scope = {}) {
  const list = Array.isArray(roles) ? roles : [roles];
  return caller.assignments.some((a) => list.includes(a.role) && covers(a, scope));
}

export function requireRole(caller, roles, scope = {}) {
  if (!hasRole(caller, roles, scope)) throw forbidden();
  return true;
}

export function requireOrgAdmin(caller) {
  const ok = caller.assignments.some((a) => ADMIN_ROLES.includes(a.role) && a.scope_type === 'Org');
  if (!ok) throw forbidden('Only an organisation administrator can do that.');
}

export function isAdmin(caller) {
  return caller.assignments.some((a) => ADMIN_ROLES.includes(a.role) && a.scope_type === 'Org');
}

export function requireNotSelf(caller, otherUserId, message = 'You cannot act on your own submission.') {
  if (caller.user.id === otherUserId) throw forbidden(message);
}

export function personLinked(caller) {
  return q('SELECT * FROM persons WHERE linked_user_id=$1', [caller.user.id]).then((r) => r.rows[0] || null);
}

/** Load a group and prove the caller may see it. */
export async function loadGroup(caller, groupId, { write = false } = {}) {
  const r = await q('SELECT * FROM groups WHERE id=$1', [groupId]);
  const group = r.rows[0];
  if (!group) throw notFound('Group not found.');
  const scope = { branchId: group.branch_id, groupId: group.id };
  if (caller.assignments.some((a) => covers(a, scope))) return group;
  const membership = await q(
    `SELECT m.id FROM memberships m JOIN persons p ON p.id=m.person_id
     WHERE m.group_id=$1 AND m.status='active' AND p.linked_user_id=$2`,
    [group.id, caller.user.id],
  );
  if (membership.rowCount) return group;
  throw forbidden('You do not have access to this group.');
}

/** Group-scoped write role, honouring branch and organisation scope. */
export async function requireGroupRole(caller, group, roles) {
  return requireRole(caller, roles, { branchId: group.branch_id, groupId: group.id });
}

export function navFor(caller) {
  const roles = new Set(caller.assignments.map((a) => a.role));
  const nav = [
    { to: '/', label: 'Home', icon: 'home', always: true },
    { to: '/groups', label: 'Groups', icon: 'groups', roles: ['field_officer', 'management', 'auditor', 'it_admin', 'chairperson', 'secretary', 'treasurer', 'member', 'verifier', 'credit_approver'] },
    { to: '/meetings', label: 'Meetings', icon: 'calendar', roles: ['secretary', 'chairperson', 'field_officer', 'management', 'auditor', 'member', 'treasurer', 'verifier', 'credit_approver'] },
    { to: '/payments', label: 'Payments', icon: 'coins', roles: ['treasurer', 'verifier', 'member', 'management', 'auditor', 'field_officer', 'chairperson'] },
    { to: '/loans', label: 'Loans', icon: 'loan', roles: ['chairperson', 'field_officer', 'credit_approver', 'verifier', 'management', 'auditor', 'member'] },
    { to: '/dashboard', label: 'Dashboard', icon: 'chart', roles: ['management', 'auditor', 'field_officer', 'credit_approver', 'verifier', 'it_admin'] },
    { to: '/ranking', label: 'Ranking', icon: 'trophy', roles: ['management', 'auditor', 'field_officer', 'credit_approver', 'it_admin'] },
    { to: '/learning', label: 'Learning', icon: 'book', always: true },
    { to: '/disputes', label: 'Disputes', icon: 'flag', roles: ['member', 'field_officer', 'verifier', 'management', 'auditor', 'secretary', 'treasurer'] },
    { to: '/tasks', label: 'Tasks', icon: 'check', always: true },
    { to: '/admin', label: 'Admin', icon: 'shield', admin: true },
  ];
  return nav.filter((n) => (n.admin ? isAdmin(caller) : (n.always || (n.roles || []).some((r) => roles.has(r)))));
}
