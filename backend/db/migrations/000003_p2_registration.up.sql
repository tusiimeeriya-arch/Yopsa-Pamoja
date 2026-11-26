-- 000003_p2_registration: identity, organisation and authority records.
--
-- Implements ERD §1: legal entities, branches, group types, groups and their
-- branch history, members, memberships, consent records and authority records.
-- The group lifecycle guard from STATE_MACHINES §1 lives in a trigger so the
-- service layer and the database agree; SD-06 (a group is activated only by a
-- reviewer other than its creator) is enforced there as well.
--
-- Idempotent on purpose: the development stack and the integration tests apply
-- every *.up.sql on boot. Never edit an applied migration; add a new one.
--
-- `registration_evidence_id`, `resolution_evidence_id` and `evidence_id` are
-- forward references to the private object-storage records that arrive with
-- the storage service (P1/P3). They are nullable UUIDs without a foreign key
-- until that table exists.

BEGIN;

-- ---------------------------------------------------------------------------
-- 1. Organisation reference data
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS pamoja.legal_entity (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    code       text NOT NULL UNIQUE
               CHECK (code ~ '^[A-Z0-9][A-Z0-9_-]{1,31}$'),
    name       text NOT NULL CHECK (length(btrim(name)) BETWEEN 2 AND 200),
    status     text NOT NULL DEFAULT 'active'
               CHECK (status IN ('active', 'inactive')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS pamoja.branch (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    legal_entity_id uuid NOT NULL REFERENCES pamoja.legal_entity (id),
    code            text NOT NULL CHECK (code ~ '^[A-Z0-9][A-Z0-9_-]{1,31}$'),
    name            text NOT NULL CHECK (length(btrim(name)) BETWEEN 2 AND 200),
    district        text NOT NULL DEFAULT '',
    status          text NOT NULL DEFAULT 'active'
                    CHECK (status IN ('active', 'inactive')),
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (legal_entity_id, code)
);

CREATE INDEX IF NOT EXISTS branch_legal_entity_idx
    ON pamoja.branch (legal_entity_id);

CREATE TABLE IF NOT EXISTS pamoja.group_type (
    code          text PRIMARY KEY,
    name          text NOT NULL,
    rules_version text NOT NULL,
    description   text NOT NULL DEFAULT ''
);

-- The five group forms named in the brief. Seeded here so a fresh database is
-- usable without a separate seed step; operators may add more types.
INSERT INTO pamoja.group_type (code, name, rules_version, description)
VALUES
    ('vsla', 'Village savings and loan association', '1',
     'Member-owned savings and lending group.'),
    ('investment_club', 'Investment club', '1',
     'Members pool capital for joint investments.'),
    ('association', 'Association', '1',
     'Common-interest association with a constitution.'),
    ('cooperative', 'Cooperative', '1',
     'Registered cooperative society.'),
    ('sacco', 'SACCO', '1',
     'Savings and credit cooperative organisation.')
ON CONFLICT (code) DO NOTHING;

-- ---------------------------------------------------------------------------
-- 2. Groups and their branch history
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS pamoja.groups (
    id                       uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    legal_entity_id          uuid NOT NULL REFERENCES pamoja.legal_entity (id),
    branch_id                uuid NOT NULL REFERENCES pamoja.branch (id),
    type_code                text NOT NULL REFERENCES pamoja.group_type (code),
    name                     text NOT NULL CHECK (length(btrim(name)) BETWEEN 2 AND 200),
    registration_status      text NOT NULL DEFAULT 'draft'
                             CHECK (registration_status IN
                                ('draft', 'submitted', 'under_review', 'active',
                                 'returned', 'rejected', 'suspended', 'closed')),
    status                   text NOT NULL DEFAULT 'pending'
                             CHECK (status IN ('pending', 'active', 'suspended', 'closed')),
    registration_evidence_id uuid,
    formed_on                date,
    closed_on                date,
    created_by               uuid NOT NULL REFERENCES pamoja.app_user (id),
    reviewed_by              uuid REFERENCES pamoja.app_user (id),
    reviewed_at              timestamptz,
    review_note              text NOT NULL DEFAULT '',
    version                  integer NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at               timestamptz NOT NULL DEFAULT now(),
    updated_at               timestamptz NOT NULL DEFAULT now(),
    CHECK (closed_on IS NULL OR formed_on IS NULL OR closed_on >= formed_on)
);

CREATE INDEX IF NOT EXISTS groups_branch_idx ON pamoja.groups (branch_id);
CREATE INDEX IF NOT EXISTS groups_legal_entity_idx ON pamoja.groups (legal_entity_id);
CREATE INDEX IF NOT EXISTS groups_registration_status_idx
    ON pamoja.groups (registration_status);

CREATE TABLE IF NOT EXISTS pamoja.group_branch_history (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id       uuid NOT NULL REFERENCES pamoja.groups (id),
    branch_id      uuid NOT NULL REFERENCES pamoja.branch (id),
    effective_from date NOT NULL DEFAULT current_date,
    effective_to   date,
    created_at     timestamptz NOT NULL DEFAULT now(),
    CHECK (effective_to IS NULL OR effective_to >= effective_from)
);

-- A group has exactly one current branch assignment.
CREATE UNIQUE INDEX IF NOT EXISTS group_branch_history_current_idx
    ON pamoja.group_branch_history (group_id)
    WHERE effective_to IS NULL;

-- The lifecycle guard from STATE_MACHINES §1 and SD-06.
CREATE OR REPLACE FUNCTION pamoja.groups_guard_transition() RETURNS trigger
LANGUAGE plpgsql AS
$$
BEGIN
    IF NEW.registration_status IS DISTINCT FROM OLD.registration_status THEN
        IF NOT (
            (OLD.registration_status = 'draft' AND NEW.registration_status = 'submitted')
            OR (OLD.registration_status = 'submitted' AND NEW.registration_status = 'under_review')
            OR (OLD.registration_status = 'under_review'
                AND NEW.registration_status IN ('active', 'returned', 'rejected'))
            OR (OLD.registration_status = 'returned' AND NEW.registration_status = 'submitted')
            OR (OLD.registration_status = 'active' AND NEW.registration_status = 'suspended')
            OR (OLD.registration_status = 'suspended' AND NEW.registration_status = 'active')
            OR (OLD.registration_status IN ('active', 'suspended')
                AND NEW.registration_status = 'closed')
        ) THEN
            RAISE EXCEPTION 'invalid group transition % -> %',
                OLD.registration_status, NEW.registration_status
                USING ERRCODE = 'check_violation';
        END IF;

        IF NEW.registration_status = 'active'
            AND NEW.reviewed_by IS NOT NULL
            AND NEW.reviewed_by = NEW.created_by THEN
            RAISE EXCEPTION 'SD-06: a group is activated only by a reviewer other than its creator'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;
    RETURN NEW;
END
$$;

DROP TRIGGER IF EXISTS groups_transition_guard ON pamoja.groups;
CREATE TRIGGER groups_transition_guard
    BEFORE UPDATE ON pamoja.groups
    FOR EACH ROW EXECUTE FUNCTION pamoja.groups_guard_transition();

-- ---------------------------------------------------------------------------
-- 3. Members, memberships, consent and authority
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS pamoja.member (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    full_name     text NOT NULL CHECK (length(btrim(full_name)) BETWEEN 2 AND 200),
    phone_enc     bytea,
    phone_hash    text NOT NULL, -- keyed hash; the number itself is never stored here
    gender        text NOT NULL DEFAULT 'undisclosed'
                  CHECK (gender IN ('female', 'male', 'other', 'undisclosed')),
    date_of_birth date,
    status        text NOT NULL DEFAULT 'active'
                  CHECK (status IN ('active', 'inactive')),
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS member_phone_hash_idx ON pamoja.member (phone_hash);

-- Duplicate detection needs to look a member up across groups, which the
-- caller's row-level security deliberately hides. This fixed function returns
-- only the identity fields the registration service needs, never the number.
CREATE OR REPLACE FUNCTION pamoja.member_by_phone_hash(p_phone_hash text)
RETURNS TABLE (id uuid, full_name text, gender text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pamoja, pg_temp AS
$$
    SELECT m.id, m.full_name, m.gender
    FROM pamoja.member m
    WHERE m.phone_hash = p_phone_hash
    ORDER BY m.created_at
    LIMIT 1
$$;

ALTER FUNCTION pamoja.member_by_phone_hash(text) OWNER TO pamoja_auth;
REVOKE ALL ON FUNCTION pamoja.member_by_phone_hash(text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION pamoja.member_by_phone_hash(text) TO pamoja_app;

CREATE TABLE IF NOT EXISTS pamoja.membership (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    member_id  uuid NOT NULL REFERENCES pamoja.member (id),
    group_id   uuid NOT NULL REFERENCES pamoja.groups (id),
    member_no  text NOT NULL CHECK (length(btrim(member_no)) BETWEEN 1 AND 32),
    joined_on  date NOT NULL,
    left_on    date,
    status     text NOT NULL DEFAULT 'active'
               CHECK (status IN ('active', 'left')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (group_id, member_no),
    CHECK (left_on IS NULL OR left_on >= joined_on),
    CHECK ((status = 'active' AND left_on IS NULL)
        OR (status = 'left' AND left_on IS NOT NULL))
);

-- A person joins a group once at a time; re-joining after leaving is a new row.
CREATE UNIQUE INDEX IF NOT EXISTS membership_current_idx
    ON pamoja.membership (member_id, group_id)
    WHERE left_on IS NULL;

CREATE INDEX IF NOT EXISTS membership_group_idx ON pamoja.membership (group_id);

CREATE TABLE IF NOT EXISTS pamoja.consent_record (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    member_id      uuid NOT NULL REFERENCES pamoja.member (id),
    purpose        text NOT NULL CHECK (purpose IN ('membership', 'data_processing', 'photos', 'notifications')),
    granted        boolean NOT NULL,
    notice_version text NOT NULL CHECK (length(btrim(notice_version)) BETWEEN 1 AND 32),
    captured_at    timestamptz NOT NULL DEFAULT now(),
    captured_by    uuid NOT NULL REFERENCES pamoja.app_user (id),
    evidence_id    uuid,
    created_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (member_id, purpose, notice_version)
);

CREATE INDEX IF NOT EXISTS consent_member_idx ON pamoja.consent_record (member_id);

CREATE TABLE IF NOT EXISTS pamoja.authority_record (
    id                      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id                uuid NOT NULL REFERENCES pamoja.groups (id),
    member_id               uuid NOT NULL REFERENCES pamoja.member (id),
    office                  text NOT NULL
                            CHECK (office IN ('chairperson', 'secretary', 'treasurer')),
    effective_from          date NOT NULL,
    effective_to            date,
    resolution_evidence_id  uuid,
    recorded_by             uuid NOT NULL REFERENCES pamoja.app_user (id),
    created_at              timestamptz NOT NULL DEFAULT now(),
    CHECK (effective_to IS NULL OR effective_to >= effective_from)
);

-- One holder per office at a time.
CREATE UNIQUE INDEX IF NOT EXISTS authority_current_office_idx
    ON pamoja.authority_record (group_id, office)
    WHERE effective_to IS NULL;

CREATE INDEX IF NOT EXISTS authority_member_idx ON pamoja.authority_record (member_id);

-- ---------------------------------------------------------------------------
-- 4. Row-level security (ADR-0003)
--
--    Reads are scoped to the ids the transaction carries (branch, legal entity
--    or group); a creator always sees the group they are registering. Writes
--    are further constrained by the service layer, which checks the RBAC
--    module and action before touching a row.
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
ALTER TABLE pamoja.consent_record ENABLE ROW LEVEL SECURITY;
ALTER TABLE pamoja.consent_record FORCE ROW LEVEL SECURITY;
ALTER TABLE pamoja.authority_record ENABLE ROW LEVEL SECURITY;
ALTER TABLE pamoja.authority_record FORCE ROW LEVEL SECURITY;


-- Scope helpers used by policies. They are SECURITY DEFINER and owned by
-- pamoja_auth (BYPASSRLS) so a policy can ask "is this row in one of the
-- caller's scopes?" without a policy on another table re-entering this one and
-- recursing. They read only the transaction-local actor and scope settings.
CREATE OR REPLACE FUNCTION pamoja.group_in_actor_scope(p_group_id uuid)
RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pamoja, pg_temp AS
$$
    SELECT EXISTS (
        SELECT 1 FROM pamoja.groups g
        WHERE g.id = p_group_id
          AND (g.id::text = ANY (pamoja.current_scopes())
               OR g.branch_id::text = ANY (pamoja.current_scopes())
               OR g.legal_entity_id::text = ANY (pamoja.current_scopes())
               OR g.created_by = pamoja.current_actor()))
$$;

CREATE OR REPLACE FUNCTION pamoja.member_in_actor_scope(p_member_id uuid)
RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pamoja, pg_temp AS
$$
    SELECT EXISTS (
               SELECT 1 FROM pamoja.member me
               WHERE me.id = p_member_id
                 AND me.phone_hash = (
                     SELECT u.phone_hash FROM pamoja.app_user u
                     WHERE u.id = pamoja.current_actor()))
        OR EXISTS (
               SELECT 1 FROM pamoja.membership m
               WHERE m.member_id = p_member_id
                 AND pamoja.group_in_actor_scope(m.group_id))
        OR EXISTS (
               SELECT 1 FROM pamoja.consent_record c
               WHERE c.member_id = p_member_id
                 AND c.captured_by = pamoja.current_actor())
$$;

ALTER FUNCTION pamoja.group_in_actor_scope(uuid) OWNER TO pamoja_auth;
ALTER FUNCTION pamoja.member_in_actor_scope(uuid) OWNER TO pamoja_auth;
REVOKE ALL ON FUNCTION pamoja.group_in_actor_scope(uuid) FROM PUBLIC;
REVOKE ALL ON FUNCTION pamoja.member_in_actor_scope(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION pamoja.group_in_actor_scope(uuid) TO pamoja_app;
GRANT EXECUTE ON FUNCTION pamoja.member_in_actor_scope(uuid) TO pamoja_app;

DROP POLICY IF EXISTS legal_entity_scope_read ON pamoja.legal_entity;
CREATE POLICY legal_entity_scope_read ON pamoja.legal_entity
    FOR SELECT TO pamoja_app
    USING (id::text = ANY (pamoja.current_scopes()));

DROP POLICY IF EXISTS branch_scope_read ON pamoja.branch;
CREATE POLICY branch_scope_read ON pamoja.branch
    FOR SELECT TO pamoja_app
    USING (id::text = ANY (pamoja.current_scopes())
        OR legal_entity_id::text = ANY (pamoja.current_scopes()));

DROP POLICY IF EXISTS group_type_reference_read ON pamoja.group_type;
CREATE POLICY group_type_reference_read ON pamoja.group_type
    FOR SELECT TO pamoja_app
    USING (true);

DROP POLICY IF EXISTS groups_scope_read ON pamoja.groups;
CREATE POLICY groups_scope_read ON pamoja.groups
    FOR SELECT TO pamoja_app
    USING (id::text = ANY (pamoja.current_scopes())
        OR branch_id::text = ANY (pamoja.current_scopes())
        OR legal_entity_id::text = ANY (pamoja.current_scopes())
        OR created_by = pamoja.current_actor());

DROP POLICY IF EXISTS groups_scope_insert ON pamoja.groups;
CREATE POLICY groups_scope_insert ON pamoja.groups
    FOR INSERT TO pamoja_app
    WITH CHECK (created_by = pamoja.current_actor()
        AND (branch_id::text = ANY (pamoja.current_scopes())
            OR legal_entity_id::text = ANY (pamoja.current_scopes())));

DROP POLICY IF EXISTS groups_scope_update ON pamoja.groups;
CREATE POLICY groups_scope_update ON pamoja.groups
    FOR UPDATE TO pamoja_app
    USING (branch_id::text = ANY (pamoja.current_scopes())
        OR legal_entity_id::text = ANY (pamoja.current_scopes())
        OR created_by = pamoja.current_actor())
    WITH CHECK (branch_id::text = ANY (pamoja.current_scopes())
        OR legal_entity_id::text = ANY (pamoja.current_scopes())
        OR created_by = pamoja.current_actor());

DROP POLICY IF EXISTS group_branch_history_scope_read ON pamoja.group_branch_history;
CREATE POLICY group_branch_history_scope_read ON pamoja.group_branch_history
    FOR SELECT TO pamoja_app
    USING (branch_id::text = ANY (pamoja.current_scopes())
        OR group_id::text = ANY (pamoja.current_scopes())
        OR EXISTS (
            SELECT 1 FROM pamoja.groups g
            WHERE g.id = group_branch_history.group_id
              AND (g.legal_entity_id::text = ANY (pamoja.current_scopes())
                   OR g.created_by = pamoja.current_actor())));

DROP POLICY IF EXISTS group_branch_history_scope_insert ON pamoja.group_branch_history;
CREATE POLICY group_branch_history_scope_insert ON pamoja.group_branch_history
    FOR INSERT TO pamoja_app
    WITH CHECK (branch_id::text = ANY (pamoja.current_scopes())
        OR EXISTS (
            SELECT 1 FROM pamoja.groups g
            WHERE g.id = group_branch_history.group_id
              AND (g.legal_entity_id::text = ANY (pamoja.current_scopes())
                   OR g.created_by = pamoja.current_actor())));

-- A member is visible to themselves (matched by keyed phone hash), to a caller
-- who captured their consent, and to anyone whose scope contains one of their
-- groups. The helper runs without row-level security, which breaks the
-- member <-> membership policy cycle.
DROP POLICY IF EXISTS member_scope_read ON pamoja.member;
CREATE POLICY member_scope_read ON pamoja.member
    FOR SELECT TO pamoja_app
    USING (pamoja.member_in_actor_scope(id));

DROP POLICY IF EXISTS member_authenticated_insert ON pamoja.member;
CREATE POLICY member_authenticated_insert ON pamoja.member
    FOR INSERT TO pamoja_app
    WITH CHECK (pamoja.current_actor() IS NOT NULL);

DROP POLICY IF EXISTS member_authenticated_update ON pamoja.member;
CREATE POLICY member_authenticated_update ON pamoja.member
    FOR UPDATE TO pamoja_app
    USING (pamoja.current_actor() IS NOT NULL)
    WITH CHECK (pamoja.current_actor() IS NOT NULL);

DROP POLICY IF EXISTS membership_scope_read ON pamoja.membership;
CREATE POLICY membership_scope_read ON pamoja.membership
    FOR SELECT TO pamoja_app
    USING (pamoja.member_in_actor_scope(member_id));

DROP POLICY IF EXISTS membership_scope_write ON pamoja.membership;
CREATE POLICY membership_scope_write ON pamoja.membership
    FOR INSERT TO pamoja_app
    WITH CHECK (pamoja.group_in_actor_scope(group_id));

DROP POLICY IF EXISTS membership_scope_update ON pamoja.membership;
CREATE POLICY membership_scope_update ON pamoja.membership
    FOR UPDATE TO pamoja_app
    USING (pamoja.group_in_actor_scope(group_id))
    WITH CHECK (pamoja.group_in_actor_scope(group_id));

DROP POLICY IF EXISTS consent_scope_read ON pamoja.consent_record;
CREATE POLICY consent_scope_read ON pamoja.consent_record
    FOR SELECT TO pamoja_app
    USING (captured_by = pamoja.current_actor()
        OR pamoja.member_in_actor_scope(member_id));

DROP POLICY IF EXISTS consent_authenticated_write ON pamoja.consent_record;
CREATE POLICY consent_authenticated_write ON pamoja.consent_record
    FOR INSERT TO pamoja_app
    WITH CHECK (captured_by = pamoja.current_actor());

DROP POLICY IF EXISTS consent_authenticated_update ON pamoja.consent_record;
CREATE POLICY consent_authenticated_update ON pamoja.consent_record
    FOR UPDATE TO pamoja_app
    USING (captured_by = pamoja.current_actor())
    WITH CHECK (captured_by = pamoja.current_actor());

DROP POLICY IF EXISTS authority_scope_read ON pamoja.authority_record;
CREATE POLICY authority_scope_read ON pamoja.authority_record
    FOR SELECT TO pamoja_app
    USING (pamoja.group_in_actor_scope(group_id));

DROP POLICY IF EXISTS authority_scope_insert ON pamoja.authority_record;
CREATE POLICY authority_scope_insert ON pamoja.authority_record
    FOR INSERT TO pamoja_app
    WITH CHECK (recorded_by = pamoja.current_actor()
        AND pamoja.group_in_actor_scope(group_id));

DROP POLICY IF EXISTS authority_scope_update ON pamoja.authority_record;
CREATE POLICY authority_scope_update ON pamoja.authority_record
    FOR UPDATE TO pamoja_app
    USING (pamoja.group_in_actor_scope(group_id))
    WITH CHECK (pamoja.group_in_actor_scope(group_id));

-- Reporting and integrity checks may read the organisation data.
DROP POLICY IF EXISTS legal_entity_readonly_read ON pamoja.legal_entity;
CREATE POLICY legal_entity_readonly_read ON pamoja.legal_entity
    FOR SELECT TO pamoja_readonly USING (true);
DROP POLICY IF EXISTS branch_readonly_read ON pamoja.branch;
CREATE POLICY branch_readonly_read ON pamoja.branch
    FOR SELECT TO pamoja_readonly USING (true);
DROP POLICY IF EXISTS groups_readonly_read ON pamoja.groups;
CREATE POLICY groups_readonly_read ON pamoja.groups
    FOR SELECT TO pamoja_readonly USING (true);
DROP POLICY IF EXISTS member_readonly_read ON pamoja.member;
CREATE POLICY member_readonly_read ON pamoja.member
    FOR SELECT TO pamoja_readonly USING (true);

-- ---------------------------------------------------------------------------
-- 5. Grants (least privilege). No DELETE: memberships and authority records
--    are end-dated, never removed.
-- ---------------------------------------------------------------------------
GRANT SELECT, INSERT, UPDATE ON
    pamoja.legal_entity, pamoja.branch, pamoja.groups,
    pamoja.group_branch_history, pamoja.member, pamoja.membership,
    pamoja.consent_record, pamoja.authority_record
    TO pamoja_app;
GRANT SELECT ON pamoja.group_type TO pamoja_app;

-- The definer helpers and lookup function run as pamoja_auth.
GRANT SELECT ON
    pamoja.member, pamoja.membership, pamoja.groups, pamoja.consent_record
    TO pamoja_auth;

GRANT SELECT ON
    pamoja.legal_entity, pamoja.branch, pamoja.group_type, pamoja.groups,
    pamoja.group_branch_history, pamoja.member, pamoja.membership,
    pamoja.consent_record, pamoja.authority_record
    TO pamoja_readonly;

-- ---------------------------------------------------------------------------
-- 6. Migration ledger
-- ---------------------------------------------------------------------------
INSERT INTO public.schema_migrations (version, dirty)
VALUES (3, false)
ON CONFLICT (version) DO NOTHING;

COMMIT;
