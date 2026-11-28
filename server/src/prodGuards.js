/**
 * Production boot guards.
 *
 * A process that claims to be production must not start with a development
 * configuration. Every check below fails closed: the API logs one JSON line
 * with the reason and exits non-zero. Development and staging are untouched.
 *
 * Run by src/index.js before the HTTP server is created.
 */
import { isProduction } from './env.js';

const DEFAULT_SECRETS = new Set([
  'pamoja_app_dev_password',
  'pamoja_dev_password',
  'postgres',
  'password',
  'changeme',
  'change-me',
  'dev-only-master-key',
  'local-development-only-token-key',
  'local-development-only-lookup-key',
  'local-development-only-master-key',
  'ci-only-token-secret',
  'ci-only-lookup-key',
  'ci-only-master-key',
  'clock-proof-only',
]);

// Environment names that carry the owner role's credentials. The API connects
// as the least-privilege pamoja_app role; owner access belongs to the one-shot
// migrate/seed/create-admin tooling.
const OWNER_KEYS = ['ADMIN_DATABASE_URL', 'ADMIN_PASSWORD', 'PGPASSWORD', 'POSTGRES_PASSWORD', 'OWNER_DB_PASSWORD', 'DB_OWNER_PASSWORD'];

function isSet(value) {
  return value !== undefined && value !== null && String(value).trim() !== '';
}

function weak(name, value) {
  if (!isSet(value)) return `${name} is required in production`;
  if (String(value).length < 32) return `${name} must be at least 32 characters in production`;
  if (DEFAULT_SECRETS.has(String(value))) return `${name} is a development default; set a real value in production`;
  return null;
}

function databasePassword(raw) {
  if (!isSet(raw)) return { problem: 'DATABASE_URL is required in production' };
  let url;
  try { url = new URL(raw); } catch { return { problem: 'DATABASE_URL is not a valid PostgreSQL URL' }; }
  if (url.protocol !== 'postgres:' && url.protocol !== 'postgresql:') {
    return { problem: 'DATABASE_URL must be a postgres:// URL' };
  }
  if (url.username !== 'pamoja_app') {
    return { problem: 'DATABASE_URL must connect as the least-privilege pamoja_app role, not the owner' };
  }
  const password = decodeURIComponent(url.password || '');
  return { password, problem: weak('DATABASE_URL password', password) };
}

/** Return the list of problems; empty means the production configuration is safe. */
export function productionConfigProblems(env = process.env) {
  if (!isProduction(env)) return [];
  const problems = [];

  if (env.APP_ENV === 'production' && env.NODE_ENV !== 'production') {
    problems.push('APP_ENV=production requires NODE_ENV=production');
  }
  if (isSet(env.OTP_ON_SCREEN)) {
    problems.push('OTP_ON_SCREEN must not be set in production; one-time codes are never returned or shown');
  }
  for (const key of OWNER_KEYS) {
    if (isSet(env[key])) problems.push(`${key} must not be set in the API environment; owner credentials are CLI-only`);
  }

  for (const key of ['AUTH_TOKEN_SECRET', 'PAMOJA_MASTER_KEY']) {
    const problem = weak(key, env[key]);
    if (problem) problems.push(problem);
  }
  const db = databasePassword(env.DATABASE_URL);
  if (db.problem) problems.push(db.problem);

  return problems;
}

export function assertProductionConfig(env = process.env) {
  const problems = productionConfigProblems(env);
  if (problems.length) {
    const err = new Error(problems.join('; '));
    err.problems = problems;
    throw err;
  }
}
