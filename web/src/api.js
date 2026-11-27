const BASE = '/api';

export class ApiError extends Error {
  constructor(message, code, status, details) {
    super(message);
    this.code = code;
    this.status = status;
    this.details = details;
  }
}

// Set for exactly one request after a one-time code has been confirmed.
let otpCode = null;
export function setOtpCode(code) { otpCode = code; }

export async function api(path, { method = 'GET', body, headers = {}, raw = false } = {}) {
  let res;
  const otpHeader = otpCode ? { 'x-pamoja-otp': otpCode } : {};
  try {
    res = await fetch(BASE + path, {
      method,
      credentials: 'same-origin',
      headers: { 'content-type': 'application/json', 'x-pamoja-client': 'web', ...otpHeader, ...headers },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
  } catch (err) {
    throw new ApiError('You appear to be offline. Your unsent work can be saved as a draft.', 'OFFLINE', 0);
  } finally {
    otpCode = null;
  }
  if (res.status === 401) {
    let payload = null;
    try { payload = await res.json(); } catch { /* no body */ }
    const code = payload?.error?.code;
    if (code === 'IDLE_TIMEOUT') window.dispatchEvent(new CustomEvent('pamoja:idle'));
    throw new ApiError(payload?.error?.message || 'Sign in to continue.', code || 'UNAUTHENTICATED', 401);
  }
  if (!res.ok) {
    let payload = null;
    try { payload = await res.json(); } catch { /* no body */ }
    throw new ApiError(payload?.error?.message || `Request failed (${res.status})`, payload?.error?.code, res.status, payload?.error?.details);
  }
  if (raw) return res;
  const type = res.headers.get('content-type') || '';
  return type.includes('application/json') ? res.json() : res.text();
}

export const get = (path) => api(path);
export const post = (path, body) => api(path, { method: 'POST', body });
export const patch = (path, body) => api(path, { method: 'PATCH', body });
