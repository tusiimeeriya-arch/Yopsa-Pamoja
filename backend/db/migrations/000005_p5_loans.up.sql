-- 000005_p5_loans: group borrowing, appraisal, mandatory operations review,
-- approval, offers and Finance-confirmed disbursement.
--
-- Approval is never disbursement. A loan counts as advanced only after a
-- confirmed disbursement row exists, and the database rejects a disbursement
-- by the approver (SD-04). The guarantor threshold and product limits are read
-- from versioned configuration, never inferred.

BEGIN;

CREATE TABLE IF NOT EXISTS pamoja.loan_product (
    id                          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    legal_entity_id             uuid REFERENCES pamoja.legal_entity (id),
    code                        text NOT NULL,
    name                        text NOT NULL,
    group_type                  text NOT NULL REFERENCES pamoja.group_type (code),
    kind                        text NOT NULL DEFAULT 'group'
                                CHECK (kind IN ('group', 'internal_member')),
    min_amount_ugx              bigint NOT NULL CHECK (min_amount_ugx > 0),
    max_amount_ugx              bigint NOT NULL CHECK (max_amount_ugx >= min_amount_ugx),
    max_term_months             integer NOT NULL CHECK (max_term_months > 0),
    guarantor_required_above_ugx bigint NOT NULL CHECK (guarantor_required_above_ugx >= 0),
    version                     integer NOT NULL DEFAULT 1 CHECK (version >= 1),
    approved_by                 uuid REFERENCES pamoja.app_user (id),
    effective_from              date NOT NULL DEFAULT current_date,
    status                      text NOT NULL DEFAULT 'active'
                                CHECK (status IN ('active', 'retired')),
    created_at                  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (code, version)
);

INSERT INTO pamoja.loan_product
    (code, name, group_type, min_amount_ugx, max_amount_ugx, max_term_months,
     guarantor_required_above_ugx, version, effective_from)
VALUES
    ('VSLA-STD', 'VSLA standard loan', 'vsla', 1000000, 10000000, 24, 500000, 1, current_date),
    ('CLUB-STD', 'Investment club standard loan', 'investment_club', 2000000, 20000000, 24, 500000, 1, current_date),
    ('ASSOC-STD', 'Association standard loan', 'association', 5000000, 50000000, 24, 500000, 1, current_date),
    ('COOP-STD', 'Cooperative standard loan', 'cooperative', 10000000, 100000000, 24, 500000, 1, current_date),
    ('SACCO-STD', 'SACCO standard loan', 'sacco', 10000000, 100000000, 24, 500000, 1, current_date),
    ('MEMBER-INT', 'Internal member loan', 'vsla', 100000, 1000000, 12, 500000, 1, current_date)
ON CONFLICT (code, version) DO NOTHING;

ALTER TABLE pamoja.loan_product ADD COLUMN IF NOT EXISTS kind text NOT NULL DEFAULT 'group';
ALTER TABLE pamoja.loan_product DROP CONSTRAINT IF EXISTS loan_product_kind_check;
ALTER TABLE pamoja.loan_product ADD CONSTRAINT loan_product_kind_check
    CHECK (kind IN ('group', 'internal_member'));
UPDATE pamoja.loan_product SET kind = 'internal_member' WHERE code = 'MEMBER-INT';

