package loans_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yopsa/pamoja/backend/internal/domain"
	"github.com/yopsa/pamoja/backend/internal/loans"
	"github.com/yopsa/pamoja/backend/internal/platform/db"
	"github.com/yopsa/pamoja/backend/internal/testsupport"
)

type fixture struct {
	owner     *pgxpool.Pool
	app       *pgxpool.Pool
	tenant    testsupport.Tenant
	chair     string
	applicant string
	appraiser string
	approver  string
	disburser string
	guarantor string
	authority string
	service   *loans.Service
}

func setupFixture(t *testing.T) fixture {
	t.Helper()
	owner, app := testsupport.Pools(t)
	tenant := testsupport.CreateTenant(t, owner, "Loans group")
	f := fixture{
		owner: owner, app: app, tenant: tenant, service: loans.New(),
		applicant: testsupport.CreateUser(t, owner, "Applicant"),
		appraiser: testsupport.CreateUser(t, owner, "Appraiser"),
		approver:  testsupport.CreateUser(t, owner, "Approver"),
		disburser: testsupport.CreateUser(t, owner, "Disburser"),
	}
	f.guarantor = testsupport.CreateMember(t, owner, tenant, "Guarantor", "")
	chairMember := testsupport.CreateMember(t, owner, tenant, "Chairperson", "")
	if err := owner.QueryRow(context.Background(),
		`INSERT INTO pamoja.authority_record
		   (group_id, legal_entity_id, branch_id, member_id, office, effective_from, recorded_by)
		 VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, 'chairperson', current_date - 30, $5::uuid)
		 RETURNING id::text`,
		tenant.GroupID, tenant.LegalEntityID, tenant.BranchID, chairMember, f.applicant).
		Scan(&f.authority); err != nil {
		t.Fatalf("authority record: %v", err)
	}
	f.chair = f.applicant
	return f
}

func (f fixture) run(t *testing.T, actor string, fn func(context.Context, pgx.Tx) error) error {
	t.Helper()
	return db.WithActor(context.Background(), f.app,
		db.Actor{UserID: actor, Scopes: []string{f.tenant.GroupID}}, uuid.NewString(), fn)
}

func (f fixture) create(t *testing.T, amount int64) loans.Loan {
	t.Helper()
	var out loans.Loan
	err := f.run(t, f.applicant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = f.service.CreateApplication(ctx, tx, loans.CreateApplicationInput{
			GroupID: f.tenant.GroupID, ProductCode: "VSLA-STD", Kind: "group",
			RequestedAmountUGX: amount, TermMonths: 12, Purpose: "Stock for the group shop",
			RepaymentSource: "Weekly market sales", AppliedBy: f.applicant,
			IdempotencyKey: uuid.NewString(),
		})
		return err
	})
	if err != nil {
		t.Fatalf("create application: %v", err)
	}
	return out
}

func (f fixture) appraise(t *testing.T, id string, guarantee int64) loans.Loan {
	t.Helper()
	var out loans.Loan
	err := f.run(t, f.appraiser, func(ctx context.Context, tx pgx.Tx) error {
		in := loans.AppraiseInput{
			ApplicationID: id, OfficerID: f.appraiser,
			CashFlowNotes:  "Two seasons of verified maize sales",
			Recommendation: "Recommend approval subject to the guarantee",
			Checks:         []loans.BorrowerCheckInput{{Kind: "prior_loans", Result: "clear"}},
		}
		if guarantee > 0 {
			in.Guarantees = []loans.GuaranteeInput{{GuarantorMemberID: f.guarantor, AmountUGX: guarantee}}
		}
		var err error
		out, err = f.service.Appraise(ctx, tx, in)
		return err
	})
	if err != nil {
		t.Fatalf("appraise: %v", err)
	}
	return out
}

func (f fixture) review(t *testing.T, id string) loans.Loan {
	t.Helper()
	var out loans.Loan
	err := f.run(t, f.appraiser, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = f.service.SendToOperationsReview(ctx, tx, id, f.appraiser)
		return err
	})
	if err != nil {
		t.Fatalf("send to review: %v", err)
	}
	return out
}

func (f fixture) approve(t *testing.T, id string) loans.Loan {
	t.Helper()
	var out loans.Loan
	err := f.run(t, f.approver, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = f.service.Decide(ctx, tx, loans.DecisionInput{
			ApplicationID: id, ActorID: f.approver, Decision: "approved",
			ApprovedAmountUGX: 2000000, Conditions: "Monthly supervision visit",
		})
		return err
	})
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	return out
}

