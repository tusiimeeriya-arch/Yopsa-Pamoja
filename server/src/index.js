import express from 'express';
import { ready } from './db.js';
import { AppError } from './errors.js';
import { attachCaller } from './authz.js';
import { router as authRouter } from './routes/auth.js';
import { router as meRouter } from './routes/me.js';
import { router as groupsRouter } from './routes/groups.js';
import { router as meetingsRouter } from './routes/meetings.js';
import { router as paymentsRouter } from './routes/payments.js';
import { router as loansRouter } from './routes/loans.js';
import { router as dashboardRouter } from './routes/dashboard.js';
import { router as supportRouter } from './routes/support.js';
import { router as adminRouter } from './routes/admin.js';
import { runEscalations } from './services/escalation.js';
import { computeRanking } from './services/ranking.js';
import { tx } from './db.js';

const app = express();
const PORT = Number(process.env.PORT || 8080);
app.disable('x-powered-by');
app.set('trust proxy', 1);

app.use((req, res, next) => {
  res.setHeader('X-Content-Type-Options', 'nosniff');
  res.setHeader('Referrer-Policy', 'strict-origin-when-cross-origin');
  res.setHeader('Permissions-Policy', 'geolocation=(), camera=(), microphone=()');
  res.setHeader('Content-Security-Policy', "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self'; frame-ancestors 'self' https://*.base44.com https://*.base44.app");
  next();
});

app.use(express.json({ limit: '10mb' }));

const buckets = new Map();
function rateLimit(key, max, windowMs) {
  const now = Date.now();
  const b = buckets.get(key);
  if (!b || now > b.reset) { buckets.set(key, { count: 1, reset: now + windowMs }); return true; }
  b.count += 1;
  return b.count <= max;
}

app.get('/api/health', async (req, res) => {
  const db = await ready().catch(() => false);
  res.status(db ? 200 : 503).json({ status: db ? 'ok' : 'degraded', database: db, time: new Date().toISOString() });
});

app.get('/api/public/config', (req, res) => {
  res.json({ name: 'Yopsa Pamoja', org: 'Yopsa Microfinance', tagline: 'Stronger groups. Informed decisions.', currency: 'UGX', timezone: 'Africa/Kampala' });
});

app.post('/api/auth/login', (req, res, next) => {
  const key = `login:${req.ip}:${String(req.body?.email || '').toLowerCase()}`;
  if (!rateLimit(key, 10, 15 * 60 * 1000)) return res.status(429).json({ error: { code: 'RATE_LIMITED', message: 'Too many attempts. Try again later.' } });
  next();
});

app.use(attachCaller);
app.use('/api', (req, res, next) => {
  if (req.method !== 'GET' && req.method !== 'HEAD' && req.headers['x-pamoja-client'] !== 'web') {
    return res.status(403).json({ error: { code: 'CSRF_BLOCKED', message: 'Request rejected.' } });
  }
  next();
});
app.use('/api/auth', authRouter);
app.use('/api', (req, res, next) => {
  if (!req.caller) return res.status(401).json({ error: { code: 'UNAUTHENTICATED', message: 'Sign in to continue.' } });
  next();
});

app.use('/api/me', meRouter);
app.use('/api', groupsRouter);
app.use('/api', meetingsRouter);
app.use('/api', paymentsRouter);
app.use('/api', loansRouter);
app.use('/api', dashboardRouter);
app.use('/api', supportRouter);
app.use('/api', adminRouter);

app.use('/api', (req, res) => res.status(404).json({ error: { code: 'NOT_FOUND', message: 'Not found.' } }));

app.use((err, req, res, next) => {
  if (res.headersSent) return next(err);
  let status = err.status || 500;
  let message = err.message;
  if (!err.status && err.code === '23505') { status = 409; message = 'That record already exists.'; }
  else if (!err.status && err.code === '23503') { status = 400; message = 'A linked record is missing or still in use.'; }
  else if (!err.status && (err.code === '22P02' || err.code === '22007')) { status = 400; message = 'One of the values is not in the expected format.'; }
  if (status >= 500) console.error(JSON.stringify({ level: 'error', message: err.message, stack: process.env.NODE_ENV === 'production' ? undefined : err.stack, path: req.path }));
  res.status(status).json({ error: { code: err.code || 'INTERNAL', message: status >= 500 ? 'Something went wrong. Try again.' : message, ...(err.details ? { details: err.details } : {}) } });
});

app.listen(PORT, '0.0.0.0', () => {
  console.log(JSON.stringify({ level: 'info', message: `pamoja api listening on ${PORT}` }));
});

// Hourly follow-up sweep. Idempotent: dedupe keys prevent duplicates.
setInterval(async () => {
  try { await tx(async (client) => runEscalations(client)); } catch (err) { console.error(JSON.stringify({ level: 'error', message: 'escalation sweep failed', error: err.message })); }
}, 60 * 60 * 1000).unref();

// Daily operational ranking snapshot (manual snapshots remain available).
setInterval(async () => {
  try { await tx(async (client) => computeRanking({ persist: true, caller: null, client })); } catch (err) { console.error(JSON.stringify({ level: 'error', message: 'ranking snapshot failed', error: err.message })); }
}, 24 * 60 * 60 * 1000).unref();
