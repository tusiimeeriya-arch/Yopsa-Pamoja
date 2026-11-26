package loans_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yopsa/pamoja/backend/internal/domain"
	"github.com/yopsa/pamoja/backend/internal/loans"
	"github.com/yopsa/pamoja/backend/internal/platform/db"
	"github.com/yopsa/pamoja/backend/internal/testsupport"
)

// Validation happens before any statement, so these cases need no database.
func TestCreateApplicationRejectsInvalidInput(t *testing.T) {
	svc := loans.New()
	base := loans.CreateApplicationInput{
		GroupID: uuid.NewString(), ProductCode: "VSLA-STD", Kind: "group",
		RequestedAmountUGX: 2000000, TermMonths: 12, Purpose: "Stock for the shop",
		AppliedBy: "user-1", IdempotencyKey: "key-1",
	}
	cases := map[string]func(*loans.CreateApplicationInput){
		"group":       func(in *loans.CreateApplicationInput) { in.GroupID = "" },
		"product":     func(in *loans.CreateApplicationInput) { in.ProductCode = "" },
		"purpose":     func(in *loans.CreateApplicationInput) { in.Purpose = "" },
		"applied_by":  func(in *loans.CreateApplicationInput) { in.AppliedBy = "" },
		"idempotency": func(in *loans.CreateApplicationInput) { in.IdempotencyKey = "" },
		"amount":      func(in *loans.CreateApplicationInput) { in.RequestedAmountUGX = 0 },
		"kind":        func(in *loans.CreateApplicationInput) { in.Kind = "personal" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			in := base
			mutate(&in)
			if _, err := svc.CreateApplication(context.Background(), nil, in); !domain.IsValidation(err) {
				t.Fatalf("%s: expected a validation error, got %v", name, err)
			}
		})
	}
}

func TestCreateApplicationProductAndAuthorityGuards(t *testing.T) {
	f := setupFixture(t)
	apply := func(productCode string, amount int64, authority string) error {
		return f.run(t, f.applicant, func(ctx context.Context, tx pgx.Tx) error {
			_, err := f.service.CreateApplication(ctx, tx, loans.CreateApplicationInput{
				GroupID: f.tenant.GroupID, ProductCode: productCode, Kind: "group",
				RequestedAmountUGX: amount, TermMonths: 12, Purpose: "Stock for the shop",
				AppliedBy: f.applicant, AuthorityRecordID: authority, IdempotencyKey: uuid.NewString(),
			})
			return err
		})
	}

	if err := apply("NO-SUCH-PRODUCT", 2000000, ""); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("an unknown product must be not found, got %v", err)
	}
	if err := apply("VSLA-STD", 100, ""); !domain.IsValidation(err) {
		t.Fatalf("an amount outside the product must be refused, got %v", err)
	}
	if err := apply("VSLA-STD", 2000000, uuid.NewString()); !domain.IsValidation(err) {
		t.Fatalf("an authority record from another group must be refused, got %v", err)
	}
}

func TestCreateApplicationWithoutLeadershipIsRefused(t *testing.T) {
	f := setupFixture(t)
	bare := testsupport.CreateTenant(t, f.owner, "Group with no leadership")
	err := db.WithActor(context.Background(), f.app,
		db.Actor{UserID: f.applicant, Scopes: []string{bare.GroupID}}, uuid.NewString(),
		func(ctx context.Context, tx pgx.Tx) error {
			_, err := f.service.CreateApplication(ctx, tx, loans.CreateApplicationInput{
				GroupID: bare.GroupID, ProductCode: "VSLA-STD", Kind: "group",
				RequestedAmountUGX: 2000000, TermMonths: 12, Purpose: "Stock for the shop",
				AppliedBy: f.applicant, IdempotencyKey: uuid.NewString(),
			})
			return err
		})
	if !domain.IsValidation(err) {
		t.Fatalf("a group with no chairperson record must not borrow, got %v", err)
	}
}

