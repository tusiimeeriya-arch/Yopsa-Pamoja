-- 0001_core.sql — tables, indexes and reference data.
-- Generated from the P1 schema; applied once by src/migrate.js.

-- Yopsa Pamoja schema. Whole-UGX BIGINT money, timestamptz in UTC.
-- Every object belongs to the public schema, whatever search_path the applying
-- role carries by default.
SET search_path = public;
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS users (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  email text NOT NULL UNIQUE,
  password_hash text NOT NULL,
  full_name text NOT NULL,
  phone text,
  status text NOT NULL DEFAULT 'active',
  failed_attempts int NOT NULL DEFAULT 0,
  locked_until timestamptz,
  is_demo boolean NOT NULL DEFAULT false,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS sessions (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  token_hash text NOT NULL UNIQUE,
  created_at timestamptz NOT NULL DEFAULT now(),
  last_seen_at timestamptz NOT NULL DEFAULT now(),
  expires_at timestamptz NOT NULL,
  step_up_at timestamptz,
  revoked_at timestamptz,
  ip text,
  user_agent text
);

CREATE TABLE IF NOT EXISTS org_branches (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  name text NOT NULL,
  district text NOT NULL,
  code text NOT NULL UNIQUE,
  is_demo boolean NOT NULL DEFAULT false,
  demo_batch text,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS group_type_configs (
  type text PRIMARY KEY,
  label text NOT NULL,
  categories jsonb NOT NULL DEFAULT '[]'::jsonb,
  flags jsonb NOT NULL DEFAULT '{}'::jsonb
);

CREATE TABLE IF NOT EXISTS policy_configs (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  key text NOT NULL,
  value jsonb NOT NULL,
  version int NOT NULL DEFAULT 1,
  effective_from date NOT NULL DEFAULT current_date,
  approved_by uuid REFERENCES users(id),
  status text NOT NULL DEFAULT 'proposed',
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (key, version)
);

CREATE TABLE IF NOT EXISTS persons (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  full_name text NOT NULL,
  phone text,
  national_id_ref text,
  gender text NOT NULL DEFAULT 'NotStated',
  date_of_birth date,
  linked_user_id uuid REFERENCES users(id),
  consent_recorded boolean NOT NULL DEFAULT false,
  consent_at timestamptz,
  created_by uuid REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  is_demo boolean NOT NULL DEFAULT false,
  demo_batch text
);

CREATE TABLE IF NOT EXISTS role_assignments (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id uuid NOT NULL REFERENCES users(id),
  role text NOT NULL,
  scope_type text NOT NULL,
  scope_id text NOT NULL,
  effective_from date NOT NULL DEFAULT current_date,
  effective_to date,
  status text NOT NULL DEFAULT 'pending',
  granted_by uuid REFERENCES users(id),
  approved_by uuid REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  revoked_at timestamptz,
  is_demo boolean NOT NULL DEFAULT false
);

CREATE TABLE IF NOT EXISTS groups (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  name text NOT NULL,
  type text NOT NULL REFERENCES group_type_configs(type),
  branch_id uuid NOT NULL REFERENCES org_branches(id),
  status text NOT NULL DEFAULT 'Draft',
  registration_evidence_status text NOT NULL DEFAULT 'NotProvided',
  created_by uuid NOT NULL REFERENCES users(id),
  submitted_at timestamptz,
  reviewed_by uuid REFERENCES users(id),
  reviewed_at timestamptz,
  review_note text,
  review_decision text,
  activated_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  is_demo boolean NOT NULL DEFAULT false,
  demo_batch text
);

CREATE TABLE IF NOT EXISTS memberships (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  person_id uuid NOT NULL REFERENCES persons(id),
  group_id uuid NOT NULL REFERENCES groups(id),
  join_date date NOT NULL,
  leave_date date,
  status text NOT NULL DEFAULT 'active',
  created_by uuid REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  is_demo boolean NOT NULL DEFAULT false
);

CREATE TABLE IF NOT EXISTS leaderships (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  group_id uuid NOT NULL REFERENCES groups(id),
  person_id uuid NOT NULL REFERENCES persons(id),
  position text NOT NULL,
  authority_scope text NOT NULL DEFAULT 'group',
  effective_from date NOT NULL,
  effective_to date,
  evidence_file_ref uuid,
  created_by uuid REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  is_demo boolean NOT NULL DEFAULT false
);

CREATE TABLE IF NOT EXISTS meetings (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  group_id uuid NOT NULL REFERENCES groups(id),
  title text NOT NULL,
  scheduled_for timestamptz NOT NULL,
  status text NOT NULL DEFAULT 'scheduled',
  location_text text,
  opened_by uuid REFERENCES users(id),
  opened_at timestamptz,
  register_confirmed_by uuid REFERENCES users(id),
  register_confirmed_at timestamptz,
  closed_by uuid REFERENCES users(id),
  closed_at timestamptz,
  cancel_reason text,
  created_by uuid REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  is_demo boolean NOT NULL DEFAULT false,
  demo_batch text
);

CREATE TABLE IF NOT EXISTS attendance_records (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  meeting_id uuid NOT NULL REFERENCES meetings(id),
  person_id uuid NOT NULL REFERENCES persons(id),
  status text NOT NULL DEFAULT 'present',
  checked_in_at timestamptz NOT NULL DEFAULT now(),
  method text NOT NULL DEFAULT 'self',
  recorded_by uuid REFERENCES users(id),
  confirmed_by uuid REFERENCES users(id),
  confirmation_method text,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (meeting_id, person_id)
);

CREATE TABLE IF NOT EXISTS minutes (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  meeting_id uuid NOT NULL UNIQUE REFERENCES meetings(id),
  body text NOT NULL,
  recorded_by uuid NOT NULL REFERENCES users(id),
  recorded_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS resolutions (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  meeting_id uuid NOT NULL REFERENCES meetings(id),
  body text NOT NULL,
  recorded_by uuid NOT NULL REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS action_items (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  meeting_id uuid REFERENCES meetings(id),
  group_id uuid NOT NULL REFERENCES groups(id),
  title text NOT NULL,
  owner_person_id uuid REFERENCES persons(id),
  deadline date NOT NULL,
  status text NOT NULL DEFAULT 'open',
  created_by uuid REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS meeting_documents (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  meeting_id uuid NOT NULL REFERENCES meetings(id),
  file_id uuid NOT NULL,
  consent_flag boolean NOT NULL DEFAULT false,
  photo_policy_ack boolean NOT NULL DEFAULT false,
  uploaded_by uuid NOT NULL REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS meeting_amendments (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  meeting_id uuid NOT NULL REFERENCES meetings(id),
  field text NOT NULL,
  original_value text,
  new_value text NOT NULL,
  reason text NOT NULL,
  requested_by uuid NOT NULL REFERENCES users(id),
  reviewed_by uuid REFERENCES users(id),
  status text NOT NULL DEFAULT 'pending',
  applied_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS file_objects (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  group_id uuid REFERENCES groups(id),
  kind text NOT NULL,
  filename text NOT NULL,
  mime text NOT NULL,
  size int NOT NULL,
  data bytea NOT NULL,
  uploaded_by uuid NOT NULL REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS payment_entries (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  group_id uuid NOT NULL REFERENCES groups(id),
  member_person_id uuid NOT NULL REFERENCES persons(id),
  category text NOT NULL,
  amount bigint NOT NULL CHECK (amount > 0),
  collection_date date NOT NULL,
  status text NOT NULL DEFAULT 'draft',
  submission_id uuid,
  created_by uuid NOT NULL REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  is_demo boolean NOT NULL DEFAULT false,
  demo_batch text
);

CREATE TABLE IF NOT EXISTS group_submissions (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  group_id uuid NOT NULL REFERENCES groups(id),
  period_start date NOT NULL,
  period_end date NOT NULL,
  status text NOT NULL DEFAULT 'submitted',
  totals jsonb NOT NULL DEFAULT '{}'::jsonb,
  total_amount bigint NOT NULL DEFAULT 0,
  submitted_by uuid NOT NULL REFERENCES users(id),
  submitted_at timestamptz NOT NULL DEFAULT now(),
  verified_by uuid REFERENCES users(id),
  verified_at timestamptz,
  difference bigint,
  receipt_total bigint,
  posting_status text,
  created_at timestamptz NOT NULL DEFAULT now(),
  is_demo boolean NOT NULL DEFAULT false,
  demo_batch text
);

CREATE TABLE IF NOT EXISTS receipts (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  group_id uuid NOT NULL REFERENCES groups(id),
  submission_id uuid REFERENCES group_submissions(id),
  amount bigint NOT NULL CHECK (amount > 0),
  receipt_date date NOT NULL,
  source_ref text NOT NULL UNIQUE,
  destination text NOT NULL,
  channel text NOT NULL DEFAULT 'bank',
  evidence_ref text,
  status text NOT NULL DEFAULT 'confirmed',
  recorded_by uuid NOT NULL REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  is_demo boolean NOT NULL DEFAULT false,
  demo_batch text
);

CREATE TABLE IF NOT EXISTS allocations (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  receipt_id uuid NOT NULL REFERENCES receipts(id),
  submission_id uuid REFERENCES group_submissions(id),
  entry_id uuid REFERENCES payment_entries(id),
  amount bigint NOT NULL CHECK (amount > 0),
  category text NOT NULL,
  created_by uuid NOT NULL REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS ledger_postings (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  submission_id uuid NOT NULL REFERENCES group_submissions(id),
  allocation_id uuid REFERENCES allocations(id),
  amount bigint NOT NULL,
  status text NOT NULL,
  ledger_ref text,
  error text,
  attempts int NOT NULL DEFAULT 1,
  created_by uuid REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  posted_at timestamptz
);

CREATE TABLE IF NOT EXISTS finance_exceptions (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  kind text NOT NULL,
  entity text NOT NULL,
  entity_id text NOT NULL,
  detail text NOT NULL,
  owner_user_id uuid NOT NULL REFERENCES users(id),
  deadline date NOT NULL,
  status text NOT NULL DEFAULT 'open',
  created_at timestamptz NOT NULL DEFAULT now(),
  resolved_at timestamptz
);

CREATE TABLE IF NOT EXISTS corrections (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  entity text NOT NULL,
  entity_id uuid NOT NULL,
  field text NOT NULL,
  original_value text,
  new_value text NOT NULL,
  reason text NOT NULL,
  requested_by uuid NOT NULL REFERENCES users(id),
  approved_by uuid REFERENCES users(id),
  status text NOT NULL DEFAULT 'pending',
  applied_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS loan_applications (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  group_id uuid NOT NULL REFERENCES groups(id),
  product text NOT NULL,
  amount bigint NOT NULL CHECK (amount > 0),
  purpose text NOT NULL,
  term_months int NOT NULL,
  repayment_source text NOT NULL,
  existing_debts bigint NOT NULL DEFAULT 0,
  cash_flow text,
  member_allocation text,
  security text,
  guarantees text,
  status text NOT NULL DEFAULT 'Draft',
  created_by uuid NOT NULL REFERENCES users(id),
  authorised_person_id uuid REFERENCES persons(id),
  resolution_ref uuid,
  quorum_evidence_ref uuid,
  approved_by uuid REFERENCES users(id),
  approved_at timestamptz,
  offered_at timestamptz,
  accepted_at timestamptz,
  disbursed_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  is_demo boolean NOT NULL DEFAULT false,
  demo_batch text
);

CREATE TABLE IF NOT EXISTS approval_steps (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  loan_id uuid NOT NULL REFERENCES loan_applications(id),
  step text NOT NULL,
  actor_user_id uuid NOT NULL REFERENCES users(id),
  decision text NOT NULL,
  reason text,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS guarantors (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  loan_id uuid NOT NULL REFERENCES loan_applications(id),
  person_id uuid REFERENCES persons(id),
  amount bigint NOT NULL,
  evidence_ref text,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS disbursements (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  loan_id uuid NOT NULL REFERENCES loan_applications(id),
  amount bigint NOT NULL CHECK (amount > 0),
  disbursed_on date NOT NULL,
  confirmed_by uuid NOT NULL REFERENCES users(id),
  ledger_ref text,
  created_at timestamptz NOT NULL DEFAULT now(),
  is_demo boolean NOT NULL DEFAULT false
);

CREATE TABLE IF NOT EXISTS disputes (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  kind text NOT NULL,
  entity text NOT NULL,
  entity_id uuid NOT NULL,
  group_id uuid REFERENCES groups(id),
  person_id uuid REFERENCES persons(id),
  raised_by_user_id uuid NOT NULL REFERENCES users(id),
  reason text NOT NULL,
  original_value text,
  status text NOT NULL DEFAULT 'open',
  assigned_to_user_id uuid REFERENCES users(id),
  reviewer_user_id uuid REFERENCES users(id),
  resolution text,
  created_at timestamptz NOT NULL DEFAULT now(),
  resolved_at timestamptz,
  is_demo boolean NOT NULL DEFAULT false
);

CREATE TABLE IF NOT EXISTS comments (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  group_id uuid NOT NULL REFERENCES groups(id),
  body text NOT NULL,
  visibility text NOT NULL DEFAULT 'group',
  author_user_id uuid NOT NULL REFERENCES users(id),
  status text NOT NULL DEFAULT 'published',
  moderation_reason text,
  moderated_by uuid REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS learning_items (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  title text NOT NULL,
  category text NOT NULL,
  language text NOT NULL DEFAULT 'English',
  version int NOT NULL DEFAULT 1,
  content_owner text NOT NULL,
  review_date date,
  kind text NOT NULL DEFAULT 'text',
  url text,
  body text,
  is_demo boolean NOT NULL DEFAULT false
);

CREATE TABLE IF NOT EXISTS learning_assignments (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  item_id uuid NOT NULL REFERENCES learning_items(id),
  group_id uuid NOT NULL REFERENCES groups(id),
  assigned_by uuid REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (item_id, group_id)
);

CREATE TABLE IF NOT EXISTS learning_progress (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  item_id uuid NOT NULL REFERENCES learning_items(id),
  person_id uuid NOT NULL REFERENCES persons(id),
  status text NOT NULL,
  opened_at timestamptz,
  completed_at timestamptz,
  UNIQUE (item_id, person_id)
);

CREATE TABLE IF NOT EXISTS tasks (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  title text NOT NULL,
  owner_user_id uuid NOT NULL REFERENCES users(id),
  deadline date NOT NULL,
  linked_entity text,
  linked_id text,
  status text NOT NULL DEFAULT 'open',
  dedupe_key text UNIQUE,
  created_at timestamptz NOT NULL DEFAULT now(),
  completed_at timestamptz,
  is_demo boolean NOT NULL DEFAULT false
);

CREATE TABLE IF NOT EXISTS notifications (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id uuid NOT NULL REFERENCES users(id),
  title text NOT NULL,
  body text NOT NULL,
  deadline date NOT NULL,
  dedupe_key text UNIQUE,
  read_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS ranking_policy_versions (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  version int NOT NULL UNIQUE,
  weights jsonb NOT NULL,
  min_loans int NOT NULL,
  min_meetings int NOT NULL,
  overdue_exclusion_days int NOT NULL DEFAULT 30,
  tie_break jsonb NOT NULL,
  status text NOT NULL DEFAULT 'proposed',
  author_user_id uuid NOT NULL REFERENCES users(id),
  approved_by uuid REFERENCES users(id),
  effective_from date,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS ranking_snapshots (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  policy_version_id uuid NOT NULL REFERENCES ranking_policy_versions(id),
  cutoff date NOT NULL,
  payload jsonb NOT NULL,
  created_by uuid REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  is_demo boolean NOT NULL DEFAULT false
);

CREATE TABLE IF NOT EXISTS mock_ledger_accounts (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  group_id uuid NOT NULL UNIQUE REFERENCES groups(id),
  principal bigint NOT NULL DEFAULT 0,
  interest_due bigint NOT NULL DEFAULT 0,
  fees_due bigint NOT NULL DEFAULT 0,
  schedule jsonb NOT NULL DEFAULT '[]'::jsonb,
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS audit_logs (
  seq bigserial PRIMARY KEY,
  id uuid NOT NULL DEFAULT gen_random_uuid(),
  actor_user_id uuid,
  role text,
  scope_type text,
  scope_id text,
  action text NOT NULL,
  entity text NOT NULL,
  entity_id text,
  before jsonb,
  after jsonb,
  reason text,
  channel text NOT NULL DEFAULT 'web',
  at timestamptz NOT NULL DEFAULT now(),
  prev_hash text NOT NULL,
  hash text NOT NULL
);

CREATE TABLE IF NOT EXISTS idempotency_keys (
  user_id uuid NOT NULL REFERENCES users(id),
  key text NOT NULL,
  function text NOT NULL,
  payload_hash text NOT NULL,
  response jsonb,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, key)
);

CREATE INDEX IF NOT EXISTS idx_memberships_group ON memberships(group_id);
CREATE INDEX IF NOT EXISTS idx_memberships_person ON memberships(person_id);
CREATE INDEX IF NOT EXISTS idx_role_assignments_user ON role_assignments(user_id);
CREATE INDEX IF NOT EXISTS idx_meetings_group ON meetings(group_id);
CREATE INDEX IF NOT EXISTS idx_attendance_meeting ON attendance_records(meeting_id);
CREATE INDEX IF NOT EXISTS idx_payment_entries_group ON payment_entries(group_id);
CREATE INDEX IF NOT EXISTS idx_payment_entries_sub ON payment_entries(submission_id);
CREATE INDEX IF NOT EXISTS idx_receipts_group ON receipts(group_id);
CREATE INDEX IF NOT EXISTS idx_allocations_receipt ON allocations(receipt_id);
CREATE INDEX IF NOT EXISTS idx_loans_group ON loan_applications(group_id);
CREATE INDEX IF NOT EXISTS idx_disputes_group ON disputes(group_id);
CREATE INDEX IF NOT EXISTS idx_audit_seq ON audit_logs(seq);
CREATE INDEX IF NOT EXISTS idx_tasks_owner ON tasks(owner_user_id);

-- ===========================================================================
-- Hardening: one-time codes, password resets, outbound messages, cash
-- reconciliation, internal member loans, scheduled-job evidence.
-- ===========================================================================

CREATE TABLE IF NOT EXISTS otp_codes (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  purpose text NOT NULL,
  code_hash text NOT NULL,
  expires_at timestamptz NOT NULL,
  consumed_at timestamptz,
  attempts int NOT NULL DEFAULT 0,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_otp_user_purpose ON otp_codes(user_id, purpose);

CREATE TABLE IF NOT EXISTS password_reset_requests (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  email text NOT NULL,
  user_id uuid REFERENCES users(id),
  requested_at timestamptz NOT NULL DEFAULT now(),
  status text NOT NULL DEFAULT 'open',
  handled_by uuid REFERENCES users(id),
  handled_at timestamptz
);

-- Provider-neutral outbound message log. The stub records what would be sent;
-- no third-party provider is called.
CREATE TABLE IF NOT EXISTS outbound_messages (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  channel text NOT NULL,
  recipient text NOT NULL,
  body text NOT NULL,
  provider text NOT NULL DEFAULT 'stub',
  status text NOT NULL DEFAULT 'queued',
  created_at timestamptz NOT NULL DEFAULT now()
);

-- Cash path: receipt -> handover -> receipt by branch -> deposit. Three steps,
-- three different people, enforced again by trigger below.
CREATE TABLE IF NOT EXISTS cash_reconciliations (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  receipt_id uuid NOT NULL UNIQUE REFERENCES receipts(id),
  group_id uuid NOT NULL REFERENCES groups(id),
  amount bigint NOT NULL CHECK (amount > 0),
  status text NOT NULL DEFAULT 'awaiting_handover',
  handover_by uuid REFERENCES users(id),
  handover_at timestamptz,
  received_by uuid REFERENCES users(id),
  received_at timestamptz,
  deposited_by uuid REFERENCES users(id),
  deposited_at timestamptz,
  bank_ref text,
  evidence_ref text,
  created_at timestamptz NOT NULL DEFAULT now()
);

-- Internal member-to-member loans. Deliberately a separate table: nothing here
-- is ever joined into Yopsa loan totals or disbursements.
CREATE TABLE IF NOT EXISTS member_loans (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  group_id uuid NOT NULL REFERENCES groups(id),
  lender_person_id uuid NOT NULL REFERENCES persons(id),
  borrower_person_id uuid NOT NULL REFERENCES persons(id),
  amount bigint NOT NULL CHECK (amount > 0),
  purpose text NOT NULL,
  agreed_on date NOT NULL,
  due_on date,
  status text NOT NULL DEFAULT 'agreed',
  witness_person_id uuid REFERENCES persons(id),
  recorded_by uuid NOT NULL REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  CHECK (lender_person_id <> borrower_person_id)
);

CREATE TABLE IF NOT EXISTS job_runs (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  job text NOT NULL,
  started_at timestamptz NOT NULL DEFAULT now(),
  finished_at timestamptz,
  status text NOT NULL DEFAULT 'running',
  result jsonb,
  error text
);
CREATE INDEX IF NOT EXISTS idx_job_runs_job ON job_runs(job, started_at DESC);

