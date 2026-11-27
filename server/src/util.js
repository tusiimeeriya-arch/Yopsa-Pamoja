import crypto from 'node:crypto';
import { badRequest } from './errors.js';

export const sha256 = (s) => crypto.createHash('sha256').update(s).digest('hex');

export function canonical(value) {
  if (value === null || value === undefined) return 'null';
  if (Array.isArray(value)) return '[' + value.map(canonical).join(',') + ']';
  if (typeof value === 'object') {
    return '{' + Object.keys(value).sort().map((k) => JSON.stringify(k) + ':' + canonical(value[k])).join(',') + '}';
  }
  return JSON.stringify(value);
}

export const asyncHandler = (fn) => (req, res, next) => Promise.resolve(fn(req, res, next)).catch(next);

export function str(value, name, { min = 1, max = 500, optional = false } = {}) {
  if (value === undefined || value === null || value === '') {
    if (optional) return null;
    throw badRequest(`${name} is required.`);
  }
  if (typeof value !== 'string') throw badRequest(`${name} must be text.`);
  const v = value.trim();
  if (v.length < min) throw badRequest(`${name} must be at least ${min} characters.`);
  if (v.length > max) throw badRequest(`${name} must be at most ${max} characters.`);
  return v;
}

export function amount(value, name = 'Amount') {
  const n = typeof value === 'string' && /^\d+$/.test(value) ? Number(value) : value;
  if (!Number.isSafeInteger(n) || n <= 0) throw badRequest(`${name} must be a whole positive UGX amount.`);
  if (n > 10_000_000_000) throw badRequest(`${name} is above the supported maximum.`);
  return n;
}

export function intValue(value, name, { min = 0, max = 100000 } = {}) {
  const n = Number(value);
  if (!Number.isInteger(n) || n < min || n > max) throw badRequest(`${name} must be a whole number between ${min} and ${max}.`);
  return n;
}

export function oneOf(value, allowed, name) {
  if (!allowed.includes(value)) throw badRequest(`${name} must be one of: ${allowed.join(', ')}.`);
  return value;
}

export function isoDate(value, name, { optional = false } = {}) {
  if ((value === undefined || value === null || value === '') && optional) return null;
  if (typeof value !== 'string' || !/^\d{4}-\d{2}-\d{2}$/.test(value)) throw badRequest(`${name} must be a date (YYYY-MM-DD).`);
  const d = new Date(value + 'T00:00:00Z');
  if (Number.isNaN(d.getTime())) throw badRequest(`${name} is not a valid date.`);
  return value;
}

export function isoDateTime(value, name) {
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) throw badRequest(`${name} must be a valid date and time.`);
  return d.toISOString();
}

export const today = () => new Date().toISOString().slice(0, 10);

export function addDays(dateStr, days) {
  const d = new Date(dateStr + 'T00:00:00Z');
  d.setUTCDate(d.getUTCDate() + days);
  return d.toISOString().slice(0, 10);
}

export function paginate(rows, limit = 200) {
  return rows.slice(0, limit);
}

export function csv(rows, columns) {
  const esc = (v) => {
    const s = v === null || v === undefined ? '' : String(v);
    return /[",\n]/.test(s) ? '"' + s.replaceAll('"', '""') + '"' : s;
  };
  return [columns.map((c) => c.label).join(','), ...rows.map((r) => columns.map((c) => esc(typeof c.value === 'function' ? c.value(r) : r[c.key])).join(','))].join('\n');
}

export function maskPhone(phone) {
  if (!phone) return null;
  return phone.replace(/(\d{3})\d+(\d{2})$/, '$1****$2');
}

export function maskId(id) {
  if (!id) return null;
  return '••••' + String(id).slice(-4);
}
