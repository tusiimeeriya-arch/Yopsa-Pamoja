export class AppError extends Error {
  constructor(status, code, message, details) {
    super(message);
    this.status = status;
    this.code = code;
    this.details = details;
  }
}

export const badRequest = (msg, details) => new AppError(400, 'INVALID_INPUT', msg, details);
export const unauthorized = (msg = 'Sign in to continue.') => new AppError(401, 'UNAUTHENTICATED', msg);
export const forbidden = (msg = 'You do not have access to do that.') => new AppError(403, 'FORBIDDEN', msg);
export const notFound = (msg = 'Not found.') => new AppError(404, 'NOT_FOUND', msg);
export const conflict = (msg, details) => new AppError(409, 'CONFLICT', msg, details);
export const stepUpRequired = () => new AppError(403, 'STEP_UP_REQUIRED', 'Confirm your password to continue with this sensitive action.');
export const locked = (msg) => new AppError(423, 'LOCKED', msg);
