-- 000007_p7_engagement (down)

BEGIN;

DROP TABLE IF EXISTS pamoja.notification;
DROP TABLE IF EXISTS pamoja.task;
DROP TABLE IF EXISTS pamoja.dispute;
DROP TABLE IF EXISTS pamoja.comment;
DROP TABLE IF EXISTS pamoja.learning_progress;
DROP TABLE IF EXISTS pamoja.learning_item;
DROP FUNCTION IF EXISTS pamoja.dispute_transition_guard();
DROP FUNCTION IF EXISTS pamoja.learning_progress_stamp();

DELETE FROM public.schema_migrations WHERE version = 7;

COMMIT;
