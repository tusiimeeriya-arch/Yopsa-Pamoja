import { Worker } from 'node:worker_threads';

/**
 * A small pool of worker threads that run Argon2id (src/argonWorker.js).
 *
 * Hashing is CPU-bound: on the main thread a handful of concurrent sign-ins
 * starves every other request, including the health probe. Jobs are queued per
 * worker, so the pool bounds concurrency instead of the event loop.
 *
 * An idle worker is unref'd, so a short-lived CLI process still exits; a worker
 * with a job in flight is ref'd until it answers.
 */
const SIZE = Math.max(1, Math.min(4, Number(process.env.ARGON_WORKERS) || 2));

let pool = null;
let next = 0;
let seq = 0;
const pending = new Map();

function spawn() {
  const worker = new Worker(new URL('./argonWorker.js', import.meta.url));
  worker.ids = new Set();
  worker.on('message', (msg) => {
    const job = pending.get(msg.id);
    if (!job) return;
    pending.delete(msg.id);
    worker.ids.delete(msg.id);
    if (worker.ids.size === 0) worker.unref();
    if (msg.error) job.reject(new Error(msg.error));
    else job.resolve(msg.result);
  });
  const lost = (err) => {
    for (const id of worker.ids) {
      pending.get(id)?.reject(err);
      pending.delete(id);
    }
    worker.ids.clear();
    if (pool) pool = pool.map((w) => (w === worker ? spawn() : w));
  };
  worker.on('error', lost);
  worker.on('exit', (code) => { if (code !== 0) lost(new Error(`argon2 worker exited with code ${code}`)); });
  // unref() must come after the 'message' listener: attaching the listener
  // refs the worker's port, and a short-lived CLI process (seed, checks) would
  // then never exit.
  worker.unref();
  return worker;
}

function start() {
  if (!pool) pool = Array.from({ length: SIZE }, spawn);
  return pool;
}

function run(op, payload) {
  const workers = start();
  const worker = workers[next++ % workers.length];
  const id = ++seq;
  return new Promise((resolve, reject) => {
    pending.set(id, { resolve, reject });
    worker.ids.add(id);
    worker.ref();
    worker.postMessage({ id, op, ...payload });
  });
}

export function argonHash(password, params) {
  return run('hash', { password: String(password), params });
}

export function argonVerify(password, hash) {
  return run('verify', { password: String(password), hash: String(hash) });
}
