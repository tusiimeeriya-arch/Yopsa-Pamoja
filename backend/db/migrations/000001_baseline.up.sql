-- 000001_baseline: schema namespace, required extensions and the migration
-- ledger understood by golang-migrate.
--
-- Idempotent on purpose: until golang-migrate is wired in P1, the development
-- stack applies this file on every boot with psql.

BEGIN;

CREATE SCHEMA IF NOT EXISTS pamoja;

CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE EXTENSION IF NOT EXISTS citext;

CREATE TABLE IF NOT EXISTS public.schema_migrations (
    version bigint PRIMARY KEY,
    dirty   boolean NOT NULL DEFAULT false
);

INSERT INTO public.schema_migrations (version, dirty)
VALUES (1, false)
ON CONFLICT (version) DO NOTHING;

COMMIT;
