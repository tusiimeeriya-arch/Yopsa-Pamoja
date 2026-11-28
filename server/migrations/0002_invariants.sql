-- 0002_invariants.sql — database-enforced invariants, row-level security,
-- the least-privilege application role and its privileges.

-- ===========================================================================
-- Database-enforced invariants. The application is the only client, but these
-- hold even for raw SQL executed by the application role.
-- ===========================================================================

-- Allocation total can never exceed the receipt. The receipt row is locked so
-- two concurrent allocations cannot both pass.
CREATE OR REPLACE FUNCTION enforce_allocation_cap() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  receipt_amount bigint;
  already bigint;
BEGIN
  SELECT amount INTO receipt_amount FROM receipts WHERE id = NEW.receipt_id FOR UPDATE;
  IF receipt_amount IS NULL THEN
    RAISE EXCEPTION 'receipt % does not exist', NEW.receipt_id USING ERRCODE = '23503';
  END IF;
  SELECT COALESCE(SUM(amount), 0) INTO already FROM allocations
   WHERE receipt_id = NEW.receipt_id AND id <> COALESCE(NEW.id, '00000000-0000-0000-0000-000000000000'::uuid);
  IF already + NEW.amount > receipt_amount THEN
    RAISE EXCEPTION 'allocation of % would make the total % exceed the receipt of %', NEW.amount, already + NEW.amount, receipt_amount USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS trg_allocation_cap ON allocations;
CREATE TRIGGER trg_allocation_cap BEFORE INSERT OR UPDATE ON allocations FOR EACH ROW EXECUTE FUNCTION enforce_allocation_cap();

-- Locked, verified, posted and disputed payment rows are immutable. Only the
-- status transitions the workflow needs are allowed, and financial fields may
-- change only when an approved Correction is being applied in the same
-- transaction (app.correction_id). Demo rows may be removed by the audited
-- purge path only (app.demo_purge).
CREATE OR REPLACE FUNCTION payment_entry_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  via_correction boolean := false;
BEGIN
  IF TG_OP = 'DELETE' THEN
    IF OLD.is_demo AND COALESCE(current_setting('app.demo_purge', true), '') = 'on' THEN
      RETURN OLD;
    END IF;
    IF OLD.status IN ('locked', 'verified', 'posted', 'disputed') THEN
      RAISE EXCEPTION 'payment entry % is % and cannot be deleted; record a correction instead', OLD.id, OLD.status USING ERRCODE = '23514';
    END IF;
    RETURN OLD;
  END IF;

  IF OLD.status NOT IN ('locked', 'verified', 'posted', 'disputed') THEN
    RETURN NEW;
  END IF;

  IF COALESCE(current_setting('app.correction_id', true), '') <> '' THEN
    SELECT EXISTS (
      SELECT 1 FROM corrections c
       WHERE c.id = current_setting('app.correction_id', true)::uuid
         AND c.entity = 'PaymentEntry' AND c.entity_id = OLD.id AND c.status = 'approved'
    ) INTO via_correction;
  END IF;
  IF via_correction THEN
    RETURN NEW;
  END IF;

  IF NEW.group_id IS DISTINCT FROM OLD.group_id
     OR NEW.member_person_id IS DISTINCT FROM OLD.member_person_id
     OR NEW.amount IS DISTINCT FROM OLD.amount
     OR NEW.category IS DISTINCT FROM OLD.category
     OR NEW.collection_date IS DISTINCT FROM OLD.collection_date
     OR NEW.created_by IS DISTINCT FROM OLD.created_by THEN
    RAISE EXCEPTION 'payment entry % is %: amount, member, category and date cannot be edited; request a correction', OLD.id, OLD.status USING ERRCODE = '23514';
  END IF;

  IF NEW.status = OLD.status THEN
    RETURN NEW;
  END IF;
  IF (OLD.status = 'locked' AND NEW.status IN ('verified', 'posted', 'returned', 'disputed'))
     OR (OLD.status = 'verified' AND NEW.status IN ('posted', 'disputed'))
     OR (OLD.status = 'disputed' AND NEW.status IN ('verified', 'posted', 'returned')) THEN
    RETURN NEW;
  END IF;
  RAISE EXCEPTION 'payment entry % cannot move from % to %', OLD.id, OLD.status, NEW.status USING ERRCODE = '23514';
