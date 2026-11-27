-- 000003_domain: the P2–P6 domain core that the web client and the Flutter
-- client both read: legal entity, branches, groups, membership, meetings,
-- attendance, payment entries, group borrowing applications and follow-up
-- tasks.
--
-- Idempotent on purpose: the development stack applies it with psql on every
-- boot. Never edit an applied migration; add a new one.
--
-- Security posture: every domain table has row-level security enabled and
-- forced, and the API role (pamoja_app) may only read and write while an
-- authenticated actor is set on the transaction (pamoja.current_actor(), set by
-- db.WithActor). DELETE is never granted: domain records are retired by status,
-- not removed, so the audit trail stays complete. Row scoping (which group a
-- caller may see) is enforced in the service layer from the machine-checked
-- RBAC matrix.

BEGIN;

-- ---------------------------------------------------------------------------
-- 1. Display-only identity data
--
--    The phone number itself stays envelope-encrypted in phone_enc. The client
--    needs something readable in a member list, so the masked form is stored
--    separately and never contains more than the country code and last digits.
-- ---------------------------------------------------------------------------
ALTER TABLE pamoja.app_user
    ADD COLUMN IF NOT EXISTS phone_masked text NOT NULL DEFAULT '';

-- ---------------------------------------------------------------------------
-- 2. Organisation: legal entity and branch
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS pamoja.legal_entity (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name       text NOT NULL,
    country    text NOT NULL DEFAULT 'Uganda',
    currency   text NOT NULL DEFAULT 'UGX',
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS pamoja.branch (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    legal_entity_id uuid NOT NULL REFERENCES pamoja.legal_entity (id),
    name            text NOT NULL,
    code            text NOT NULL UNIQUE,
    district        text NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS branch_entity_idx ON pamoja.branch (legal_entity_id);

-- ---------------------------------------------------------------------------
-- 3. Groups and membership
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS pamoja.grp (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    branch_id         uuid NOT NULL REFERENCES pamoja.branch (id),
    code              text NOT NULL UNIQUE,
    name              text NOT NULL,
    group_type        text NOT NULL DEFAULT 'vsla'
                      CHECK (group_type IN ('vsla', 'investment_club', 'association', 'cooperative', 'sacco')),
    status            text NOT NULL DEFAULT 'active'
                      CHECK (status IN ('draft', 'pending_activation', 'active', 'suspended', 'closed')),
    formed_on         date NOT NULL,
    meeting_day       text NOT NULL DEFAULT 'Sunday',
    meeting_frequency text NOT NULL DEFAULT 'weekly'
                      CHECK (meeting_frequency IN ('weekly', 'fortnightly', 'monthly')),
    village           text NOT NULL DEFAULT '',
    sub_county        text NOT NULL DEFAULT '',
    district          text NOT NULL DEFAULT '',
    cycle_start       date,
    cycle_end         date,
    created_by        uuid REFERENCES pamoja.app_user (id),
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS grp_branch_idx ON pamoja.grp (branch_id);
CREATE INDEX IF NOT EXISTS grp_status_idx ON pamoja.grp (status);

CREATE TABLE IF NOT EXISTS pamoja.group_member (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id      uuid NOT NULL REFERENCES pamoja.grp (id),
    user_id       uuid REFERENCES pamoja.app_user (id),
    member_no     text NOT NULL,
    full_name     text NOT NULL,
    phone_masked  text NOT NULL DEFAULT '',
    phone_hash    text NOT NULL DEFAULT '',
    gender        text NOT NULL DEFAULT 'female'
                  CHECK (gender IN ('female', 'male', 'other')),
    role_in_group text NOT NULL DEFAULT 'member'
                  CHECK (role_in_group IN ('chairperson', 'secretary', 'treasurer', 'member')),
    status        text NOT NULL DEFAULT 'active'
                  CHECK (status IN ('active', 'inactive', 'exited')),
    joined_on     date NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (group_id, member_no)
);

CREATE INDEX IF NOT EXISTS group_member_group_idx ON pamoja.group_member (group_id);
CREATE INDEX IF NOT EXISTS group_member_user_idx ON pamoja.group_member (user_id);

-- ---------------------------------------------------------------------------
-- 4. Meetings, registers and attendance
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS pamoja.meeting (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id      uuid NOT NULL REFERENCES pamoja.grp (id),
    meeting_no    integer NOT NULL CHECK (meeting_no > 0),
    held_on       date NOT NULL,
    meeting_type  text NOT NULL DEFAULT 'regular'
                  CHECK (meeting_type IN ('regular', 'special', 'annual')),
    status        text NOT NULL DEFAULT 'draft'
                  CHECK (status IN ('draft', 'closed')),
    location      text NOT NULL DEFAULT '',
    agenda        text NOT NULL DEFAULT '',
    minutes       text NOT NULL DEFAULT '',
    created_by    uuid REFERENCES pamoja.app_user (id),
    closed_at     timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (group_id, meeting_no)
);

CREATE INDEX IF NOT EXISTS meeting_group_idx ON pamoja.meeting (group_id, held_on DESC);

CREATE TABLE IF NOT EXISTS pamoja.attendance (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    meeting_id uuid NOT NULL REFERENCES pamoja.meeting (id),
    member_id  uuid NOT NULL REFERENCES pamoja.group_member (id),
    status     text NOT NULL DEFAULT 'present'
               CHECK (status IN ('present', 'absent', 'excused', 'late')),
    note       text NOT NULL DEFAULT '',
    UNIQUE (meeting_id, member_id)
);

CREATE INDEX IF NOT EXISTS attendance_meeting_idx ON pamoja.attendance (meeting_id);

-- ---------------------------------------------------------------------------
-- 5. Payment entries (savings, shares, social fund, loan repayment, fines)
--
--    Recording and verifying are separate: the recorder submits, an independent
--    verifier confirms (SD-01, SD-02). Money is BIGINT whole UGX (ADR-0009).
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS pamoja.contribution (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id     uuid NOT NULL REFERENCES pamoja.grp (id),
    member_id    uuid NOT NULL REFERENCES pamoja.group_member (id),
    meeting_id   uuid REFERENCES pamoja.meeting (id),
    kind         text NOT NULL DEFAULT 'savings'
                 CHECK (kind IN ('savings', 'shares', 'social_fund', 'loan_repayment', 'fine')),
    amount_ugx   bigint NOT NULL CHECK (amount_ugx > 0),
    method       text NOT NULL DEFAULT 'cash'
                 CHECK (method IN ('cash', 'mobile_money', 'bank')),
    reference    text NOT NULL DEFAULT '',
    status       text NOT NULL DEFAULT 'submitted'
                 CHECK (status IN ('draft', 'submitted', 'verified', 'returned')),
    note         text NOT NULL DEFAULT '',
    recorded_by  uuid REFERENCES pamoja.app_user (id),
    verified_by  uuid REFERENCES pamoja.app_user (id),
    occurred_on  date NOT NULL,
    verified_at  timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS contribution_group_idx ON pamoja.contribution (group_id, occurred_on DESC);
CREATE INDEX IF NOT EXISTS contribution_member_idx ON pamoja.contribution (member_id);
CREATE INDEX IF NOT EXISTS contribution_status_idx ON pamoja.contribution (status);

-- ---------------------------------------------------------------------------
-- 6. Group borrowing applications
--
--    submitted → appraised → approved | declined → disbursed → closed
--    The separation-of-duties rules (SD-03, SD-04) are checked in the service
--    layer before a transition is written.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS pamoja.loan_application (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id            uuid NOT NULL REFERENCES pamoja.grp (id),
    applicant_member_id uuid REFERENCES pamoja.group_member (id),
    amount_ugx          bigint NOT NULL CHECK (amount_ugx > 0),
    purpose             text NOT NULL,
    term_months         integer NOT NULL DEFAULT 6 CHECK (term_months BETWEEN 1 AND 36),
    status              text NOT NULL DEFAULT 'submitted'
                        CHECK (status IN ('draft', 'submitted', 'appraised', 'operations_reviewed',
                                          'approved', 'declined', 'offered', 'accepted',
                                          'disbursed', 'closed')),
    appraisal_note      text NOT NULL DEFAULT '',
    decision_note       text NOT NULL DEFAULT '',
    appraised_by        uuid REFERENCES pamoja.app_user (id),
    approved_by         uuid REFERENCES pamoja.app_user (id),
    submitted_on        date,
    decided_on          date,
    disbursed_on        date,
    created_by          uuid REFERENCES pamoja.app_user (id),
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS loan_group_idx ON pamoja.loan_application (group_id, created_at DESC);
CREATE INDEX IF NOT EXISTS loan_status_idx ON pamoja.loan_application (status);

-- ---------------------------------------------------------------------------
-- 7. Follow-up tasks
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS pamoja.task (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id         uuid REFERENCES pamoja.grp (id),
    title            text NOT NULL,
    detail           text NOT NULL DEFAULT '',
    assignee_user_id uuid REFERENCES pamoja.app_user (id),
    due_on           date,
    status           text NOT NULL DEFAULT 'open'
                     CHECK (status IN ('open', 'done', 'cancelled')),
    priority         text NOT NULL DEFAULT 'normal'
                     CHECK (priority IN ('low', 'normal', 'high')),
    created_by       uuid REFERENCES pamoja.app_user (id),
    created_at       timestamptz NOT NULL DEFAULT now(),
    closed_at        timestamptz
);

CREATE INDEX IF NOT EXISTS task_status_idx ON pamoja.task (status, due_on);

-- ---------------------------------------------------------------------------
-- 8. Row-level security and grants
-- ---------------------------------------------------------------------------
DO $$
DECLARE
    t text;
BEGIN
    FOREACH t IN ARRAY ARRAY[
        'legal_entity', 'branch', 'grp', 'group_member', 'meeting',
        'attendance', 'contribution', 'loan_application', 'task'
    ] LOOP
        EXECUTE format('ALTER TABLE pamoja.%I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE pamoja.%I FORCE ROW LEVEL SECURITY', t);

        EXECUTE format('DROP POLICY IF EXISTS %I ON pamoja.%I', t || '_read', t);
        EXECUTE format(
            'CREATE POLICY %I ON pamoja.%I FOR SELECT TO pamoja_app USING (pamoja.current_actor() IS NOT NULL)',
            t || '_read', t);

        EXECUTE format('DROP POLICY IF EXISTS %I ON pamoja.%I', t || '_insert', t);
        EXECUTE format(
            'CREATE POLICY %I ON pamoja.%I FOR INSERT TO pamoja_app WITH CHECK (pamoja.current_actor() IS NOT NULL)',
            t || '_insert', t);

        EXECUTE format('DROP POLICY IF EXISTS %I ON pamoja.%I', t || '_update', t);
        EXECUTE format(
            'CREATE POLICY %I ON pamoja.%I FOR UPDATE TO pamoja_app USING (pamoja.current_actor() IS NOT NULL) WITH CHECK (pamoja.current_actor() IS NOT NULL)',
            t || '_update', t);
    END LOOP;
END
$$;

-- Organisation data is reference data: the API reads it, only migrations and
-- the seed write it.
GRANT SELECT ON pamoja.legal_entity, pamoja.branch TO pamoja_app;

-- Domain records: read and write, never delete.
GRANT SELECT, INSERT, UPDATE ON
    pamoja.grp, pamoja.group_member, pamoja.meeting, pamoja.attendance,
    pamoja.contribution, pamoja.loan_application, pamoja.task
    TO pamoja_app;

-- ---------------------------------------------------------------------------
-- 9. Narrow definer functions for data the API role must not read directly
-- ---------------------------------------------------------------------------

-- The role bindings of one user, with the scope type each binding reaches.
CREATE OR REPLACE FUNCTION pamoja.auth_bindings(p_user_id uuid)
RETURNS TABLE (role text, scope_type text, scope_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pamoja, pg_temp AS
$$
    SELECT g.role,
           CASE
               WHEN g.group_id IS NOT NULL THEN 'group'
               WHEN g.branch_id IS NOT NULL THEN 'branch'
               WHEN g.legal_entity_id IS NOT NULL THEN 'legal_entity'
               ELSE 'platform'
           END,
           COALESCE(g.group_id, g.branch_id, g.legal_entity_id)
    FROM pamoja.role_grant g
    WHERE g.user_id = p_user_id
      AND g.status = 'active'
      AND g.effective_from <= current_date
      AND (g.effective_to IS NULL OR g.effective_to >= current_date)
$$;

-- A display name for an actor id, so a list can show who recorded an entry
-- without exposing the identity row itself.
CREATE OR REPLACE FUNCTION pamoja.domain_user_name(p_user_id uuid)
RETURNS text
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pamoja, pg_temp AS
$$
    SELECT COALESCE((SELECT u.full_name FROM pamoja.app_user u WHERE u.id = p_user_id), '')
$$;

-- Staff directory for the users-and-roles screen. The API role has no direct
-- read of other users' identity rows.
CREATE OR REPLACE FUNCTION pamoja.domain_list_users()
RETURNS TABLE (
    id uuid, full_name text, phone_masked text, status text,
    is_staff boolean, mfa_required boolean, created_at timestamptz, roles text
)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pamoja, pg_temp AS
$$
    SELECT u.id, u.full_name, u.phone_masked, u.status, u.is_staff,
           u.mfa_required, u.created_at,
           COALESCE((
               SELECT string_agg(DISTINCT r.role, ', ' ORDER BY r.role)
               FROM pamoja.role_grant r
               WHERE r.user_id = u.id AND r.status = 'active'
           ), '')
    FROM pamoja.app_user u
    ORDER BY u.full_name
$$;

-- One page of the append-only audit log, newest first.
CREATE OR REPLACE FUNCTION pamoja.domain_audit_list(p_limit integer, p_offset integer)
RETURNS TABLE (
    seq bigint, occurred_at timestamptz, actor_name text, actor_role text,
    action text, entity_type text, entity_id uuid, reason text, channel text,
    scope_type text
)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pamoja, pg_temp AS
$$
    SELECT a.seq, a.occurred_at, COALESCE(u.full_name, 'System'), a.actor_role,
           a.action, a.entity_type, a.entity_id, a.reason, a.channel, a.scope_type
    FROM pamoja.audit_log a
    LEFT JOIN pamoja.app_user u ON u.id = a.actor_user_id
    ORDER BY a.seq DESC
    LIMIT p_limit OFFSET p_offset
$$;

CREATE OR REPLACE FUNCTION pamoja.domain_audit_count()
RETURNS bigint
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pamoja, pg_temp AS
$$
    SELECT count(*) FROM pamoja.audit_log
$$;

ALTER FUNCTION pamoja.auth_bindings(uuid) OWNER TO pamoja_auth;
ALTER FUNCTION pamoja.domain_user_name(uuid) OWNER TO pamoja_auth;
ALTER FUNCTION pamoja.domain_list_users() OWNER TO pamoja_auth;
ALTER FUNCTION pamoja.domain_audit_list(integer, integer) OWNER TO pamoja_auth;
ALTER FUNCTION pamoja.domain_audit_count() OWNER TO pamoja_auth;

REVOKE ALL ON FUNCTION pamoja.auth_bindings(uuid) FROM PUBLIC;
REVOKE ALL ON FUNCTION pamoja.domain_user_name(uuid) FROM PUBLIC;
REVOKE ALL ON FUNCTION pamoja.domain_list_users() FROM PUBLIC;
REVOKE ALL ON FUNCTION pamoja.domain_audit_list(integer, integer) FROM PUBLIC;
REVOKE ALL ON FUNCTION pamoja.domain_audit_count() FROM PUBLIC;

GRANT EXECUTE ON FUNCTION
    pamoja.auth_bindings(uuid),
    pamoja.domain_user_name(uuid),
    pamoja.domain_list_users(),
    pamoja.domain_audit_list(integer, integer),
    pamoja.domain_audit_count()
    TO pamoja_app;

-- ---------------------------------------------------------------------------
-- 10. Migration ledger
-- ---------------------------------------------------------------------------
INSERT INTO public.schema_migrations (version, dirty)
VALUES (3, false)
ON CONFLICT (version) DO NOTHING;

COMMIT;
