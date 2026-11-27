/**
 * One definition of "production" for the whole server.
 *
 * Both NODE_ENV and APP_ENV are accepted so an operator can set either; the
 * value must be exactly "production" (case-sensitive) to count.
 */
export function isProduction(env = process.env) {
  return env.NODE_ENV === 'production' || env.APP_ENV === 'production';
}