END $$;
DROP TRIGGER IF EXISTS trg_payment_entry_guard ON payment_entries;
CREATE TRIGGER trg_payment_entry_guard BEFORE UPDATE OR DELETE ON payment_entries FOR EACH ROW EXECUTE FUNCTION payment_entry_guard();

-- Cash separation of duties: the person receiving must differ from the person
-- handing over, and the deposit must be confirmed by a third person, in order.
CREATE OR REPLACE FUNCTION cash_reconciliation_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.received_by IS NOT NULL THEN
    IF NEW.handover_by IS NULL THEN
      RAISE EXCEPTION 'cash cannot be received before it is handed over' USING ERRCODE = '23514';
    END IF;
    IF NEW.received_by = NEW.handover_by THEN
      RAISE EXCEPTION 'the person receiving cash must differ from the person handing it over' USING ERRCODE = '23514';
    END IF;
  END IF;
  IF NEW.deposited_by IS NOT NULL THEN
    IF NEW.received_by IS NULL THEN
      RAISE EXCEPTION 'cash cannot be deposited before it is received by the branch' USING ERRCODE = '23514';
    END IF;
    IF NEW.deposited_by = NEW.handover_by OR NEW.deposited_by = NEW.received_by THEN
      RAISE EXCEPTION 'the deposit must be confirmed by a third person' USING ERRCODE = '23514';
    END IF;
    IF NEW.bank_ref IS NULL OR length(btrim(NEW.bank_ref)) = 0 THEN
      RAISE EXCEPTION 'a bank reference is required to confirm the deposit' USING ERRCODE = '23514';
    END IF;
  END IF;
  RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS trg_cash_separation ON cash_reconciliations;
CREATE TRIGGER trg_cash_separation BEFORE INSERT OR UPDATE ON cash_reconciliations FOR EACH ROW EXECUTE FUNCTION cash_reconciliation_guard();

-- AuditLog is append-only: no update, delete or truncate, by anyone.
CREATE OR REPLACE FUNCTION audit_logs_append_only() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'audit_logs is append-only: % is not permitted', TG_OP USING ERRCODE = '42501';
END $$;
DROP TRIGGER IF EXISTS trg_audit_no_update ON audit_logs;
CREATE TRIGGER trg_audit_no_update BEFORE UPDATE OR DELETE ON audit_logs FOR EACH ROW EXECUTE FUNCTION audit_logs_append_only();
DROP TRIGGER IF EXISTS trg_audit_no_truncate ON audit_logs;
CREATE TRIGGER trg_audit_no_truncate BEFORE TRUNCATE ON audit_logs FOR EACH STATEMENT EXECUTE FUNCTION audit_logs_append_only();

-- Real records are never hard-deleted. A user is disabled, a membership or a
-- role assignment is end-dated, a group is closed and a person is archived, so
-- the audit trail keeps pointing at the same row. Test and demo rows
-- (is_demo = true) stay deletable: the demo purge and the self-tests clean up
-- after themselves, and nothing else does.
CREATE OR REPLACE FUNCTION record_no_hard_delete() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.is_demo THEN RETURN OLD; END IF;
  RAISE EXCEPTION '% % cannot be deleted; disable or end-date it instead', TG_TABLE_NAME, OLD.id
    USING ERRCODE = '42501';
END $$;

