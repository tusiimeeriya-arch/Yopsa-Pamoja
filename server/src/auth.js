import crypto from 'node:crypto';
import { q } from './db.js';
import { AppError, unauthorized, forbidden, locked, badRequest } from './errors.js';
import { sha256, str } from './util.js';
import { policy } from './config.js';
import { sendMessage, PROVIDER as MESSAGE_PROVIDER } from './notify.js';
import { isProduction } from './env.js';

const SCRYPT = { N: 16384, r: 8, p: 1 };
const SESSION_COOKIE = 'pamoja_sid';
const SESSION_HOURS = 12;
const OTP_TTL_MINUTES = 5;

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
  const idleMinutes = Number(await policy('auth.idle_minutes'));
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
  // Demo accounts exist only to show the product. Production must never accept
  // one, whatever its password is.
  if (user && user.is_demo && isProduction()) {
    await new Promise((s) => setTimeout(s, 250));
    throw generic();
  }
  if (!user || user.status !== 'active') { await new Promise((s) => setTimeout(s, 250)); throw generic(); }
  if (user.locked_until && new Date(user.locked_until) > new Date()) {
    throw locked('Too many attempts. Try again in a few minutes.');
  }
  if (!verifyPassword(p, user.password_hash)) {
    const maxAttempts = Number(await policy('auth.lockout_attempts'));
    const lockMinutes = Number(await policy('auth.lockout_minutes'));
    const attempts = user.failed_attempts + 1;
    const lock = attempts >= maxAttempts ? new Date(Date.now() + lockMinutes * 60 * 1000) : null;
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

/**
 * One-time code confirmation for sensitive actions. The code is generated
 * server-side, stored only as a hash, delivered through the provider-neutral
 * message stub, and consumed on first correct use.
 */
export async function requestOtp(caller, purpose) {
  const p = str(purpose, 'Purpose', { max: 40 });
  // In production the code must reach the person through a real provider. The
  // stub records messages instead of sending them, so production fails closed
  // rather than handing a code to nobody (or to the API response).
  if (isProduction() && MESSAGE_PROVIDER === 'stub') {
    throw new AppError(503, 'OTP_DELIVERY_UNAVAILABLE', 'Confirmation codes cannot be delivered until an SMS provider is configured.');
  }
  const recent = Number((await q(
    "SELECT COUNT(*) AS n FROM otp_codes WHERE user_id=$1 AND purpose=$2 AND created_at > now() - interval '15 minutes'",
    [caller.user.id, p],
  )).rows[0].n);
  if (recent >= 5) throw new AppError(429, 'OTP_RATE_LIMITED', 'Too many codes requested. Try again in a few minutes.');
  const code = String(crypto.randomInt(0, 1000000)).padStart(6, '0');
  await q("UPDATE otp_codes SET consumed_at=now() WHERE user_id=$1 AND purpose=$2 AND consumed_at IS NULL", [caller.user.id, p]);
  await q(
    `INSERT INTO otp_codes (user_id, purpose, code_hash, expires_at)
     VALUES ($1,$2,$3, now() + ($4 || ' minutes')::interval)`,
    [caller.user.id, p, sha256(code), String(OTP_TTL_MINUTES)],
  );
  const person = (await q('SELECT phone FROM persons WHERE linked_user_id=$1 AND phone IS NOT NULL LIMIT 1', [caller.user.id])).rows[0];
  const recipient = person?.phone || caller.user.email;
  const delivery = await sendMessage({
    channel: 'sms',
    recipient,
    body: `Pamoja confirmation code for ${p.replace(/_/g, ' ')}: ${code}. It expires in ${OTP_TTL_MINUTES} minutes.`,
    userId: caller.user.id,
  });
  // The stub cannot deliver a real SMS. Outside production the code is returned
  // so the flow can be completed; in production it is never returned.
  return {
    purpose: p,
    delivery,
    expiresInMinutes: OTP_TTL_MINUTES,
    ...(isProduction() ? {} : { devCode: code }),
  };
}

export async function requireOtp(caller, purpose, code) {
  const p = str(purpose, 'Purpose', { max: 40 });
  // No code yet is not a validation error: it is the prompt to ask for one.
  if (code === undefined || code === null || String(code).trim() === '') {
    throw new AppError(403, 'OTP_REQUIRED', 'Enter the one-time code sent to you to continue.');
  }
  const supplied = str(code, 'One-time code', { min: 6, max: 6 });
  const row = (await q(
    "SELECT * FROM otp_codes WHERE user_id=$1 AND purpose=$2 AND consumed_at IS NULL ORDER BY created_at DESC LIMIT 1",
    [caller.user.id, p],
  )).rows[0];
  if (!row || new Date(row.expires_at) < new Date()) {
    throw new AppError(403, 'OTP_REQUIRED', 'Enter the one-time code sent to you to continue.');
  }
  if (row.attempts >= 5) {
    throw new AppError(403, 'OTP_REQUIRED', 'Too many incorrect codes. Request a new one.');
  }
  if (sha256(supplied) !== row.code_hash) {
    await q('UPDATE otp_codes SET attempts=attempts+1 WHERE id=$1', [row.id]);
    throw new AppError(403, 'OTP_INVALID', 'That confirmation code is not correct.');
  }
  await q('UPDATE otp_codes SET consumed_at=now() WHERE id=$1', [row.id]);
}

/** Forgot password: records the request for an administrator, reveals nothing. */
export async function requestPasswordReset(email) {
  const e = str(email, 'Email', { max: 200 }).toLowerCase();
  const user = (await q('SELECT id FROM users WHERE lower(email)=$1', [e])).rows[0];
  await q('INSERT INTO password_reset_requests (email, user_id) VALUES ($1,$2)', [e, user?.id || null]);
  if (user) {
    const admins = (await q(
      "SELECT DISTINCT user_id FROM role_assignments WHERE role IN ('it_admin','management') AND scope_type='Org' AND status='active'",
    )).rows;
    for (const a of admins) {
      await q(
        `INSERT INTO notifications (user_id, title, body, deadline, dedupe_key)
         VALUES ($1,'Password reset requested',$2,current_date + 1,$3) ON CONFLICT (dedupe_key) DO NOTHING`,
        [a.user_id, `A password reset was requested for ${e}. Verify the person before resetting.`, `pwd-reset:${e}:${new Date().toISOString().slice(0, 10)}`],
      );
    }
  }
  return { ok: true, message: 'If that account exists, an administrator has been asked to help you reset the password.' };
}
