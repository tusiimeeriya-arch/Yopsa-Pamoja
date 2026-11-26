-- 000002_p1_auth_rls: platform core for P1 — identity, sessions, scoped role
-- grants, the append-only audit chain, the idempotency store, row-level
-- security and the least-privilege roles from ADR-0003.
--
-- Idempotent on purpose: the development stack and the integration test apply
-- it with psql. Never edit an applied migration; add a new one.

BEGIN;

-- ---------------------------------------------------------------------------
-- 1. Database roles (ADR-0003)
--
--    pamoja_owner    owns the schema and runs migrations (no login in dev).
--    pamoja_app      the API/worker role: no DDL, no DELETE on protected
--                    tables, not the owner, not superuser, not BYPASSRLS.
--    pamoja_readonly reporting and integrity verification; SELECT only.
--    pamoja_auth     narrow non-login owner of the SECURITY DEFINER functions
--                    that must read identity rows before a session exists
--                    (login, refresh, audit chain). BYPASSRLS is confined to
--                    those fixed functions; the API role cannot use it.
-- ---------------------------------------------------------------------------
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'pamoja_owner') THEN
        CREATE ROLE pamoja_owner NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOBYPASSRLS;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'pamoja_app') THEN
        CREATE ROLE pamoja_app LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOBYPASSRLS;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'pamoja_readonly') THEN
        CREATE ROLE pamoja_readonly NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOBYPASSRLS;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'pamoja_auth') THEN
        CREATE ROLE pamoja_auth NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE BYPASSRLS;
    END IF;
END
$$;

-- The migration role must be able to hand the auth functions to pamoja_auth.
-- Creating roles with BYPASSRLS needs a superuser, so this block only runs in
-- that case; production migrations run as a superuser owner role.
DO $$
BEGIN
    IF (SELECT rolsuper FROM pg_roles WHERE rolname = current_user) THEN
        EXECUTE 'GRANT pamoja_auth TO pamoja_owner';
    END IF;
END
$$;

REVOKE ALL ON SCHEMA pamoja FROM PUBLIC;
GRANT USAGE ON SCHEMA pamoja TO pamoja_app, pamoja_readonly, pamoja_auth;

-- pgcrypto is installed by the baseline migration. The default search_path
-- ("$user", public) puts it in the pamoja schema whenever the connecting role
-- is named pamoja, so pin it there explicitly: the SECURITY DEFINER functions
-- below must resolve digest() the same way in every deployment.
CREATE EXTENSION IF NOT EXISTS pgcrypto WITH SCHEMA pamoja;
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM pg_extension e
        JOIN pg_namespace n ON n.oid = e.extnamespace
        WHERE e.extname = 'pgcrypto' AND n.nspname <> 'pamoja'
    ) THEN
        EXECUTE 'ALTER EXTENSION pgcrypto SET SCHEMA pamoja';
    END IF;
END
$$;

-- ---------------------------------------------------------------------------
-- 2. Transaction scope helpers (ARCHITECTURE §5, §7)
--
--    The pool wrapper sets these with set_config(..., true) at the start of
--    every transaction. Policies read them; nothing the client sends is used.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION pamoja.current_actor() RETURNS uuid
LANGUAGE sql STABLE AS
$$ SELECT NULLIF(current_setting('pamoja.actor_id', true), '')::uuid $$;

CREATE OR REPLACE FUNCTION pamoja.current_scopes() RETURNS text[]
LANGUAGE sql STABLE AS
$$
    SELECT COALESCE(
        string_to_array(NULLIF(current_setting('pamoja.scope_ids', true), ''), ','),
        ARRAY[]::text[])
$$;

CREATE OR REPLACE FUNCTION pamoja.current_request_id() RETURNS text
LANGUAGE sql STABLE AS
$$ SELECT COALESCE(NULLIF(current_setting('pamoja.request_id', true), ''), '') $$;

