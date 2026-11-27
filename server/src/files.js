import crypto from 'node:crypto';
import { badRequest } from './errors.js';

const KEY = process.env.PAMOJA_MASTER_KEY || 'dev-only-master-key';
const ALLOWED = ['image/png', 'image/jpeg', 'image/webp', 'application/pdf'];
const MAX = 5 * 1024 * 1024;

export function validateUpload({ mime, size, filename }) {
  if (!ALLOWED.includes(mime)) throw badRequest('Only images (PNG, JPEG, WebP) and PDF files are allowed.');
  if (!Number.isInteger(size) || size <= 0) throw badRequest('The file is empty.');
  if (size > MAX) throw badRequest('The file must be 5 MB or smaller.');
  if (!filename || filename.length > 200) throw badRequest('A file name is required.');
}

/** Detect the real file type from its first bytes. */
export function sniffMime(buf) {
  if (!buf || buf.length < 4) return null;
  if (buf[0] === 0x89 && buf[1] === 0x50 && buf[2] === 0x4e && buf[3] === 0x47) return 'image/png';
  if (buf[0] === 0xff && buf[1] === 0xd8 && buf[2] === 0xff) return 'image/jpeg';
  if (buf.slice(0, 4).toString('ascii') === 'RIFF' && buf.slice(8, 12).toString('ascii') === 'WEBP') return 'image/webp';
  if (buf.slice(0, 5).toString('ascii') === '%PDF-') return 'application/pdf';
  return null;
}

export function decodeBase64(data, declaredMime) {
  if (typeof data !== 'string' || !/^[A-Za-z0-9+/=\s]+$/.test(data)) throw badRequest('The file content is not valid base64.');
  const buf = Buffer.from(data, 'base64');
  if (buf.length > MAX) throw badRequest('The file must be 5 MB or smaller.');
  if (declaredMime) {
    const actual = sniffMime(buf);
    if (!actual) throw badRequest('The file content is not a supported image or PDF.');
    if (actual !== declaredMime) throw badRequest(`The file content is ${actual}, not ${declaredMime}.`);
  }
  return buf;
}

export function signFile(id, userId, expires) {
  return crypto.createHmac('sha256', KEY).update(`${id}|${userId}|${expires}`).digest('base64url');
}

export function verifyFileSignature(id, userId, expires, sig) {
  if (!expires || Number(expires) < Date.now()) return false;
  const expected = signFile(id, userId, expires);
  const a = Buffer.from(String(sig || ''));
  const b = Buffer.from(expected);
  return a.length === b.length && crypto.timingSafeEqual(a, b);
}

export function signedFileUrl(id, userId) {
  const expires = Date.now() + 5 * 60 * 1000;
  return `/api/files/${id}?expires=${expires}&sig=${encodeURIComponent(signFile(id, userId, expires))}`;
}
