-- 000004_p4_payments: payment entries, group submissions, receipts,
-- allocations, ledger postings, finance exceptions and corrections.
--
-- The three financial facts (ARCHITECTURE §6) are separate rows:
--   payment_entry  — what a member says they paid (treasurer records)
--   receipt        — what Yopsa confirmed arrived (verifier records)
--   ledger_posting — what the core ledger accepted (system, Finance confirms)
-- An allocation matches receipt money to an entry. It can never exceed the
-- receipt, and a shortage is never spread. PostgreSQL enforces the caps and
-- the immutability of locked/verified rows, not only the service.

BEGIN;

CREATE TABLE IF NOT EXISTS pamoja.payment_entry (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    legal_entity_id uuid NOT NULL REFERENCES pamoja.legal_entity (id),
    branch_id       uuid NOT NULL REFERENCES pamoja.branch (id),
    group_id        uuid NOT NULL REFERENCES pamoja.groups (id),
    member_id       uuid NOT NULL REFERENCES pamoja.member (id),
    meeting_id      uuid REFERENCES pamoja.meeting (id),
    submission_id   uuid,
    category        text NOT NULL
                    CHECK (category IN ('savings', 'social_fund', 'loan_repayment', 'fine', 'share_capital', 'other')),
    amount_ugx      bigint NOT NULL CHECK (amount_ugx > 0),
    paid_on         date NOT NULL,
    channel         text NOT NULL DEFAULT 'cash'
                    CHECK (channel IN ('cash', 'mobile_money', 'bank', 'other')),
    source_ref      text NOT NULL DEFAULT '',
    evidence_id     uuid,
    status          text NOT NULL DEFAULT 'draft'
                    CHECK (status IN ('draft', 'pending_sync', 'submitted', 'verified', 'posting_pending', 'posted', 'returned', 'disputed', 'finance_exception')),
    recorded_by     uuid NOT NULL REFERENCES pamoja.app_user (id),
    recorded_via    text NOT NULL DEFAULT 'self'
                    CHECK (recorded_via IN ('self', 'assisted', 'offline')),
    idempotency_key text NOT NULL UNIQUE,
    version         integer NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS payment_entry_group_idx ON pamoja.payment_entry (group_id, paid_on DESC);
CREATE INDEX IF NOT EXISTS payment_entry_submission_idx ON pamoja.payment_entry (submission_id);

CREATE TABLE IF NOT EXISTS pamoja.group_submission (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    legal_entity_id     uuid NOT NULL REFERENCES pamoja.legal_entity (id),
    branch_id           uuid NOT NULL REFERENCES pamoja.branch (id),
    group_id            uuid NOT NULL REFERENCES pamoja.groups (id),
    period_start        date NOT NULL,
    period_end          date NOT NULL,
    declared_total_ugx  bigint NOT NULL DEFAULT 0 CHECK (declared_total_ugx >= 0),
    entry_count         integer NOT NULL DEFAULT 0 CHECK (entry_count >= 0),
    locked_hash         text NOT NULL DEFAULT '',
    status              text NOT NULL DEFAULT 'draft'
                        CHECK (status IN ('draft', 'locked', 'under_review', 'verified', 'partially_verified', 'returned', 'posting_pending', 'posted', 'finance_exception')),
    locked_by           uuid REFERENCES pamoja.app_user (id),
    locked_at           timestamptz,
    submitted_by        uuid REFERENCES pamoja.app_user (id),
    submitted_at        timestamptz,
    verified_by         uuid REFERENCES pamoja.app_user (id),
    verified_at         timestamptz,
    received_total_ugx  bigint CHECK (received_total_ugx IS NULL OR received_total_ugx >= 0),
    decision_note       text NOT NULL DEFAULT '',
    version             integer NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_at          timestamptz NOT NULL DEFAULT now(),
    UNIQUE (group_id, period_start, period_end),
    CHECK (period_end >= period_start)
);

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'payment_entry_submission_fk'
          AND conrelid = 'pamoja.payment_entry'::regclass
    ) THEN
        ALTER TABLE pamoja.payment_entry
            ADD CONSTRAINT payment_entry_submission_fk
            FOREIGN KEY (submission_id) REFERENCES pamoja.group_submission (id);
    END IF;
