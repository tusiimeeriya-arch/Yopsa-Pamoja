# Migrations

Migrations are plain SQL files named `<version>_<name>.up.sql` /
`<version>_<name>.down.sql`. The development stack and the database integration
tests apply the `up` files with `psql`; adopting golang-migrate remains the
target for the deployment phase, and the ledger it expects is already maintained.

## Current state

| Version | Contents |
|---|---|
| `000001_baseline` | `pamoja` schema, `pgcrypto`/`citext` extensions, `schema_migrations` ledger |
| `000002_p1_auth_rls` | P1 platform core: identity, credentials, TOTP secrets, sessions, scoped role grants, append-only audit hash chain, idempotency store, row-level security and the least-privilege roles (`pamoja_owner`, `pamoja_app`, `pamoja_readonly`, `pamoja_auth`) |

Both files are idempotent because the development stack applies them on every
boot. `000002` also pins `pgcrypto` to the `pamoja` schema so the hash-chain
functions resolve `digest()` identically in every deployment.

## Rules from P1 onwards

1. Migrations run as a **separate owner role**, never as the API role. The API
   role (`pamoja_app`) has no DDL rights and cannot bypass row-level security.
2. Every migration has a tested `down` path; CI applies up → down → up against a
   throwaway PostgreSQL container.
3. Financial and audit tables are append-only: `UPDATE`/`DELETE` are revoked and
   blocked by triggers, so corrections ship as new rows, never as rewrites.
4. Production follows expand → migrate → contract; a migration must not require
   downtime on the previous release.
5. Never edit an applied migration. Add a new one.
6. Row-level security is enabled **and forced** on every tenant or user-scoped
   table, with policies that read `pamoja.actor_id` / `pamoja.scope_ids` set by
   the transaction wrapper — never a value the client sent.