// TestFullBorrowingJourney walks application -> appraisal -> review -> approval
// -> offer -> acceptance -> disbursement and checks the schedule.
func TestFullBorrowingJourney(t *testing.T) {
	f := setupFixture(t)
	loan := f.create(t, 2000000)
	if loan.Status != "group_authorised" || loan.ApprovedAmountUGX != 0 {
		t.Fatalf("unexpected create: %+v", loan)
	}

	loan = f.appraise(t, loan.ID, 2000000)
	if loan.Status != "officer_appraised" || loan.AppraiserID != f.appraiser {
		t.Fatalf("unexpected appraisal: %+v", loan)
	}

	loan = f.review(t, loan.ID)
	if loan.Status != "operations_review" {
		t.Fatalf("unexpected review: %+v", loan)
	}

	// Approval is not disbursement: nothing is disbursed yet.
	loan = f.approve(t, loan.ID)
	if loan.Status != "approved" || loan.ApprovedBy != f.approver {
		t.Fatalf("unexpected approval: %+v", loan)
	}
	if loan.ApprovedAmountUGX != 2000000 {
		t.Fatalf("approved amount not recorded: %+v", loan)
	}

	err := f.run(t, f.disburser, func(ctx context.Context, tx pgx.Tx) error {
		_, err := f.service.Disburse(ctx, tx, loans.DisburseInput{
			ApplicationID: loan.ID, ActorID: f.disburser, AmountUGX: 2000000,
			ExternalRef: "DISB-EARLY-" + uuid.NewString(),
		})
		return err
	})
	if !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("disbursement before an accepted offer must fail, got %v", err)
	}

	loan = f.mustIssueOffer(t, loan.ID)
	if loan.Status != "offer_issued" {
		t.Fatalf("unexpected offer: %+v", loan)
	}

	err = f.run(t, f.chair, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		loan, err = f.service.AcceptOffer(ctx, tx, loan.ID, f.chair)
		return err
	})
	if err != nil || loan.Status != "offer_accepted" {
		t.Fatalf("accept offer: %v (%+v)", err, loan)
	}

	externalRef := "DISB-" + uuid.NewString()
	err = f.run(t, f.disburser, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		loan, err = f.service.Disburse(ctx, tx, loans.DisburseInput{
			ApplicationID: loan.ID, ActorID: f.disburser, AmountUGX: 2000000,
			ExternalRef: externalRef,
		})
		return err
	})
	if err != nil {
		t.Fatalf("disburse: %v", err)
	}
	if loan.Status != "disbursed" {
		t.Fatalf("loan not disbursed: %+v", loan)
	}

	var count int
	var total int64
	if err := f.owner.QueryRow(context.Background(),
		`SELECT count(*), COALESCE(sum(amount_ugx), 0) FROM pamoja.loan_installment
		 WHERE application_id = $1::uuid`, loan.ID).Scan(&count, &total); err != nil {
		t.Fatalf("installments: %v", err)
	}
	if count != 12 || total != 2000000 {
		t.Fatalf("schedule is %d installments totalling %d, want 12 / 2000000", count, total)
	}

	// A repayment applies to its installment.
	var installment string
	var due int64
	if err := f.owner.QueryRow(context.Background(),
		`SELECT id::text, amount_ugx FROM pamoja.loan_installment
		 WHERE application_id = $1::uuid ORDER BY due_on LIMIT 1`, loan.ID).Scan(&installment, &due); err != nil {
		t.Fatalf("first installment: %v", err)
	}
	err = f.run(t, f.disburser, func(ctx context.Context, tx pgx.Tx) error {
		return f.service.RecordRepayment(ctx, tx, loans.RepaymentInput{
			ApplicationID: loan.ID, InstallmentID: installment, ActorID: f.disburser,
			AmountUGX: due, PaidOn: time.Now().UTC(), ExternalRef: "REP-" + uuid.NewString(),
		})
	})
	if err != nil {
		t.Fatalf("repayment: %v", err)
	}
	var paid, amount int64
	var status string
	if err := f.owner.QueryRow(context.Background(),
		`SELECT paid_ugx, amount_ugx, status FROM pamoja.loan_installment WHERE id = $1::uuid`,
		installment).Scan(&paid, &amount, &status); err != nil {
		t.Fatalf("read installment: %v", err)
	}
	if paid != amount || status != "paid" {
		t.Fatalf("repayment not applied: paid %d of %d, status %s", paid, amount, status)
	}
}

func (f fixture) mustIssueOffer(t *testing.T, id string) loans.Loan {
	t.Helper()
	var out loans.Loan
	err := f.run(t, f.approver, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = f.service.IssueOffer(ctx, tx, loans.OfferInput{
			ApplicationID: id, IssuedBy: f.approver,
		})
		return err
	})
	if err != nil {
		t.Fatalf("issue offer: %v", err)
	}
	return out
}

