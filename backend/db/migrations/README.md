# Migrations

Migrations are plain SQL files named `<version>_<name>.up.sql` /
`<version>_<name>.down.sql` and are applied with
[golang-migrate](https://github.com/golang-migrate/migrate) in P1 onwards.

## Current state (P0)

`000001_baseline` creates the `pamoja` schema, the `pgcrypto` and `citext`
extensions, and the `schema_migrations` ledger (`version`, `dirty`) that
golang-migrate expects. It is idempotent because the development stack applies
it with `psql` on every boot until the migrate command lands.

## Rules from P1 onwards

1. Migrations run as a **separate owner role**, never as the API role. The API
   role has no DDL rights and cannot bypass row-level security.
2. Every migration has a tested `down` path; CI applies up → down → up against a
   throwaway PostgreSQL container.
3. Financial and audit tables are append-only: `UPDATE`/`DELETE` are revoked and
   blocked by triggers, so corrections ship as new rows, never as rewrites.
4. Production follows expand → migrate → contract; a migration must not require
   downtime on the previous release.
5. Never edit an applied migration. Add a new one.