-- ---------------------------------------------------------------------------
-- 3. Identity, sessions and scoped role grants
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS pamoja.app_user (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    full_name          text NOT NULL,
    phone_hash         text NOT NULL UNIQUE, -- keyed hash, never the number
    phone_enc          bytea,                -- envelope-encrypted number
    is_staff           boolean NOT NULL DEFAULT false,
    mfa_required       boolean NOT NULL DEFAULT false,
    status             text NOT NULL DEFAULT 'active'
                       CHECK (status IN ('active', 'suspended', 'disabled')),
    failed_login_count integer NOT NULL DEFAULT 0 CHECK (failed_login_count >= 0),
    locked_until       timestamptz,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS pamoja.user_credential (
    user_id       uuid PRIMARY KEY REFERENCES pamoja.app_user (id) ON DELETE CASCADE,
    password_hash text NOT NULL,
    argon2_params jsonb NOT NULL,
    changed_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS pamoja.totp_secret (
    user_id      uuid PRIMARY KEY REFERENCES pamoja.app_user (id) ON DELETE CASCADE,
    secret_enc   bytea NOT NULL,
    confirmed_at timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS pamoja.recovery_code (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    uuid NOT NULL REFERENCES pamoja.app_user (id) ON DELETE CASCADE,
    code_hash  text NOT NULL,
    used_at    timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS recovery_code_user_idx
    ON pamoja.recovery_code (user_id) WHERE used_at IS NULL;

CREATE TABLE IF NOT EXISTS pamoja.session (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id             uuid NOT NULL REFERENCES pamoja.app_user (id) ON DELETE CASCADE,
    family_id           uuid NOT NULL,
    refresh_hash        text NOT NULL UNIQUE, -- SHA-256 of the opaque token
    device_id           text NOT NULL DEFAULT '',
    user_agent          text NOT NULL DEFAULT '',
    ip                  inet,
    shared_phone        boolean NOT NULL DEFAULT false,
    expires_at          timestamptz NOT NULL,
    absolute_expires_at timestamptz NOT NULL,
    revoked_at          timestamptz,
    revoked_reason      text NOT NULL DEFAULT '',
    last_seen_at        timestamptz NOT NULL DEFAULT now(),
    mfa_verified_at     timestamptz,
    step_up_at          timestamptz,
    created_at          timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS session_user_idx ON pamoja.session (user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS session_family_idx ON pamoja.session (family_id);

CREATE TABLE IF NOT EXISTS pamoja.role_grant (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         uuid NOT NULL REFERENCES pamoja.app_user (id) ON DELETE CASCADE,
    role            text NOT NULL,
    legal_entity_id uuid,
    branch_id       uuid,
    group_id        uuid,
    effective_from  date NOT NULL DEFAULT current_date,
    effective_to    date,
    granted_by      uuid REFERENCES pamoja.app_user (id),
    approved_by     uuid REFERENCES pamoja.app_user (id),
    status          text NOT NULL DEFAULT 'active'
                    CHECK (status IN ('pending', 'active', 'revoked')),
    created_at      timestamptz NOT NULL DEFAULT now(),
    CHECK (effective_to IS NULL OR effective_to >= effective_from)
);

CREATE INDEX IF NOT EXISTS role_grant_user_idx
    ON pamoja.role_grant (user_id) WHERE status = 'active';

-- ---------------------------------------------------------------------------
-- 4. Audit log (append-only, SHA-256 hash chain — ADR-0004)
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS pamoja.audit_log (
    seq             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    occurred_at     timestamptz NOT NULL DEFAULT clock_timestamp(),
    actor_user_id   uuid,
    actor_role      text NOT NULL DEFAULT '',
    scope_type      text NOT NULL DEFAULT '',
    scope_id        uuid,
    action          text NOT NULL,
    entity_type     text NOT NULL DEFAULT '',
    entity_id       uuid,
    before_redacted jsonb,
    after_redacted  jsonb,
    reason          text NOT NULL DEFAULT '',
    channel         text NOT NULL DEFAULT 'self',
    ip              inet,
    device_id       text NOT NULL DEFAULT '',
    correlation_id  text NOT NULL DEFAULT '',
    prev_hash       bytea NOT NULL,
    hash            bytea NOT NULL
);

CREATE INDEX IF NOT EXISTS audit_log_actor_idx ON pamoja.audit_log (actor_user_id);
CREATE INDEX IF NOT EXISTS audit_log_entity_idx ON pamoja.audit_log (entity_type, entity_id);

-- Canonical row form. Versioned by the function name; a future change ships as
-- audit_canonical_v2 so old rows keep verifying.
CREATE OR REPLACE FUNCTION pamoja.audit_canonical(
    p_seq bigint,
    p_occurred_at timestamptz,
    p_actor_user_id uuid,
    p_actor_role text,
    p_scope_type text,
    p_scope_id uuid,
    p_action text,
    p_entity_type text,
    p_entity_id uuid,
    p_before jsonb,
    p_after jsonb,
    p_reason text,
    p_channel text,
    p_ip inet,
    p_device_id text,
    p_correlation_id text
) RETURNS text
LANGUAGE sql STABLE AS
$$
    SELECT concat_ws('|',
        p_seq::text,
        to_char(p_occurred_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"'),
        COALESCE(p_actor_user_id::text, ''),
        COALESCE(p_actor_role, ''),
        COALESCE(p_scope_type, ''),
        COALESCE(p_scope_id::text, ''),
        p_action,
        COALESCE(p_entity_type, ''),
        COALESCE(p_entity_id::text, ''),
        COALESCE(p_before::text, ''),
        COALESCE(p_after::text, ''),
        COALESCE(p_reason, ''),
        COALESCE(p_channel, ''),
        COALESCE(host(p_ip), ''),
        COALESCE(p_device_id, ''),
        COALESCE(p_correlation_id, ''))
$$;

-- Chain writer: fills prev_hash and hash on insert, under a transaction-scoped
-- advisory lock so concurrent inserts cannot fork the chain.
CREATE OR REPLACE FUNCTION pamoja.audit_chain_row() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pamoja, pg_temp AS
$$
DECLARE
    v_prev bytea;
BEGIN
    PERFORM pg_advisory_xact_lock(hashtext('pamoja.audit_log'));
    SELECT a.hash INTO v_prev FROM pamoja.audit_log a ORDER BY a.seq DESC LIMIT 1;
    NEW.prev_hash := COALESCE(v_prev, ''::bytea);
    NEW.hash := pamoja.digest(
        NEW.prev_hash || convert_to(pamoja.audit_canonical(
            NEW.seq, NEW.occurred_at, NEW.actor_user_id, NEW.actor_role,
            NEW.scope_type, NEW.scope_id, NEW.action, NEW.entity_type,
            NEW.entity_id, NEW.before_redacted, NEW.after_redacted, NEW.reason,
            NEW.channel, NEW.ip, NEW.device_id, NEW.correlation_id), 'UTF8'),
        'sha256');
    RETURN NEW;
END
$$;

DROP TRIGGER IF EXISTS audit_log_chain ON pamoja.audit_log;
CREATE TRIGGER audit_log_chain
    BEFORE INSERT ON pamoja.audit_log
    FOR EACH ROW EXECUTE FUNCTION pamoja.audit_chain_row();

-- Append-only: no role may rewrite or remove a row, owner included.
CREATE OR REPLACE FUNCTION pamoja.reject_audit_mutation() RETURNS trigger
LANGUAGE plpgsql AS
$$
BEGIN
    RAISE EXCEPTION 'audit_log is append-only'
        USING ERRCODE = 'insufficient_privilege';
END
$$;

DROP TRIGGER IF EXISTS audit_log_append_only ON pamoja.audit_log;
CREATE TRIGGER audit_log_append_only
    BEFORE UPDATE OR DELETE ON pamoja.audit_log
    FOR EACH ROW EXECUTE FUNCTION pamoja.reject_audit_mutation();

DROP TRIGGER IF EXISTS audit_log_no_truncate ON pamoja.audit_log;
CREATE TRIGGER audit_log_no_truncate
    BEFORE TRUNCATE ON pamoja.audit_log
    FOR EACH STATEMENT EXECUTE FUNCTION pamoja.reject_audit_mutation();

-- Append path. Owned by pamoja_auth so the API role never needs INSERT or
-- SELECT on audit_log: rows are written only through this function and the
-- chain trigger fills prev_hash/hash.
CREATE OR REPLACE FUNCTION pamoja.audit_append(
    p_actor_user_id uuid,
    p_actor_role text,
    p_scope_type text,
    p_scope_id uuid,
    p_action text,
    p_entity_type text,
    p_entity_id uuid,
    p_before jsonb,
    p_after jsonb,
    p_reason text,
    p_channel text,
    p_ip text,
    p_device_id text,
    p_correlation_id text
) RETURNS bigint
LANGUAGE sql SECURITY DEFINER SET search_path = pamoja, pg_temp AS
$$
    INSERT INTO pamoja.audit_log (
        actor_user_id, actor_role, scope_type, scope_id, action, entity_type,
        entity_id, before_redacted, after_redacted, reason, channel, ip,
        device_id, correlation_id)
    VALUES (
        p_actor_user_id, COALESCE(p_actor_role, ''),
        COALESCE(p_scope_type, ''), p_scope_id, p_action,
        COALESCE(p_entity_type, ''), p_entity_id, p_before, p_after,
        COALESCE(p_reason, ''), COALESCE(p_channel, 'self'),
        NULLIF(p_ip, '')::inet, COALESCE(p_device_id, ''),
        COALESCE(p_correlation_id, ''))
    RETURNING seq
$$;

-- Verifier: walks the chain from p_from_seq and reports the first break.
CREATE OR REPLACE FUNCTION pamoja.audit_verify(p_from_seq bigint DEFAULT 1)
RETURNS TABLE (checked bigint, first_bad_seq bigint, reason text)
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pamoja, pg_temp AS
$$
DECLARE
    r record;
    v_prev bytea;
    v_expected bytea;
    v_checked bigint := 0;
BEGIN
    SELECT a.hash INTO v_prev FROM pamoja.audit_log a WHERE a.seq = p_from_seq - 1;

    FOR r IN SELECT * FROM pamoja.audit_log a WHERE a.seq >= p_from_seq ORDER BY a.seq LOOP
        v_expected := pamoja.digest(
            COALESCE(v_prev, ''::bytea) || convert_to(pamoja.audit_canonical(
                r.seq, r.occurred_at, r.actor_user_id, r.actor_role, r.scope_type,
                r.scope_id, r.action, r.entity_type, r.entity_id,
                r.before_redacted, r.after_redacted, r.reason, r.channel, r.ip,
                r.device_id, r.correlation_id), 'UTF8'),
            'sha256');
        IF r.prev_hash IS DISTINCT FROM COALESCE(v_prev, ''::bytea)
            OR r.hash IS DISTINCT FROM v_expected THEN
            RETURN QUERY SELECT v_checked, r.seq, 'hash chain break at seq ' || r.seq::text;
            RETURN;
        END IF;
        v_prev := r.hash;
        v_checked := v_checked + 1;
    END LOOP;

    RETURN QUERY SELECT v_checked, NULL::bigint, NULL::text;
END
$$;

-- ---------------------------------------------------------------------------
-- 5. Idempotency store (ARCHITECTURE §5, ASSUMPTIONS A-029)
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS pamoja.idempotency_key (
    id                     uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_user_id          uuid NOT NULL,
    key                    text NOT NULL,
    route                  text NOT NULL,
    request_hash           text NOT NULL,
    response_status        integer,
    response_body_redacted jsonb,
    created_at             timestamptz NOT NULL DEFAULT now(),
    expires_at             timestamptz NOT NULL,
    UNIQUE (actor_user_id, key)
);

CREATE INDEX IF NOT EXISTS idempotency_expiry_idx ON pamoja.idempotency_key (expires_at);

-- ---------------------------------------------------------------------------
-- 6. Authentication functions
--
--    Owned by pamoja_auth so they can read identity rows before an actor
--    exists. Each returns only what the authentication flow needs; the API
--    role has no direct read of credentials.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION pamoja.auth_find_user(p_phone_hash text)
RETURNS TABLE (
    user_id uuid, status text, is_staff boolean, mfa_required boolean,
    locked_until timestamptz, failed_login_count integer,
    password_hash text, argon2_params jsonb,
    totp_secret_enc bytea, totp_confirmed_at timestamptz
)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pamoja, pg_temp AS
$$
    SELECT u.id, u.status, u.is_staff, u.mfa_required, u.locked_until,
           u.failed_login_count, c.password_hash, c.argon2_params,
           t.secret_enc, t.confirmed_at
    FROM pamoja.app_user u
    LEFT JOIN pamoja.user_credential c ON c.user_id = u.id
    LEFT JOIN pamoja.totp_secret t ON t.user_id = u.id
    WHERE u.phone_hash = p_phone_hash
$$;

CREATE OR REPLACE FUNCTION pamoja.auth_user_secrets(p_user_id uuid)
RETURNS TABLE (
    user_id uuid, status text, mfa_required boolean,
    password_hash text, argon2_params jsonb,
    totp_secret_enc bytea, totp_confirmed_at timestamptz
)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pamoja, pg_temp AS
$$
    SELECT u.id, u.status, u.mfa_required, c.password_hash, c.argon2_params,
           t.secret_enc, t.confirmed_at
    FROM pamoja.app_user u
    LEFT JOIN pamoja.user_credential c ON c.user_id = u.id
    LEFT JOIN pamoja.totp_secret t ON t.user_id = u.id
    WHERE u.id = p_user_id
$$;

-- Failed login: count it and apply the exponential lockout (A-009). The
-- backoff starts at p_base_seconds once p_max_failures is reached and doubles
-- per further failure, capped at p_max_seconds.
CREATE OR REPLACE FUNCTION pamoja.auth_register_failure(
    p_user_id uuid, p_max_failures integer, p_base_seconds integer, p_max_seconds integer
) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pamoja, pg_temp AS
$$
DECLARE
    v_failures integer;
BEGIN
    UPDATE pamoja.app_user
    SET failed_login_count = failed_login_count + 1
    WHERE id = p_user_id
    RETURNING failed_login_count INTO v_failures;

    IF v_failures IS NULL THEN
        RETURN;
    END IF;

    IF v_failures >= p_max_failures THEN
        UPDATE pamoja.app_user
        SET locked_until = now() + make_interval(secs => LEAST(
                p_max_seconds,
                (p_base_seconds * power(2, v_failures - p_max_failures))::integer))
        WHERE id = p_user_id;
    END IF;
END
$$;

CREATE OR REPLACE FUNCTION pamoja.auth_record_success(p_user_id uuid) RETURNS void
LANGUAGE sql SECURITY DEFINER SET search_path = pamoja, pg_temp AS
$$
    UPDATE pamoja.app_user
    SET failed_login_count = 0, locked_until = NULL
    WHERE id = p_user_id
$$;

CREATE OR REPLACE FUNCTION pamoja.auth_create_session(
    p_user_id uuid,
    p_family_id uuid,
    p_refresh_hash text,
    p_device_id text,
    p_user_agent text,
    p_ip text,
    p_expires_at timestamptz,
    p_absolute_expires_at timestamptz,
    p_shared_phone boolean,
    p_mfa_verified boolean
) RETURNS uuid
LANGUAGE sql SECURITY DEFINER SET search_path = pamoja, pg_temp AS
$$
    INSERT INTO pamoja.session (
        user_id, family_id, refresh_hash, device_id, user_agent, ip,
        shared_phone, expires_at, absolute_expires_at, last_seen_at,
        mfa_verified_at)
    VALUES (
        p_user_id, p_family_id, p_refresh_hash, p_device_id, p_user_agent,
        NULLIF(p_ip, '')::inet, p_shared_phone, p_expires_at,
        p_absolute_expires_at, now(),
        CASE WHEN p_mfa_verified THEN now() END)
    RETURNING id
$$;

CREATE OR REPLACE FUNCTION pamoja.auth_session_by_refresh(p_refresh_hash text)
RETURNS TABLE (
    session_id uuid, user_id uuid, family_id uuid, status text,
    is_staff boolean, mfa_required boolean, shared_phone boolean,
    expires_at timestamptz, absolute_expires_at timestamptz,
    revoked_at timestamptz, revoked_reason text, last_seen_at timestamptz,
    mfa_verified_at timestamptz, step_up_at timestamptz
)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pamoja, pg_temp AS
$$
    SELECT s.id, s.user_id, s.family_id, u.status, u.is_staff, u.mfa_required,
           s.shared_phone, s.expires_at, s.absolute_expires_at, s.revoked_at,
           s.revoked_reason, s.last_seen_at, s.mfa_verified_at, s.step_up_at
    FROM pamoja.session s
    JOIN pamoja.app_user u ON u.id = s.user_id
    WHERE s.refresh_hash = p_refresh_hash
$$;

-- Rotates a refresh token in one statement: revokes the presented row and
-- inserts its successor in the same family. Zero rows means the token was
-- already rotated, which the caller treats as reuse.
CREATE OR REPLACE FUNCTION pamoja.auth_rotate_session(
    p_old_session_id uuid,
    p_refresh_hash text,
    p_device_id text,
    p_user_agent text,
    p_ip text,
    p_expires_at timestamptz,
    p_absolute_expires_at timestamptz
) RETURNS uuid
LANGUAGE sql SECURITY DEFINER SET search_path = pamoja, pg_temp AS
$$
    WITH old AS (
        UPDATE pamoja.session
        SET revoked_at = now(), revoked_reason = 'rotated'
        WHERE id = p_old_session_id AND revoked_at IS NULL
        RETURNING user_id, family_id, shared_phone, mfa_verified_at, step_up_at
    )
    INSERT INTO pamoja.session (
        user_id, family_id, refresh_hash, device_id, user_agent, ip,
        shared_phone, expires_at, absolute_expires_at, last_seen_at,
        mfa_verified_at, step_up_at)
    SELECT old.user_id, old.family_id, p_refresh_hash, p_device_id,
           p_user_agent, NULLIF(p_ip, '')::inet, old.shared_phone,
           p_expires_at, p_absolute_expires_at, now(), old.mfa_verified_at,
           old.step_up_at
    FROM old
    RETURNING id
$$;

CREATE OR REPLACE FUNCTION pamoja.auth_principal(p_session_id uuid)
RETURNS TABLE (
    session_id uuid, user_id uuid, family_id uuid, status text,
    is_staff boolean, mfa_required boolean, shared_phone boolean,
    mfa_verified_at timestamptz, step_up_at timestamptz,
    expires_at timestamptz, revoked_at timestamptz
)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pamoja, pg_temp AS
$$
    SELECT s.id, s.user_id, s.family_id, u.status, u.is_staff, u.mfa_required,
           s.shared_phone, s.mfa_verified_at, s.step_up_at, s.expires_at,
           s.revoked_at
    FROM pamoja.session s
    JOIN pamoja.app_user u ON u.id = s.user_id
    WHERE s.id = p_session_id
$$;

-- Active, in-date role grants. The API turns these into the token's scope set
-- and the RLS session variables.
CREATE OR REPLACE FUNCTION pamoja.auth_roles(p_user_id uuid)
RETURNS TABLE (role text, legal_entity_id uuid, branch_id uuid, group_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pamoja, pg_temp AS
$$
    SELECT g.role, g.legal_entity_id, g.branch_id, g.group_id
    FROM pamoja.role_grant g
    WHERE g.user_id = p_user_id
      AND g.status = 'active'
      AND g.effective_from <= current_date
      AND (g.effective_to IS NULL OR g.effective_to >= current_date)
$$;

CREATE OR REPLACE FUNCTION pamoja.auth_revoke_family(p_family_id uuid, p_reason text)
RETURNS integer
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pamoja, pg_temp AS
$$
DECLARE
    v_count integer;
BEGIN
    UPDATE pamoja.session
    SET revoked_at = now(), revoked_reason = p_reason
    WHERE family_id = p_family_id AND revoked_at IS NULL;
    GET DIAGNOSTICS v_count = ROW_COUNT;
    RETURN v_count;
END
$$;

CREATE OR REPLACE FUNCTION pamoja.auth_revoke_oldest_sessions(p_user_id uuid, p_keep integer)
RETURNS integer
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pamoja, pg_temp AS
$$
DECLARE
    v_count integer;
BEGIN
    WITH stale AS (
        SELECT id FROM pamoja.session
        WHERE user_id = p_user_id
          AND revoked_at IS NULL
          AND absolute_expires_at > now()
        ORDER BY last_seen_at DESC
        OFFSET GREATEST(p_keep, 0)
    )
    UPDATE pamoja.session s
    SET revoked_at = now(), revoked_reason = 'device_limit'
    FROM stale
    WHERE s.id = stale.id;
    GET DIAGNOSTICS v_count = ROW_COUNT;
    RETURN v_count;
END
$$;

CREATE OR REPLACE FUNCTION pamoja.auth_set_step_up(p_session_id uuid)
RETURNS timestamptz
LANGUAGE sql SECURITY DEFINER SET search_path = pamoja, pg_temp AS
$$
    UPDATE pamoja.session
    SET step_up_at = now(), mfa_verified_at = COALESCE(mfa_verified_at, now())
    WHERE id = p_session_id AND revoked_at IS NULL
    RETURNING step_up_at
$$;

-- ---------------------------------------------------------------------------
-- 7. Row-level security (ADR-0003)
-- ---------------------------------------------------------------------------
ALTER TABLE pamoja.app_user ENABLE ROW LEVEL SECURITY;
ALTER TABLE pamoja.app_user FORCE ROW LEVEL SECURITY;
ALTER TABLE pamoja.user_credential ENABLE ROW LEVEL SECURITY;
ALTER TABLE pamoja.user_credential FORCE ROW LEVEL SECURITY;
ALTER TABLE pamoja.totp_secret ENABLE ROW LEVEL SECURITY;
ALTER TABLE pamoja.totp_secret FORCE ROW LEVEL SECURITY;
ALTER TABLE pamoja.recovery_code ENABLE ROW LEVEL SECURITY;
ALTER TABLE pamoja.recovery_code FORCE ROW LEVEL SECURITY;
ALTER TABLE pamoja.session ENABLE ROW LEVEL SECURITY;
ALTER TABLE pamoja.session FORCE ROW LEVEL SECURITY;
ALTER TABLE pamoja.role_grant ENABLE ROW LEVEL SECURITY;
ALTER TABLE pamoja.role_grant FORCE ROW LEVEL SECURITY;
ALTER TABLE pamoja.audit_log ENABLE ROW LEVEL SECURITY;
ALTER TABLE pamoja.audit_log FORCE ROW LEVEL SECURITY;
ALTER TABLE pamoja.idempotency_key ENABLE ROW LEVEL SECURITY;
ALTER TABLE pamoja.idempotency_key FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS app_user_self_read ON pamoja.app_user;
CREATE POLICY app_user_self_read ON pamoja.app_user
    FOR SELECT TO pamoja_app
    USING (id = pamoja.current_actor());

DROP POLICY IF EXISTS session_self_read ON pamoja.session;
CREATE POLICY session_self_read ON pamoja.session
    FOR SELECT TO pamoja_app
    USING (user_id = pamoja.current_actor());

DROP POLICY IF EXISTS role_grant_self_read ON pamoja.role_grant;
CREATE POLICY role_grant_self_read ON pamoja.role_grant
    FOR SELECT TO pamoja_app
    USING (user_id = pamoja.current_actor());

DROP POLICY IF EXISTS idempotency_self_all ON pamoja.idempotency_key;
CREATE POLICY idempotency_self_all ON pamoja.idempotency_key
    FOR ALL TO pamoja_app
    USING (actor_user_id = pamoja.current_actor())
    WITH CHECK (actor_user_id = pamoja.current_actor());

-- Reporting and integrity checks read the whole chain.
DROP POLICY IF EXISTS audit_readonly_read ON pamoja.audit_log;
CREATE POLICY audit_readonly_read ON pamoja.audit_log
    FOR SELECT TO pamoja_readonly
    USING (true);

-- ---------------------------------------------------------------------------
-- 8. Grants (least privilege)
-- ---------------------------------------------------------------------------
GRANT SELECT ON pamoja.app_user, pamoja.session, pamoja.role_grant TO pamoja_app;
-- Readiness needs the applied migration version; the ledger lives in public.
GRANT SELECT ON public.schema_migrations TO pamoja_app;
GRANT SELECT, INSERT, UPDATE ON pamoja.idempotency_key TO pamoja_app;

GRANT SELECT ON pamoja.audit_log TO pamoja_readonly;

-- The definer functions need their own table privileges.
GRANT SELECT, INSERT, UPDATE ON
    pamoja.app_user, pamoja.user_credential, pamoja.totp_secret,
    pamoja.recovery_code, pamoja.session, pamoja.role_grant
    TO pamoja_auth;
GRANT SELECT, INSERT ON pamoja.audit_log TO pamoja_auth;

ALTER FUNCTION pamoja.audit_chain_row() OWNER TO pamoja_auth;
ALTER FUNCTION pamoja.audit_append(uuid, text, text, uuid, text, text, uuid, jsonb, jsonb, text, text, text, text, text) OWNER TO pamoja_auth;
ALTER FUNCTION pamoja.audit_verify(bigint) OWNER TO pamoja_auth;
ALTER FUNCTION pamoja.auth_find_user(text) OWNER TO pamoja_auth;
ALTER FUNCTION pamoja.auth_user_secrets(uuid) OWNER TO pamoja_auth;
ALTER FUNCTION pamoja.auth_register_failure(uuid, integer, integer, integer) OWNER TO pamoja_auth;
ALTER FUNCTION pamoja.auth_record_success(uuid) OWNER TO pamoja_auth;
ALTER FUNCTION pamoja.auth_create_session(uuid, uuid, text, text, text, text, timestamptz, timestamptz, boolean, boolean) OWNER TO pamoja_auth;
ALTER FUNCTION pamoja.auth_session_by_refresh(text) OWNER TO pamoja_auth;
ALTER FUNCTION pamoja.auth_rotate_session(uuid, text, text, text, text, timestamptz, timestamptz) OWNER TO pamoja_auth;
ALTER FUNCTION pamoja.auth_principal(uuid) OWNER TO pamoja_auth;
ALTER FUNCTION pamoja.auth_roles(uuid) OWNER TO pamoja_auth;
ALTER FUNCTION pamoja.auth_revoke_family(uuid, text) OWNER TO pamoja_auth;
ALTER FUNCTION pamoja.auth_revoke_oldest_sessions(uuid, integer) OWNER TO pamoja_auth;
ALTER FUNCTION pamoja.auth_set_step_up(uuid) OWNER TO pamoja_auth;

-- Functions are executable by PUBLIC by default; close that and grant only the
-- roles that need them.
REVOKE ALL ON FUNCTION pamoja.auth_find_user(text) FROM PUBLIC;
REVOKE ALL ON FUNCTION pamoja.auth_user_secrets(uuid) FROM PUBLIC;
REVOKE ALL ON FUNCTION pamoja.auth_register_failure(uuid, integer, integer, integer) FROM PUBLIC;
REVOKE ALL ON FUNCTION pamoja.auth_record_success(uuid) FROM PUBLIC;
REVOKE ALL ON FUNCTION pamoja.auth_create_session(uuid, uuid, text, text, text, text, timestamptz, timestamptz, boolean, boolean) FROM PUBLIC;
REVOKE ALL ON FUNCTION pamoja.auth_session_by_refresh(text) FROM PUBLIC;
REVOKE ALL ON FUNCTION pamoja.auth_rotate_session(uuid, text, text, text, text, timestamptz, timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION pamoja.auth_principal(uuid) FROM PUBLIC;
REVOKE ALL ON FUNCTION pamoja.auth_roles(uuid) FROM PUBLIC;
REVOKE ALL ON FUNCTION pamoja.auth_revoke_family(uuid, text) FROM PUBLIC;
REVOKE ALL ON FUNCTION pamoja.auth_revoke_oldest_sessions(uuid, integer) FROM PUBLIC;
REVOKE ALL ON FUNCTION pamoja.auth_set_step_up(uuid) FROM PUBLIC;
REVOKE ALL ON FUNCTION pamoja.audit_append(uuid, text, text, uuid, text, text, uuid, jsonb, jsonb, text, text, text, text, text) FROM PUBLIC;
REVOKE ALL ON FUNCTION pamoja.audit_verify(bigint) FROM PUBLIC;

GRANT EXECUTE ON FUNCTION
    pamoja.auth_find_user(text),
    pamoja.auth_user_secrets(uuid),
    pamoja.auth_register_failure(uuid, integer, integer, integer),
    pamoja.auth_record_success(uuid),
    pamoja.auth_create_session(uuid, uuid, text, text, text, text, timestamptz, timestamptz, boolean, boolean),
    pamoja.auth_session_by_refresh(text),
    pamoja.auth_rotate_session(uuid, text, text, text, text, timestamptz, timestamptz),
    pamoja.auth_principal(uuid),
    pamoja.auth_roles(uuid),
    pamoja.auth_revoke_family(uuid, text),
    pamoja.auth_revoke_oldest_sessions(uuid, integer),
    pamoja.auth_set_step_up(uuid),
    pamoja.audit_append(uuid, text, text, uuid, text, text, uuid, jsonb, jsonb, text, text, text, text, text),
    pamoja.audit_verify(bigint)
    TO pamoja_app;

GRANT EXECUTE ON FUNCTION pamoja.audit_verify(bigint) TO pamoja_readonly;

-- ---------------------------------------------------------------------------
-- 9. Migration ledger
-- ---------------------------------------------------------------------------
INSERT INTO public.schema_migrations (version, dirty)
VALUES (2, false)
ON CONFLICT (version) DO NOTHING;

COMMIT;
