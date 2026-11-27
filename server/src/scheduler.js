import { q, systemTx } from './db.js';
import { runEscalations } from './services/escalation.js';
import { computeRanking } from './services/ranking.js';

const ESCALATION_SECONDS = Number(process.env.ESCALATION_JOB_SECONDS || 3600);
const RANKING_SECONDS = Number(process.env.RANKING_JOB_SECONDS || 86400);

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
export const runRankingJob = () => recordRun('ranking_snapshot', (client) => computeRanking({ persist: true, caller: null, client }));

/** Start the two scheduled jobs. Intervals are configurable so the schedule can be observed. */
export function startScheduler() {
  const timers = [];
  const schedule = (name, seconds, fn) => {
    const ms = Math.max(1, seconds) * 1000;
    console.log(JSON.stringify({ level: 'info', message: 'scheduler registered', job: name, everySeconds: seconds }));
    const timer = setInterval(() => { fn().catch(() => {}); }, ms);
    timer.unref();
    timers.push(timer);
  };
  schedule('escalation', ESCALATION_SECONDS, runEscalationJob);
  schedule('ranking_snapshot', RANKING_SECONDS, runRankingJob);
  // First run shortly after boot so a fresh stack has evidence without waiting a full period.
  const kick = setTimeout(() => {
    runEscalationJob().catch(() => {});
    runRankingJob().catch(() => {});
  }, 5000);
  kick.unref();
  timers.push(kick);
  return timers;
}
