-- 000007_p7_engagement: learning progress, comments, disputes, follow-up tasks
-- and in-app notifications.
--
-- A dispute cannot be closed without a resolution note and resolver, a task
-- cannot be closed without a closer, and a notification belongs to exactly one
-- user. Learning items are shipped with the build; progress is per member.

BEGIN;

CREATE TABLE IF NOT EXISTS pamoja.learning_item (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    legal_entity_id uuid REFERENCES pamoja.legal_entity (id),
    code            text NOT NULL UNIQUE,
    title           text NOT NULL,
    body            text NOT NULL,
    version         integer NOT NULL DEFAULT 1 CHECK (version >= 1),
    published_by    uuid REFERENCES pamoja.app_user (id),
    published_at    timestamptz NOT NULL DEFAULT now(),
    status          text NOT NULL DEFAULT 'published'
                    CHECK (status IN ('draft', 'published', 'retired'))
);

INSERT INTO pamoja.learning_item (code, title, body) VALUES
    ('L-001', 'How a VSLA meeting is recorded',
     'Every meeting opens, takes a register, records minutes and closes with confirmed attendance. A register confirmed after close can only be amended through a controlled amendment with a reason.'),
    ('L-002', 'Why a shortage is never spread',
     'When the money received is less than the group declared, the verifier records a logged partial verification. The shortage stays visible on the exact entries it belongs to; it is never spread across members to make the totals match.'),
    ('L-003', 'Borrowing: approval is not disbursement',
     'A loan is approved by a credit approver who is not the applicant or appraiser. Money moves only when Finance confirms a disbursement, and that person must differ from the approver.')
ON CONFLICT (code) DO NOTHING;

CREATE TABLE IF NOT EXISTS pamoja.learning_progress (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    legal_entity_id uuid NOT NULL REFERENCES pamoja.legal_entity (id),
    branch_id       uuid NOT NULL REFERENCES pamoja.branch (id),
    group_id        uuid NOT NULL REFERENCES pamoja.groups (id),
    item_id         uuid NOT NULL REFERENCES pamoja.learning_item (id),
    member_id       uuid NOT NULL REFERENCES pamoja.member (id),
    status          text NOT NULL DEFAULT 'assigned'
                    CHECK (status IN ('assigned', 'in_progress', 'completed')),
    completed_at    timestamptz,
    recorded_by     uuid NOT NULL REFERENCES pamoja.app_user (id),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (item_id, member_id)
);

CREATE TABLE IF NOT EXISTS pamoja.comment (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    legal_entity_id uuid NOT NULL REFERENCES pamoja.legal_entity (id),
    branch_id       uuid NOT NULL REFERENCES pamoja.branch (id),
    group_id        uuid NOT NULL REFERENCES pamoja.groups (id),
    entity_type     text NOT NULL
                    CHECK (entity_type IN ('meeting', 'payment_entry', 'loan_application', 'dispute', 'task')),
    entity_id       uuid NOT NULL,
    author_user_id  uuid NOT NULL REFERENCES pamoja.app_user (id),
    body            text NOT NULL
                    CHECK (length(btrim(body)) > 0 AND length(body) <= 2000),
    visibility      text NOT NULL DEFAULT 'group'
                    CHECK (visibility IN ('group', 'restricted')),
    status          text NOT NULL DEFAULT 'visible'
                    CHECK (status IN ('visible', 'hidden')),
    moderated_by    uuid REFERENCES pamoja.app_user (id),
    moderated_at    timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    CHECK (status = 'visible' OR moderated_by IS NOT NULL)
);

CREATE INDEX IF NOT EXISTS comment_entity_idx
    ON pamoja.comment (group_id, entity_type, entity_id, created_at DESC);

