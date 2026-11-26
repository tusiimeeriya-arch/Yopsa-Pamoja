package loans_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/yopsa/pamoja/backend/internal/domain"
	"github.com/yopsa/pamoja/backend/internal/loans"
	"github.com/yopsa/pamoja/backend/internal/platform/db"
)

// failingTx refuses the first statement whose SQL contains marker, so a test
// can prove that a database error is returned to the caller rather than
// swallowed.
type failingTx struct {
	pgx.Tx
	marker string
	err    error
}

func (t *failingTx) check(sql string) error {
	if strings.Contains(sql, t.marker) {
		return t.err
	}
	return nil
}

func (t *failingTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if err := t.check(sql); err != nil {
		return pgconn.CommandTag{}, err
	}
	return t.Tx.Exec(ctx, sql, args...)
}

func (t *failingTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if err := t.check(sql); err != nil {
		return nil, err
	}
	return t.Tx.Query(ctx, sql, args...)
}

func (t *failingTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if err := t.check(sql); err != nil {
		return failingRow{err: err}
	}
	return t.Tx.QueryRow(ctx, sql, args...)
}

type failingRow struct{ err error }

func (r failingRow) Scan(...any) error { return r.err }

func (f fixture) assertSurfaced(t *testing.T, actor string, markers []string, fn func(context.Context, pgx.Tx) error) {
	t.Helper()
	for _, marker := range markers {
		err := db.WithActor(context.Background(), f.app,
			db.Actor{UserID: actor, Scopes: []string{f.tenant.GroupID}}, uuid.NewString(),
			func(ctx context.Context, tx pgx.Tx) error {
				return fn(ctx, &failingTx{Tx: tx, marker: marker, err: errors.New("injected database failure")})
			})
		if err == nil {
			t.Fatalf("%q: the database error was swallowed", marker)
		}
		if domain.IsValidation(err) {
			t.Fatalf("%q: a database failure must not look like a validation error: %v", marker, err)
		}
		if errors.Is(err, domain.ErrInvalidTransition) || errors.Is(err, domain.ErrConflict) ||
			errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrSeparationOfDuties) {
			t.Fatalf("%q: the scenario did not reach the injected failure: %v", marker, err)
		}
	}
}

// disbursedLoan walks the whole workflow so the repayment and material-change
// paths have a loan in the state they need.
func (f fixture) disbursedLoan(t *testing.T) loans.Loan {
	t.Helper()
	loan := f.create(t)
	f.appraise(t, loan.ID, 2000000)
	f.review(t, loan.ID)
	loan = f.approve(t, loan.ID)
	loan = f.mustIssueOffer(t, loan.ID)
	if err := f.run(t, f.chair, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		loan, err = f.service.AcceptOffer(ctx, tx, loan.ID, f.chair)
		return err
	}); err != nil {
		t.Fatalf("accept offer: %v", err)
	}
	err := f.run(t, f.disburser, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		loan, err = f.service.Disburse(ctx, tx, loans.DisburseInput{
			ApplicationID: loan.ID, ActorID: f.disburser, AmountUGX: 2000000,
			ExternalRef: "DISB-" + uuid.NewString(),
		})
		return err
	})
	if err != nil {
		t.Fatalf("disburse: %v", err)
	}
	return loan
}

func TestCreateApplicationSurfacesDatabaseErrors(t *testing.T) {
	f := setupFixture(t)
	f.assertSurfaced(t, f.applicant, []string{
		"FROM pamoja.loan_product",
		"FROM pamoja.authority_record",
		"SET status = 'group_authorised'",
	}, func(ctx context.Context, tx pgx.Tx) error {
		_, err := f.service.CreateApplication(ctx, tx, loans.CreateApplicationInput{
			GroupID: f.tenant.GroupID, ProductCode: "VSLA-STD", Kind: "group",
			RequestedAmountUGX: 2000000, TermMonths: 12, Purpose: "Stock for the shop",
			AppliedBy: f.applicant, IdempotencyKey: uuid.NewString(),
		})
		return err
	})
}

