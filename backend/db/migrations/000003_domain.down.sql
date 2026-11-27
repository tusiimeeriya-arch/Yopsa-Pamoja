-- 000003_domain.down: remove the domain core added by the up migration.
BEGIN;

DROP FUNCTION IF EXISTS pamoja.domain_audit_count();
DROP FUNCTION IF EXISTS pamoja.domain_audit_list(integer, integer);
DROP FUNCTION IF EXISTS pamoja.domain_list_users();
DROP FUNCTION IF EXISTS pamoja.domain_user_name(uuid);
DROP FUNCTION IF EXISTS pamoja.auth_bindings(uuid);

DROP TABLE IF EXISTS pamoja.task;
DROP TABLE IF EXISTS pamoja.loan_application;
DROP TABLE IF EXISTS pamoja.contribution;
DROP TABLE IF EXISTS pamoja.attendance;
DROP TABLE IF EXISTS pamoja.meeting;
DROP TABLE IF EXISTS pamoja.group_member;
DROP TABLE IF EXISTS pamoja.grp;
DROP TABLE IF EXISTS pamoja.branch;
DROP TABLE IF EXISTS pamoja.legal_entity;

ALTER TABLE pamoja.app_user DROP COLUMN IF EXISTS phone_masked;

DELETE FROM public.schema_migrations WHERE version = 3;

COMMIT;
