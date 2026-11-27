-- Down for 000005.

BEGIN;

DROP TABLE IF EXISTS pamoja.loan_material_change;
DROP TABLE IF EXISTS pamoja.loan_repayment;
DROP TABLE IF EXISTS pamoja.loan_installment;
DROP TABLE IF EXISTS pamoja.loan_disbursement;
DROP TABLE IF EXISTS pamoja.loan_offer;
DROP TABLE IF EXISTS pamoja.loan_decision;
DROP TABLE IF EXISTS pamoja.loan_guarantee;
DROP TABLE IF EXISTS pamoja.borrower_check;
DROP TABLE IF EXISTS pamoja.loan_appraisal;
DROP TABLE IF EXISTS pamoja.loan_application;
DROP TABLE IF EXISTS pamoja.loan_product;

DROP FUNCTION IF EXISTS pamoja.loan_repayment_apply();
DROP FUNCTION IF EXISTS pamoja.loan_repayment_guard();
DROP FUNCTION IF EXISTS pamoja.loan_disbursement_guard();
DROP FUNCTION IF EXISTS pamoja.loan_transition_guard();

DELETE FROM public.schema_migrations WHERE version = 5;

COMMIT;