END
$$;

CREATE TABLE IF NOT EXISTS pamoja.receipt (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    legal_entity_id    uuid NOT NULL REFERENCES pamoja.legal_entity (id),
    branch_id          uuid NOT NULL REFERENCES pamoja.branch (id),
    group_id           uuid NOT NULL REFERENCES pamoja.groups (id),
    submission_id      uuid REFERENCES pamoja.group_submission (id),
    amount_ugx         bigint NOT NULL CHECK (amount_ugx > 0),
    source             text NOT NULL,
    provider_ref       text NOT NULL,
    evidence_id        uuid,
    status             text NOT NULL DEFAULT 'recorded'
                       CHECK (status IN ('recorded', 'confirmed', 'voided')),
    recorded_by        uuid NOT NULL REFERENCES pamoja.app_user (id),
    confirmed_by       uuid REFERENCES pamoja.app_user (id),
    received_on        date NOT NULL,
    cash_handover_by   uuid REFERENCES pamoja.app_user (id),
    cash_deposit_by    uuid REFERENCES pamoja.app_user (id),
    created_at         timestamptz NOT NULL DEFAULT now(),
    UNIQUE (source, provider_ref)
);

CREATE INDEX IF NOT EXISTS receipt_group_idx ON pamoja.receipt (group_id, received_on DESC);

CREATE TABLE IF NOT EXISTS pamoja.allocation (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    legal_entity_id uuid NOT NULL REFERENCES pamoja.legal_entity (id),
    branch_id       uuid NOT NULL REFERENCES pamoja.branch (id),
    group_id        uuid NOT NULL REFERENCES pamoja.groups (id),
    receipt_id      uuid NOT NULL REFERENCES pamoja.receipt (id),
    entry_id        uuid NOT NULL REFERENCES pamoja.payment_entry (id),
    amount_ugx      bigint NOT NULL CHECK (amount_ugx > 0),
    status          text NOT NULL DEFAULT 'created'
                    CHECK (status IN ('created', 'posted', 'failed')),
    created_by      uuid NOT NULL REFERENCES pamoja.app_user (id),
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (receipt_id, entry_id)
);

