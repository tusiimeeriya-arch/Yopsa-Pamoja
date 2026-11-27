-- 000008_p8_offline (down)

BEGIN;

DROP TABLE IF EXISTS pamoja.sync_operation;
DROP TABLE IF EXISTS pamoja.sync_batch;

DELETE FROM public.schema_migrations WHERE version = 8;

COMMIT;
