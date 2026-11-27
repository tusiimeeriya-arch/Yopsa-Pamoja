package loans

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/yopsa/pamoja/backend/internal/domain"
)

// Service applies the borrowing workflow inside one database transaction. The
// database guards remain the last line of defence: every transition here is
// also enforced by pamoja.loan_transition_guard and the disbursement guard.
type Service struct {
	Now func() time.Time
}

// New returns a loans service.
func New() *Service { return &Service{Now: time.Now} }

// Loan is an application as the API and reports see it.
type Loan struct {
	ID                 string
	GroupID            string
	ProductCode        string
	ProductVersion     int
	Kind               string
	RequestedAmountUGX int64
	ApprovedAmountUGX  int64
	TermMonths         int
	Purpose            string
	Status             string
	AppliedBy          string
	AppraiserID        string
	ApprovedBy         string
	MaterialChanges    int
	CreatedAt          time.Time
}

// CreateApplicationInput is a group's borrowing request.
type CreateApplicationInput struct {
	GroupID            string
	ProductCode        string
	ProductVersion     int // 0 selects the latest active version
	Kind               string
	RequestedAmountUGX int64
	TermMonths         int
	Purpose            string
	RepaymentSource    string
	AuthorityRecordID  string // optional: resolved to the current chairperson record
	AppliedBy          string
	IdempotencyKey     string
}

// AppraiseInput records the field officer's appraisal and evidence.
type AppraiseInput struct {
	ApplicationID          string
	OfficerID              string
	CashFlowNotes          string
	ExistingObligationsUGX int64
	Recommendation         string
	Checks                 []BorrowerCheckInput
	Guarantees             []GuaranteeInput
}

// BorrowerCheckInput is one external check (e.g. prior-loan history).
type BorrowerCheckInput struct {
	Kind   string
	Result string
}

// GuaranteeInput is a guarantor's checked pledge.
type GuaranteeInput struct {
	GuarantorMemberID string
	AmountUGX         int64
}

// DecisionInput records an operations review or credit decision.
type DecisionInput struct {
	ApplicationID     string
	ActorID           string
	Decision          string // approved | declined | returned
	ApprovedAmountUGX int64
	Conditions        string
	Reason            string
}

// OfferInput issues the terms approved for the application.
type OfferInput struct {
	ApplicationID string
	IssuedBy      string
	AmountUGX     int64 // 0 uses the approved amount
	TermMonths    int   // 0 uses the application term
	ValidDays     int   // 0 defaults to 14
}

// DisburseInput confirms money actually leaving the group's account.
type DisburseInput struct {
	ApplicationID string
	OfferID       string
	ActorID       string
	AmountUGX     int64
	DisbursedOn   time.Time
	ExternalRef   string
}

// MaterialChangeInput records a change that forces re-approval (A-034).
type MaterialChangeInput struct {
	ApplicationID string
	ActorID       string
	Field         string
	OldValue      string
	NewValue      string
	Reason        string
}

// RepaymentInput records a loan repayment against one installment.
type RepaymentInput struct {
	ApplicationID string
	InstallmentID string
	ActorID       string
	AmountUGX     int64
	PaidOn        time.Time
	ExternalRef   string
}

