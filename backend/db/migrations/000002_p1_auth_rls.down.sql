-- Reverse of 000002_p1_auth_rls. Drops the P1 objects and the roles it created.
-- The pamoja schema and extensions from 000001 are left in place.

BEGIN;

DROP FUNCTION IF EXISTS pamoja.auth_set_step_up(uuid);
DROP FUNCTION IF EXISTS pamoja.auth_revoke_oldest_sessions(uuid, integer);
DROP FUNCTION IF EXISTS pamoja.auth_revoke_family(uuid, text);
DROP FUNCTION IF EXISTS pamoja.auth_roles(uuid);
DROP FUNCTION IF EXISTS pamoja.auth_principal(uuid);
DROP FUNCTION IF EXISTS pamoja.auth_rotate_session(uuid, text, text, text, text, timestamptz, timestamptz);
DROP FUNCTION IF EXISTS pamoja.auth_session_by_refresh(text);
DROP FUNCTION IF EXISTS pamoja.auth_create_session(uuid, uuid, text, text, text, text, timestamptz, timestamptz, boolean, boolean);
DROP FUNCTION IF EXISTS pamoja.auth_record_success(uuid);
DROP FUNCTION IF EXISTS pamoja.auth_register_failure(uuid, integer, integer, integer);
DROP FUNCTION IF EXISTS pamoja.auth_user_secrets(uuid);
DROP FUNCTION IF EXISTS pamoja.auth_find_user(text);
DROP FUNCTION IF EXISTS pamoja.audit_verify(bigint);
DROP FUNCTION IF EXISTS pamoja.audit_append(uuid, text, text, uuid, text, text, uuid, jsonb, jsonb, text, text, text, text, text);

DROP TABLE IF EXISTS pamoja.idempotency_key;
DROP TABLE IF EXISTS pamoja.audit_log;
DROP TABLE IF EXISTS pamoja.role_grant;
DROP TABLE IF EXISTS pamoja.session;
DROP TABLE IF EXISTS pamoja.recovery_code;
DROP TABLE IF EXISTS pamoja.totp_secret;
DROP TABLE IF EXISTS pamoja.user_credential;
DROP TABLE IF EXISTS pamoja.app_user;

DROP FUNCTION IF EXISTS pamoja.audit_chain_row();
DROP FUNCTION IF EXISTS pamoja.audit_canonical(bigint, timestamptz, uuid, text, text, uuid, text, text, uuid, jsonb, jsonb, text, text, inet, text, text);
DROP FUNCTION IF EXISTS pamoja.reject_audit_mutation();
DROP FUNCTION IF EXISTS pamoja.current_request_id();
DROP FUNCTION IF EXISTS pamoja.current_scopes();
DROP FUNCTION IF EXISTS pamoja.current_actor();

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'pamoja_readonly') THEN
        DROP ROLE pamoja_readonly;
    END IF;
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'pamoja_app') THEN
        DROP ROLE pamoja_app;
    END IF;
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'pamoja_auth') THEN
        DROP ROLE pamoja_auth;
    END IF;
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'pamoja_owner') THEN
        DROP ROLE pamoja_owner;
    END IF;
END
$$;

DELETE FROM public.schema_migrations WHERE version = 2;

COMMIT;