func TestCreateApplicationDefaultsLoanKind(t *testing.T) {
	f := setupFixture(t)
	var loan loans.Loan
	err := f.run(t, f.applicant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		loan, err = f.service.CreateApplication(ctx, tx, loans.CreateApplicationInput{
			GroupID: f.tenant.GroupID, ProductCode: "VSLA-STD",
			RequestedAmountUGX: 2000000, TermMonths: 12, Purpose: "Stock for the shop",
			AppliedBy: f.applicant, IdempotencyKey: uuid.NewString(),
		})
		return err
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if loan.Kind != "group" {
		t.Fatalf("an empty kind must default to group, got %q", loan.Kind)
	}
}

func TestAppraiseSurfacesDatabaseErrors(t *testing.T) {
	f := setupFixture(t)
	loan := f.create(t)
	f.assertSurfaced(t, f.appraiser, []string{
		"SELECT 1 FROM pamoja.loan_application",
		"INSERT INTO pamoja.loan_appraisal",
		"INSERT INTO pamoja.borrower_check",
		"INSERT INTO pamoja.loan_guarantee",
		"SET status = 'officer_appraised'",
	}, func(ctx context.Context, tx pgx.Tx) error {
		_, err := f.service.Appraise(ctx, tx, loans.AppraiseInput{
			ApplicationID: loan.ID, OfficerID: f.appraiser,
			CashFlowNotes: "Two seasons of verified sales", Recommendation: "Approve",
			Checks:     []loans.BorrowerCheckInput{{Kind: "prior_loans", Result: "clear"}},
			Guarantees: []loans.GuaranteeInput{{GuarantorMemberID: f.guarantor, AmountUGX: 2000000}},
		})
		return err
	})
}

func TestReviewAndDecisionSurfaceDatabaseErrors(t *testing.T) {
	f := setupFixture(t)
	loan := f.create(t)
	f.appraise(t, loan.ID, 2000000)

	f.assertSurfaced(t, f.appraiser, []string{"SET status = 'operations_review'"},
		func(ctx context.Context, tx pgx.Tx) error {
			_, err := f.service.SendToOperationsReview(ctx, tx, loan.ID, f.appraiser)
			return err
		})

	f.review(t, loan.ID)
	f.assertSurfaced(t, f.approver, []string{
		"VALUES ($1::uuid, 'operations_review', 'returned'",
		"SET status = 'returned'",
	}, func(ctx context.Context, tx pgx.Tx) error {
		_, err := f.service.Decide(ctx, tx, loans.DecisionInput{
			ApplicationID: loan.ID, ActorID: f.approver, Decision: "returned",
			Reason: "The cash-flow evidence is missing",
		})
		return err
	})

	f.assertSurfaced(t, f.approver, []string{
		"VALUES ($1::uuid, 'credit_decision', 'declined'",
		"SET status = 'declined', rejection_reason",
	}, func(ctx context.Context, tx pgx.Tx) error {
		_, err := f.service.Decide(ctx, tx, loans.DecisionInput{
			ApplicationID: loan.ID, ActorID: f.approver, Decision: "declined",
			Reason: "No repayment capacity",
		})
		return err
	})

	f.assertSurfaced(t, f.approver, []string{
		"SELECT count(DISTINCT decided_by)",
		"VALUES ($1::uuid, 'credit_decision', 'approved'",
		"SET status = 'approved', approved_by",
	}, func(ctx context.Context, tx pgx.Tx) error {
		_, err := f.service.Decide(ctx, tx, loans.DecisionInput{
			ApplicationID: loan.ID, ActorID: f.approver, Decision: "approved",
			ApprovedAmountUGX: 2000000,
		})
		return err
	})
}

// An approval without an explicit amount is priced at the requested amount.
func TestDecideDefaultsToRequestedAmount(t *testing.T) {
	f := setupFixture(t)
	loan := f.create(t)
	f.appraise(t, loan.ID, 2000000)
	f.review(t, loan.ID)
	var approved loans.Loan
	err := f.run(t, f.approver, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		approved, err = f.service.Decide(ctx, tx, loans.DecisionInput{
			ApplicationID: loan.ID, ActorID: f.approver, Decision: "approved",
		})
		return err
	})
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if approved.Status != "approved" || approved.ApprovedAmountUGX != 2000000 {
		t.Fatalf("approval did not use the requested amount: %+v", approved)
	}
}

func TestOfferAndAcceptSurfaceDatabaseErrors(t *testing.T) {
	f := setupFixture(t)
	loan := f.create(t)
	f.appraise(t, loan.ID, 2000000)
	f.review(t, loan.ID)
	f.approve(t, loan.ID)

	f.assertSurfaced(t, f.approver, []string{
		"INSERT INTO pamoja.loan_offer",
		"SET status = 'offer_issued'",
	}, func(ctx context.Context, tx pgx.Tx) error {
		_, err := f.service.IssueOffer(ctx, tx, loans.OfferInput{ApplicationID: loan.ID, IssuedBy: f.approver})
		return err
	})

	f.mustIssueOffer(t, loan.ID)
	f.assertSurfaced(t, f.chair, []string{
		"UPDATE pamoja.loan_offer",
		"SET status = 'offer_accepted'",
	}, func(ctx context.Context, tx pgx.Tx) error {
		_, err := f.service.AcceptOffer(ctx, tx, loan.ID, f.chair)
		return err
	})
}

