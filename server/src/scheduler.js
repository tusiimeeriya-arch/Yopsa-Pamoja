import { q, systemTx } from './db.js';
import { isProduction } from './env.js';
import { runEscalations } from './services/escalation.js';
import { computeRanking } from './services/ranking.js';

// Production defaults. The schedule can be shortened for observation through
// ESCALATION_JOB_SECONDS / RANKING_JOB_SECONDS, but never below the production
// floor: an accidental one-second interval in production would hammer the
// database and the notification table.
export const PRODUCTION_DEFAULTS = Object.freeze({ escalationSeconds: 3600, rankingSeconds: 86400 });
export const MIN_PRODUCTION_INTERVAL_SECONDS = 60;

function intervalFrom(env, name, fallback) {
  const raw = env[name];
  if (raw === undefined || raw === null || String(raw).trim() === '') return fallback;
  const seconds = Number(raw);
  if (!Number.isFinite(seconds) || seconds <= 0) {
    throw new Error(`${name} must be a positive number of seconds, got "${raw}".`);
  }
  return Math.floor(seconds);
}

export function schedulerConfig(env = process.env) {
  return {
    escalationSeconds: intervalFrom(env, 'ESCALATION_JOB_SECONDS', PRODUCTION_DEFAULTS.escalationSeconds),
    rankingSeconds: intervalFrom(env, 'RANKING_JOB_SECONDS', PRODUCTION_DEFAULTS.rankingSeconds),
  };
}

/** Refuse a production configuration whose interval is below the floor. */
export function assertSchedulerConfig(env = process.env) {
  const config = schedulerConfig(env);
  if (isProduction(env)) {
    for (const [name, seconds] of [
      ['ESCALATION_JOB_SECONDS', config.escalationSeconds],
      ['RANKING_JOB_SECONDS', config.rankingSeconds],
    ]) {
      if (seconds < MIN_PRODUCTION_INTERVAL_SECONDS) {
        throw new Error(`${name}=${seconds}s is below the ${MIN_PRODUCTION_INTERVAL_SECONDS}s production minimum.`);
      }
    }
  }
  return config;
}

async function recordRun(job, fn) {
  const runId = (await q('INSERT INTO job_runs (job) VALUES ($1) RETURNING id', [job])).rows[0].id;
  const started = Date.now();
  try {
    const result = await systemTx(fn);
    await q("UPDATE job_runs SET status='ok', finished_at=now(), result=$2 WHERE id=$1", [runId, result]);
    console.log(JSON.stringify({ level: 'info', message: 'scheduled job ok', job, runId, ms: Date.now() - started, result }));
    return result;
  } catch (err) {
    await q("UPDATE job_runs SET status='failed', finished_at=now(), error=$2 WHERE id=$1", [runId, String(err.message).slice(0, 500)]);
    console.error(JSON.stringify({ level: 'error', message: 'scheduled job failed', job, runId, error: err.message }));
    return null;
  }
}

export const runEscalationJob = () => recordRun('escalation', (client) => runEscalations(client));
export const runRankingJob = () => recordRun('ranking_snapshot', async (client) => {
  // A fresh install has no approved ranking policy until an administrator
  // approves one. That is a skip, not a failed run.
  const approved = await client.query("SELECT 1 FROM ranking_policy_versions WHERE status='approved' LIMIT 1");
  if (!approved.rowCount) return { skipped: 'no approved ranking policy version yet' };
  return computeRanking({ persist: true, caller: null, client });
});

/** Start the two scheduled jobs with the configured (validated) intervals. */
export function startScheduler(env = process.env) {
  const { escalationSeconds, rankingSeconds } = assertSchedulerConfig(env);
  const timers = [];
  const schedule = (name, seconds, fn) => {
    console.log(JSON.stringify({ level: 'info', message: 'scheduler registered', job: name, everySeconds: seconds }));
    const timer = setInterval(() => { fn().catch(() => {}); }, seconds * 1000);
    timer.unref();
    timers.push(timer);
  };
  schedule('escalation', escalationSeconds, runEscalationJob);
  schedule('ranking_snapshot', rankingSeconds, runRankingJob);
  // Outside production, run once shortly after boot so a fresh stack has
  // evidence without waiting a full period. Production keeps to its interval.
  if (!isProduction(env)) {
    const kick = setTimeout(() => {
      runEscalationJob().catch(() => {});
      runRankingJob().catch(() => {});
    }, 5000);
    kick.unref();
    timers.push(kick);
  }
  return timers;
}
