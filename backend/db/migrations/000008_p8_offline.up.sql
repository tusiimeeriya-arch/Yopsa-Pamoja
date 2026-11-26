-- 000008_p8_offline: offline outbox batches replayed through the server.
--
-- A batch records what a device queued, the server's own time, the measured
-- clock skew and one row per operation with its outcome. Replaying a batch is
-- safe: an operation's idempotency key is unique, so a retry returns the
-- recorded result instead of applying the change twice.

BEGIN;

CREATE TABLE IF NOT EXISTS pamoja.sync_batch (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    legal_entity_id uuid NOT NULL REFERENCES pamoja.legal_entity (id),
    branch_id       uuid NOT NULL REFERENCES pamoja.branch (id),
    group_id        uuid NOT NULL REFERENCES pamoja.groups (id),
    client_id       text NOT NULL,
    client_time     timestamptz NOT NULL,
    server_time     timestamptz NOT NULL DEFAULT now(),
    skew_ms         bigint NOT NULL DEFAULT 0,
    operation_count integer NOT NULL CHECK (operation_count >= 0),
    applied_count   integer NOT NULL DEFAULT 0 CHECK (applied_count >= 0),
    conflict_count  integer NOT NULL DEFAULT 0 CHECK (conflict_count >= 0),
    status          text NOT NULL DEFAULT 'processing'
                    CHECK (status IN ('processing', 'completed', 'completed_with_conflicts')),
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS sync_batch_group_idx
    ON pamoja.sync_batch (group_id, created_at DESC);

CREATE TABLE IF NOT EXISTS pamoja.sync_operation (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    batch_id          uuid NOT NULL REFERENCES pamoja.sync_batch (id),
    seq               integer NOT NULL CHECK (seq >= 1),
    kind              text NOT NULL
                      CHECK (kind IN ('attendance.check_in', 'payment.entry.create')),
    idempotency_key   text NOT NULL UNIQUE,
    client_created_at timestamptz NOT NULL,
    payload           jsonb NOT NULL,
    status            text NOT NULL DEFAULT 'pending'
                      CHECK (status IN ('pending', 'applied', 'conflict', 'rejected')),
    conflict_reason   text NOT NULL DEFAULT '',
    result            jsonb,
    applied_at        timestamptz,
    UNIQUE (batch_id, seq)
);

-- ---------------------------------------------------------------------------
-- RLS and grants
-- ---------------------------------------------------------------------------
ALTER TABLE pamoja.sync_batch ENABLE ROW LEVEL SECURITY;
ALTER TABLE pamoja.sync_batch FORCE ROW LEVEL SECURITY;
ALTER TABLE pamoja.sync_operation ENABLE ROW LEVEL SECURITY;
ALTER TABLE pamoja.sync_operation FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS sync_batch_scope ON pamoja.sync_batch;
CREATE POLICY sync_batch_scope ON pamoja.sync_batch
    FOR ALL TO pamoja_app
    USING (pamoja.scope_allows(legal_entity_id, branch_id, group_id))
    WITH CHECK (pamoja.scope_allows(legal_entity_id, branch_id, group_id));

DROP POLICY IF EXISTS sync_operation_scope ON pamoja.sync_operation;
CREATE POLICY sync_operation_scope ON pamoja.sync_operation
    FOR ALL TO pamoja_app
    USING (batch_id IN (SELECT id FROM pamoja.sync_batch))
    WITH CHECK (batch_id IN (SELECT id FROM pamoja.sync_batch));

GRANT SELECT, INSERT, UPDATE ON pamoja.sync_batch, pamoja.sync_operation TO pamoja_app;
GRANT SELECT ON pamoja.sync_batch, pamoja.sync_operation TO pamoja_readonly;

INSERT INTO public.schema_migrations (version, dirty)
VALUES (8, false)
ON CONFLICT (version) DO NOTHING;

COMMIT;