DROP TRIGGER IF EXISTS trg_no_delete_users ON users;
CREATE TRIGGER trg_no_delete_users BEFORE DELETE ON users FOR EACH ROW EXECUTE FUNCTION record_no_hard_delete();
DROP TRIGGER IF EXISTS trg_no_delete_persons ON persons;
CREATE TRIGGER trg_no_delete_persons BEFORE DELETE ON persons FOR EACH ROW EXECUTE FUNCTION record_no_hard_delete();
DROP TRIGGER IF EXISTS trg_no_delete_groups ON groups;
CREATE TRIGGER trg_no_delete_groups BEFORE DELETE ON groups FOR EACH ROW EXECUTE FUNCTION record_no_hard_delete();
DROP TRIGGER IF EXISTS trg_no_delete_memberships ON memberships;
CREATE TRIGGER trg_no_delete_memberships BEFORE DELETE ON memberships FOR EACH ROW EXECUTE FUNCTION record_no_hard_delete();
DROP TRIGGER IF EXISTS trg_no_delete_role_assignments ON role_assignments;
CREATE TRIGGER trg_no_delete_role_assignments BEFORE DELETE ON role_assignments FOR EACH ROW EXECUTE FUNCTION record_no_hard_delete();

-- ===========================================================================
-- Row-level security. The API connects as pamoja_app (not the owner, not a
-- superuser), so a query that forgets its scope returns nothing at all.
-- ===========================================================================

CREATE OR REPLACE FUNCTION app_user_id() RETURNS uuid LANGUAGE sql STABLE AS $$
  SELECT NULLIF(current_setting('app.user_id', true), '')::uuid
$$;

-- Set only by the in-process scheduler (no HTTP path sets it).
CREATE OR REPLACE FUNCTION app_system() RETURNS boolean LANGUAGE sql STABLE AS $$
  SELECT COALESCE(current_setting('app.system', true), '') = 'on'
$$;

-- is_demo marks a row as demo/test data. Once inserted it cannot be flipped on
-- a real row: only the seed (app.seed) or the scheduler/system path
-- (app.system) may change it.
CREATE OR REPLACE FUNCTION is_demo_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.is_demo IS DISTINCT FROM OLD.is_demo
     AND COALESCE(current_setting('app.seed', true), '') <> 'on'
     AND NOT app_system() THEN
    RAISE EXCEPTION 'is_demo is immutable after insert; only the seed or system path may set it'
      USING ERRCODE = '42501';
  END IF;
  RETURN NEW;
END $$;

DROP TRIGGER IF EXISTS trg_is_demo_immutable_users ON users;
CREATE TRIGGER trg_is_demo_immutable_users BEFORE UPDATE ON users FOR EACH ROW EXECUTE FUNCTION is_demo_immutable();
DROP TRIGGER IF EXISTS trg_is_demo_immutable_persons ON persons;
CREATE TRIGGER trg_is_demo_immutable_persons BEFORE UPDATE ON persons FOR EACH ROW EXECUTE FUNCTION is_demo_immutable();
DROP TRIGGER IF EXISTS trg_is_demo_immutable_groups ON groups;
CREATE TRIGGER trg_is_demo_immutable_groups BEFORE UPDATE ON groups FOR EACH ROW EXECUTE FUNCTION is_demo_immutable();
DROP TRIGGER IF EXISTS trg_is_demo_immutable_memberships ON memberships;
CREATE TRIGGER trg_is_demo_immutable_memberships BEFORE UPDATE ON memberships FOR EACH ROW EXECUTE FUNCTION is_demo_immutable();
DROP TRIGGER IF EXISTS trg_is_demo_immutable_role_assignments ON role_assignments;
CREATE TRIGGER trg_is_demo_immutable_role_assignments BEFORE UPDATE ON role_assignments FOR EACH ROW EXECUTE FUNCTION is_demo_immutable();

