-- Down for 000003. Destructive only in development; production restores from
-- backup rather than running this file.

BEGIN;

DROP TABLE IF EXISTS pamoja.action_item;
DROP TABLE IF EXISTS pamoja.meeting_resolution;
DROP TABLE IF EXISTS pamoja.meeting_minutes;
DROP TABLE IF EXISTS pamoja.meeting_attendance;
DROP TABLE IF EXISTS pamoja.meeting;
DROP TABLE IF EXISTS pamoja.config_value;
DROP TABLE IF EXISTS pamoja.consent_record;
DROP TABLE IF EXISTS pamoja.authority_record;
DROP TABLE IF EXISTS pamoja.membership;
DROP TABLE IF EXISTS pamoja.member;
DROP TABLE IF EXISTS pamoja.group_branch_history;
DROP TABLE IF EXISTS pamoja.groups;
DROP TABLE IF EXISTS pamoja.group_type;
DROP TABLE IF EXISTS pamoja.branch;
DROP TABLE IF EXISTS pamoja.legal_entity;

DROP FUNCTION IF EXISTS pamoja.scope_contains_branch(uuid);
DROP FUNCTION IF EXISTS pamoja.scope_contains_entity(uuid);
DROP FUNCTION IF EXISTS pamoja.member_owned_by_actor(uuid);
DROP FUNCTION IF EXISTS pamoja.member_in_scope(uuid);
DROP FUNCTION IF EXISTS pamoja.scope_allows(uuid, uuid, uuid);
DROP FUNCTION IF EXISTS pamoja.is_group_member(uuid);

DELETE FROM public.schema_migrations WHERE version = 3;

COMMIT;
