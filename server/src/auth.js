import crypto from 'node:crypto';
import { q } from './db.js';
import { AppError, unauthorized, forbidden, locked, badRequest } from './errors.js';
import { sha256, str } from './util.js';

const SCRYPT = { N: 16384, r: 8, p: 1 };
const SESSION_COOKIE = 'pamoja_sid';
const SESSION_HOURS = 12;

export function hashPassword(password) {
  const salt = crypto.randomBytes(16).toString('base64');
  const hash = crypto.scryptSync(password, salt, 64, SCRYPT).toString('base64');
  return `scrypt$${SCRYPT.N}$${SCRYPT.r}$${SCRYPT.p}$${salt}$${hash}`;
}

export function verifyPassword(password, stored) {
  try {
    const [algo, N, r, p, salt, hash] = String(stored).split('$');
    if (algo !== 'scrypt') return false;
    const calc = crypto.scryptSync(password, salt, 64, { N: Number(N), r: Number(r), p: Number(p) });
    const expected = Buffer.from(hash, 'base64');
    return calc.length === expected.length && crypto.timingSafeEqual(calc, expected);
  } catch { return false; }
}

export function parseCookies(req) {
  const out = {};
  for (const part of (req.headers.cookie || '').split(';')) {
    const i = part.indexOf('=');
    if (i > 0) out[part.slice(0, i).trim()] = decodeURIComponent(part.slice(i + 1).trim());
  }
  return out;
}

function cookieOptions(req, maxAgeSeconds) {
  const secure = req.headers['x-forwarded-proto'] === 'https' || req.protocol === 'https';
  return { httpOnly: true, sameSite: 'lax', secure, path: '/', maxAge: maxAgeSeconds * 1000 };
}

export async function createSession(req, res, userId) {
  const token = crypto.randomBytes(32).toString('base64url');
  const expires = new Date(Date.now() + SESSION_HOURS * 3600 * 1000);
  await q(
    `INSERT INTO sessions (user_id, token_hash, expires_at, ip, user_agent) VALUES ($1,$2,$3,$4,$5)`,
    [userId, sha256(token), expires, req.ip || null, String(req.headers['user-agent'] || '').slice(0, 300)],
  );
  res.cookie(SESSION_COOKIE, token, cookieOptions(req, SESSION_HOURS * 3600));
  return token;
}

export async function destroySession(req, res) {
  const token = parseCookies(req)[SESSION_COOKIE];
  if (token) await q('UPDATE sessions SET revoked_at=now() WHERE token_hash=$1', [sha256(token)]);
  res.clearCookie(SESSION_COOKIE, { path: '/' });
}

export async function sessionFromRequest(req) {
  const token = parseCookies(req)[SESSION_COOKIE];
  if (!token) return null;
  const r = await q(
    `SELECT s.*, u.email, u.full_name, u.status AS user_status, u.is_demo
     FROM sessions s JOIN users u ON u.id=s.user_id
     WHERE s.token_hash=$1 AND s.revoked_at IS NULL AND s.expires_at > now()`,
    [sha256(token)],
  );
  const row = r.rows[0];
  if (!row) return null;
  if (row.user_status !== 'active') throw forbidden('This account is not active.');
  const idleMinutes = Number((await import('./config.js')).DEFAULTS['auth.idle_minutes']);
  const idleMs = idleMinutes * 60 * 1000;
  if (Date.now() - new Date(row.last_seen_at).getTime() > idleMs) {
    await q('UPDATE sessions SET revoked_at=now() WHERE id=$1', [row.id]);
    throw new AppError(401, 'IDLE_TIMEOUT', 'You were signed out for security after a period of inactivity.');
  }
  if (Date.now() - new Date(row.last_seen_at).getTime() > 60000) {
    await q('UPDATE sessions SET last_seen_at=now() WHERE id=$1', [row.id]);
  }
  return {
    id: row.id,
    stepUpAt: row.step_up_at,
    user: { id: row.user_id, email: row.email, full_name: row.full_name, isDemo: row.is_demo },
  };
}

export async function login(email, password, req, res) {
  const e = str(email, 'Email', { max: 200 }).toLowerCase();
  const p = str(password, 'Password', { max: 200 });
  const r = await q('SELECT * FROM users WHERE lower(email)=$1', [e]);
  const user = r.rows[0];
  const generic = () => new AppError(401, 'INVALID_CREDENTIALS', 'Email or password is incorrect.');
  if (!user || user.status !== 'active') { await new Promise((s) => setTimeout(s, 250)); throw generic(); }
  if (user.locked_until && new Date(user.locked_until) > new Date()) {
    throw locked('Too many attempts. Try again in a few minutes.');
  }
  if (!verifyPassword(p, user.password_hash)) {
    const attempts = user.failed_attempts + 1;
    const lock = attempts >= 5 ? new Date(Date.now() + 15 * 60 * 1000) : null;
    await q('UPDATE users SET failed_attempts=$2, locked_until=$3 WHERE id=$1', [user.id, lock ? 0 : attempts, lock]);
    throw generic();
  }
  await q('UPDATE users SET failed_attempts=0, locked_until=NULL WHERE id=$1', [user.id]);
  await createSession(req, res, user.id);
  return { id: user.id, email: user.email, full_name: user.full_name, isDemo: user.is_demo };
}

export async function stepUp(caller, password) {
  const p = str(password, 'Password', { max: 200 });
  const r = await q('SELECT password_hash FROM users WHERE id=$1', [caller.user.id]);
  if (!r.rowCount || !verifyPassword(p, r.rows[0].password_hash)) {
    throw badRequest('Password confirmation failed.');
  }
  await q('UPDATE sessions SET step_up_at=now() WHERE id=$1', [caller.sessionId]);
  caller.stepUpAt = new Date();
}

export function requireStepUp(caller, minutes = 5) {
  const at = caller.stepUpAt ? new Date(caller.stepUpAt).getTime() : 0;
  if (Date.now() - at > minutes * 60 * 1000) {
    throw new AppError(403, 'STEP_UP_REQUIRED', 'Confirm your password to continue with this sensitive action.');
  }
}