// CreateApplication validates the request against the approved product version
// and the group's current authority, then records the group-authorised
// application.
func (s *Service) CreateApplication(ctx context.Context, tx pgx.Tx, in CreateApplicationInput) (Loan, error) {
	if err := domain.Required("group_id", in.GroupID); err != nil {
		return Loan{}, err
	}
	if err := domain.Required("product_code", in.ProductCode); err != nil {
		return Loan{}, err
	}
	if err := domain.Required("purpose", in.Purpose); err != nil {
		return Loan{}, err
	}
	if err := domain.Required("applied_by", in.AppliedBy); err != nil {
		return Loan{}, err
	}
	if err := domain.Required("idempotency_key", in.IdempotencyKey); err != nil {
		return Loan{}, err
	}
	if err := domain.PositiveMoney(in.RequestedAmountUGX); err != nil {
		return Loan{}, err
	}
	if in.Kind == "" {
		in.Kind = "group"
	}
	if in.Kind != "group" && in.Kind != "internal_member" {
		return Loan{}, domain.Invalid("loan_kind", "must be group or internal_member")
	}

	product, productID, err := s.loadProduct(ctx, tx, in.ProductCode, in.ProductVersion)
	if err != nil {
		return Loan{}, err
	}
	if err := ValidateProduct(product, Application{
		Kind: in.Kind, AmountUGX: in.RequestedAmountUGX, TermMonths: in.TermMonths,
	}); err != nil {
		return Loan{}, domain.Invalid("requested_amount_ugx", err.Error())
	}

	authorityID, err := s.resolveAuthority(ctx, tx, in.GroupID, in.AuthorityRecordID)
	if err != nil {
		return Loan{}, err
	}

	var id string
	err = tx.QueryRow(ctx,
		`INSERT INTO pamoja.loan_application
		   (legal_entity_id, branch_id, group_id, product_id, product_version, loan_kind,
		    requested_amount_ugx, purpose, term_months, repayment_source,
		    authority_record_id, applied_by, status)
		 SELECT g.legal_entity_id, g.branch_id, g.id, $2::uuid, $3, $4, $5, $6, $7, $8,
		        $9::uuid, $10::uuid, 'draft'
		 FROM pamoja.groups g
		 WHERE g.id = $1::uuid
		 RETURNING id::text`,
		in.GroupID, productID, product.Version, in.Kind, in.RequestedAmountUGX,
		in.Purpose, in.TermMonths, in.RepaymentSource, authorityID, in.AppliedBy).Scan(&id)
	if err != nil {
		if isUniqueViolation(err) {
			return Loan{}, domain.ErrConflict
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return Loan{}, domain.ErrNotFound
		}
		return Loan{}, mapGuardError(err)
	}

	// The group's authority record is the authorisation evidence; the explicit
	// draft -> group_authorised step keeps the transition in the ledger of states.
	if _, err := tx.Exec(ctx,
		`UPDATE pamoja.loan_application SET status = 'group_authorised', updated_at = now()
		 WHERE id = $1::uuid`, id); err != nil {
		return Loan{}, mapGuardError(err)
	}
	return s.Get(ctx, tx, id)
}