CREATE TABLE IF NOT EXISTS pamoja.ledger_posting (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    legal_entity_id uuid NOT NULL REFERENCES pamoja.legal_entity (id),
    branch_id       uuid NOT NULL REFERENCES pamoja.branch (id),
    group_id        uuid NOT NULL REFERENCES pamoja.groups (id),
    allocation_id   uuid NOT NULL UNIQUE REFERENCES pamoja.allocation (id),
    external_ref    text NOT NULL UNIQUE,
    status          text NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending', 'confirmed', 'failed')),
    posted_at       timestamptz,
    attempts        integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    last_error      text NOT NULL DEFAULT '',
    confirmed_by    uuid REFERENCES pamoja.app_user (id),
    confirmed_at    timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS pamoja.finance_exception (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    legal_entity_id uuid NOT NULL REFERENCES pamoja.legal_entity (id),
    branch_id       uuid NOT NULL REFERENCES pamoja.branch (id),
    group_id        uuid NOT NULL REFERENCES pamoja.groups (id),
    kind            text NOT NULL,
    submission_id   uuid REFERENCES pamoja.group_submission (id),
    receipt_id      uuid REFERENCES pamoja.receipt (id),
    posting_id      uuid REFERENCES pamoja.ledger_posting (id),
    owner_user_id   uuid NOT NULL REFERENCES pamoja.app_user (id),
    due_on          date NOT NULL,
    status          text NOT NULL DEFAULT 'open'
                    CHECK (status IN ('open', 'in_progress', 'escalated', 'resolved')),
    resolution_note text NOT NULL DEFAULT '',
    resolved_by     uuid REFERENCES pamoja.app_user (id),
    resolved_at     timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS finance_exception_open_idx
    ON pamoja.finance_exception (group_id, due_on) WHERE status <> 'resolved';

CREATE TABLE IF NOT EXISTS pamoja.correction (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    legal_entity_id   uuid NOT NULL REFERENCES pamoja.legal_entity (id),
    branch_id         uuid NOT NULL REFERENCES pamoja.branch (id),
    group_id          uuid NOT NULL REFERENCES pamoja.groups (id),
    entity_type       text NOT NULL,
    entity_id         uuid NOT NULL,
    kind              text NOT NULL CHECK (kind IN ('reversal', 'adjustment', 'new_version')),
    reason            text NOT NULL,
    requested_by      uuid NOT NULL REFERENCES pamoja.app_user (id),
    approved_by       uuid REFERENCES pamoja.app_user (id),
    approved_at       timestamptz,
    original_snapshot jsonb,
    new_snapshot      jsonb,
    status            text NOT NULL DEFAULT 'requested'
                      CHECK (status IN ('requested', 'approved', 'rejected', 'applied')),
    created_at        timestamptz NOT NULL DEFAULT now()
);

-- The sandbox ledger is the working LedgerAdapter implementation (ADR-0008).
-- It is not a real bank: it records the contract the adapter honours so the
-- payment path can be tested end to end without vendor access.
CREATE TABLE IF NOT EXISTS pamoja.sandbox_ledger (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    external_ref text NOT NULL UNIQUE,
    group_id     uuid NOT NULL,
    kind         text NOT NULL CHECK (kind IN ('repayment', 'disbursement')),
    amount_ugx   bigint NOT NULL CHECK (amount_ugx > 0),
    status       text NOT NULL DEFAULT 'posted' CHECK (status IN ('posted', 'rejected')),
    posted_at    timestamptz NOT NULL DEFAULT now()
);

-- ---------------------------------------------------------------------------
-- Guards and invariants
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION pamoja.payment_entry_guard() RETURNS trigger
LANGUAGE plpgsql AS
$$
BEGIN
    IF TG_OP = 'DELETE' THEN
        IF OLD.status <> 'draft' THEN
            RAISE EXCEPTION 'only a draft payment entry may be deleted' USING ERRCODE = '23514';
        END IF;
        RETURN OLD;
    END IF;

    IF OLD.status <> 'draft' THEN
        IF NEW.amount_ugx <> OLD.amount_ugx
           OR NEW.member_id <> OLD.member_id
           OR NEW.group_id <> OLD.group_id
           OR NEW.category <> OLD.category
           OR NEW.paid_on <> OLD.paid_on
           OR NEW.source_ref <> OLD.source_ref THEN
            IF NOT (OLD.status = 'returned' AND NEW.status = 'draft') THEN
                RAISE EXCEPTION 'a locked or verified entry is immutable; use a correction'
                    USING ERRCODE = '23514';
            END IF;
        END IF;
    END IF;

    IF NEW.status <> OLD.status THEN
        IF NOT ((OLD.status = 'draft' AND NEW.status IN ('pending_sync', 'submitted', 'returned', 'disputed'))
             OR (OLD.status = 'pending_sync' AND NEW.status IN ('submitted', 'returned'))
             OR (OLD.status = 'submitted' AND NEW.status IN ('verified', 'returned', 'disputed'))
             OR (OLD.status = 'verified' AND NEW.status IN ('posting_pending', 'posted', 'finance_exception'))
             OR (OLD.status = 'posting_pending' AND NEW.status IN ('posted', 'finance_exception'))
             OR (OLD.status = 'finance_exception' AND NEW.status IN ('posting_pending', 'posted'))
             OR (OLD.status = 'returned' AND NEW.status = 'draft')
             OR (OLD.status = 'disputed' AND NEW.status IN ('submitted', 'returned'))) THEN
            RAISE EXCEPTION 'invalid payment entry transition % -> %', OLD.status, NEW.status
                USING ERRCODE = '23514';
        END IF;
    END IF;
    RETURN NEW;
END
$$;

DROP TRIGGER IF EXISTS payment_entry_guard ON pamoja.payment_entry;
CREATE TRIGGER payment_entry_guard
    BEFORE UPDATE OR DELETE ON pamoja.payment_entry
    FOR EACH ROW EXECUTE FUNCTION pamoja.payment_entry_guard();

CREATE OR REPLACE FUNCTION pamoja.submission_guard() RETURNS trigger
LANGUAGE plpgsql AS
$$
DECLARE
    v_total bigint;
    v_count integer;
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'submissions are never deleted' USING ERRCODE = '23514';
    END IF;

    IF NEW.status <> OLD.status THEN
        IF NOT ((OLD.status = 'draft' AND NEW.status IN ('locked', 'returned'))
             OR (OLD.status = 'locked' AND NEW.status IN ('under_review', 'returned'))
             OR (OLD.status = 'under_review' AND NEW.status IN ('verified', 'partially_verified', 'returned'))
             OR (OLD.status IN ('verified', 'partially_verified') AND NEW.status IN ('posting_pending', 'posted'))
             OR (OLD.status = 'posting_pending' AND NEW.status IN ('posted', 'finance_exception'))
             OR (OLD.status = 'finance_exception' AND NEW.status IN ('posting_pending', 'posted'))
             OR (OLD.status = 'returned' AND NEW.status = 'draft')) THEN
            RAISE EXCEPTION 'invalid submission transition % -> %', OLD.status, NEW.status
                USING ERRCODE = '23514';
        END IF;

        IF NEW.status = 'locked' THEN
            SELECT COALESCE(sum(amount_ugx), 0), count(*) INTO v_total, v_count
            FROM pamoja.payment_entry WHERE submission_id = NEW.id;
            IF v_total <> NEW.declared_total_ugx OR v_count <> NEW.entry_count THEN
                RAISE EXCEPTION 'declared total % / % entries does not match the locked entries % / %',
                    NEW.declared_total_ugx, NEW.entry_count, v_total, v_count
                    USING ERRCODE = '23514';
            END IF;
            IF NEW.locked_hash = '' THEN
                RAISE EXCEPTION 'a locked submission needs its entry hash' USING ERRCODE = '23514';
            END IF;
        END IF;
    END IF;
    RETURN NEW;
END
$$;

DROP TRIGGER IF EXISTS submission_guard ON pamoja.group_submission;
CREATE TRIGGER submission_guard
    BEFORE UPDATE OR DELETE ON pamoja.group_submission
    FOR EACH ROW EXECUTE FUNCTION pamoja.submission_guard();

CREATE OR REPLACE FUNCTION pamoja.receipt_guard() RETURNS trigger
LANGUAGE plpgsql AS
$$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'receipts are never deleted; void them with a linked correction'
            USING ERRCODE = '23514';
    END IF;
    IF NEW.amount_ugx <> OLD.amount_ugx
       OR NEW.source <> OLD.source
       OR NEW.provider_ref <> OLD.provider_ref
       OR NEW.group_id <> OLD.group_id THEN
        RAISE EXCEPTION 'a receipt is never edited; void it and record a new one'
            USING ERRCODE = '23514';
    END IF;
    IF OLD.status = 'confirmed' AND NEW.status = 'recorded' THEN
        RAISE EXCEPTION 'a confirmed receipt cannot return to recorded' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END
$$;

DROP TRIGGER IF EXISTS receipt_guard ON pamoja.receipt;
CREATE TRIGGER receipt_guard
    BEFORE UPDATE OR DELETE ON pamoja.receipt
    FOR EACH ROW EXECUTE FUNCTION pamoja.receipt_guard();

CREATE OR REPLACE FUNCTION pamoja.allocation_guard() RETURNS trigger
LANGUAGE plpgsql AS
$$
DECLARE
    r record;
    e record;
    v_allocated bigint;
    v_entry_allocated bigint;
BEGIN
    IF TG_OP = 'UPDATE' AND NEW.amount_ugx = OLD.amount_ugx
       AND NEW.receipt_id = OLD.receipt_id AND NEW.entry_id = OLD.entry_id THEN
        RETURN NEW;
    END IF;

    SELECT amount_ugx, group_id, submission_id, status INTO r
    FROM pamoja.receipt WHERE id = NEW.receipt_id;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'receipt does not exist' USING ERRCODE = '23503';
    END IF;
    IF r.status <> 'confirmed' THEN
        RAISE EXCEPTION 'only a confirmed receipt may be allocated' USING ERRCODE = '23514';
    END IF;
    IF r.group_id <> NEW.group_id THEN
        RAISE EXCEPTION 'allocation and receipt belong to different groups' USING ERRCODE = '23514';
    END IF;

    SELECT amount_ugx, group_id, submission_id, status INTO e
    FROM pamoja.payment_entry WHERE id = NEW.entry_id;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'payment entry does not exist' USING ERRCODE = '23503';
    END IF;
    IF e.group_id <> NEW.group_id OR e.submission_id IS DISTINCT FROM r.submission_id THEN
        RAISE EXCEPTION 'allocation entry does not belong to the receipt submission'
            USING ERRCODE = '23514';
    END IF;
    IF e.status NOT IN ('submitted', 'verified', 'posting_pending') THEN
        RAISE EXCEPTION 'entry status % cannot be allocated', e.status USING ERRCODE = '23514';
    END IF;

    SELECT COALESCE(sum(amount_ugx), 0) INTO v_allocated
    FROM pamoja.allocation WHERE receipt_id = NEW.receipt_id AND id <> NEW.id;
    IF v_allocated + NEW.amount_ugx > r.amount_ugx THEN
        RAISE EXCEPTION 'allocation exceeds the confirmed receipt (% + % > %)',
            v_allocated, NEW.amount_ugx, r.amount_ugx USING ERRCODE = '23514';
    END IF;

    SELECT COALESCE(sum(amount_ugx), 0) INTO v_entry_allocated
    FROM pamoja.allocation WHERE entry_id = NEW.entry_id AND id <> NEW.id;
    IF v_entry_allocated + NEW.amount_ugx > e.amount_ugx THEN
        RAISE EXCEPTION 'allocation exceeds the entry amount' USING ERRCODE = '23514';
    END IF;

    RETURN NEW;
END
$$;

DROP TRIGGER IF EXISTS allocation_guard ON pamoja.allocation;
CREATE TRIGGER allocation_guard
    BEFORE INSERT OR UPDATE ON pamoja.allocation
    FOR EACH ROW EXECUTE FUNCTION pamoja.allocation_guard();

CREATE OR REPLACE FUNCTION pamoja.posting_guard() RETURNS trigger
LANGUAGE plpgsql AS
$$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'ledger postings are never deleted' USING ERRCODE = '23514';
    END IF;
    IF TG_OP = 'UPDATE' THEN
        IF NEW.allocation_id <> OLD.allocation_id OR NEW.external_ref <> OLD.external_ref THEN
            RAISE EXCEPTION 'a posting identity cannot change' USING ERRCODE = '23514';
        END IF;
        IF NEW.status <> OLD.status AND NOT (
               (OLD.status = 'pending' AND NEW.status IN ('confirmed', 'failed'))
            OR (OLD.status = 'failed' AND NEW.status IN ('pending', 'confirmed'))) THEN
            RAISE EXCEPTION 'invalid posting transition % -> %', OLD.status, NEW.status
                USING ERRCODE = '23514';
        END IF;
        IF NEW.status = 'confirmed' AND NEW.confirmed_by IS NULL THEN
            RAISE EXCEPTION 'a confirmed posting needs its confirmer' USING ERRCODE = '23514';
        END IF;
    END IF;
    RETURN NEW;
END
$$;

DROP TRIGGER IF EXISTS posting_guard ON pamoja.ledger_posting;
CREATE TRIGGER posting_guard
    BEFORE UPDATE OR DELETE ON pamoja.ledger_posting
    FOR EACH ROW EXECUTE FUNCTION pamoja.posting_guard();

CREATE OR REPLACE FUNCTION pamoja.finance_exception_guard() RETURNS trigger
LANGUAGE plpgsql AS
$$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'finance exceptions are never deleted' USING ERRCODE = '23514';
    END IF;
    IF NEW.status = 'resolved' THEN
        IF NEW.resolution_note = '' OR NEW.resolved_by IS NULL OR NEW.resolved_at IS NULL THEN
            RAISE EXCEPTION 'resolving an exception needs a note, resolver and time'
                USING ERRCODE = '23514';
        END IF;
    END IF;
    RETURN NEW;
END
$$;

DROP TRIGGER IF EXISTS finance_exception_guard ON pamoja.finance_exception;
CREATE TRIGGER finance_exception_guard
    BEFORE UPDATE OR DELETE ON pamoja.finance_exception
    FOR EACH ROW EXECUTE FUNCTION pamoja.finance_exception_guard();

CREATE OR REPLACE FUNCTION pamoja.correction_guard() RETURNS trigger
LANGUAGE plpgsql AS
$$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'corrections are never deleted' USING ERRCODE = '23514';
    END IF;
    IF NEW.status IN ('approved', 'applied') THEN
        IF NEW.approved_by IS NULL OR NEW.approved_by = NEW.requested_by THEN
            RAISE EXCEPTION 'a correction needs an independent approver (SD-07)'
                USING ERRCODE = '23514';
        END IF;
    END IF;
    IF NEW.status = 'applied' AND OLD.status <> 'approved' THEN
        RAISE EXCEPTION 'a correction is applied only after approval' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END
$$;

DROP TRIGGER IF EXISTS correction_guard ON pamoja.correction;
CREATE TRIGGER correction_guard
    BEFORE UPDATE OR DELETE ON pamoja.correction
    FOR EACH ROW EXECUTE FUNCTION pamoja.correction_guard();

-- ---------------------------------------------------------------------------
-- Row-level security and grants
-- ---------------------------------------------------------------------------
ALTER TABLE pamoja.payment_entry ENABLE ROW LEVEL SECURITY;
ALTER TABLE pamoja.payment_entry FORCE ROW LEVEL SECURITY;
ALTER TABLE pamoja.group_submission ENABLE ROW LEVEL SECURITY;
ALTER TABLE pamoja.group_submission FORCE ROW LEVEL SECURITY;
ALTER TABLE pamoja.receipt ENABLE ROW LEVEL SECURITY;
ALTER TABLE pamoja.receipt FORCE ROW LEVEL SECURITY;
ALTER TABLE pamoja.allocation ENABLE ROW LEVEL SECURITY;
ALTER TABLE pamoja.allocation FORCE ROW LEVEL SECURITY;
ALTER TABLE pamoja.ledger_posting ENABLE ROW LEVEL SECURITY;
ALTER TABLE pamoja.ledger_posting FORCE ROW LEVEL SECURITY;
ALTER TABLE pamoja.finance_exception ENABLE ROW LEVEL SECURITY;
ALTER TABLE pamoja.finance_exception FORCE ROW LEVEL SECURITY;
ALTER TABLE pamoja.correction ENABLE ROW LEVEL SECURITY;
ALTER TABLE pamoja.correction FORCE ROW LEVEL SECURITY;
ALTER TABLE pamoja.sandbox_ledger ENABLE ROW LEVEL SECURITY;
ALTER TABLE pamoja.sandbox_ledger FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS payment_entry_scope ON pamoja.payment_entry;
CREATE POLICY payment_entry_scope ON pamoja.payment_entry
    FOR ALL TO pamoja_app
    USING (pamoja.scope_allows(legal_entity_id, branch_id, group_id))
    WITH CHECK (pamoja.scope_allows(legal_entity_id, branch_id, group_id));

DROP POLICY IF EXISTS submission_scope ON pamoja.group_submission;
CREATE POLICY submission_scope ON pamoja.group_submission
    FOR ALL TO pamoja_app
    USING (pamoja.scope_allows(legal_entity_id, branch_id, group_id))
    WITH CHECK (pamoja.scope_allows(legal_entity_id, branch_id, group_id));

DROP POLICY IF EXISTS receipt_scope ON pamoja.receipt;
CREATE POLICY receipt_scope ON pamoja.receipt
    FOR ALL TO pamoja_app
    USING (pamoja.scope_allows(legal_entity_id, branch_id, group_id))
    WITH CHECK (pamoja.scope_allows(legal_entity_id, branch_id, group_id));

DROP POLICY IF EXISTS allocation_scope ON pamoja.allocation;
CREATE POLICY allocation_scope ON pamoja.allocation
    FOR ALL TO pamoja_app
    USING (pamoja.scope_allows(legal_entity_id, branch_id, group_id))
    WITH CHECK (pamoja.scope_allows(legal_entity_id, branch_id, group_id));

DROP POLICY IF EXISTS posting_scope ON pamoja.ledger_posting;
CREATE POLICY posting_scope ON pamoja.ledger_posting
    FOR ALL TO pamoja_app
    USING (pamoja.scope_allows(legal_entity_id, branch_id, group_id))
    WITH CHECK (pamoja.scope_allows(legal_entity_id, branch_id, group_id));

DROP POLICY IF EXISTS exception_scope ON pamoja.finance_exception;
CREATE POLICY exception_scope ON pamoja.finance_exception
    FOR ALL TO pamoja_app
    USING (pamoja.scope_allows(legal_entity_id, branch_id, group_id))
    WITH CHECK (pamoja.scope_allows(legal_entity_id, branch_id, group_id));

DROP POLICY IF EXISTS correction_scope ON pamoja.correction;
CREATE POLICY correction_scope ON pamoja.correction
    FOR ALL TO pamoja_app
    USING (pamoja.scope_allows(legal_entity_id, branch_id, group_id))
    WITH CHECK (pamoja.scope_allows(legal_entity_id, branch_id, group_id));

DROP POLICY IF EXISTS sandbox_ledger_scope ON pamoja.sandbox_ledger;
CREATE POLICY sandbox_ledger_scope ON pamoja.sandbox_ledger
    FOR ALL TO pamoja_app
    USING (group_id::text = ANY (pamoja.current_scopes())
           OR pamoja.is_group_member(group_id))
    WITH CHECK (group_id::text = ANY (pamoja.current_scopes()));

GRANT SELECT, INSERT, UPDATE, DELETE ON
    pamoja.payment_entry, pamoja.group_submission, pamoja.receipt,
    pamoja.allocation, pamoja.ledger_posting, pamoja.finance_exception,
    pamoja.correction, pamoja.sandbox_ledger
    TO pamoja_app;

GRANT SELECT ON
    pamoja.payment_entry, pamoja.group_submission, pamoja.receipt,
    pamoja.allocation, pamoja.ledger_posting, pamoja.finance_exception,
    pamoja.correction, pamoja.sandbox_ledger
    TO pamoja_readonly;

INSERT INTO public.schema_migrations (version, dirty)
VALUES (4, false)
ON CONFLICT (version) DO NOTHING;

COMMIT;