func TestDisburseSurfacesDatabaseErrors(t *testing.T) {
	f := setupFixture(t)
	loan := f.create(t)
	f.appraise(t, loan.ID, 2000000)
	f.review(t, loan.ID)
	loan = f.approve(t, loan.ID)
	loan = f.mustIssueOffer(t, loan.ID)
	if err := f.run(t, f.chair, func(ctx context.Context, tx pgx.Tx) error {
		_, err := f.service.AcceptOffer(ctx, tx, loan.ID, f.chair)
		return err
	}); err != nil {
		t.Fatalf("accept offer: %v", err)
	}

	disburse := func(externalRef string) func(context.Context, pgx.Tx) error {
		return func(ctx context.Context, tx pgx.Tx) error {
			_, err := f.service.Disburse(ctx, tx, loans.DisburseInput{
				ApplicationID: loan.ID, ActorID: f.disburser, AmountUGX: 2000000,
				ExternalRef: externalRef,
			})
			return err
		}
	}

	// The external reference is required before any statement runs.
	err := db.WithActor(context.Background(), f.app,
		db.Actor{UserID: f.disburser, Scopes: []string{f.tenant.GroupID}}, uuid.NewString(),
		disburse(""))
	if !domain.IsValidation(err) {
		t.Fatalf("a disbursement without a reference must be refused, got %v", err)
	}

	f.assertSurfaced(t, f.disburser, []string{
		"INSERT INTO pamoja.loan_disbursement",
		"SELECT count(*) FROM pamoja.loan_installment",
		"SET status = 'disbursed'",
	}, disburse("DISB-"+uuid.NewString()))
}

func TestMaterialChangeSurfacesDatabaseErrors(t *testing.T) {
	f := setupFixture(t)
	loan := f.create(t)
	f.appraise(t, loan.ID, 2000000)
	f.review(t, loan.ID)
	f.approve(t, loan.ID)
	f.assertSurfaced(t, f.approver, []string{
		"INSERT INTO pamoja.loan_material_change",
		"material_change_count = material_change_count + 1",
	}, func(ctx context.Context, tx pgx.Tx) error {
		_, err := f.service.RecordMaterialChange(ctx, tx, loans.MaterialChangeInput{
			ApplicationID: loan.ID, ActorID: f.approver, Field: "amount_ugx",
			OldValue: "2000000", NewValue: "3000000", Reason: "The price of stock rose",
		})
		return err
	})
}

func TestRepaymentSurfacesDatabaseErrors(t *testing.T) {
	f := setupFixture(t)
	loan := f.disbursedLoan(t)
	var installmentID string
	if err := f.owner.QueryRow(context.Background(),
		`SELECT id::text FROM pamoja.loan_installment WHERE application_id = $1::uuid
		 ORDER BY due_on LIMIT 1`, loan.ID).Scan(&installmentID); err != nil {
		t.Fatalf("installment: %v", err)
	}

	repay := func(amount int64, externalRef string) func(context.Context, pgx.Tx) error {
		return func(ctx context.Context, tx pgx.Tx) error {
			return f.service.RecordRepayment(ctx, tx, loans.RepaymentInput{
				ApplicationID: loan.ID, InstallmentID: installmentID, ActorID: f.disburser,
				AmountUGX: amount, ExternalRef: externalRef,
			})
		}
	}

	err := db.WithActor(context.Background(), f.app,
		db.Actor{UserID: f.disburser, Scopes: []string{f.tenant.GroupID}}, uuid.NewString(),
		repay(100000, ""))
	if !domain.IsValidation(err) {
		t.Fatalf("a repayment without a reference must be refused, got %v", err)
	}

	// A repayment without a date uses the server's date.
	err = db.WithActor(context.Background(), f.app,
		db.Actor{UserID: f.disburser, Scopes: []string{f.tenant.GroupID}}, uuid.NewString(),
		repay(100000, "REP-"+uuid.NewString()))
	if err != nil {
		t.Fatalf("repayment without a date: %v", err)
	}
}