// Appraise records the field officer's appraisal, borrower checks and checked
// guarantees, and moves the application on for operations review.
func (s *Service) Appraise(ctx context.Context, tx pgx.Tx, in AppraiseInput) (Loan, error) {
	if err := domain.Required("officer_id", in.OfficerID); err != nil {
		return Loan{}, err
	}
	if err := domain.Required("cash_flow_notes", in.CashFlowNotes); err != nil {
		return Loan{}, err
	}
	if err := domain.Required("recommendation", in.Recommendation); err != nil {
		return Loan{}, err
	}

	loan, err := s.lockApplication(ctx, tx, in.ApplicationID)
	if err != nil {
		return Loan{}, err
	}
	if loan.Status != "group_authorised" {
		return Loan{}, fmt.Errorf("%w: an appraisal needs a group-authorised application (status %s)",
			domain.ErrInvalidTransition, loan.Status)
	}
	if in.OfficerID == loan.AppliedBy {
		return Loan{}, fmt.Errorf("%w: the appraiser must differ from the applicant",
			domain.ErrSeparationOfDuties)
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO pamoja.loan_appraisal
		   (application_id, officer_id, cash_flow_notes, existing_obligations_ugx, recommendation)
		 VALUES ($1::uuid, $2::uuid, $3, $4, $5)`,
		in.ApplicationID, in.OfficerID, in.CashFlowNotes, in.ExistingObligationsUGX,
		in.Recommendation); err != nil {
		return Loan{}, err
	}
	for _, check := range in.Checks {
		if check.Kind == "" || check.Result == "" {
			return Loan{}, domain.Invalid("checks", "each check needs a kind and a result")
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO pamoja.borrower_check (application_id, kind, result, checked_by)
			 VALUES ($1::uuid, $2, $3, $4::uuid)`,
			in.ApplicationID, check.Kind, check.Result, in.OfficerID); err != nil {
			return Loan{}, err
		}
	}
	for _, g := range in.Guarantees {
		if err := domain.PositiveMoney(g.AmountUGX); err != nil {
			return Loan{}, err
		}
		if err := domain.Required("guarantor_member_id", g.GuarantorMemberID); err != nil {
			return Loan{}, err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO pamoja.loan_guarantee
			   (application_id, guarantor_member_id, amount_ugx, checked_by, checked_at)
			 VALUES ($1::uuid, $2::uuid, $3, $4::uuid, now())`,
			in.ApplicationID, g.GuarantorMemberID, g.AmountUGX, in.OfficerID); err != nil {
			return Loan{}, err
		}
	}

	if _, err := tx.Exec(ctx,
		`UPDATE pamoja.loan_application SET status = 'officer_appraised', updated_at = now()
		 WHERE id = $1::uuid`, in.ApplicationID); err != nil {
		return Loan{}, mapGuardError(err)
	}
	return s.Get(ctx, tx, in.ApplicationID)
}

// SendToOperationsReview moves an appraised application to the mandatory
// operations review. It is a separate step so the reviewer is a different
// person from the appraising officer.
func (s *Service) SendToOperationsReview(ctx context.Context, tx pgx.Tx, applicationID, actorID string) (Loan, error) {
	loan, err := s.lockApplication(ctx, tx, applicationID)
	if err != nil {
		return Loan{}, err
	}
	if loan.Status != "officer_appraised" {
		return Loan{}, fmt.Errorf("%w: only an appraised application goes to operations review (status %s)",
			domain.ErrInvalidTransition, loan.Status)
	}
	if actorID == loan.AppliedBy {
		return Loan{}, fmt.Errorf("%w: the reviewer must differ from the applicant", domain.ErrSeparationOfDuties)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE pamoja.loan_application SET status = 'operations_review', updated_at = now()
		 WHERE id = $1::uuid`, applicationID); err != nil {
		return Loan{}, mapGuardError(err)
	}
	return s.Get(ctx, tx, applicationID)
}