func TestAppraiseGuards(t *testing.T) {
	f := setupFixture(t)
	svc := loans.New()
	for name, in := range map[string]loans.AppraiseInput{
		"officer":        {CashFlowNotes: "notes", Recommendation: "approve"},
		"cash_flow":      {OfficerID: f.appraiser, Recommendation: "approve"},
		"recommendation": {OfficerID: f.appraiser, CashFlowNotes: "notes"},
	} {
		if _, err := svc.Appraise(context.Background(), nil, in); !domain.IsValidation(err) {
			t.Fatalf("%s: expected a validation error, got %v", name, err)
		}
	}

	loan := f.create(t)
	f.appraise(t, loan.ID, 0)
	err := f.run(t, f.appraiser, func(ctx context.Context, tx pgx.Tx) error {
		_, err := svc.Appraise(ctx, tx, loans.AppraiseInput{
			ApplicationID: loan.ID, OfficerID: f.appraiser,
			CashFlowNotes: "Second pass", Recommendation: "Approve",
		})
		return err
	})
	if !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("an appraised application cannot be appraised again, got %v", err)
	}

	other := f.create(t)
	err = f.run(t, f.applicant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := svc.Appraise(ctx, tx, loans.AppraiseInput{
			ApplicationID: other.ID, OfficerID: f.applicant,
			CashFlowNotes: "Own books", Recommendation: "Approve",
		})
		return err
	})
	if !errors.Is(err, domain.ErrSeparationOfDuties) {
		t.Fatalf("the applicant must not appraise their own application, got %v", err)
	}

	err = f.run(t, f.appraiser, func(ctx context.Context, tx pgx.Tx) error {
		_, err := svc.Appraise(ctx, tx, loans.AppraiseInput{
			ApplicationID: other.ID, OfficerID: f.appraiser,
			CashFlowNotes: "Checked", Recommendation: "Approve",
			Checks: []loans.BorrowerCheckInput{{Kind: "", Result: "clear"}},
		})
		return err
	})
	if !domain.IsValidation(err) {
		t.Fatalf("a check without a kind must be refused, got %v", err)
	}

	err = f.run(t, f.appraiser, func(ctx context.Context, tx pgx.Tx) error {
		_, err := svc.Appraise(ctx, tx, loans.AppraiseInput{
			ApplicationID: other.ID, OfficerID: f.appraiser,
			CashFlowNotes: "Checked", Recommendation: "Approve",
			Guarantees: []loans.GuaranteeInput{{GuarantorMemberID: f.guarantor, AmountUGX: 0}},
		})
		return err
	})
	if !domain.IsValidation(err) {
		t.Fatalf("a guarantee without an amount must be refused, got %v", err)
	}
}

func TestSendToReviewGuards(t *testing.T) {
	f := setupFixture(t)
	loan := f.create(t)

	err := f.run(t, f.appraiser, func(ctx context.Context, tx pgx.Tx) error {
		_, err := f.service.SendToOperationsReview(ctx, tx, loan.ID, f.appraiser)
		return err
	})
	if !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("an unappraised application cannot go to review, got %v", err)
	}

	f.appraise(t, loan.ID, 0)
	err = f.run(t, f.applicant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := f.service.SendToOperationsReview(ctx, tx, loan.ID, f.applicant)
		return err
	})
	if !errors.Is(err, domain.ErrSeparationOfDuties) {
		t.Fatalf("the applicant must not review their own application, got %v", err)
	}
}

