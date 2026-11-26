-- Reverse of 000001_baseline. Extensions are left in place because other
-- databases on the same cluster may rely on them.

BEGIN;

DROP SCHEMA IF EXISTS pamoja CASCADE;
DELETE FROM public.schema_migrations WHERE version = 1;

COMMIT;
