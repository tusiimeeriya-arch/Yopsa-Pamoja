-- Database proof. Run as the application role, which is not the owner and not
-- a superuser:
--
--   docker exec -i <db-container> psql -U pamoja_app -d pamoja < scripts/db-proof.sql
--
-- Every failing statement below is the database refusing the operation. Each
-- attempt sits behind a SAVEPOINT so one refusal does not hide the next. The
-- fixtures are created inside a transaction and rolled back.

\echo '== role and privileges =='
SELECT current_user AS role,
       (SELECT rolsuper FROM pg_roles WHERE rolname = current_user) AS is_superuser,
       (SELECT rolbypassrls FROM pg_roles WHERE rolname = current_user) AS bypasses_rls;

\echo ''
\echo '== 1. allocation total cannot exceed the receipt =='
BEGIN;
SELECT set_config('app.user_id', (SELECT id::text FROM users WHERE email='verifier@yopsa.demo'), false);
INSERT INTO receipts (id, group_id, amount, receipt_date, source_ref, destination, channel, recorded_by)
VALUES ('11111111-1111-1111-1111-111111111111',
        (SELECT id FROM groups WHERE name='Tukolere Wamu VSLA'), 100000, current_date,
        'PROOF-ALLOC-1', 'Proof destination', 'bank',
        (SELECT id FROM users WHERE email='verifier@yopsa.demo'));
INSERT INTO allocations (receipt_id, amount, category, created_by)
VALUES ('11111111-1111-1111-1111-111111111111', 80000, 'savings',
        (SELECT id FROM users WHERE email='verifier@yopsa.demo'));
\echo '-- 80,000 allocated of 100,000. Now try to allocate 30,000 more (must be refused):'
SAVEPOINT a1;
INSERT INTO allocations (receipt_id, amount, category, created_by)
VALUES ('11111111-1111-1111-1111-111111111111', 30000, 'savings',
        (SELECT id FROM users WHERE email='verifier@yopsa.demo'));
ROLLBACK TO SAVEPOINT a1;
ROLLBACK;

\echo ''
\echo '== 2. locked / verified payment rows cannot be updated or deleted =='
BEGIN;
SELECT set_config('app.user_id', (SELECT id::text FROM users WHERE email='verifier@yopsa.demo'), false);
INSERT INTO payment_entries (id, group_id, member_person_id, category, amount, collection_date, status, created_by)
VALUES ('22222222-2222-2222-2222-222222222222',
        (SELECT id FROM groups WHERE name='Tukolere Wamu VSLA'),
        (SELECT m.person_id FROM memberships m JOIN groups g ON g.id=m.group_id WHERE g.name='Tukolere Wamu VSLA' LIMIT 1),
        'savings', 1000, current_date, 'locked',
        (SELECT id FROM users WHERE email='verifier@yopsa.demo'));
\echo '-- update the amount (must be refused):'
SAVEPOINT a2;
UPDATE payment_entries SET amount=5000 WHERE id='22222222-2222-2222-2222-222222222222';
ROLLBACK TO SAVEPOINT a2;
\echo '-- delete the row (must be refused):'
SAVEPOINT a3;
DELETE FROM payment_entries WHERE id='22222222-2222-2222-2222-222222222222';
ROLLBACK TO SAVEPOINT a3;
\echo '-- move it to an unknown status (must be refused):'
SAVEPOINT a4;
UPDATE payment_entries SET status='cancelled' WHERE id='22222222-2222-2222-2222-222222222222';
ROLLBACK TO SAVEPOINT a4;
ROLLBACK;

\echo ''
\echo '== 3. AuditLog rows cannot be updated, deleted or truncated (app role) =='
BEGIN;
SELECT set_config('app.user_id', (SELECT id::text FROM users WHERE email='admin@yopsa.demo'), false);
\echo '-- update an audit row (must be refused):'
SAVEPOINT a5;
UPDATE audit_logs SET action='tampered' WHERE seq=(SELECT max(seq) FROM audit_logs);
ROLLBACK TO SAVEPOINT a5;
\echo '-- delete an audit row (must be refused):'
SAVEPOINT a6;
DELETE FROM audit_logs WHERE seq=(SELECT max(seq) FROM audit_logs);
ROLLBACK TO SAVEPOINT a6;
\echo '-- truncate the audit log (must be refused):'
SAVEPOINT a7;
TRUNCATE audit_logs;
ROLLBACK TO SAVEPOINT a7;
ROLLBACK;

\echo ''
\echo '(The append-only trigger is proved separately as the owner, who does have the grant:'
\echo ' see scripts/db-proof-owner-trigger.sql.)'
\echo ''
\echo '== 4. cross-group reads return nothing =='
BEGIN;
SELECT set_config('app.user_id', (SELECT id::text FROM users WHERE email='member@yopsa.demo'), false);
\echo '-- payment entries in the member''s own group (Tukolere Wamu VSLA):'
SELECT count(*) AS own_group_entries FROM payment_entries
 WHERE group_id = (SELECT id FROM groups WHERE name='Tukolere Wamu VSLA');
\echo '-- payment entries in a group the member does not belong to (Nkumba Investment Club):'
SELECT count(*) AS other_group_entries FROM payment_entries
 WHERE group_id = (SELECT id FROM groups WHERE name='Nkumba Investment Club');
\echo '-- every group the member can see:'
SELECT name FROM groups ORDER BY name;
\echo '-- persons in a group the member does not belong to (must be 0):'
SELECT count(*) AS other_group_persons FROM persons
 WHERE id IN (SELECT person_id FROM memberships WHERE group_id = (SELECT id FROM groups WHERE name='Nkumba Investment Club'));
ROLLBACK;

\echo ''
\echo '== 5. app role cannot DELETE or TRUNCATE the five protected tables =='
SELECT t AS table_name,
       has_table_privilege(current_user, t, 'DELETE') AS can_delete,
       has_table_privilege(current_user, t, 'TRUNCATE') AS can_truncate
  FROM (VALUES ('users'),('persons'),('groups'),('memberships'),('role_assignments')) AS v(t);

\echo '-- is_demo cannot be flipped on a real row (must be refused):'
BEGIN;
SELECT set_config('app.user_id', (SELECT id::text FROM users WHERE email='admin@yopsa.demo'), false);
INSERT INTO users (id, email, password_hash, full_name, is_demo)
VALUES ('33333333-3333-3333-3333-333333333333', 'proof-real@selftest.invalid', 'not-a-real-hash', 'Proof real row', false);
SAVEPOINT d1;
UPDATE users SET is_demo=true WHERE id='33333333-3333-3333-3333-333333333333';
ROLLBACK TO SAVEPOINT d1;
\echo '-- DELETE on users must be refused by privilege:'
SAVEPOINT d2;
DELETE FROM users WHERE id='33333333-3333-3333-3333-333333333333';
ROLLBACK TO SAVEPOINT d2;
\echo '-- TRUNCATE on users must be refused by privilege:'
SAVEPOINT d3;
TRUNCATE users;
ROLLBACK TO SAVEPOINT d3;
ROLLBACK;

\echo ''
\echo '== done =='