CREATE TABLE IF NOT EXISTS pamoja.dispute (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    legal_entity_id uuid NOT NULL REFERENCES pamoja.legal_entity (id),
    branch_id       uuid NOT NULL REFERENCES pamoja.branch (id),
    group_id        uuid NOT NULL REFERENCES pamoja.groups (id),
    kind            text NOT NULL CHECK (kind IN ('attendance', 'payment', 'loan')),
    subject_ref     uuid,
    member_id       uuid REFERENCES pamoja.member (id),
    raised_by       uuid NOT NULL REFERENCES pamoja.app_user (id),
    description     text NOT NULL CHECK (length(btrim(description)) > 0),
    status          text NOT NULL DEFAULT 'open'
                    CHECK (status IN ('open', 'investigating', 'resolved', 'rejected')),
    resolution_note text NOT NULL DEFAULT '',
    resolved_by     uuid REFERENCES pamoja.app_user (id),
    resolved_at     timestamptz,
    due_on          date NOT NULL DEFAULT current_date + 7,
    created_at      timestamptz NOT NULL DEFAULT now(),
    CHECK (status NOT IN ('resolved', 'rejected')
           OR (resolved_by IS NOT NULL AND length(btrim(resolution_note)) > 0))
);

CREATE INDEX IF NOT EXISTS dispute_group_idx ON pamoja.dispute (group_id, created_at DESC);

CREATE TABLE IF NOT EXISTS pamoja.task (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    legal_entity_id uuid NOT NULL REFERENCES pamoja.legal_entity (id),
    branch_id       uuid NOT NULL REFERENCES pamoja.branch (id),
    group_id        uuid NOT NULL REFERENCES pamoja.groups (id),
    title           text NOT NULL CHECK (length(btrim(title)) > 0),
    detail          text NOT NULL DEFAULT '',
    owner_user_id   uuid NOT NULL REFERENCES pamoja.app_user (id),
    due_on          date NOT NULL,
    status          text NOT NULL DEFAULT 'open'
                    CHECK (status IN ('open', 'done', 'cancelled')),
    closed_by       uuid REFERENCES pamoja.app_user (id),
    closed_at       timestamptz,
    created_by      uuid NOT NULL REFERENCES pamoja.app_user (id),
    created_at      timestamptz NOT NULL DEFAULT now(),
    CHECK (status = 'open' OR (closed_by IS NOT NULL AND closed_at IS NOT NULL))
);

CREATE INDEX IF NOT EXISTS task_group_idx ON pamoja.task (group_id, status, due_on);