func TestDecideGuards(t *testing.T) {
	f := setupFixture(t)
	loan := f.create(t)
	f.appraise(t, loan.ID, 0)
	f.review(t, loan.ID)

	decide := func(actor string, in loans.DecisionInput) error {
		in.ApplicationID = loan.ID
		in.ActorID = actor
		return f.run(t, actor, func(ctx context.Context, tx pgx.Tx) error {
			_, err := f.service.Decide(ctx, tx, in)
			return err
		})
	}

	if err := decide(f.approver, loans.DecisionInput{Decision: "maybe"}); !domain.IsValidation(err) {
		t.Fatalf("an unknown decision must be refused, got %v", err)
	}
	if err := decide(f.approver, loans.DecisionInput{Decision: "declined"}); !domain.IsValidation(err) {
		t.Fatalf("a decline without a reason must be refused, got %v", err)
	}
	if err := decide(f.applicant, loans.DecisionInput{Decision: "approved", ApprovedAmountUGX: 2000000}); !errors.Is(err, domain.ErrSeparationOfDuties) {
		t.Fatalf("the applicant must not approve their own application, got %v", err)
	}
	if err := decide(f.approver, loans.DecisionInput{Decision: "approved", ApprovedAmountUGX: 3000000}); !domain.IsValidation(err) {
		t.Fatalf("an approval above the requested amount must be refused, got %v", err)
	}

	var returned loans.Loan
	err := f.run(t, f.approver, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		returned, err = f.service.Decide(ctx, tx, loans.DecisionInput{
			ApplicationID: loan.ID, ActorID: f.approver, Decision: "returned",
			Reason: "The cash-flow evidence is missing",
		})
		return err
	})
	if err != nil {
		t.Fatalf("return: %v", err)
	}
	if returned.Status != "returned" {
		t.Fatalf("application not returned: %+v", returned)
	}
	if err := decide(f.approver, loans.DecisionInput{Decision: "approved", ApprovedAmountUGX: 2000000}); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("a returned application cannot be approved, got %v", err)
	}

	declinedLoan := f.create(t)
	f.appraise(t, declinedLoan.ID, 0)
	f.review(t, declinedLoan.ID)
	var declined loans.Loan
	err = f.run(t, f.approver, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		declined, err = f.service.Decide(ctx, tx, loans.DecisionInput{
			ApplicationID: declinedLoan.ID, ActorID: f.approver, Decision: "declined",
			Reason: "No repayment capacity",
		})
		return err
	})
	if err != nil {
		t.Fatalf("decline: %v", err)
	}
	if declined.Status != "declined" {
		t.Fatalf("application not declined: %+v", declined)
	}
}

func TestOfferGuards(t *testing.T) {
	f := setupFixture(t)
	loan := f.create(t)

	if _, err := f.service.IssueOffer(context.Background(), nil, loans.OfferInput{ApplicationID: loan.ID}); !domain.IsValidation(err) {
		t.Fatalf("an offer without an issuer must be refused, got %v", err)
	}
	err := f.run(t, f.approver, func(ctx context.Context, tx pgx.Tx) error {
		_, err := f.service.IssueOffer(ctx, tx, loans.OfferInput{ApplicationID: loan.ID, IssuedBy: f.approver})
		return err
	})
	if !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("an unapproved application cannot get an offer, got %v", err)
	}

	err = f.run(t, f.chair, func(ctx context.Context, tx pgx.Tx) error {
		_, err := f.service.AcceptOffer(ctx, tx, loan.ID, f.chair)
		return err
	})
	if !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("an application without an offer cannot accept one, got %v", err)
	}
}

func TestAcceptedOfferIsAcceptedOnlyOnce(t *testing.T) {
	f := setupFixture(t)
	loan := f.create(t)
	f.appraise(t, loan.ID, 2000000)
	f.review(t, loan.ID)
	loan = f.approve(t, loan.ID)
	loan = f.mustIssueOffer(t, loan.ID)

	accept := func() error {
		return f.run(t, f.chair, func(ctx context.Context, tx pgx.Tx) error {
			_, err := f.service.AcceptOffer(ctx, tx, loan.ID, f.chair)
			return err
		})
	}
	if err := accept(); err != nil {
		t.Fatalf("accept offer: %v", err)
	}
	if err := accept(); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("accepting the same offer twice must be refused, got %v", err)
	}
}

func TestDisburseGuards(t *testing.T) {
	f := setupFixture(t)
	loan := f.create(t)
	base := loans.DisburseInput{
		ApplicationID: loan.ID, ActorID: f.disburser, AmountUGX: 2000000, ExternalRef: "DISB-1",
	}

	if _, err := f.service.Disburse(context.Background(), nil, loans.DisburseInput{ApplicationID: loan.ID}); !domain.IsValidation(err) {
		t.Fatalf("a disbursement without an amount must be refused, got %v", err)
	}
	err := f.run(t, f.disburser, func(ctx context.Context, tx pgx.Tx) error {
		_, err := f.service.Disburse(ctx, tx, base)
		return err
	})
	if !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("an unaccepted offer cannot be disbursed, got %v", err)
	}

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

	future := base
	future.DisbursedOn = time.Now().UTC().AddDate(0, 0, 3)
	err = f.run(t, f.disburser, func(ctx context.Context, tx pgx.Tx) error {
		_, err := f.service.Disburse(ctx, tx, future)
		return err
	})
	if !domain.IsValidation(err) {
		t.Fatalf("a future disbursement date must be refused, got %v", err)
	}

	unknownOffer := base
	unknownOffer.OfferID = uuid.NewString()
	err = f.run(t, f.disburser, func(ctx context.Context, tx pgx.Tx) error {
		_, err := f.service.Disburse(ctx, tx, unknownOffer)
		return err
	})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("an unknown offer must be not found, got %v", err)
	}
}

