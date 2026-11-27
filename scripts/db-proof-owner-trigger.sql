-- Second layer: even a role that holds UPDATE/DELETE on audit_logs (the owner)
-- is stopped by the append-only trigger.
--
--   docker exec -i <db-container> psql -U pamoja -d pamoja < scripts/db-proof-owner-trigger.sql

\echo '== append-only trigger as the table owner (grant present) =='
SELECT current_user AS role, has_table_privilege(current_user, 'audit_logs', 'UPDATE') AS can_update;
BEGIN;
SAVEPOINT t1;
UPDATE audit_logs SET action='tampered' WHERE seq=(SELECT max(seq) FROM audit_logs);
ROLLBACK TO SAVEPOINT t1;
SAVEPOINT t2;
DELETE FROM audit_logs WHERE seq=(SELECT max(seq) FROM audit_logs);
ROLLBACK TO SAVEPOINT t2;
SAVEPOINT t3;
TRUNCATE audit_logs;
ROLLBACK TO SAVEPOINT t3;
ROLLBACK;
\echo '== done =='
