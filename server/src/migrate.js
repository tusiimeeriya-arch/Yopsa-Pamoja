import fs from 'node:fs/promises';
import path from 'node:path';
import crypto from 'node:crypto';
import process from 'node:process';
import { fileURLToPath } from 'node:url';
import pg from 'pg';

/**
 * Apply the versioned SQL migrations as the owner role.
 *
 *   ADMIN_DATABASE_URL=postgres://owner:...@db:5432/pamoja npm run migrate
 *
 * Every file in server/migrations/ whose name sorts before the next is applied
 * once, in order, inside its own transaction. Applied versions and their
 * checksums live in pamoja_migrations.schema_migrations; an edited file that
 * was already applied is refused instead of silently diverging. The API never
 * runs this: it connects as the least-privilege pamoja_app role.
 */
const MIGRATIONS_DIR = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../migrations');
const LOCK_KEY = 918274;

async function files() {
  const names = (await fs.readdir(MIGRATIONS_DIR)).filter((n) => /^\d{4}_.+\.sql$/.test(n)).sort();
  if (!names.length) throw new Error(`no migrations found in ${MIGRATIONS_DIR}`);
  return Promise.all(names.map(async (name) => {
    const body = await fs.readFile(path.join(MIGRATIONS_DIR, name), 'utf8');
    return { name, body, checksum: crypto.createHash('sha256').update(body).digest('hex') };
  }));
}

async function main() {
  const url = process.env.ADMIN_DATABASE_URL;
  if (!url) {
    console.error('refusing to migrate: ADMIN_DATABASE_URL is not set (owner role is CLI-only)');
    process.exit(1);
  }
  const client = new pg.Client({ connectionString: url });
  await client.connect();
  let applied = 0;
  try {
    await client.query('SELECT pg_advisory_lock($1)', [LOCK_KEY]);
    await client.query('CREATE SCHEMA IF NOT EXISTS pamoja_migrations');
    await client.query(`CREATE TABLE IF NOT EXISTS pamoja_migrations.schema_migrations (
      version text PRIMARY KEY,
      checksum text NOT NULL,
      applied_at timestamptz NOT NULL DEFAULT now()
    )`);
    const rows = (await client.query('SELECT version, checksum FROM pamoja_migrations.schema_migrations')).rows;
    const seen = new Map(rows.map((r) => [r.version, r.checksum]));

    for (const file of await files()) {
      const previous = seen.get(file.name);
      if (previous) {
        if (previous !== file.checksum) {
          throw new Error(`${file.name} changed after it was applied (checksum ${previous.slice(0, 12)} -> ${file.checksum.slice(0, 12)}); add a new migration instead`);
        }
        console.log(`up to date ${file.name}`);
        continue;
      }
      await client.query('BEGIN');
      try {
        await client.query(file.body);
        await client.query(
          'INSERT INTO pamoja_migrations.schema_migrations (version, checksum) VALUES ($1,$2)',
          [file.name, file.checksum],
        );
        await client.query('COMMIT');
      } catch (err) {
        await client.query('ROLLBACK').catch(() => {});
        throw new Error(`${file.name}: ${err.message}`);
      }
      applied += 1;
      console.log(`applied ${file.name}`);
    }
    console.log(`migrations complete: ${applied} applied, ${seen.size + applied} total`);

    // The application role is created by 0002 with a development password. A
    // deployment supplies the real one here, as the owner, so it never has to
    // appear in a migration file or in the API's environment as owner access.
    const appPassword = process.env.APP_DB_PASSWORD;
    if (appPassword) {
      const quoted = String(appPassword).replaceAll("'", "''");
      await client.query(`ALTER ROLE pamoja_app PASSWORD '${quoted}'`);
      console.log('application role password updated');
    }
  } finally {
    await client.query('SELECT pg_advisory_unlock($1)', [LOCK_KEY]).catch(() => {});
    await client.end();
  }
}

main().catch((err) => {
  console.error(JSON.stringify({ level: 'error', message: 'migration failed', error: err.message }));
  process.exit(1);
});