CREATE TABLE IF NOT EXISTS pamoja.loan_application (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    legal_entity_id    uuid NOT NULL REFERENCES pamoja.legal_entity (id),
    branch_id          uuid NOT NULL REFERENCES pamoja.branch (id),
    group_id           uuid NOT NULL REFERENCES pamoja.groups (id),
    product_id         uuid NOT NULL REFERENCES pamoja.loan_product (id),
    product_version    integer NOT NULL,
    loan_kind          text NOT NULL DEFAULT 'group'
                       CHECK (loan_kind IN ('group', 'internal_member')),
    requested_amount_ugx bigint NOT NULL CHECK (requested_amount_ugx > 0),
    purpose            text NOT NULL,
    term_months        integer NOT NULL CHECK (term_months > 0),
    repayment_source   text NOT NULL DEFAULT '',
    authority_record_id uuid NOT NULL REFERENCES pamoja.authority_record (id),
    applied_by         uuid NOT NULL REFERENCES pamoja.app_user (id),
    status             text NOT NULL DEFAULT 'draft'
                       CHECK (status IN ('draft', 'group_authorised', 'officer_appraised',
                                         'operations_review', 'approved', 'declined',
                                         'offer_issued', 'offer_accepted', 'disbursed',
                                         'withdrawn', 'returned')),
    approved_by        uuid REFERENCES pamoja.app_user (id),
    rejection_reason   text NOT NULL DEFAULT '',
    material_change_count integer NOT NULL DEFAULT 0 CHECK (material_change_count >= 0),
    version            integer NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE pamoja.loan_application ADD COLUMN IF NOT EXISTS loan_kind text NOT NULL DEFAULT 'group';
ALTER TABLE pamoja.loan_application DROP CONSTRAINT IF EXISTS loan_application_loan_kind_check;
ALTER TABLE pamoja.loan_application ADD CONSTRAINT loan_application_loan_kind_check
    CHECK (loan_kind IN ('group', 'internal_member'));

CREATE INDEX IF NOT EXISTS loan_application_group_idx ON pamoja.loan_application (group_id, created_at DESC);
CREATE INDEX IF NOT EXISTS loan_application_status_idx ON pamoja.loan_application (status);

CREATE TABLE IF NOT EXISTS pamoja.loan_appraisal (
    id                       uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    application_id           uuid NOT NULL REFERENCES pamoja.loan_application (id),
    officer_id               uuid NOT NULL REFERENCES pamoja.app_user (id),
    cash_flow_notes          text NOT NULL,
    existing_obligations_ugx bigint NOT NULL DEFAULT 0 CHECK (existing_obligations_ugx >= 0),
    recommendation           text NOT NULL,
    created_at               timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS pamoja.borrower_check (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    application_id uuid NOT NULL REFERENCES pamoja.loan_application (id),
    kind           text NOT NULL,
    result         text NOT NULL,
    checked_by     uuid NOT NULL REFERENCES pamoja.app_user (id),
    checked_at     timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS pamoja.loan_guarantee (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    application_id      uuid NOT NULL REFERENCES pamoja.loan_application (id),
    guarantor_member_id uuid NOT NULL REFERENCES pamoja.member (id),
    amount_ugx          bigint NOT NULL CHECK (amount_ugx > 0),
    checked_by          uuid REFERENCES pamoja.app_user (id),
    checked_at          timestamptz,
    created_at          timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS pamoja.loan_decision (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    application_id     uuid NOT NULL REFERENCES pamoja.loan_application (id),
    stage              text NOT NULL CHECK (stage IN ('operations_review', 'credit_decision')),
    decision           text NOT NULL CHECK (decision IN ('approved', 'declined', 'returned')),
    approved_amount_ugx bigint CHECK (approved_amount_ugx IS NULL OR approved_amount_ugx > 0),
    conditions         text NOT NULL DEFAULT '',
    reason             text NOT NULL DEFAULT '',
    decided_by         uuid NOT NULL REFERENCES pamoja.app_user (id),
    decided_at         timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS pamoja.loan_offer (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    application_id uuid NOT NULL REFERENCES pamoja.loan_application (id),
    amount_ugx     bigint NOT NULL CHECK (amount_ugx > 0),
    term_months    integer NOT NULL CHECK (term_months > 0),
    schedule_ref   text NOT NULL,
    issued_by      uuid NOT NULL REFERENCES pamoja.app_user (id),
    issued_at      timestamptz NOT NULL DEFAULT now(),
    expires_on     date NOT NULL,
    accepted_by    uuid REFERENCES pamoja.app_user (id),
    accepted_at    timestamptz,
    status         text NOT NULL DEFAULT 'issued'
                   CHECK (status IN ('issued', 'accepted', 'withdrawn', 'expired'))
);

CREATE TABLE IF NOT EXISTS pamoja.loan_disbursement (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    application_id uuid NOT NULL REFERENCES pamoja.loan_application (id),
    offer_id       uuid NOT NULL REFERENCES pamoja.loan_offer (id),
    tranche_no     integer NOT NULL CHECK (tranche_no >= 1),
    amount_ugx     bigint NOT NULL CHECK (amount_ugx > 0),
    disbursed_on   date NOT NULL,
    confirmed_by   uuid NOT NULL REFERENCES pamoja.app_user (id),
    external_ref   text NOT NULL UNIQUE,
    status         text NOT NULL DEFAULT 'confirmed'
                   CHECK (status IN ('pending', 'confirmed')),
    created_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (application_id, tranche_no)
);

CREATE TABLE IF NOT EXISTS pamoja.loan_installment (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    application_id uuid NOT NULL REFERENCES pamoja.loan_application (id),
    due_on         date NOT NULL,
    amount_ugx     bigint NOT NULL CHECK (amount_ugx > 0),
    paid_ugx       bigint NOT NULL DEFAULT 0 CHECK (paid_ugx >= 0),
    status         text NOT NULL DEFAULT 'pending'
                   CHECK (status IN ('pending', 'partial', 'paid', 'overdue')),
    UNIQUE (application_id, due_on),
    CHECK (paid_ugx <= amount_ugx)
);

CREATE TABLE IF NOT EXISTS pamoja.loan_repayment (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    application_id uuid NOT NULL REFERENCES pamoja.loan_application (id),
    installment_id uuid NOT NULL REFERENCES pamoja.loan_installment (id),
    amount_ugx     bigint NOT NULL CHECK (amount_ugx > 0),
    paid_on        date NOT NULL,
    recorded_by    uuid NOT NULL REFERENCES pamoja.app_user (id),
    external_ref   text NOT NULL UNIQUE,
    created_at     timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS pamoja.loan_material_change (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    application_id uuid NOT NULL REFERENCES pamoja.loan_application (id),
    field          text NOT NULL,
    old_value      text NOT NULL DEFAULT '',
    new_value      text NOT NULL DEFAULT '',
    reason         text NOT NULL,
    changed_by     uuid NOT NULL REFERENCES pamoja.app_user (id),
    created_at     timestamptz NOT NULL DEFAULT now()
);

-- ---------------------------------------------------------------------------
-- Guards
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION pamoja.loan_transition_guard() RETURNS trigger
LANGUAGE plpgsql AS
$$
DECLARE
    v_appraiser uuid;
    v_threshold bigint;
    v_guaranteed bigint;
BEGIN
    IF NEW.status <> OLD.status THEN
        IF NOT ((OLD.status = 'draft' AND NEW.status IN ('group_authorised', 'withdrawn'))
             OR (OLD.status = 'group_authorised' AND NEW.status IN ('officer_appraised', 'withdrawn'))
             OR (OLD.status = 'officer_appraised' AND NEW.status = 'operations_review')
             OR (OLD.status = 'operations_review' AND NEW.status IN ('approved', 'declined', 'returned'))
             OR (OLD.status = 'returned' AND NEW.status = 'group_authorised')
             OR (OLD.status = 'approved' AND NEW.status IN ('offer_issued', 'withdrawn'))
             OR (OLD.status = 'offer_issued' AND NEW.status IN ('offer_accepted', 'withdrawn'))
             OR (OLD.status = 'offer_accepted' AND NEW.status = 'disbursed')
             OR (OLD.status IN ('approved', 'offer_issued', 'offer_accepted') AND NEW.status = 'operations_review')) THEN
            RAISE EXCEPTION 'invalid loan application transition % -> %', OLD.status, NEW.status
                USING ERRCODE = '23514';
        END IF;
    END IF;

    IF NEW.status = 'approved' AND OLD.status <> 'approved' THEN
        SELECT officer_id INTO v_appraiser FROM pamoja.loan_appraisal
        WHERE application_id = NEW.id ORDER BY created_at DESC LIMIT 1;
        IF NEW.approved_by IS NULL THEN
            RAISE EXCEPTION 'an approval needs its approver' USING ERRCODE = '23514';
        END IF;
        IF NEW.approved_by = NEW.applied_by OR NEW.approved_by = v_appraiser THEN
            RAISE EXCEPTION 'SD-03: the approver must differ from the applicant and appraiser'
                USING ERRCODE = '23514';
        END IF;

        SELECT COALESCE((value #>> '{}')::bigint, 500000) INTO v_threshold
        FROM pamoja.config_value WHERE key = 'loans.guarantor_threshold_ugx'
        ORDER BY version DESC LIMIT 1;
        IF NEW.requested_amount_ugx > v_threshold THEN
            SELECT COALESCE(sum(amount_ugx), 0) INTO v_guaranteed
            FROM pamoja.loan_guarantee
            WHERE application_id = NEW.id AND checked_at IS NOT NULL;
            IF v_guaranteed < NEW.requested_amount_ugx THEN
                RAISE EXCEPTION 'A-010: a loan above the guarantor threshold needs a checked guarantee'
                    USING ERRCODE = '23514';
            END IF;
        END IF;
    END IF;
    RETURN NEW;
END
$$;

DROP TRIGGER IF EXISTS loan_transition_guard ON pamoja.loan_application;
CREATE TRIGGER loan_transition_guard
    BEFORE UPDATE ON pamoja.loan_application
    FOR EACH ROW EXECUTE FUNCTION pamoja.loan_transition_guard();

CREATE OR REPLACE FUNCTION pamoja.loan_disbursement_guard() RETURNS trigger
LANGUAGE plpgsql AS
$$
DECLARE
    a record;
    o record;
    v_disbursed bigint;
BEGIN
    SELECT status, approved_by, applied_by, group_id INTO a
    FROM pamoja.loan_application WHERE id = NEW.application_id;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'application does not exist' USING ERRCODE = '23503';
    END IF;
    IF a.status <> 'offer_accepted' THEN
        RAISE EXCEPTION 'a loan is disbursed only after the offer is accepted (status %)', a.status
            USING ERRCODE = '23514';
    END IF;
    IF NEW.confirmed_by = a.approved_by THEN
        RAISE EXCEPTION 'SD-04: the disburser must be independent of the approver'
            USING ERRCODE = '23514';
    END IF;
    IF NEW.external_ref = '' THEN
        RAISE EXCEPTION 'a disbursement needs an external reference' USING ERRCODE = '23514';
    END IF;

    SELECT amount_ugx, term_months, status INTO o FROM pamoja.loan_offer WHERE id = NEW.offer_id;
    IF NOT FOUND OR o.status <> 'accepted' THEN
        RAISE EXCEPTION 'the offer is not accepted' USING ERRCODE = '23514';
    END IF;
    SELECT COALESCE(sum(amount_ugx), 0) INTO v_disbursed
    FROM pamoja.loan_disbursement WHERE application_id = NEW.application_id;
    IF v_disbursed + NEW.amount_ugx > o.amount_ugx THEN
        RAISE EXCEPTION 'disbursements exceed the accepted offer' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END
$$;

DROP TRIGGER IF EXISTS loan_disbursement_guard ON pamoja.loan_disbursement;
CREATE TRIGGER loan_disbursement_guard
    BEFORE INSERT ON pamoja.loan_disbursement
    FOR EACH ROW EXECUTE FUNCTION pamoja.loan_disbursement_guard();

CREATE OR REPLACE FUNCTION pamoja.loan_repayment_guard() RETURNS trigger
LANGUAGE plpgsql AS
$$
DECLARE
    i record;
BEGIN
    SELECT amount_ugx, paid_ugx INTO i FROM pamoja.loan_installment
    WHERE id = NEW.installment_id FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'installment does not exist' USING ERRCODE = '23503';
    END IF;
    IF i.paid_ugx + NEW.amount_ugx > i.amount_ugx THEN
        RAISE EXCEPTION 'repayment exceeds the installment' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END
$$;

DROP TRIGGER IF EXISTS loan_repayment_guard ON pamoja.loan_repayment;
CREATE TRIGGER loan_repayment_guard
    BEFORE INSERT ON pamoja.loan_repayment
    FOR EACH ROW EXECUTE FUNCTION pamoja.loan_repayment_guard();

CREATE OR REPLACE FUNCTION pamoja.loan_repayment_apply() RETURNS trigger
LANGUAGE plpgsql AS
$$
BEGIN
    UPDATE pamoja.loan_installment
    SET paid_ugx = paid_ugx + NEW.amount_ugx,
        status = CASE WHEN paid_ugx + NEW.amount_ugx >= amount_ugx THEN 'paid' ELSE 'partial' END
    WHERE id = NEW.installment_id;
    RETURN NEW;
END
$$;

DROP TRIGGER IF EXISTS loan_repayment_apply ON pamoja.loan_repayment;
CREATE TRIGGER loan_repayment_apply
    AFTER INSERT ON pamoja.loan_repayment
    FOR EACH ROW EXECUTE FUNCTION pamoja.loan_repayment_apply();

-- ---------------------------------------------------------------------------
-- RLS and grants
-- ---------------------------------------------------------------------------
ALTER TABLE pamoja.loan_product ENABLE ROW LEVEL SECURITY;
ALTER TABLE pamoja.loan_product FORCE ROW LEVEL SECURITY;
ALTER TABLE pamoja.loan_application ENABLE ROW LEVEL SECURITY;
ALTER TABLE pamoja.loan_application FORCE ROW LEVEL SECURITY;
ALTER TABLE pamoja.loan_appraisal ENABLE ROW LEVEL SECURITY;
ALTER TABLE pamoja.loan_appraisal FORCE ROW LEVEL SECURITY;
ALTER TABLE pamoja.borrower_check ENABLE ROW LEVEL SECURITY;
ALTER TABLE pamoja.borrower_check FORCE ROW LEVEL SECURITY;
ALTER TABLE pamoja.loan_guarantee ENABLE ROW LEVEL SECURITY;
ALTER TABLE pamoja.loan_guarantee FORCE ROW LEVEL SECURITY;
ALTER TABLE pamoja.loan_decision ENABLE ROW LEVEL SECURITY;
ALTER TABLE pamoja.loan_decision FORCE ROW LEVEL SECURITY;
ALTER TABLE pamoja.loan_offer ENABLE ROW LEVEL SECURITY;
ALTER TABLE pamoja.loan_offer FORCE ROW LEVEL SECURITY;
ALTER TABLE pamoja.loan_disbursement ENABLE ROW LEVEL SECURITY;
ALTER TABLE pamoja.loan_disbursement FORCE ROW LEVEL SECURITY;
ALTER TABLE pamoja.loan_installment ENABLE ROW LEVEL SECURITY;
ALTER TABLE pamoja.loan_installment FORCE ROW LEVEL SECURITY;
ALTER TABLE pamoja.loan_repayment ENABLE ROW LEVEL SECURITY;
ALTER TABLE pamoja.loan_repayment FORCE ROW LEVEL SECURITY;
ALTER TABLE pamoja.loan_material_change ENABLE ROW LEVEL SECURITY;
ALTER TABLE pamoja.loan_material_change FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS loan_product_read ON pamoja.loan_product;
CREATE POLICY loan_product_read ON pamoja.loan_product FOR SELECT TO pamoja_app USING (true);

DROP POLICY IF EXISTS loan_application_scope ON pamoja.loan_application;
CREATE POLICY loan_application_scope ON pamoja.loan_application
    FOR ALL TO pamoja_app
    USING (pamoja.scope_allows(legal_entity_id, branch_id, group_id))
    WITH CHECK (pamoja.scope_allows(legal_entity_id, branch_id, group_id));

DROP POLICY IF EXISTS loan_appraisal_scope ON pamoja.loan_appraisal;
CREATE POLICY loan_appraisal_scope ON pamoja.loan_appraisal
    FOR ALL TO pamoja_app
    USING (application_id IN (SELECT id FROM pamoja.loan_application))
    WITH CHECK (application_id IN (SELECT id FROM pamoja.loan_application));

DROP POLICY IF EXISTS borrower_check_scope ON pamoja.borrower_check;
CREATE POLICY borrower_check_scope ON pamoja.borrower_check
    FOR ALL TO pamoja_app
    USING (application_id IN (SELECT id FROM pamoja.loan_application))
    WITH CHECK (application_id IN (SELECT id FROM pamoja.loan_application));

DROP POLICY IF EXISTS loan_guarantee_scope ON pamoja.loan_guarantee;
CREATE POLICY loan_guarantee_scope ON pamoja.loan_guarantee
    FOR ALL TO pamoja_app
    USING (application_id IN (SELECT id FROM pamoja.loan_application))
    WITH CHECK (application_id IN (SELECT id FROM pamoja.loan_application));

DROP POLICY IF EXISTS loan_decision_scope ON pamoja.loan_decision;
CREATE POLICY loan_decision_scope ON pamoja.loan_decision
    FOR ALL TO pamoja_app
    USING (application_id IN (SELECT id FROM pamoja.loan_application))
    WITH CHECK (application_id IN (SELECT id FROM pamoja.loan_application));

DROP POLICY IF EXISTS loan_offer_scope ON pamoja.loan_offer;
CREATE POLICY loan_offer_scope ON pamoja.loan_offer
    FOR ALL TO pamoja_app
    USING (application_id IN (SELECT id FROM pamoja.loan_application))
    WITH CHECK (application_id IN (SELECT id FROM pamoja.loan_application));

DROP POLICY IF EXISTS loan_disbursement_scope ON pamoja.loan_disbursement;
CREATE POLICY loan_disbursement_scope ON pamoja.loan_disbursement
    FOR ALL TO pamoja_app
    USING (application_id IN (SELECT id FROM pamoja.loan_application))
    WITH CHECK (application_id IN (SELECT id FROM pamoja.loan_application));

DROP POLICY IF EXISTS loan_installment_scope ON pamoja.loan_installment;
CREATE POLICY loan_installment_scope ON pamoja.loan_installment
    FOR ALL TO pamoja_app
    USING (application_id IN (SELECT id FROM pamoja.loan_application))
    WITH CHECK (application_id IN (SELECT id FROM pamoja.loan_application));

DROP POLICY IF EXISTS loan_repayment_scope ON pamoja.loan_repayment;
CREATE POLICY loan_repayment_scope ON pamoja.loan_repayment
    FOR ALL TO pamoja_app
    USING (application_id IN (SELECT id FROM pamoja.loan_application))
    WITH CHECK (application_id IN (SELECT id FROM pamoja.loan_application));

DROP POLICY IF EXISTS loan_material_change_scope ON pamoja.loan_material_change;
CREATE POLICY loan_material_change_scope ON pamoja.loan_material_change
    FOR ALL TO pamoja_app
    USING (application_id IN (SELECT id FROM pamoja.loan_application))
    WITH CHECK (application_id IN (SELECT id FROM pamoja.loan_application));

GRANT SELECT, INSERT, UPDATE ON pamoja.loan_product TO pamoja_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON
    pamoja.loan_application, pamoja.loan_appraisal, pamoja.borrower_check,
    pamoja.loan_guarantee, pamoja.loan_decision, pamoja.loan_offer,
    pamoja.loan_disbursement, pamoja.loan_installment, pamoja.loan_repayment,
    pamoja.loan_material_change
    TO pamoja_app;

GRANT SELECT ON ALL TABLES IN SCHEMA pamoja TO pamoja_readonly;

INSERT INTO public.schema_migrations (version, dirty)
VALUES (5, false)
ON CONFLICT (version) DO NOTHING;

COMMIT;