func TestMaterialChangeGuards(t *testing.T) {
	f := setupFixture(t)
	loan := f.create(t)
	base := loans.MaterialChangeInput{
		ApplicationID: loan.ID, ActorID: f.approver, Field: "amount_ugx",
		OldValue: "2000000", NewValue: "3000000", Reason: "The price of stock rose",
	}

	if _, err := f.service.RecordMaterialChange(context.Background(), nil, loans.MaterialChangeInput{ApplicationID: loan.ID, Field: "amount_ugx"}); !domain.IsValidation(err) {
		t.Fatalf("a change without a reason must be refused, got %v", err)
	}
	if _, err := f.service.RecordMaterialChange(context.Background(), nil, loans.MaterialChangeInput{ApplicationID: loan.ID, Field: "colour", Reason: "why"}); !domain.IsValidation(err) {
		t.Fatalf("a non-material field must be refused, got %v", err)
	}
	err := f.run(t, f.approver, func(ctx context.Context, tx pgx.Tx) error {
		_, err := f.service.RecordMaterialChange(ctx, tx, base)
		return err
	})
	if !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("a material change applies after approval, got %v", err)
	}

	f.appraise(t, loan.ID, 2000000)
	f.review(t, loan.ID)
	f.approve(t, loan.ID)
	var changed loans.Loan
	err = f.run(t, f.approver, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		changed, err = f.service.RecordMaterialChange(ctx, tx, base)
		return err
	})
	if err != nil {
		t.Fatalf("material change: %v", err)
	}
	if changed.Status != "operations_review" || changed.MaterialChanges != 1 {
		t.Fatalf("a material change must force re-approval: %+v", changed)
	}
}

func TestRepaymentGuards(t *testing.T) {
	f := setupFixture(t)
	loan := f.create(t)

	if err := f.service.RecordRepayment(context.Background(), nil, loans.RepaymentInput{ApplicationID: loan.ID}); !domain.IsValidation(err) {
		t.Fatalf("a repayment without an amount must be refused, got %v", err)
	}
	err := f.run(t, f.disburser, func(ctx context.Context, tx pgx.Tx) error {
		return f.service.RecordRepayment(ctx, tx, loans.RepaymentInput{
			ApplicationID: loan.ID, ActorID: f.disburser, AmountUGX: 100000, ExternalRef: "REP-1",
		})
	})
	if !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("a repayment needs a disbursed loan, got %v", err)
	}
}

func TestGetAndListByGroup(t *testing.T) {
	f := setupFixture(t)
	loan := f.create(t)

	var got loans.Loan
	err := f.run(t, f.applicant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		got, err = f.service.Get(ctx, tx, loan.ID)
		return err
	})
	if err != nil || got.ID != loan.ID {
		t.Fatalf("get: %v %+v", err, got)
	}

	err = f.run(t, f.applicant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := f.service.Get(ctx, tx, uuid.NewString())
		return err
	})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("an unknown application must be not found, got %v", err)
	}

	for _, limit := range []int{0, 500} {
		var list []loans.Loan
		err := f.run(t, f.applicant, func(ctx context.Context, tx pgx.Tx) error {
			var err error
			list, err = f.service.ListByGroup(ctx, tx, f.tenant.GroupID, limit)
			return err
		})
		if err != nil {
			t.Fatalf("list with limit %d: %v", limit, err)
		}
		if len(list) == 0 {
			t.Fatalf("list with limit %d returned nothing", limit)
		}
	}
}
