/**
 * The check suites assert that PostgreSQL refuses things: a missing grant, a
 * locked payment entry, an immutable `is_demo`, a duplicate key. Those refusals
 * are the evidence, but PostgreSQL logs every ERROR server-side, so a run fills
 * the database log with lines that read like application faults.
 *
 * This returns a connection string that runs the suite as `pamoja_app` — the
 * same least-privilege role, grants and row-level security as the API — while
 * the session keeps its server-side error logging off. The rejection still
 * reaches the caller and the check still fails when a guard does not fire; only
 * the database log stays readable.
 *
 * The owner URL is needed because `log_min_messages` and `SET ROLE` are
 * superuser-only. These scripts are command-line tooling; the API process never
 * loads this module.
 */
export function quietAppUrl(env = process.env) {
  if (env.CHECK_DATABASE_URL) return env.CHECK_DATABASE_URL;
  if (!env.ADMIN_DATABASE_URL) return null;
  const sep = env.ADMIN_DATABASE_URL.includes('?') ? '&' : '?';
  return `${env.ADMIN_DATABASE_URL}${sep}options=-c log_min_messages=fatal -c role=pamoja_app`;
}
