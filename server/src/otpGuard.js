import { requireOtp } from './auth.js';

/**
 * One-time-code confirmation for sensitive actions. The client requests a code
 * for the exact purpose (POST /api/auth/otp/request) and sends it back in the
 * x-pamoja-otp header.
 */
export async function guardOtp(req, purpose) {
  await requireOtp(req.caller, purpose, req.headers['x-pamoja-otp']);
}