CREATE TABLE IF NOT EXISTS pamoja.notification (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     uuid NOT NULL REFERENCES pamoja.app_user (id),
    kind        text NOT NULL,
    title       text NOT NULL,
    body        text NOT NULL DEFAULT '',
    entity_type text NOT NULL DEFAULT '',
    entity_id   uuid,
    read_at     timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS notification_user_idx
    ON pamoja.notification (user_id, created_at DESC);

-- ---------------------------------------------------------------------------
-- Guards
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION pamoja.learning_progress_stamp() RETURNS trigger
LANGUAGE plpgsql AS
$$
BEGIN
    IF NEW.status = 'completed' AND NEW.completed_at IS NULL THEN
        NEW.completed_at := now();
    END IF;
    IF NEW.status <> 'completed' THEN
        NEW.completed_at := NULL;
    END IF;
    NEW.updated_at := now();
    RETURN NEW;
END
$$;

DROP TRIGGER IF EXISTS learning_progress_stamp ON pamoja.learning_progress;
CREATE TRIGGER learning_progress_stamp
    BEFORE INSERT OR UPDATE ON pamoja.learning_progress
    FOR EACH ROW EXECUTE FUNCTION pamoja.learning_progress_stamp();

CREATE OR REPLACE FUNCTION pamoja.dispute_transition_guard() RETURNS trigger
LANGUAGE plpgsql AS
$$
BEGIN
    IF NEW.status <> OLD.status THEN
        IF NOT ((OLD.status = 'open' AND NEW.status IN ('investigating', 'resolved', 'rejected'))
             OR (OLD.status = 'investigating' AND NEW.status IN ('resolved', 'rejected'))) THEN
            RAISE EXCEPTION 'invalid dispute transition % -> %', OLD.status, NEW.status
                USING ERRCODE = '23514';
        END IF;
    END IF;
    IF NEW.status IN ('resolved', 'rejected') AND OLD.status NOT IN ('resolved', 'rejected') THEN
        IF NEW.resolved_by IS NULL OR length(btrim(NEW.resolution_note)) = 0 THEN
            RAISE EXCEPTION 'a closed dispute needs a resolver and a resolution note'
                USING ERRCODE = '23514';
        END IF;
        NEW.resolved_at := COALESCE(NEW.resolved_at, now());
    END IF;
    RETURN NEW;
END
$$;

DROP TRIGGER IF EXISTS dispute_transition_guard ON pamoja.dispute;
CREATE TRIGGER dispute_transition_guard
    BEFORE UPDATE ON pamoja.dispute
    FOR EACH ROW EXECUTE FUNCTION pamoja.dispute_transition_guard();

-- ---------------------------------------------------------------------------
-- RLS and grants
-- ---------------------------------------------------------------------------
ALTER TABLE pamoja.learning_item ENABLE ROW LEVEL SECURITY;
ALTER TABLE pamoja.learning_item FORCE ROW LEVEL SECURITY;
ALTER TABLE pamoja.learning_progress ENABLE ROW LEVEL SECURITY;
ALTER TABLE pamoja.learning_progress FORCE ROW LEVEL SECURITY;
ALTER TABLE pamoja.comment ENABLE ROW LEVEL SECURITY;
ALTER TABLE pamoja.comment FORCE ROW LEVEL SECURITY;
ALTER TABLE pamoja.dispute ENABLE ROW LEVEL SECURITY;
ALTER TABLE pamoja.dispute FORCE ROW LEVEL SECURITY;
ALTER TABLE pamoja.task ENABLE ROW LEVEL SECURITY;
ALTER TABLE pamoja.task FORCE ROW LEVEL SECURITY;
ALTER TABLE pamoja.notification ENABLE ROW LEVEL SECURITY;
ALTER TABLE pamoja.notification FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS learning_item_read ON pamoja.learning_item;
CREATE POLICY learning_item_read ON pamoja.learning_item
    FOR SELECT TO pamoja_app
    USING (legal_entity_id IS NULL OR pamoja.scope_allows(legal_entity_id, NULL, NULL));

DROP POLICY IF EXISTS learning_progress_scope ON pamoja.learning_progress;
CREATE POLICY learning_progress_scope ON pamoja.learning_progress
    FOR ALL TO pamoja_app
    USING (pamoja.scope_allows(legal_entity_id, branch_id, group_id))
    WITH CHECK (pamoja.scope_allows(legal_entity_id, branch_id, group_id));

DROP POLICY IF EXISTS comment_scope ON pamoja.comment;
CREATE POLICY comment_scope ON pamoja.comment
    FOR ALL TO pamoja_app
    USING (pamoja.scope_allows(legal_entity_id, branch_id, group_id))
    WITH CHECK (pamoja.scope_allows(legal_entity_id, branch_id, group_id));

DROP POLICY IF EXISTS dispute_scope ON pamoja.dispute;
CREATE POLICY dispute_scope ON pamoja.dispute
    FOR ALL TO pamoja_app
    USING (pamoja.scope_allows(legal_entity_id, branch_id, group_id))
    WITH CHECK (pamoja.scope_allows(legal_entity_id, branch_id, group_id));

DROP POLICY IF EXISTS task_scope ON pamoja.task;
CREATE POLICY task_scope ON pamoja.task
    FOR ALL TO pamoja_app
    USING (pamoja.scope_allows(legal_entity_id, branch_id, group_id))
    WITH CHECK (pamoja.scope_allows(legal_entity_id, branch_id, group_id));

DROP POLICY IF EXISTS notification_owner ON pamoja.notification;
CREATE POLICY notification_owner ON pamoja.notification
    FOR ALL TO pamoja_app
    USING (user_id = pamoja.current_actor())
    WITH CHECK (user_id = pamoja.current_actor());

GRANT SELECT ON pamoja.learning_item TO pamoja_app;
GRANT SELECT, INSERT, UPDATE ON pamoja.learning_progress, pamoja.comment,
    pamoja.dispute, pamoja.task, pamoja.notification TO pamoja_app;
GRANT SELECT ON ALL TABLES IN SCHEMA pamoja TO pamoja_readonly;

INSERT INTO public.schema_migrations (version, dirty)
VALUES (7, false)
ON CONFLICT (version) DO NOTHING;

COMMIT;