// Decide records an operations review or credit decision. Approval above the
// single-approver limit needs two independent credit approvers; until the
// second decision lands the application stays under review.
func (s *Service) Decide(ctx context.Context, tx pgx.Tx, in DecisionInput) (Loan, error) {
	switch in.Decision {
	case "approved", "declined", "returned":
	default:
		return Loan{}, domain.Invalid("decision", "must be approved, declined or returned")
	}
	if in.Decision != "approved" {
		if err := domain.Required("reason", in.Reason); err != nil {
			return Loan{}, err
		}
	}

	loan, err := s.lockApplication(ctx, tx, in.ApplicationID)
	if err != nil {
		return Loan{}, err
	}
	if loan.Status != "operations_review" {
		return Loan{}, fmt.Errorf("%w: a decision needs an application under review (status %s)",
			domain.ErrInvalidTransition, loan.Status)
	}
	if err := CheckApprover(in.ActorID, Application{
		ApplicantID: loan.AppliedBy, AppraiserID: loan.AppraiserID, ApproverID: loan.ApprovedBy,
	}); err != nil {
		return Loan{}, fmt.Errorf("%w: %s", domain.ErrSeparationOfDuties, err.Error())
	}

	if in.Decision == "returned" {
		if _, err := tx.Exec(ctx,
			`INSERT INTO pamoja.loan_decision
			   (application_id, stage, decision, conditions, reason, decided_by)
			 VALUES ($1::uuid, 'operations_review', 'returned', '', $2, $3::uuid)`,
			in.ApplicationID, in.Reason, in.ActorID); err != nil {
			return Loan{}, err
		}
		if _, err := tx.Exec(ctx,
			`UPDATE pamoja.loan_application SET status = 'returned', updated_at = now()
			 WHERE id = $1::uuid`, in.ApplicationID); err != nil {
			return Loan{}, mapGuardError(err)
		}
		return s.Get(ctx, tx, in.ApplicationID)
	}

	if in.Decision == "declined" {
		if _, err := tx.Exec(ctx,
			`INSERT INTO pamoja.loan_decision
			   (application_id, stage, decision, conditions, reason, decided_by)
			 VALUES ($1::uuid, 'credit_decision', 'declined', '', $2, $3::uuid)`,
			in.ApplicationID, in.Reason, in.ActorID); err != nil {
			return Loan{}, err
		}
		if _, err := tx.Exec(ctx,
			`UPDATE pamoja.loan_application SET status = 'declined', rejection_reason = $2, updated_at = now()
			 WHERE id = $1::uuid`, in.ApplicationID, in.Reason); err != nil {
			return Loan{}, mapGuardError(err)
		}
		return s.Get(ctx, tx, in.ApplicationID)
	}

	// Approval. The approved amount is priced against the same product version.
	amount := in.ApprovedAmountUGX
	if amount == 0 {
		amount = loan.RequestedAmountUGX
	}
	if err := domain.PositiveMoney(amount); err != nil {
		return Loan{}, err
	}
	product, _, err := s.loadProduct(ctx, tx, loan.ProductCode, loan.ProductVersion)
	if err != nil {
		return Loan{}, err
	}
	if amount > loan.RequestedAmountUGX {
		return Loan{}, domain.Invalid("approved_amount_ugx", "cannot exceed the requested amount")
	}
	if err := ValidateProduct(product, Application{
		Kind: loan.Kind, AmountUGX: amount, TermMonths: loan.TermMonths,
	}); err != nil {
		return Loan{}, domain.Invalid("approved_amount_ugx", err.Error())
	}

	authority, err := s.loadAuthority(ctx, tx)
	if err != nil {
		return Loan{}, err
	}
	required := ApprovalsRequired(amount, authority)

	var decided int
	if err := tx.QueryRow(ctx,
		`SELECT count(DISTINCT decided_by) FROM pamoja.loan_decision
		 WHERE application_id = $1::uuid AND stage = 'credit_decision' AND decision = 'approved'`,
		in.ApplicationID).Scan(&decided); err != nil {
		return Loan{}, err
	}
	if decided > 0 {
		var priorApprover string
		if err := tx.QueryRow(ctx,
			`SELECT decided_by::text FROM pamoja.loan_decision
			 WHERE application_id = $1::uuid AND stage = 'credit_decision' AND decision = 'approved'
			 LIMIT 1`, in.ApplicationID).Scan(&priorApprover); err != nil {
			return Loan{}, err
		}
		if priorApprover == in.ActorID {
			return Loan{}, fmt.Errorf("%w: the second approval needs another credit approver",
				domain.ErrSeparationOfDuties)
		}
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO pamoja.loan_decision
		   (application_id, stage, decision, approved_amount_ugx, conditions, reason, decided_by)
		 VALUES ($1::uuid, 'credit_decision', 'approved', $2, $3, $4, $5::uuid)`,
		in.ApplicationID, amount, in.Conditions, in.Reason, in.ActorID); err != nil {
		return Loan{}, err
	}

	if decided+1 < required {
		// Still under review: the second approver has not decided yet.
		return s.Get(ctx, tx, in.ApplicationID)
	}

	if _, err := tx.Exec(ctx,
		`UPDATE pamoja.loan_application
		 SET status = 'approved', approved_by = $2::uuid, updated_at = now()
		 WHERE id = $1::uuid`, in.ApplicationID, in.ActorID); err != nil {
		return Loan{}, mapGuardError(err)
	}
	return s.Get(ctx, tx, in.ApplicationID)
}

// IssueOffer issues terms that come from the approved product version only.
func (s *Service) IssueOffer(ctx context.Context, tx pgx.Tx, in OfferInput) (Loan, error) {
	if err := domain.Required("issued_by", in.IssuedBy); err != nil {
		return Loan{}, err
	}
	loan, err := s.lockApplication(ctx, tx, in.ApplicationID)
	if err != nil {
		return Loan{}, err
	}
	if loan.Status != "approved" {
		return Loan{}, fmt.Errorf("%w: an offer needs an approved application (status %s)",
			domain.ErrInvalidTransition, loan.Status)
	}
	amount := in.AmountUGX
	if amount == 0 {
		amount = loan.ApprovedAmountUGX
	}
	if amount == 0 {
		amount = loan.RequestedAmountUGX
	}
	term := in.TermMonths
	if term == 0 {
		term = loan.TermMonths
	}
	product, _, err := s.loadProduct(ctx, tx, loan.ProductCode, loan.ProductVersion)
	if err != nil {
		return Loan{}, err
	}
	offer, err := OfferFromProduct(product, amount, term)
	if err != nil {
		return Loan{}, domain.Invalid("amount_ugx", err.Error())
	}
	validDays := in.ValidDays
	if validDays <= 0 {
		validDays = 14
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO pamoja.loan_offer
		   (application_id, amount_ugx, term_months, schedule_ref, issued_by, expires_on)
		 VALUES ($1::uuid, $2, $3, $4, $5::uuid, current_date + $6::integer)`,
		in.ApplicationID, offer.AmountUGX, offer.TermMonths, offer.ScheduleRef,
		in.IssuedBy, validDays); err != nil {
		return Loan{}, err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE pamoja.loan_application SET status = 'offer_issued', updated_at = now()
		 WHERE id = $1::uuid`, in.ApplicationID); err != nil {
		return Loan{}, mapGuardError(err)
	}
	return s.Get(ctx, tx, in.ApplicationID)
}

// AcceptOffer records the group accepting the issued terms.
func (s *Service) AcceptOffer(ctx context.Context, tx pgx.Tx, applicationID, actorID string) (Loan, error) {
	loan, err := s.lockApplication(ctx, tx, applicationID)
	if err != nil {
		return Loan{}, err
	}
	if loan.Status != "offer_issued" {
		return Loan{}, fmt.Errorf("%w: there is no issued offer to accept (status %s)",
			domain.ErrInvalidTransition, loan.Status)
	}
	tag, err := tx.Exec(ctx,
		`UPDATE pamoja.loan_offer
		 SET status = 'accepted', accepted_by = $2::uuid, accepted_at = now()
		 WHERE application_id = $1::uuid AND status = 'issued'`, applicationID, actorID)
	if err != nil {
		return Loan{}, err
	}
	if tag.RowsAffected() != 1 {
		return Loan{}, fmt.Errorf("%w: the offer is not open for acceptance", domain.ErrInvalidTransition)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE pamoja.loan_application SET status = 'offer_accepted', updated_at = now()
		 WHERE id = $1::uuid`, applicationID); err != nil {
		return Loan{}, mapGuardError(err)
	}
	return s.Get(ctx, tx, applicationID)
}