CREATE OR REPLACE FUNCTION app_allowed_groups() RETURNS uuid[] LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT COALESCE(array_agg(DISTINCT gid), ARRAY[]::uuid[]) FROM (
    SELECT g.id AS gid FROM groups g
      JOIN role_assignments ra ON ra.status = 'active'
       AND ra.effective_from <= current_date AND (ra.effective_to IS NULL OR ra.effective_to >= current_date)
     WHERE ra.user_id = app_user_id()
       AND (ra.scope_type = 'Org'
            OR (ra.scope_type = 'Branch' AND ra.scope_id = g.branch_id::text)
            OR (ra.scope_type = 'Group' AND ra.scope_id = g.id::text))
    UNION
    SELECT m.group_id FROM memberships m JOIN persons p ON p.id = m.person_id
     WHERE p.linked_user_id = app_user_id() AND m.status = 'active'
    UNION
    SELECT l.group_id FROM leaderships l JOIN persons p ON p.id = l.person_id
     WHERE p.linked_user_id = app_user_id()
       AND l.effective_from <= current_date AND (l.effective_to IS NULL OR l.effective_to >= current_date)
  ) s
$$;

-- Branch coverage for a whole-branch role. A Group-scoped role is deliberately
-- NOT covered here; it sees only its own groups through app_allowed_groups().
CREATE OR REPLACE FUNCTION app_can_access_branch(b uuid) RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT EXISTS (
    SELECT 1 FROM role_assignments ra
     WHERE ra.user_id = app_user_id() AND ra.status = 'active'
       AND ra.effective_from <= current_date AND (ra.effective_to IS NULL OR ra.effective_to >= current_date)
       AND (ra.scope_type = 'Org'
            OR (ra.scope_type = 'Branch' AND ra.scope_id = b::text))
  )
$$;

DO $$
DECLARE spec record;
BEGIN
  FOR spec IN
    SELECT * FROM (VALUES
      ('memberships', 'group_id'),
      ('leaderships', 'group_id'),
      ('meetings', 'group_id'),
      ('action_items', 'group_id'),
      ('payment_entries', 'group_id'),
      ('group_submissions', 'group_id'),
      ('receipts', 'group_id'),
      ('loan_applications', 'group_id'),
      ('mock_ledger_accounts', 'group_id'),
      ('disputes', 'group_id'),
      ('comments', 'group_id'),
      ('file_objects', 'group_id'),
      ('learning_assignments', 'group_id'),
      ('cash_reconciliations', 'group_id'),
      ('member_loans', 'group_id')
    ) AS t(tbl, col)
  LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', spec.tbl);
    EXECUTE format('DROP POLICY IF EXISTS tenant_scope ON %I', spec.tbl);
    EXECUTE format(
      'CREATE POLICY tenant_scope ON %I USING (app_system() OR %I = ANY(app_allowed_groups())) WITH CHECK (app_system() OR %I = ANY(app_allowed_groups()))',
      spec.tbl, spec.col, spec.col);
  END LOOP;
END $$;

-- Groups: readable inside the caller's scope, creatable only in a branch the
-- caller covers.
ALTER TABLE groups ENABLE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_scope ON groups;
CREATE POLICY tenant_scope ON groups
  USING (app_system() OR id = ANY(app_allowed_groups()) OR app_can_access_branch(branch_id))
  WITH CHECK (app_system() OR app_can_access_branch(branch_id));

-- Child tables scoped through their parent.
DO $$
DECLARE spec record;
BEGIN
  FOR spec IN
    SELECT * FROM (VALUES
      ('attendance_records', 'meeting_id', 'meetings'),
      ('minutes', 'meeting_id', 'meetings'),
      ('resolutions', 'meeting_id', 'meetings'),
      ('meeting_documents', 'meeting_id', 'meetings'),
      ('meeting_amendments', 'meeting_id', 'meetings'),
      ('allocations', 'receipt_id', 'receipts'),
      ('ledger_postings', 'submission_id', 'group_submissions'),
      ('approval_steps', 'loan_id', 'loan_applications'),
      ('guarantors', 'loan_id', 'loan_applications'),
      ('disbursements', 'loan_id', 'loan_applications')
    ) AS t(tbl, fk, parent)
  LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', spec.tbl);
    EXECUTE format('DROP POLICY IF EXISTS tenant_scope ON %I', spec.tbl);
    EXECUTE format(
      'CREATE POLICY tenant_scope ON %I USING (app_system() OR %I IN (SELECT id FROM %I)) WITH CHECK (app_system() OR %I IN (SELECT id FROM %I))',
      spec.tbl, spec.fk, spec.parent, spec.fk, spec.parent);
  END LOOP;
