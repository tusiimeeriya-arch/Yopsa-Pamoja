-- 000006_p6_reports (down)

BEGIN;

DROP TABLE IF EXISTS pamoja.report_export;
DROP TABLE IF EXISTS pamoja.ranking_snapshot;
DROP FUNCTION IF EXISTS pamoja.append_only_evidence_guard();

DELETE FROM public.schema_migrations WHERE version = 6;

COMMIT;