// Disburse confirms money leaving the group's account. Approval is never
// disbursement: the actor must differ from the approver (SD-04), and the
// database re-checks that before the row lands.
func (s *Service) Disburse(ctx context.Context, tx pgx.Tx, in DisburseInput) (Loan, error) {
	if err := domain.PositiveMoney(in.AmountUGX); err != nil {
		return Loan{}, err
	}
	if err := domain.Required("external_ref", in.ExternalRef); err != nil {
		return Loan{}, err
	}
	loan, err := s.lockApplication(ctx, tx, in.ApplicationID)
	if err != nil {
		return Loan{}, err
	}
	if loan.Status != "offer_accepted" {
		return Loan{}, fmt.Errorf("%w: only an accepted offer is disbursed (status %s)",
			domain.ErrInvalidTransition, loan.Status)
	}
	if err := CheckDisburser(in.ActorID, Application{ApproverID: loan.ApprovedBy}); err != nil {
		return Loan{}, fmt.Errorf("%w: %s", domain.ErrSeparationOfDuties, err.Error())
	}

	disbursedOn := in.DisbursedOn
	if disbursedOn.IsZero() {
		disbursedOn = s.Now().UTC()
	}
	if disbursedOn.After(s.Now().UTC().Add(24 * time.Hour)) {
		return Loan{}, domain.Invalid("disbursed_on", "cannot be in the future; server time wins")
	}

	offerID := in.OfferID
	var offerAmount int64
	var term int
	if offerID == "" {
		if err := tx.QueryRow(ctx,
			`SELECT id::text, amount_ugx, term_months FROM pamoja.loan_offer
			 WHERE application_id = $1::uuid AND status = 'accepted'
			 ORDER BY issued_at DESC LIMIT 1`, in.ApplicationID).
			Scan(&offerID, &offerAmount, &term); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return Loan{}, domain.ErrNotFound
			}
			return Loan{}, err
		}
	} else if err := tx.QueryRow(ctx,
		`SELECT amount_ugx, term_months FROM pamoja.loan_offer
		 WHERE id = $1::uuid AND application_id = $2::uuid`,
		offerID, in.ApplicationID).Scan(&offerAmount, &term); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Loan{}, domain.ErrNotFound
		}
		return Loan{}, err
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO pamoja.loan_disbursement
		   (application_id, offer_id, tranche_no, amount_ugx, disbursed_on, confirmed_by, external_ref)
		 VALUES ($1::uuid, $2::uuid,
		         (SELECT COALESCE(max(tranche_no), 0) + 1 FROM pamoja.loan_disbursement
		          WHERE application_id = $1::uuid),
		         $3, $4, $5::uuid, $6)`,
		in.ApplicationID, offerID, in.AmountUGX, disbursedOn, in.ActorID, in.ExternalRef); err != nil {
		return Loan{}, mapGuardError(err)
	}

	// The schedule is built once, from the accepted offer's full amount.
	var scheduled int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM pamoja.loan_installment WHERE application_id = $1::uuid`,
		in.ApplicationID).Scan(&scheduled); err != nil {
		return Loan{}, err
	}
	if scheduled == 0 && term > 0 {
		if err := createSchedule(ctx, tx, in.ApplicationID, offerAmount, term, disbursedOn); err != nil {
			return Loan{}, err
		}
	}

	if _, err := tx.Exec(ctx,
		`UPDATE pamoja.loan_application SET status = 'disbursed', updated_at = now()
		 WHERE id = $1::uuid`, in.ApplicationID); err != nil {
		return Loan{}, mapGuardError(err)
	}
	return s.Get(ctx, tx, in.ApplicationID)
}