END $$;

-- Persons: visible when linked to the caller or a member/leader of a group in
-- scope. The cross-group duplicate lookup below is the one deliberate
-- privileged read, so adding a member still detects an existing obligation.
ALTER TABLE persons ENABLE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_scope ON persons;
CREATE POLICY tenant_scope ON persons
  USING (
    app_system()
    OR linked_user_id = app_user_id()
    OR created_by = app_user_id()
    OR id IN (SELECT person_id FROM memberships WHERE group_id = ANY(app_allowed_groups()))
    OR id IN (SELECT person_id FROM leaderships WHERE group_id = ANY(app_allowed_groups()))
  )
  WITH CHECK (app_system() OR app_user_id() IS NOT NULL);

CREATE OR REPLACE FUNCTION app_person_lookup(p_phone text, p_national_id text)
RETURNS TABLE(person_id uuid, full_name text, group_id uuid, group_name text, group_status text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT p.id, p.full_name, g.id, g.name, g.status
    FROM persons p
    LEFT JOIN memberships m ON m.person_id = p.id AND m.status = 'active'
    LEFT JOIN groups g ON g.id = m.group_id
   WHERE p.phone = p_phone
      OR (p_national_id IS NOT NULL AND p_national_id <> '' AND p.national_id_ref = p_national_id)
$$;

-- Narrow privileged write used when an officer registers a person who already
-- exists in another group (the duplicate path). Consent is recorded on the
-- shared person record; nothing else about that person is exposed.
CREATE OR REPLACE FUNCTION app_record_consent(p_person_id uuid) RETURNS void
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  UPDATE persons SET consent_recorded = true, consent_at = now() WHERE id = p_person_id
$$;

-- ===========================================================================
-- Least-privilege application role. The API is the only client; there is no
-- browser-to-database path at all. The role is NOT the owner and NOT a
-- superuser, so every policy above applies to it.
-- ===========================================================================
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'pamoja_app') THEN
    CREATE ROLE pamoja_app LOGIN PASSWORD 'pamoja_app_dev_password';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'pamoja_web') THEN
    CREATE ROLE pamoja_web LOGIN PASSWORD 'pamoja_web_dev_password';
  END IF;
END $$;
DO $$ BEGIN EXECUTE format('GRANT CONNECT ON DATABASE %I TO pamoja_app', current_database()); END $$;
GRANT USAGE ON SCHEMA public TO pamoja_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO pamoja_app;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO pamoja_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO pamoja_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT USAGE, SELECT ON SEQUENCES TO pamoja_app;

-- The audit log is readable and append-only. No role may change or remove it.
REVOKE UPDATE, DELETE, TRUNCATE ON audit_logs FROM pamoja_app;
REVOKE UPDATE, DELETE, TRUNCATE ON audit_logs FROM pamoja_web;
GRANT SELECT, INSERT ON audit_logs TO pamoja_app;

-- The five protected identity tables are never hard-deleted. The API role has
-- no DELETE or TRUNCATE privilege on them at all; the owner-only demo purge is
-- the one path that removes demo rows.
REVOKE DELETE, TRUNCATE ON users, persons, groups, memberships, role_assignments FROM pamoja_app;
REVOKE DELETE, TRUNCATE ON users, persons, groups, memberships, role_assignments FROM pamoja_web;
