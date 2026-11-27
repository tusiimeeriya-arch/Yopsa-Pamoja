-- Down for 000004.

BEGIN;

DROP TABLE IF EXISTS pamoja.sandbox_ledger;
DROP TABLE IF EXISTS pamoja.correction;
DROP TABLE IF EXISTS pamoja.finance_exception;
DROP TABLE IF EXISTS pamoja.ledger_posting;
DROP TABLE IF EXISTS pamoja.allocation;
DROP TABLE IF EXISTS pamoja.receipt;
DROP TABLE IF EXISTS pamoja.payment_entry;
DROP TABLE IF EXISTS pamoja.group_submission;

DROP FUNCTION IF EXISTS pamoja.correction_guard();
DROP FUNCTION IF EXISTS pamoja.finance_exception_guard();
DROP FUNCTION IF EXISTS pamoja.posting_guard();
DROP FUNCTION IF EXISTS pamoja.allocation_guard();
DROP FUNCTION IF EXISTS pamoja.receipt_guard();
DROP FUNCTION IF EXISTS pamoja.submission_guard();
DROP FUNCTION IF EXISTS pamoja.payment_entry_guard();

DELETE FROM public.schema_migrations WHERE version = 4;

COMMIT;
