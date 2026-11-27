import test from 'node:test';
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { mkdtemp, writeFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';

const authUrl = new URL('../src/auth.js', import.meta.url).href;

// Regression: the worker pool spawns idle workers. If an idle worker is left
// referenced, short-lived CLI processes (seed, system checks, purge) hash once
// and then hang forever — CI stalled on `npm run seed` because of it.
test('a CLI process that hashes once exits on its own', async () => {
  const dir = await mkdtemp(path.join(tmpdir(), 'argon-exit-'));
  const file = path.join(dir, 'probe.mjs');
  await writeFile(file, `import { hashPassword } from ${JSON.stringify(authUrl)};\nawait hashPassword('Exit#Probe2026');\n`);
  try {
    const child = spawn(process.execPath, [file], { stdio: ['ignore', 'pipe', 'pipe'] });

    let stderr = '';
    child.stderr.on('data', (chunk) => { stderr += chunk; });

    const code = await new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        child.kill('SIGKILL');
        reject(new Error('hashing process did not exit within 10s (idle worker still referenced)'));
      }, 10000);
      child.on('exit', (exitCode) => { clearTimeout(timer); resolve(exitCode); });
      child.on('error', (err) => { clearTimeout(timer); reject(err); });
    });

    assert.equal(code, 0, stderr);
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
});
