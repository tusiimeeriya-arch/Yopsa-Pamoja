import process from 'node:process';
import { adminPool } from './owner.js';
import { hashPassword } from './auth.js';

/**
 * Create the first organisation administrator out of band.
 *
 *   ADMIN_EMAIL=you@yopsa.co.ug ADMIN_PASSWORD='...' npm run create-admin
 *
 * There is no default account and no default password. The password must be
 * supplied (or typed when prompted) and is stored only as an Argon2id hash.
 * The role grant is written to the audit log as a bootstrap event.
 */
function strongEnough(password) {
  return typeof password === 'string' && password.length >= 12
    && /[a-z]/.test(password) && /[A-Z]/.test(password) && /\d/.test(password) && /[^A-Za-z0-9]/.test(password);
}

async function prompt(question, { hidden = false } = {}) {
  process.stdout.write(question);
  return new Promise((resolve) => {
    let value = '';
    const stdin = process.stdin;
    stdin.resume();
    stdin.setEncoding('utf8');
    if (hidden) stdin.setRawMode?.(true);
    const onData = (chunk) => {
      for (const ch of chunk) {
        if (ch === '\n' || ch === '\r') {
          stdin.setRawMode?.(false);
          stdin.pause();
          stdin.removeListener('data', onData);
          process.stdout.write('\n');
          resolve(value);
          return;
        }
        if (ch === '\u0003') { process.stdout.write('\n'); process.exit(130); }
        if (ch === '\u007f') { value = value.slice(0, -1); continue; }
        value += ch;
        if (!hidden) process.stdout.write(ch);
      }
    };
    stdin.on('data', onData);
  });
}

async function main() {
  const email = (process.env.ADMIN_EMAIL || await prompt('Admin email: ')).trim().toLowerCase();
  if (!/^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(email)) { console.error('A valid email address is required.'); process.exit(1); }
  const name = (process.env.ADMIN_NAME || await prompt('Full name: ')).trim();
  if (name.length < 2) { console.error('A full name is required.'); process.exit(1); }
  const password = process.env.ADMIN_PASSWORD || await prompt('Password (min 12 chars, upper, lower, digit, symbol): ', { hidden: true });
  if (!strongEnough(password)) {
    console.error('Password too weak: use at least 12 characters with upper case, lower case, a digit and a symbol.');
    process.exit(1);
  }

  const client = await adminPool.connect();
  try {
    await client.query('BEGIN');
    const existing = (await client.query('SELECT id FROM users WHERE lower(email)=$1', [email])).rows[0];
    let userId;
    if (existing) {
      userId = existing.id;
      await client.query('UPDATE users SET password_hash=$2, full_name=$3, status=$4, failed_attempts=0, locked_until=NULL WHERE id=$1',
        [userId, await hashPassword(password), name, 'active']);
      console.log(`Updated the password for existing user ${email}.`);
    } else {
      userId = (await client.query(
        'INSERT INTO users (email, password_hash, full_name, status) VALUES ($1,$2,$3,$4) RETURNING id',
        [email, await hashPassword(password), name, 'active'],
      )).rows[0].id;
      console.log(`Created user ${email}.`);
    }
    const has = await client.query(
      "SELECT 1 FROM role_assignments WHERE user_id=$1 AND role='it_admin' AND scope_type='Org' AND status='active'",
      [userId],
    );
    if (!has.rowCount) {
      await client.query(
        `INSERT INTO role_assignments (user_id, role, scope_type, scope_id, effective_from, status, granted_by, approved_by)
         VALUES ($1,'it_admin','Org','org',current_date,'active',$1,$1)`,
        [userId],
      );
      console.log('Granted it_admin (Org).');
    }
    // Append a bootstrap audit entry (the audit log is append-only and chained).
    await client.query('SELECT pg_advisory_xact_lock(918273)');
    const last = await client.query('SELECT hash FROM audit_logs ORDER BY seq DESC LIMIT 1');
    const prev = last.rows[0]?.hash || 'GENESIS';
    const at = new Date().toISOString();
    const payload = {
      actor_user_id: userId, role: 'it_admin', scope_type: 'Org', scope_id: 'org',
      action: 'bootstrap.first_admin', entity: 'User', entity_id: userId,
      before: null, after: { email, role: 'it_admin', scope: 'Org' }, reason: 'Out-of-band first administrator bootstrap',
      channel: 'cli', at,
    };
    const { canonical, sha256 } = await import('./util.js');
    const hash = sha256(prev + '|' + canonical(payload));
    await client.query(
      `INSERT INTO audit_logs (actor_user_id, role, scope_type, scope_id, action, entity, entity_id, before, after, reason, channel, at, prev_hash, hash)
       VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
      [payload.actor_user_id, payload.role, payload.scope_type, payload.scope_id, payload.action, payload.entity, payload.entity_id,
        payload.before, payload.after, payload.reason, payload.channel, payload.at, prev, hash],
    );
    await client.query('COMMIT');
    console.log('First administrator is ready. Sign in and change the password after the first sign-in.');
  } catch (err) {
    await client.query('ROLLBACK').catch(() => {});
    console.error(err.message);
    process.exitCode = 1;
  } finally {
    client.release();
    await adminPool.end();
  }
}

main();
