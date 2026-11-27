import test from 'node:test';
import assert from 'node:assert/strict';
import crypto from 'node:crypto';
import { hashPassword, verifyPassword, needsRehash, verifyAndUpgrade, PASSWORD_HASH } from '../src/auth.js';

const ARGON2_PREFIX = `$argon2id$v=19$m=${PASSWORD_HASH.m},t=${PASSWORD_HASH.t},p=${PASSWORD_HASH.p}$`;

function legacyScryptHash(password) {
  const salt = crypto.randomBytes(16).toString('base64');
  const hash = crypto.scryptSync(password, salt, 64, { N: 16384, r: 8, p: 1 }).toString('base64');
  return `scrypt$16384$8$1$${salt}$${hash}`;
}

test('new hashes use Argon2id m=19456 t=2 p=1', async () => {
  const stored = await hashPassword('Correct#Horse2026');
  assert.ok(stored.startsWith(ARGON2_PREFIX), stored);
  assert.equal(needsRehash(stored), false);
  assert.equal(await verifyPassword('Correct#Horse2026', stored), true);
  assert.equal(await verifyPassword('wrong-password', stored), false);
});

test('an old scrypt hash verifies and upgrades on the next successful login', async () => {
  const legacy = legacyScryptHash('Legacy#Pass2026');
  assert.equal(needsRehash(legacy), true);

  const first = await verifyAndUpgrade('Legacy#Pass2026', legacy);
  assert.equal(first.ok, true);
  assert.ok(first.upgraded?.startsWith(ARGON2_PREFIX), first.upgraded);
  assert.equal(await verifyPassword('Legacy#Pass2026', first.upgraded), true);

  // The upgraded hash is already at the approved parameters: no second write.
  const second = await verifyAndUpgrade('Legacy#Pass2026', first.upgraded);
  assert.equal(second.ok, true);
  assert.equal(second.upgraded, null);
});

test('a failed login never produces an upgraded hash', async () => {
  const legacy = legacyScryptHash('Legacy#Pass2026');
  const result = await verifyAndUpgrade('not-the-password', legacy);
  assert.equal(result.ok, false);
  assert.equal(result.upgraded, null);
});
