-- 000003_p3_registration_meetings: P2 registration dependency + P3 meetings.
--
-- P2 was not present in the repository, so the minimum registration model that
-- P3–P5 depend on (legal entity, branch, group, member, membership, authority,
-- consent) is delivered here and marked as a dependency. Every scoped table
-- carries legal_entity_id / branch_id / group_id, forced row-level security and
-- the scope helper described in ARCHITECTURE §7.
--
-- Idempotent: the development stack applies every *.up.sql on each boot.

BEGIN;

-- ---------------------------------------------------------------------------
-- 1. Registration (P2 dependency)
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS pamoja.legal_entity (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    code       text NOT NULL UNIQUE,
    name       text NOT NULL,
    status     text NOT NULL DEFAULT 'active'
               CHECK (status IN ('active', 'suspended', 'closed')),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS pamoja.branch (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    legal_entity_id uuid NOT NULL REFERENCES pamoja.legal_entity (id),
    code            text NOT NULL UNIQUE,
    name            text NOT NULL,
    district        text NOT NULL DEFAULT '',
    status          text NOT NULL DEFAULT 'active'
                    CHECK (status IN ('active', 'suspended', 'closed')),
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS pamoja.group_type (
    code         text PRIMARY KEY,
    name         text NOT NULL,
    rules_version text NOT NULL DEFAULT 'v1'
);

INSERT INTO pamoja.group_type (code, name) VALUES
    ('vsla', 'Village savings and loan association'),
    ('investment_club', 'Investment club'),
    ('association', 'Association'),
    ('cooperative', 'Cooperative'),
    ('sacco', 'SACCO')
ON CONFLICT (code) DO NOTHING;

CREATE TABLE IF NOT EXISTS pamoja.groups (
    id                        uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    legal_entity_id           uuid NOT NULL REFERENCES pamoja.legal_entity (id),
    branch_id                 uuid NOT NULL REFERENCES pamoja.branch (id),
    type_code                 text NOT NULL REFERENCES pamoja.group_type (code),
    name                      text NOT NULL,
    registration_status       text NOT NULL DEFAULT 'active'
                              CHECK (registration_status IN ('pending', 'active', 'suspended', 'closed')),
    registration_evidence_id  uuid,
    status                    text NOT NULL DEFAULT 'active'
                              CHECK (status IN ('pending', 'active', 'suspended', 'closed')),
    formed_on                 date,
    closed_on                 date,
    created_by                uuid REFERENCES pamoja.app_user (id),
    version                   integer NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_at                timestamptz NOT NULL DEFAULT now(),
    updated_at                timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS groups_branch_idx ON pamoja.groups (branch_id);

CREATE TABLE IF NOT EXISTS pamoja.group_branch_history (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id       uuid NOT NULL REFERENCES pamoja.groups (id),
    branch_id      uuid NOT NULL REFERENCES pamoja.branch (id),
    effective_from date NOT NULL,
    effective_to   date,
    CHECK (effective_to IS NULL OR effective_to >= effective_from)
);

CREATE TABLE IF NOT EXISTS pamoja.member (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       uuid UNIQUE REFERENCES pamoja.app_user (id),
    full_name     text NOT NULL,
    phone_enc     bytea,
    phone_hash    text UNIQUE,
    gender        text NOT NULL DEFAULT 'unspecified'
                  CHECK (gender IN ('female', 'male', 'unspecified')),
    date_of_birth date,
    status        text NOT NULL DEFAULT 'active'
                  CHECK (status IN ('active', 'suspended', 'left')),
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS pamoja.membership (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    member_id       uuid NOT NULL REFERENCES pamoja.member (id),
    group_id        uuid NOT NULL REFERENCES pamoja.groups (id),
    legal_entity_id uuid NOT NULL REFERENCES pamoja.legal_entity (id),
    branch_id       uuid NOT NULL REFERENCES pamoja.branch (id),
    member_no       text NOT NULL,
    joined_on       date NOT NULL,
    left_on         date,
    status          text NOT NULL DEFAULT 'active'
                    CHECK (status IN ('active', 'left', 'suspended')),
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (group_id, member_no),
    CHECK (left_on IS NULL OR left_on >= joined_on)
);

CREATE INDEX IF NOT EXISTS membership_member_idx ON pamoja.membership (member_id);
CREATE INDEX IF NOT EXISTS membership_group_idx ON pamoja.membership (group_id);

CREATE TABLE IF NOT EXISTS pamoja.authority_record (
    id                       uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id                 uuid NOT NULL REFERENCES pamoja.groups (id),
    legal_entity_id          uuid NOT NULL REFERENCES pamoja.legal_entity (id),
    branch_id                uuid NOT NULL REFERENCES pamoja.branch (id),
    member_id                uuid NOT NULL REFERENCES pamoja.member (id),
    office                   text NOT NULL
                             CHECK (office IN ('chairperson', 'secretary', 'treasurer', 'committee_member')),
    effective_from           date NOT NULL,
    effective_to             date,
    resolution_evidence_id   uuid,
    recorded_by              uuid NOT NULL REFERENCES pamoja.app_user (id),
    created_at               timestamptz NOT NULL DEFAULT now(),
    CHECK (effective_to IS NULL OR effective_to >= effective_from)
);

CREATE INDEX IF NOT EXISTS authority_group_office_idx
    ON pamoja.authority_record (group_id, office, effective_from DESC);

CREATE TABLE IF NOT EXISTS pamoja.consent_record (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    member_id      uuid NOT NULL REFERENCES pamoja.member (id),
    purpose        text NOT NULL,
    granted        boolean NOT NULL,
    notice_version text NOT NULL,
    captured_at    timestamptz NOT NULL DEFAULT now(),
    captured_by    uuid REFERENCES pamoja.app_user (id),
    evidence_id    uuid
);

-- Scope helpers. They are created after the registration tables because a
-- LANGUAGE sql function body is validated at creation time.
CREATE OR REPLACE FUNCTION pamoja.is_group_member(p_group_id uuid)
RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pamoja, pg_temp AS
$$
    SELECT EXISTS (
        SELECT 1
        FROM pamoja.membership ms
        JOIN pamoja.member m ON m.id = ms.member_id
        WHERE ms.group_id = p_group_id
          AND ms.status = 'active'
          AND m.user_id = pamoja.current_actor()
    )
$$;

CREATE OR REPLACE FUNCTION pamoja.scope_allows(
    p_legal_entity_id uuid, p_branch_id uuid, p_group_id uuid
) RETURNS boolean
LANGUAGE sql STABLE AS
$$
    SELECT COALESCE(
        p_legal_entity_id::text = ANY (pamoja.current_scopes())
        OR p_branch_id::text = ANY (pamoja.current_scopes())
        OR p_group_id::text = ANY (pamoja.current_scopes())
        OR (p_group_id IS NOT NULL AND pamoja.is_group_member(p_group_id)),
        false)
$$;

REVOKE ALL ON FUNCTION pamoja.is_group_member(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION pamoja.is_group_member(uuid) TO pamoja_app;

-- Membership lookups used by RLS run as the definer so a policy on member and
-- a policy on membership cannot recurse into each other.
CREATE OR REPLACE FUNCTION pamoja.member_in_scope(p_member_id uuid)
RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pamoja, pg_temp AS
$$
    SELECT EXISTS (
        SELECT 1 FROM pamoja.membership ms
        WHERE ms.member_id = p_member_id
          AND pamoja.scope_allows(ms.legal_entity_id, ms.branch_id, ms.group_id)
    )
$$;

CREATE OR REPLACE FUNCTION pamoja.member_owned_by_actor(p_member_id uuid)
RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pamoja, pg_temp AS
$$
    SELECT EXISTS (
        SELECT 1 FROM pamoja.member m
        WHERE m.id = p_member_id AND m.user_id = pamoja.current_actor()
    )
$$;

REVOKE ALL ON FUNCTION pamoja.member_in_scope(uuid) FROM PUBLIC;
REVOKE ALL ON FUNCTION pamoja.member_owned_by_actor(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION pamoja.member_in_scope(uuid) TO pamoja_app;
GRANT EXECUTE ON FUNCTION pamoja.member_owned_by_actor(uuid) TO pamoja_app;

-- ---------------------------------------------------------------------------
-- 3. Configuration (versioned policy values, ASSUMPTIONS.md)
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS pamoja.config_value (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    key             text NOT NULL,
    version         integer NOT NULL CHECK (version >= 1),
    value           jsonb NOT NULL,
    description     text NOT NULL DEFAULT '',
    legal_entity_id uuid REFERENCES pamoja.legal_entity (id),
    approved_by     uuid REFERENCES pamoja.app_user (id),
    effective_from  date NOT NULL DEFAULT current_date,
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (key, version)
);

INSERT INTO pamoja.config_value (key, version, value, description) VALUES
    ('attendance.late_counts_as_present', 1, 'true'::jsonb, 'A-002'),
    ('attendance.excused_counts_as_present', 1, 'true'::jsonb, 'A-003'),
    ('attendance.expected_rule', 1,
     '{"basis":"active_membership","exclude_cancelled":true,"exclude_before_join":true}'::jsonb, 'A-004'),
    ('payments.partial_verification', 1, 'true'::jsonb, 'A-016 — never spread a shortage'),
    ('payments.cash_path', 1,
     '{"receipt":true,"handover":true,"deposit":true,"distinct_people":true}'::jsonb, 'A-017'),
    ('loans.guarantor_threshold_ugx', 1, '500000'::jsonb, 'A-010'),
    ('loans.product_limits', 1,
     '{"vsla":{"min":1000000,"max":10000000},"investment_club":{"min":2000000,"max":20000000},"association":{"min":5000000,"max":50000000},"cooperative":{"min":10000000,"max":100000000},"sacco":{"min":10000000,"max":100000000},"max_term_months":24}'::jsonb,
     'A-011 — placeholder until product policy is signed'),
    ('loans.approval_authority', 1,
     '{"credit_approver_max":5000000,"committee_max":20000000,"two_approvers_above":20000000}'::jsonb,
     'A-012'),
    ('ranking.policy', 1,
     '{"window_days":90,"min_loans":1,"min_meetings":4,"weights":{"on_time":50,"arrears":25,"attendance":15,"records":10},"overdue_exclusion_days":30,"tie_break":["attendance","verified_repayment","group_id"]}'::jsonb,
     'A-013 — not activated for public display'),
    ('exports.max_rows', 1, '50000'::jsonb, 'A-025'),
    ('exports.watermark', 1, 'true'::jsonb, 'A-025'),
    ('idempotency.ttl_hours', 1, '24'::jsonb, 'A-029'),
    ('tasks.default_due_days', 1, '7'::jsonb, 'A-022'),
    ('timezone', 1, '"Africa/Kampala"'::jsonb, 'A-015')
ON CONFLICT (key, version) DO NOTHING;

-- ---------------------------------------------------------------------------
-- 4. Meetings, register, minutes, resolutions, action items (P3)
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS pamoja.meeting (
    id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    legal_entity_id       uuid NOT NULL REFERENCES pamoja.legal_entity (id),
    branch_id             uuid NOT NULL REFERENCES pamoja.branch (id),
    group_id              uuid NOT NULL REFERENCES pamoja.groups (id),
    scheduled_for         timestamptz NOT NULL,
    location              text NOT NULL DEFAULT '',
    status                text NOT NULL DEFAULT 'scheduled'
                          CHECK (status IN ('scheduled', 'open', 'register_confirmed', 'closed', 'amended', 'cancelled')),
    cancellation_reason   text NOT NULL DEFAULT '',
    opened_by             uuid REFERENCES pamoja.app_user (id),
    opened_at             timestamptz,
    register_confirmed_by uuid REFERENCES pamoja.app_user (id),
    register_confirmed_at timestamptz,
    closed_by             uuid REFERENCES pamoja.app_user (id),
    closed_at             timestamptz,
    version               integer NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_by            uuid NOT NULL REFERENCES pamoja.app_user (id),
    created_at            timestamptz NOT NULL DEFAULT now(),
    updated_at            timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS meeting_group_idx ON pamoja.meeting (group_id, scheduled_for DESC);

CREATE TABLE IF NOT EXISTS pamoja.meeting_attendance (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    legal_entity_id    uuid NOT NULL REFERENCES pamoja.legal_entity (id),
    branch_id          uuid NOT NULL REFERENCES pamoja.branch (id),
    group_id           uuid NOT NULL REFERENCES pamoja.groups (id),
    meeting_id         uuid NOT NULL REFERENCES pamoja.meeting (id),
    member_id          uuid NOT NULL REFERENCES pamoja.member (id),
    status             text NOT NULL DEFAULT 'present'
                       CHECK (status IN ('present', 'late', 'excused', 'absent')),
    checkin_channel    text NOT NULL DEFAULT 'self'
                       CHECK (checkin_channel IN ('self', 'assisted', 'secretary')),
    recorded_by        uuid NOT NULL REFERENCES pamoja.app_user (id),
    recorded_at        timestamptz NOT NULL DEFAULT now(),
    confirmed_by       uuid REFERENCES pamoja.app_user (id),
    confirmed_at       timestamptz,
    amendment_of_id    uuid REFERENCES pamoja.meeting_attendance (id),
    amendment_reason   text NOT NULL DEFAULT '',
    reviewer_id        uuid REFERENCES pamoja.app_user (id),
    version            integer NOT NULL DEFAULT 1 CHECK (version >= 1),
    CHECK (amendment_of_id IS NULL OR length(btrim(amendment_reason)) > 0)
);

CREATE UNIQUE INDEX IF NOT EXISTS meeting_attendance_original_uidx
    ON pamoja.meeting_attendance (meeting_id, member_id)
    WHERE amendment_of_id IS NULL;

CREATE INDEX IF NOT EXISTS meeting_attendance_meeting_idx
    ON pamoja.meeting_attendance (meeting_id);

CREATE TABLE IF NOT EXISTS pamoja.meeting_minutes (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    legal_entity_id uuid NOT NULL REFERENCES pamoja.legal_entity (id),
    branch_id       uuid NOT NULL REFERENCES pamoja.branch (id),
    group_id        uuid NOT NULL REFERENCES pamoja.groups (id),
    meeting_id      uuid NOT NULL REFERENCES pamoja.meeting (id),
    body            text NOT NULL,
    version         integer NOT NULL DEFAULT 1 CHECK (version >= 1),
    confirmed_by    uuid REFERENCES pamoja.app_user (id),
    confirmed_at    timestamptz,
    amendment_of_id uuid REFERENCES pamoja.meeting_minutes (id),
    created_by      uuid NOT NULL REFERENCES pamoja.app_user (id),
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS pamoja.meeting_resolution (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    legal_entity_id uuid NOT NULL REFERENCES pamoja.legal_entity (id),
    branch_id       uuid NOT NULL REFERENCES pamoja.branch (id),
    group_id        uuid NOT NULL REFERENCES pamoja.groups (id),
    meeting_id      uuid NOT NULL REFERENCES pamoja.meeting (id),
    body            text NOT NULL,
    votes_for       integer NOT NULL DEFAULT 0 CHECK (votes_for >= 0),
    votes_against   integer NOT NULL DEFAULT 0 CHECK (votes_against >= 0),
    abstentions     integer NOT NULL DEFAULT 0 CHECK (abstentions >= 0),
    recorded_by     uuid NOT NULL REFERENCES pamoja.app_user (id),
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS pamoja.action_item (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    legal_entity_id uuid NOT NULL REFERENCES pamoja.legal_entity (id),
    branch_id       uuid NOT NULL REFERENCES pamoja.branch (id),
    group_id        uuid NOT NULL REFERENCES pamoja.groups (id),
    meeting_id      uuid NOT NULL REFERENCES pamoja.meeting (id),
    owner_user_id   uuid NOT NULL REFERENCES pamoja.app_user (id),
    description     text NOT NULL,
    due_on          date NOT NULL,
    status          text NOT NULL DEFAULT 'open'
                    CHECK (status IN ('open', 'done', 'escalated')),
    closed_at       timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now()
);

-- ---------------------------------------------------------------------------
-- 5. Database-enforced workflow and immutability
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION pamoja.meeting_transition_guard() RETURNS trigger
LANGUAGE plpgsql AS
$$
BEGIN
    IF NEW.status = OLD.status THEN
        RETURN NEW;
    END IF;
    IF (OLD.status = 'scheduled' AND NEW.status IN ('open', 'cancelled'))
       OR (OLD.status = 'open' AND NEW.status IN ('register_confirmed', 'cancelled'))
       OR (OLD.status = 'register_confirmed' AND NEW.status = 'closed')
       OR (OLD.status = 'closed' AND NEW.status = 'amended')
       OR (OLD.status = 'amended' AND NEW.status = 'closed') THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'invalid meeting transition % -> %', OLD.status, NEW.status
        USING ERRCODE = '23514';
END
$$;

DROP TRIGGER IF EXISTS meeting_transition_guard ON pamoja.meeting;
CREATE TRIGGER meeting_transition_guard
    BEFORE UPDATE ON pamoja.meeting
    FOR EACH ROW EXECUTE FUNCTION pamoja.meeting_transition_guard();

-- A confirmed register is immutable; a correction is a new row that names the
-- original, the reason and an independent reviewer (SD-09).
CREATE OR REPLACE FUNCTION pamoja.attendance_guard() RETURNS trigger
LANGUAGE plpgsql AS
$$
DECLARE
    v_meeting_status text;
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'attendance rows are never deleted' USING ERRCODE = '23514';
    END IF;

    SELECT status INTO v_meeting_status FROM pamoja.meeting WHERE id = NEW.meeting_id;

    IF TG_OP = 'UPDATE' THEN
        IF OLD.confirmed_at IS NOT NULL THEN
            RAISE EXCEPTION 'a confirmed attendance row is immutable; use the amendment path'
                USING ERRCODE = '23514';
        END IF;
        IF OLD.meeting_id <> NEW.meeting_id OR OLD.member_id <> NEW.member_id THEN
            RAISE EXCEPTION 'attendance identity cannot change' USING ERRCODE = '23514';
        END IF;
        RETURN NEW;
    END IF;

    IF NEW.amendment_of_id IS NOT NULL THEN
        IF v_meeting_status NOT IN ('closed', 'amended') THEN
            RAISE EXCEPTION 'attendance can be amended only after the register is closed'
                USING ERRCODE = '23514';
        END IF;
        IF NEW.reviewer_id IS NULL OR NEW.reviewer_id = NEW.recorded_by THEN
            RAISE EXCEPTION 'an amendment needs an independent reviewer (SD-09)'
                USING ERRCODE = '23514';
        END IF;
    ELSE
        IF v_meeting_status <> 'open' THEN
            RAISE EXCEPTION 'check-in is allowed only while the register is open'
                USING ERRCODE = '23514';
        END IF;
    END IF;
    RETURN NEW;
END
$$;

DROP TRIGGER IF EXISTS attendance_guard ON pamoja.meeting_attendance;
CREATE TRIGGER attendance_guard
    BEFORE INSERT OR UPDATE OR DELETE ON pamoja.meeting_attendance
    FOR EACH ROW EXECUTE FUNCTION pamoja.attendance_guard();

-- Minutes are retained; an amendment is a new row.
CREATE OR REPLACE FUNCTION pamoja.minutes_guard() RETURNS trigger
LANGUAGE plpgsql AS
$$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'minutes are never deleted' USING ERRCODE = '23514';
    END IF;
    IF TG_OP = 'UPDATE' AND OLD.confirmed_at IS NOT NULL THEN
        RAISE EXCEPTION 'confirmed minutes are immutable; use the amendment path'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END
$$;

DROP TRIGGER IF EXISTS minutes_guard ON pamoja.meeting_minutes;
CREATE TRIGGER minutes_guard
    BEFORE UPDATE OR DELETE ON pamoja.meeting_minutes
    FOR EACH ROW EXECUTE FUNCTION pamoja.minutes_guard();

-- A group-scoped binding also reaches the group's branch and legal entity for
-- read-only scope metadata. These helpers run as the definer so the lookup
-- itself is not blocked by RLS.
CREATE OR REPLACE FUNCTION pamoja.scope_contains_entity(p_legal_entity_id uuid)
RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pamoja, pg_temp AS
$$
    SELECT p_legal_entity_id::text = ANY (pamoja.current_scopes())
        OR EXISTS (
            SELECT 1 FROM pamoja.groups g
            WHERE g.legal_entity_id = p_legal_entity_id
              AND g.id::text = ANY (pamoja.current_scopes()))
$$;

CREATE OR REPLACE FUNCTION pamoja.scope_contains_branch(p_branch_id uuid)
RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pamoja, pg_temp AS
$$
    SELECT p_branch_id::text = ANY (pamoja.current_scopes())
        OR EXISTS (
            SELECT 1 FROM pamoja.groups g
            WHERE g.branch_id = p_branch_id
              AND g.id::text = ANY (pamoja.current_scopes()))
$$;

REVOKE ALL ON FUNCTION pamoja.scope_contains_entity(uuid) FROM PUBLIC;
REVOKE ALL ON FUNCTION pamoja.scope_contains_branch(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION pamoja.scope_contains_entity(uuid) TO pamoja_app;
GRANT EXECUTE ON FUNCTION pamoja.scope_contains_branch(uuid) TO pamoja_app;

-- ---------------------------------------------------------------------------
-- 6. Row-level security and least-privilege grants
-- ---------------------------------------------------------------------------
ALTER TABLE pamoja.legal_entity ENABLE ROW LEVEL SECURITY;
ALTER TABLE pamoja.legal_entity FORCE ROW LEVEL SECURITY;
ALTER TABLE pamoja.branch ENABLE ROW LEVEL SECURITY;
ALTER TABLE pamoja.branch FORCE ROW LEVEL SECURITY;
ALTER TABLE pamoja.group_type ENABLE ROW LEVEL SECURITY;
ALTER TABLE pamoja.group_type FORCE ROW LEVEL SECURITY;
ALTER TABLE pamoja.groups ENABLE ROW LEVEL SECURITY;
ALTER TABLE pamoja.groups FORCE ROW LEVEL SECURITY;
ALTER TABLE pamoja.group_branch_history ENABLE ROW LEVEL SECURITY;
ALTER TABLE pamoja.group_branch_history FORCE ROW LEVEL SECURITY;
ALTER TABLE pamoja.member ENABLE ROW LEVEL SECURITY;
ALTER TABLE pamoja.member FORCE ROW LEVEL SECURITY;
ALTER TABLE pamoja.membership ENABLE ROW LEVEL SECURITY;
ALTER TABLE pamoja.membership FORCE ROW LEVEL SECURITY;
ALTER TABLE pamoja.authority_record ENABLE ROW LEVEL SECURITY;
ALTER TABLE pamoja.authority_record FORCE ROW LEVEL SECURITY;
ALTER TABLE pamoja.consent_record ENABLE ROW LEVEL SECURITY;
ALTER TABLE pamoja.consent_record FORCE ROW LEVEL SECURITY;
ALTER TABLE pamoja.config_value ENABLE ROW LEVEL SECURITY;
ALTER TABLE pamoja.config_value FORCE ROW LEVEL SECURITY;
ALTER TABLE pamoja.meeting ENABLE ROW LEVEL SECURITY;
ALTER TABLE pamoja.meeting FORCE ROW LEVEL SECURITY;
ALTER TABLE pamoja.meeting_attendance ENABLE ROW LEVEL SECURITY;
ALTER TABLE pamoja.meeting_attendance FORCE ROW LEVEL SECURITY;
ALTER TABLE pamoja.meeting_minutes ENABLE ROW LEVEL SECURITY;
ALTER TABLE pamoja.meeting_minutes FORCE ROW LEVEL SECURITY;
ALTER TABLE pamoja.meeting_resolution ENABLE ROW LEVEL SECURITY;
ALTER TABLE pamoja.meeting_resolution FORCE ROW LEVEL SECURITY;
ALTER TABLE pamoja.action_item ENABLE ROW LEVEL SECURITY;
ALTER TABLE pamoja.action_item FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS legal_entity_scope ON pamoja.legal_entity;
CREATE POLICY legal_entity_scope ON pamoja.legal_entity
    FOR ALL TO pamoja_app
    USING (pamoja.scope_contains_entity(id))
    WITH CHECK (pamoja.scope_contains_entity(id));

DROP POLICY IF EXISTS branch_scope ON pamoja.branch;
CREATE POLICY branch_scope ON pamoja.branch
    FOR ALL TO pamoja_app
    USING (pamoja.scope_contains_branch(id)
           OR pamoja.scope_contains_entity(legal_entity_id))
    WITH CHECK (pamoja.scope_contains_branch(id)
           OR pamoja.scope_contains_entity(legal_entity_id));

DROP POLICY IF EXISTS group_type_read ON pamoja.group_type;
CREATE POLICY group_type_read ON pamoja.group_type
    FOR SELECT TO pamoja_app USING (true);

DROP POLICY IF EXISTS groups_scope ON pamoja.groups;
CREATE POLICY groups_scope ON pamoja.groups
    FOR ALL TO pamoja_app
    USING (pamoja.scope_allows(legal_entity_id, branch_id, id))
    WITH CHECK (pamoja.scope_allows(legal_entity_id, branch_id, id));

DROP POLICY IF EXISTS group_branch_history_scope ON pamoja.group_branch_history;
CREATE POLICY group_branch_history_scope ON pamoja.group_branch_history
    FOR ALL TO pamoja_app
    USING (pamoja.scope_allows(NULL, branch_id, group_id))
    WITH CHECK (pamoja.scope_allows(NULL, branch_id, group_id));

DROP POLICY IF EXISTS member_scope ON pamoja.member;
CREATE POLICY member_scope ON pamoja.member
    FOR ALL TO pamoja_app
    USING (user_id = pamoja.current_actor() OR pamoja.member_in_scope(id))
    WITH CHECK (user_id = pamoja.current_actor() OR pamoja.member_in_scope(id));

DROP POLICY IF EXISTS membership_scope ON pamoja.membership;
CREATE POLICY membership_scope ON pamoja.membership
    FOR ALL TO pamoja_app
    USING (pamoja.scope_allows(legal_entity_id, branch_id, group_id)
           OR pamoja.member_owned_by_actor(member_id))
    WITH CHECK (pamoja.scope_allows(legal_entity_id, branch_id, group_id));

DROP POLICY IF EXISTS authority_scope ON pamoja.authority_record;
CREATE POLICY authority_scope ON pamoja.authority_record
    FOR ALL TO pamoja_app
    USING (pamoja.scope_allows(legal_entity_id, branch_id, group_id))
    WITH CHECK (pamoja.scope_allows(legal_entity_id, branch_id, group_id));

DROP POLICY IF EXISTS consent_scope ON pamoja.consent_record;
CREATE POLICY consent_scope ON pamoja.consent_record
    FOR ALL TO pamoja_app
    USING (pamoja.member_owned_by_actor(member_id) OR pamoja.member_in_scope(member_id))
    WITH CHECK (pamoja.member_owned_by_actor(member_id) OR pamoja.member_in_scope(member_id));

DROP POLICY IF EXISTS config_read ON pamoja.config_value;
CREATE POLICY config_read ON pamoja.config_value
    FOR SELECT TO pamoja_app USING (true);
DROP POLICY IF EXISTS config_write ON pamoja.config_value;
CREATE POLICY config_write ON pamoja.config_value
    FOR ALL TO pamoja_app
    USING (legal_entity_id IS NULL OR legal_entity_id::text = ANY (pamoja.current_scopes()))
    WITH CHECK (legal_entity_id IS NULL OR legal_entity_id::text = ANY (pamoja.current_scopes()));

DROP POLICY IF EXISTS meeting_scope ON pamoja.meeting;
CREATE POLICY meeting_scope ON pamoja.meeting
    FOR ALL TO pamoja_app
    USING (pamoja.scope_allows(legal_entity_id, branch_id, group_id))
    WITH CHECK (pamoja.scope_allows(legal_entity_id, branch_id, group_id));

DROP POLICY IF EXISTS attendance_scope ON pamoja.meeting_attendance;
CREATE POLICY attendance_scope ON pamoja.meeting_attendance
    FOR ALL TO pamoja_app
    USING (pamoja.scope_allows(legal_entity_id, branch_id, group_id))
    WITH CHECK (pamoja.scope_allows(legal_entity_id, branch_id, group_id));

DROP POLICY IF EXISTS minutes_scope ON pamoja.meeting_minutes;
CREATE POLICY minutes_scope ON pamoja.meeting_minutes
    FOR ALL TO pamoja_app
    USING (pamoja.scope_allows(legal_entity_id, branch_id, group_id))
    WITH CHECK (pamoja.scope_allows(legal_entity_id, branch_id, group_id));

DROP POLICY IF EXISTS resolution_scope ON pamoja.meeting_resolution;
CREATE POLICY resolution_scope ON pamoja.meeting_resolution
    FOR ALL TO pamoja_app
    USING (pamoja.scope_allows(legal_entity_id, branch_id, group_id))
    WITH CHECK (pamoja.scope_allows(legal_entity_id, branch_id, group_id));

DROP POLICY IF EXISTS action_item_scope ON pamoja.action_item;
CREATE POLICY action_item_scope ON pamoja.action_item
    FOR ALL TO pamoja_app
    USING (pamoja.scope_allows(legal_entity_id, branch_id, group_id))
    WITH CHECK (pamoja.scope_allows(legal_entity_id, branch_id, group_id));

GRANT SELECT, INSERT, UPDATE ON
    pamoja.legal_entity, pamoja.branch, pamoja.group_type, pamoja.groups,
    pamoja.group_branch_history, pamoja.member, pamoja.membership,
    pamoja.authority_record, pamoja.consent_record, pamoja.config_value,
    pamoja.meeting, pamoja.meeting_attendance, pamoja.meeting_minutes,
    pamoja.meeting_resolution, pamoja.action_item
    TO pamoja_app;

-- Financial/workflow history is never deleted; only draft rows may be removed
-- and the triggers above block the protected paths.
GRANT DELETE ON pamoja.meeting_attendance, pamoja.meeting_minutes TO pamoja_app;

GRANT SELECT ON ALL TABLES IN SCHEMA pamoja TO pamoja_readonly;

-- ---------------------------------------------------------------------------
-- 7. Migration ledger
-- ---------------------------------------------------------------------------
INSERT INTO public.schema_migrations (version, dirty)
VALUES (3, false)
ON CONFLICT (version) DO NOTHING;

COMMIT;
