-- Reverse of 000003_p2_registration. Drops the registration domain only; the
-- P1 identity, audit and session objects are left in place.

BEGIN;

DROP TRIGGER IF EXISTS groups_transition_guard ON pamoja.groups;
DROP FUNCTION IF EXISTS pamoja.groups_guard_transition();
DROP FUNCTION IF EXISTS pamoja.member_by_phone_hash(text);
DROP FUNCTION IF EXISTS pamoja.member_in_actor_scope(uuid);
DROP FUNCTION IF EXISTS pamoja.group_in_actor_scope(uuid);

DROP TABLE IF EXISTS pamoja.authority_record;
DROP TABLE IF EXISTS pamoja.consent_record;
DROP TABLE IF EXISTS pamoja.membership;
DROP TABLE IF EXISTS pamoja.member;
DROP TABLE IF EXISTS pamoja.group_branch_history;
DROP TABLE IF EXISTS pamoja.groups;
DROP TABLE IF EXISTS pamoja.group_type;
DROP TABLE IF EXISTS pamoja.branch;
DROP TABLE IF EXISTS pamoja.legal_entity;

DELETE FROM public.schema_migrations WHERE version = 3;

COMMIT;
