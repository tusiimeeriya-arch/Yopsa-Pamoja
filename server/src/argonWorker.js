import crypto from 'node:crypto';
import { parentPort } from 'node:worker_threads';
import { argon2id, argon2Verify } from 'hash-wasm';

// Argon2id runs here, on a worker thread, so a sign-in burst never blocks the
// API's event loop (health checks, other requests).
parentPort.on('message', async ({ id, op, password, hash, params }) => {
  try {
    if (op === 'hash') {
      const result = await argon2id({
        password,
        salt: crypto.randomBytes(16),
        parallelism: params.p,
        iterations: params.t,
        memorySize: params.m,
        hashLength: params.hashLength,
        outputType: 'encoded',
      });
      parentPort.postMessage({ id, result });
      return;
    }
    parentPort.postMessage({ id, result: await argon2Verify({ password, hash }) });
  } catch (err) {
    parentPort.postMessage({ id, error: String(err?.message || err) });
  }
});
