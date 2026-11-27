-- 000006_p6_reports: dashboard evidence, ranking snapshots and audited exports.
--
-- A ranking snapshot and an export are both evidence: once written they cannot
-- be updated or deleted. The export row keeps the exact bytes it produced, so
-- its hash can be re-checked later against what a user downloaded.

BEGIN;

CREATE TABLE IF NOT EXISTS pamoja.ranking_snapshot (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    legal_entity_id uuid NOT NULL REFERENCES pamoja.legal_entity (id),
    branch_id       uuid NOT NULL REFERENCES pamoja.branch (id),
    group_id        uuid NOT NULL REFERENCES pamoja.groups (id),
    policy_version  integer NOT NULL CHECK (policy_version >= 1),
    period_end      date NOT NULL,
    score           numeric(6, 2) NOT NULL CHECK (score >= 0 AND score <= 100),
    components      jsonb NOT NULL,
    rank_in_branch  integer CHECK (rank_in_branch >= 1),
    computed_by     uuid REFERENCES pamoja.app_user (id),
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (group_id, period_end)
);

CREATE INDEX IF NOT EXISTS ranking_snapshot_branch_idx
    ON pamoja.ranking_snapshot (branch_id, period_end DESC, score DESC);

CREATE TABLE IF NOT EXISTS pamoja.report_export (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    legal_entity_id uuid NOT NULL REFERENCES pamoja.legal_entity (id),
    branch_id       uuid NOT NULL REFERENCES pamoja.branch (id),
    group_id        uuid NOT NULL REFERENCES pamoja.groups (id),
    kind            text NOT NULL,
    params          jsonb NOT NULL DEFAULT '{}'::jsonb,
    row_count       integer NOT NULL CHECK (row_count >= 0),
    content_sha256  text NOT NULL,
    content         text NOT NULL,
    generated_by    uuid NOT NULL REFERENCES pamoja.app_user (id),
    generated_at    timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS report_export_group_idx
    ON pamoja.report_export (group_id, generated_at DESC);

-- ---------------------------------------------------------------------------
-- Guards: snapshots and exports are append-only evidence.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION pamoja.append_only_evidence_guard() RETURNS trigger
LANGUAGE plpgsql AS
$$
BEGIN
    RAISE EXCEPTION 'a recorded % row is immutable', TG_TABLE_NAME USING ERRCODE = '23514';
END
$$;

DROP TRIGGER IF EXISTS ranking_snapshot_guard ON pamoja.ranking_snapshot;
CREATE TRIGGER ranking_snapshot_guard
    BEFORE UPDATE OR DELETE ON pamoja.ranking_snapshot
    FOR EACH ROW EXECUTE FUNCTION pamoja.append_only_evidence_guard();

DROP TRIGGER IF EXISTS report_export_guard ON pamoja.report_export;
CREATE TRIGGER report_export_guard
    BEFORE UPDATE OR DELETE ON pamoja.report_export
    FOR EACH ROW EXECUTE FUNCTION pamoja.append_only_evidence_guard();

-- ---------------------------------------------------------------------------
-- RLS and grants
-- ---------------------------------------------------------------------------
ALTER TABLE pamoja.ranking_snapshot ENABLE ROW LEVEL SECURITY;
ALTER TABLE pamoja.ranking_snapshot FORCE ROW LEVEL SECURITY;
ALTER TABLE pamoja.report_export ENABLE ROW LEVEL SECURITY;
ALTER TABLE pamoja.report_export FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS ranking_snapshot_scope ON pamoja.ranking_snapshot;
CREATE POLICY ranking_snapshot_scope ON pamoja.ranking_snapshot
    FOR ALL TO pamoja_app
    USING (pamoja.scope_allows(legal_entity_id, branch_id, group_id))
    WITH CHECK (pamoja.scope_allows(legal_entity_id, branch_id, group_id));

DROP POLICY IF EXISTS report_export_scope ON pamoja.report_export;
CREATE POLICY report_export_scope ON pamoja.report_export
    FOR ALL TO pamoja_app
    USING (pamoja.scope_allows(legal_entity_id, branch_id, group_id))
    WITH CHECK (pamoja.scope_allows(legal_entity_id, branch_id, group_id));

GRANT SELECT, INSERT ON pamoja.ranking_snapshot, pamoja.report_export TO pamoja_app;
GRANT SELECT ON pamoja.ranking_snapshot, pamoja.report_export TO pamoja_readonly;

INSERT INTO public.schema_migrations (version, dirty)
VALUES (6, false)
ON CONFLICT (version) DO NOTHING;

COMMIT;