// T-LOAN-03: the applicant may not approve their own application (SD-03).
func TestApplicantCannotApprove(t *testing.T) {
	f := setupFixture(t)
	loan := f.create(t, 2000000)
	f.appraise(t, loan.ID, 2000000)
	f.review(t, loan.ID)

	err := f.run(t, f.applicant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := f.service.Decide(ctx, tx, loans.DecisionInput{
			ApplicationID: loan.ID, ActorID: f.applicant, Decision: "approved", ApprovedAmountUGX: 2000000,
		})
		return err
	})
	if !errors.Is(err, domain.ErrSeparationOfDuties) {
		t.Fatalf("self-approval must be refused, got %v", err)
	}
}

// T-LOAN-04: the approver may not confirm the disbursement (SD-04).
func TestApproverCannotDisburse(t *testing.T) {
	f := setupFixture(t)
	loan := f.create(t, 2000000)
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

	err := f.run(t, f.approver, func(ctx context.Context, tx pgx.Tx) error {
		_, err := f.service.Disburse(ctx, tx, loans.DisburseInput{
			ApplicationID: loan.ID, ActorID: f.approver, AmountUGX: 2000000,
			ExternalRef: "DISB-SD04-" + uuid.NewString(),
		})
		return err
	})
	if !errors.Is(err, domain.ErrSeparationOfDuties) {
		t.Fatalf("the approver must not disburse, got %v", err)
	}
}

// A-010: above the guarantor threshold, approval needs a checked guarantee.
func TestApprovalNeedsCheckedGuarantee(t *testing.T) {
	f := setupFixture(t)
	loan := f.create(t, 2000000)
	f.appraise(t, loan.ID, 0)
	f.review(t, loan.ID)

	err := f.run(t, f.approver, func(ctx context.Context, tx pgx.Tx) error {
		_, err := f.service.Decide(ctx, tx, loans.DecisionInput{
			ApplicationID: loan.ID, ActorID: f.approver, Decision: "approved", ApprovedAmountUGX: 2000000,
		})
		return err
	})
	if err == nil {
		t.Fatal("approval without a checked guarantee must fail")
	}
}

// A-034: a material change after approval sends the application back to review.
func TestMaterialChangeForcesReapproval(t *testing.T) {
	f := setupFixture(t)
	loan := f.create(t, 2000000)
	f.appraise(t, loan.ID, 2000000)
	f.review(t, loan.ID)
	loan = f.approve(t, loan.ID)

	err := f.run(t, f.approver, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		loan, err = f.service.RecordMaterialChange(ctx, tx, loans.MaterialChangeInput{
			ApplicationID: loan.ID, ActorID: f.approver, Field: "amount_ugx",
			OldValue: "2000000", NewValue: "3000000", Reason: "Group added a verified buyer contract",
		})
		return err
	})
	if err != nil {
		t.Fatalf("material change: %v", err)
	}
	if loan.Status != "operations_review" || loan.MaterialChanges != 1 || loan.ApprovedBy != "" {
		t.Fatalf("material change did not force re-approval: %+v", loan)
	}
}

// Product limits are enforced against the approved product version.
func TestApplicationOutsideProductRejected(t *testing.T) {
	f := setupFixture(t)
	err := f.run(t, f.applicant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := f.service.CreateApplication(ctx, tx, loans.CreateApplicationInput{
			GroupID: f.tenant.GroupID, ProductCode: "VSLA-STD", Kind: "group",
			RequestedAmountUGX: 500000, TermMonths: 12, Purpose: "Below the product minimum",
			AppliedBy: f.applicant, IdempotencyKey: uuid.NewString(),
		})
		return err
	})
	if err == nil || !domain.IsValidation(err) {
		t.Fatalf("an amount below the product minimum must be rejected, got %v", err)
	}
}

// A group without a current chairperson authority record cannot borrow.
func TestApplicationNeedsGroupAuthority(t *testing.T) {
	f := setupFixture(t)
	other := testsupport.CreateTenant(t, f.owner, "No authority group")
	err := f.run(t, f.applicant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := f.service.CreateApplication(ctx, tx, loans.CreateApplicationInput{
			GroupID: other.GroupID, ProductCode: "VSLA-STD", Kind: "group",
			RequestedAmountUGX: 2000000, TermMonths: 12, Purpose: "No leadership on record",
			AppliedBy: f.applicant, IdempotencyKey: uuid.NewString(),
		})
		return err
	})
	if err == nil || !domain.IsValidation(err) {
		t.Fatalf("a group with no authority record must be refused, got %v", err)
	}
}