// createSchedule splits the principal into equal monthly installments, with the
// rounding remainder on the last one.
func createSchedule(ctx context.Context, tx pgx.Tx, applicationID string, amount int64, term int, firstDue time.Time) error {
	base := amount / int64(term)
	remainder := amount % int64(term)
	for i := 1; i <= term; i++ {
		due := base
		if i == term {
			due += remainder
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO pamoja.loan_installment (application_id, due_on, amount_ugx)
			 VALUES ($1::uuid, $2::date, $3)`,
			applicationID, firstDue.AddDate(0, i, 0).Format("2006-01-02"), due); err != nil {
			return err
		}
	}
	return nil
}

// RecordMaterialChange forces re-approval after a material change (A-034).
func (s *Service) RecordMaterialChange(ctx context.Context, tx pgx.Tx, in MaterialChangeInput) (Loan, error) {
	if err := domain.Required("reason", in.Reason); err != nil {
		return Loan{}, err
	}
	if !IsMaterial(in.Field) {
		return Loan{}, domain.Invalid("field", "is not a material field")
	}
	loan, err := s.lockApplication(ctx, tx, in.ApplicationID)
	if err != nil {
		return Loan{}, err
	}
	switch loan.Status {
	case "approved", "offer_issued", "offer_accepted":
	default:
		return Loan{}, fmt.Errorf("%w: a material change applies after approval (status %s)",
			domain.ErrInvalidTransition, loan.Status)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO pamoja.loan_material_change
		   (application_id, field, old_value, new_value, reason, changed_by)
		 VALUES ($1::uuid, $2, $3, $4, $5, $6::uuid)`,
		in.ApplicationID, in.Field, in.OldValue, in.NewValue, in.Reason, in.ActorID); err != nil {
		return Loan{}, err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE pamoja.loan_application
		 SET status = 'operations_review',
		     material_change_count = material_change_count + 1,
		     approved_by = NULL, updated_at = now()
		 WHERE id = $1::uuid`, in.ApplicationID); err != nil {
		return Loan{}, mapGuardError(err)
	}
	return s.Get(ctx, tx, in.ApplicationID)
}

// RecordRepayment records a repayment; the database caps it at the installment
// and applies it in the same transaction.
func (s *Service) RecordRepayment(ctx context.Context, tx pgx.Tx, in RepaymentInput) error {
	if err := domain.PositiveMoney(in.AmountUGX); err != nil {
		return err
	}
	if err := domain.Required("external_ref", in.ExternalRef); err != nil {
		return err
	}
	loan, err := s.lockApplication(ctx, tx, in.ApplicationID)
	if err != nil {
		return err
	}
	if loan.Status != "disbursed" {
		return fmt.Errorf("%w: a repayment needs a disbursed loan (status %s)",
			domain.ErrInvalidTransition, loan.Status)
	}
	paidOn := in.PaidOn
	if paidOn.IsZero() {
		paidOn = s.Now().UTC()
	}
	_, err = tx.Exec(ctx,
		`INSERT INTO pamoja.loan_repayment
		   (application_id, installment_id, amount_ugx, paid_on, recorded_by, external_ref)
		 VALUES ($1::uuid, $2::uuid, $3, $4::date, $5::uuid, $6)`,
		in.ApplicationID, in.InstallmentID, in.AmountUGX, paidOn.Format("2006-01-02"),
		in.ActorID, in.ExternalRef)
	return mapGuardError(err)
}

// Get returns one application visible inside the caller's scope.
func (s *Service) Get(ctx context.Context, tx pgx.Tx, applicationID string) (Loan, error) {
	var out Loan
	err := tx.QueryRow(ctx, loanSelect+` WHERE a.id = $1::uuid`, applicationID).Scan(loanScanTargets(&out)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return Loan{}, domain.ErrNotFound
	}
	if err != nil {
		return Loan{}, err
	}
	return out, nil
}

// ListByGroup returns a group's applications, newest first.
func (s *Service) ListByGroup(ctx context.Context, tx pgx.Tx, groupID string, limit int) ([]Loan, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := tx.Query(ctx, loanSelect+` WHERE a.group_id = $1::uuid ORDER BY a.created_at DESC LIMIT $2`,
		groupID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Loan
	for rows.Next() {
		var l Loan
		if err := rows.Scan(loanScanTargets(&l)...); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

const loanSelect = `
	SELECT a.id::text, a.group_id::text, p.code, a.product_version, a.loan_kind,
	       a.requested_amount_ugx, a.term_months, a.purpose, a.status,
	       a.applied_by::text, COALESCE(a.approved_by::text, ''),
	       a.material_change_count, a.created_at,
	       COALESCE((SELECT ap.officer_id::text FROM pamoja.loan_appraisal ap
	                 WHERE ap.application_id = a.id ORDER BY ap.created_at DESC LIMIT 1), ''),
	       COALESCE((SELECT d.approved_amount_ugx FROM pamoja.loan_decision d
	                 WHERE d.application_id = a.id AND d.decision = 'approved'
	                   AND d.approved_amount_ugx IS NOT NULL
	                 ORDER BY d.decided_at DESC LIMIT 1), 0)
	FROM pamoja.loan_application a
	JOIN pamoja.loan_product p ON p.id = a.product_id`

func loanScanTargets(l *Loan) []any {
	return []any{&l.ID, &l.GroupID, &l.ProductCode, &l.ProductVersion, &l.Kind,
		&l.RequestedAmountUGX, &l.TermMonths, &l.Purpose, &l.Status,
		&l.AppliedBy, &l.ApprovedBy, &l.MaterialChanges, &l.CreatedAt,
		&l.AppraiserID, &l.ApprovedAmountUGX}
}

func (s *Service) lockApplication(ctx context.Context, tx pgx.Tx, applicationID string) (Loan, error) {
	if err := domain.Required("application_id", applicationID); err != nil {
		return Loan{}, err
	}
	if _, err := tx.Exec(ctx,
		`SELECT 1 FROM pamoja.loan_application WHERE id = $1::uuid FOR UPDATE`, applicationID); err != nil {
		return Loan{}, err
	}
	return s.Get(ctx, tx, applicationID)
}

// loadProduct reads an approved product version. A zero version selects the
// latest active version of that code.
func (s *Service) loadProduct(ctx context.Context, tx pgx.Tx, code string, version int) (Product, string, error) {
	var p Product
	var id string
	err := tx.QueryRow(ctx,
		`SELECT id::text, code, kind, min_amount_ugx, max_amount_ugx, max_term_months,
		        guarantor_required_above_ugx, version
		 FROM pamoja.loan_product
		 WHERE code = $1 AND status = 'active' AND ($2 = 0 OR version = $2)
		 ORDER BY version DESC LIMIT 1`, code, version).
		Scan(&id, &p.Code, &p.Kind, &p.MinAmountUGX, &p.MaxAmountUGX, &p.MaxTermMonths,
			&p.GuarantorAboveUGX, &p.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return Product{}, "", fmt.Errorf("%w: no active product version for %s", domain.ErrNotFound, code)
	}
	if err != nil {
		return Product{}, "", err
	}
	return p, id, nil
}

// loadAuthority reads the versioned approval-authority configuration (A-012).
func (s *Service) loadAuthority(ctx context.Context, tx pgx.Tx) (Authority, error) {
	var raw []byte
	err := tx.QueryRow(ctx,
		`SELECT value FROM pamoja.config_value
		 WHERE key = 'loans.approval_authority' ORDER BY version DESC LIMIT 1`).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		// Documented default from A-012.
		return Authority{CreditApproverMax: 5_000_000, CommitteeMax: 20_000_000, TwoApproversAbove: 20_000_000}, nil
	}
	if err != nil {
		return Authority{}, err
	}
	var cfg struct {
		CreditApproverMax int64 `json:"credit_approver_max"`
		CommitteeMax      int64 `json:"committee_max"`
		TwoApproversAbove int64 `json:"two_approvers_above"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return Authority{}, err
	}
	return Authority{
		CreditApproverMax: cfg.CreditApproverMax,
		CommitteeMax:      cfg.CommitteeMax,
		TwoApproversAbove: cfg.TwoApproversAbove,
	}, nil
}

// resolveAuthority pins the group's current chairperson authority record, or
// verifies the one the caller supplied.
func (s *Service) resolveAuthority(ctx context.Context, tx pgx.Tx, groupID, provided string) (string, error) {
	if provided != "" {
		var found string
		err := tx.QueryRow(ctx,
			`SELECT id::text FROM pamoja.authority_record
			 WHERE id = $1::uuid AND group_id = $2::uuid`, provided, groupID).Scan(&found)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", domain.Invalid("authority_record_id", "does not belong to this group")
		}
		return found, err
	}
	var found string
	err := tx.QueryRow(ctx,
		`SELECT id::text FROM pamoja.authority_record
		 WHERE group_id = $1::uuid AND office = 'chairperson'
		   AND effective_from <= current_date
		   AND (effective_to IS NULL OR effective_to >= current_date)
		 ORDER BY effective_from DESC LIMIT 1`, groupID).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", domain.Invalid("authority_record_id",
			"the group has no current chairperson authority record; register its leadership first")
	}
	return found, err
}

func isUniqueViolation(err error) bool {
	type sqlState interface{ SQLState() string }
	var s sqlState
	if errors.As(err, &s) {
		return s.SQLState() == "23505"
	}
	return false
}

// mapGuardError turns the database guards' refusals into typed domain errors so
// the HTTP layer answers 409/422 instead of a 500.
func mapGuardError(err error) error {
	if err == nil {
		return nil
	}
	type sqlState interface{ SQLState() string }
	var s sqlState
	if errors.As(err, &s) {
		switch s.SQLState() {
		case "23514", "23503":
			return fmt.Errorf("%w: %s", domain.ErrInvalidTransition, err.Error())
		}
	}
	return err
}
